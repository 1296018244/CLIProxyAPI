package management

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	defaultCodexQuotaRefreshConcurrency = 8
	maxCodexQuotaRefreshConcurrency     = 20
	defaultCodexQuotaRefreshDelay       = 100 * time.Millisecond
	maxCodexQuotaRefreshDelay           = 10 * time.Second
)

type codexQuotaRefreshAllRequest struct {
	Concurrency  int  `json:"concurrency"`
	DelayMSSnake *int `json:"delay_ms"`
	DelayMSCamel *int `json:"delayMs"`
}

type codexQuotaRefreshAllResponse struct {
	Total     int                       `json:"total"`
	Succeeded int                       `json:"succeeded"`
	Failed    int                       `json:"failed"`
	Skipped   int                       `json:"skipped"`
	Results   []codexQuotaRefreshResult `json:"results"`
}

type codexQuotaRefreshResult struct {
	AuthID           string   `json:"auth_id"`
	AuthIndex        string   `json:"auth_index"`
	Name             string   `json:"name,omitempty"`
	AccountID        string   `json:"account_id,omitempty"`
	Status           string   `json:"status"`
	StatusCode       int      `json:"status_code,omitempty"`
	RemainingPercent *float64 `json:"remaining_percent,omitempty"`
	Error            string   `json:"error,omitempty"`
}

// RefreshAllCodexQuota refreshes Codex quota metadata for all known Codex auths.
// The request is one management API call, but upstream quota checks are still
// rate-limited internally to avoid sending a burst to ChatGPT.
func (h *Handler) RefreshAllCodexQuota(c *gin.Context) {
	var body codexQuotaRefreshAllRequest
	if errBind := c.ShouldBindJSON(&body); errBind != nil && !errors.Is(errBind, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	auths := h.codexQuotaRefreshCandidates()
	response := codexQuotaRefreshAllResponse{
		Total:   len(auths),
		Results: make([]codexQuotaRefreshResult, 0, len(auths)),
	}
	if len(auths) == 0 {
		c.JSON(http.StatusOK, response)
		return
	}

	concurrency := normalizeCodexQuotaRefreshConcurrency(body.Concurrency)
	delay := normalizeCodexQuotaRefreshDelay(body.DelayMSSnake, body.DelayMSCamel)
	results := h.refreshCodexQuotaCandidates(c.Request.Context(), auths, concurrency, delay)
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].AuthIndex == results[j].AuthIndex {
			return results[i].AuthID < results[j].AuthID
		}
		return results[i].AuthIndex < results[j].AuthIndex
	})

	for _, result := range results {
		switch result.Status {
		case "ok":
			response.Succeeded++
		case "skipped":
			response.Skipped++
		default:
			response.Failed++
		}
		response.Results = append(response.Results, result)
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) codexQuotaRefreshCandidates() []*coreauth.Auth {
	if h == nil || h.authManager == nil {
		return nil
	}
	auths := h.authManager.List()
	out := make([]*coreauth.Auth, 0, len(auths))
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		auth.EnsureIndex()
		out = append(out, auth)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Index == out[j].Index {
			return out[i].ID < out[j].ID
		}
		return out[i].Index < out[j].Index
	})
	return out
}

func (h *Handler) refreshCodexQuotaCandidates(ctx context.Context, auths []*coreauth.Auth, concurrency int, delay time.Duration) []codexQuotaRefreshResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(auths) == 0 {
		return nil
	}

	jobs := make(chan *coreauth.Auth)
	results := make(chan codexQuotaRefreshResult, len(auths))
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for auth := range jobs {
				results <- h.refreshCodexQuotaForAuth(ctx, auth)
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i, auth := range auths {
			select {
			case <-ctx.Done():
				for _, remaining := range auths[i:] {
					results <- baseCodexQuotaRefreshResult(remaining, "failed", ctx.Err().Error())
				}
				return
			case jobs <- auth:
			}
			if delay > 0 && i < len(auths)-1 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					for _, remaining := range auths[i+1:] {
						results <- baseCodexQuotaRefreshResult(remaining, "failed", ctx.Err().Error())
					}
					return
				case <-timer.C:
				}
			}
		}
	}()

	wg.Wait()
	close(results)

	out := make([]codexQuotaRefreshResult, 0, len(auths))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func (h *Handler) refreshCodexQuotaForAuth(ctx context.Context, auth *coreauth.Auth) codexQuotaRefreshResult {
	result := baseCodexQuotaRefreshResult(auth, "ok", "")
	if auth == nil {
		result.Status = "skipped"
		result.Error = "auth missing"
		return result
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		result.Status = "skipped"
		result.Error = "auth disabled"
		return result
	}

	accountID := codexChatGPTAccountID(auth)
	if accountID == "" {
		result.Status = "skipped"
		result.Error = "chatgpt_account_id missing"
		return result
	}
	result.AccountID = accountID

	token, errToken := h.resolveTokenForAuth(ctx, auth)
	if errToken != nil {
		result.Status = "failed"
		result.Error = "auth token refresh failed"
		log.WithError(errToken).Debug("codex quota refresh token resolution failed")
		return result
	}
	if strings.TrimSpace(token) == "" {
		result.Status = "skipped"
		result.Error = "auth token missing"
		return result
	}

	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(codexWhamUsageURL), nil)
	if errRequest != nil {
		result.Status = "failed"
		result.Error = "failed to build request"
		return result
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal")
	req.Header.Set("Chatgpt-Account-Id", accountID)

	httpClient := &http.Client{
		Timeout:   defaultAPICallTimeout,
		Transport: h.apiCallTransport(auth),
	}
	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		result.Status = "failed"
		result.Error = "request failed"
		log.WithError(errDo).Debug("codex quota refresh request failed")
		return result
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}
	}()
	result.StatusCode = resp.StatusCode

	respBody, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		result.Status = "failed"
		result.Error = "failed to read response"
		return result
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		result.Status = "failed"
		result.Error = fmt.Sprintf("upstream status %d", resp.StatusCode)
		return result
	}
	_, weeklyRemaining, ok := parseCodexQuotaWindowsForRouting(respBody)
	if !ok {
		result.Status = "failed"
		result.Error = "quota response missing weekly percentage"
		return result
	}
	h.maybeCacheCodexQuotaFromAPICall(ctx, auth, codexWhamUsageURL, resp.StatusCode, respBody)
	result.RemainingPercent = &weeklyRemaining
	return result
}

func baseCodexQuotaRefreshResult(auth *coreauth.Auth, status string, errMsg string) codexQuotaRefreshResult {
	result := codexQuotaRefreshResult{Status: status, Error: errMsg}
	if auth == nil {
		return result
	}
	auth.EnsureIndex()
	result.AuthID = auth.ID
	result.AuthIndex = auth.Index
	result.Name = strings.TrimSpace(auth.FileName)
	if result.Name == "" {
		result.Name = strings.TrimSpace(auth.Label)
	}
	if result.Name == "" {
		result.Name = auth.ID
	}
	return result
}

func codexChatGPTAccountID(auth *coreauth.Auth) string {
	claims := extractCodexIDTokenClaims(auth)
	if claims == nil {
		return ""
	}
	if accountID, ok := claims["chatgpt_account_id"].(string); ok {
		return strings.TrimSpace(accountID)
	}
	return ""
}

func normalizeCodexQuotaRefreshConcurrency(raw int) int {
	if raw <= 0 {
		return defaultCodexQuotaRefreshConcurrency
	}
	if raw > maxCodexQuotaRefreshConcurrency {
		return maxCodexQuotaRefreshConcurrency
	}
	return raw
}

func normalizeCodexQuotaRefreshDelay(values ...*int) time.Duration {
	delayMS := int(defaultCodexQuotaRefreshDelay / time.Millisecond)
	for _, value := range values {
		if value != nil {
			delayMS = *value
			break
		}
	}
	if delayMS < 0 {
		delayMS = 0
	}
	delay := time.Duration(delayMS) * time.Millisecond
	if delay > maxCodexQuotaRefreshDelay {
		return maxCodexQuotaRefreshDelay
	}
	return delay
}
