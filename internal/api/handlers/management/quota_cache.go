package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codexWhamUsageHost = "chatgpt.com"
	codexWhamUsagePath = "/backend-api/wham/usage"
)

var codexWhamUsageURL = "https://" + codexWhamUsageHost + codexWhamUsagePath

type codexQuotaWindowSnapshot struct {
	ID                 string
	Label              string
	LimitWindowSeconds int64
	UsedPercent        float64
	RemainingPercent   float64
	ResetAfterSeconds  int64
	ResetAtUnixSeconds int64
}

func (h *Handler) maybeCacheCodexQuotaFromAPICall(ctx context.Context, auth *coreauth.Auth, requestURL string, statusCode int, respBody []byte) {
	if h == nil || h.authManager == nil || auth == nil {
		return
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return
	}
	if !isCodexWhamUsageURL(requestURL) {
		return
	}
	windows, weeklyRemaining, ok := parseCodexQuotaWindowsForRouting(respBody)
	if !ok {
		return
	}
	current, exists := h.authManager.GetByID(auth.ID)
	if !exists || current == nil {
		return
	}
	if current.Metadata == nil {
		current.Metadata = make(map[string]any)
	}
	current.Metadata["quota"] = map[string]any{
		"provider":   "codex",
		"source":     "wham_usage",
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"windows":    quotaWindowsForMetadata(windows),
	}
	current.Metadata["quota_remaining_percent"] = weeklyRemaining
	current.Metadata["codex_quota_remaining_percent"] = weeklyRemaining
	if _, errUpdate := h.authManager.Update(ctx, current); errUpdate != nil {
		log.WithError(errUpdate).Debug("failed to cache codex quota metadata")
	}
}

func isCodexWhamUsageURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	target, errTarget := url.Parse(strings.TrimSpace(codexWhamUsageURL))
	if errTarget != nil || target.Host == "" {
		return strings.EqualFold(parsed.Host, codexWhamUsageHost) && parsed.Path == codexWhamUsagePath
	}
	return strings.EqualFold(parsed.Host, target.Host) && parsed.Path == target.Path
}

func parseCodexQuotaWindowsForRouting(data []byte) ([]codexQuotaWindowSnapshot, float64, bool) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, 0, false
	}
	rateLimit, _ := firstMap(payload, "rate_limit", "rateLimit")
	if len(rateLimit) == 0 {
		return nil, 0, false
	}
	primary, _ := firstMap(rateLimit, "primary_window", "primaryWindow")
	secondary, _ := firstMap(rateLimit, "secondary_window", "secondaryWindow")
	fiveHour, weekly := classifyCodexQuotaWindows(primary, secondary)
	windows := make([]codexQuotaWindowSnapshot, 0, 2)
	if window, ok := buildCodexQuotaWindow("code-5h", "5h", fiveHour); ok {
		windows = append(windows, window)
	}
	weeklyWindow, ok := buildCodexQuotaWindow("code-7d", "7d", weekly)
	if !ok {
		return windows, 0, false
	}
	windows = append(windows, weeklyWindow)
	return windows, weeklyWindow.RemainingPercent, true
}

func classifyCodexQuotaWindows(primary, secondary map[string]any) (map[string]any, map[string]any) {
	const (
		window5HSeconds = int64(5 * 60 * 60)
		window7DSeconds = int64(7 * 24 * 60 * 60)
	)
	var fiveHour map[string]any
	var weekly map[string]any
	for _, candidate := range []map[string]any{primary, secondary} {
		if len(candidate) == 0 {
			continue
		}
		duration := int64(numberFromAny(firstAny(candidate, "limit_window_seconds", "limitWindowSeconds")))
		switch duration {
		case window5HSeconds:
			if fiveHour == nil {
				fiveHour = candidate
			}
		case window7DSeconds:
			if weekly == nil {
				weekly = candidate
			}
		}
	}
	if fiveHour == nil {
		fiveHour = primary
	}
	if weekly == nil {
		weekly = secondary
	}
	return fiveHour, weekly
}

func buildCodexQuotaWindow(id, label string, raw map[string]any) (codexQuotaWindowSnapshot, bool) {
	if len(raw) == 0 {
		return codexQuotaWindowSnapshot{}, false
	}
	used, ok := quotaNumberFromAny(firstAny(raw, "used_percent", "usedPercent"))
	if !ok {
		return codexQuotaWindowSnapshot{}, false
	}
	used = clampQuotaPercent(used)
	return codexQuotaWindowSnapshot{
		ID:                 id,
		Label:              label,
		LimitWindowSeconds: int64(numberFromAny(firstAny(raw, "limit_window_seconds", "limitWindowSeconds"))),
		UsedPercent:        used,
		RemainingPercent:   clampQuotaPercent(100 - used),
		ResetAfterSeconds:  int64(numberFromAny(firstAny(raw, "reset_after_seconds", "resetAfterSeconds"))),
		ResetAtUnixSeconds: int64(numberFromAny(firstAny(raw, "reset_at", "resetAt"))),
	}, true
}

func quotaWindowsForMetadata(windows []codexQuotaWindowSnapshot) []any {
	out := make([]any, 0, len(windows))
	for _, window := range windows {
		entry := map[string]any{
			"id":                   window.ID,
			"label":                window.Label,
			"used_percent":         window.UsedPercent,
			"remaining_percent":    window.RemainingPercent,
			"limit_window_seconds": window.LimitWindowSeconds,
		}
		if window.ResetAfterSeconds > 0 {
			entry["reset_after_seconds"] = window.ResetAfterSeconds
		}
		if window.ResetAtUnixSeconds > 0 {
			entry["reset_at"] = window.ResetAtUnixSeconds
		}
		out = append(out, entry)
	}
	return out
}

func firstMap(values map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		m, ok := value.(map[string]any)
		if ok {
			return m, true
		}
	}
	return nil, false
}

func firstAny(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func quotaNumberFromAny(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberFromAny(raw any) float64 {
	value, _ := quotaNumberFromAny(raw)
	return value
}

func clampQuotaPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
