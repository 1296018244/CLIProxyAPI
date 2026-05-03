package management

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestRefreshAllCodexQuotaCachesQuotaForEveryCodexAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var mu sync.Mutex
	seenAccounts := make(map[string]string)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" {
			t.Fatalf("request path = %q, want /backend-api/wham/usage", r.URL.Path)
		}
		accountID := r.Header.Get("Chatgpt-Account-Id")
		authHeader := r.Header.Get("Authorization")
		mu.Lock()
		seenAccounts[accountID] = authHeader
		mu.Unlock()

		usedPercent := 9
		if accountID == "acc-two" {
			usedPercent = 33
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000},"secondary_window":{"used_percent":` + jsonNumber(usedPercent) + `,"limit_window_seconds":604800}}}`))
	}))
	defer ts.Close()

	oldURL := codexWhamUsageURL
	codexWhamUsageURL = ts.URL + "/backend-api/wham/usage"
	defer func() { codexWhamUsageURL = oldURL }()

	manager := coreauth.NewManager(nil, nil, nil)
	auths := []*coreauth.Auth{
		{
			ID:       "codex-one",
			Provider: "codex",
			Metadata: map[string]any{
				"access_token": "token-one",
				"id_token":     fakeCodexIDToken("acc-one"),
			},
		},
		{
			ID:       "codex-two",
			Provider: "codex",
			Metadata: map[string]any{
				"access_token": "token-two",
				"id_token":     fakeCodexIDToken("acc-two"),
			},
		},
		{
			ID:       "gemini-one",
			Provider: "gemini",
			Metadata: map[string]any{"access_token": "gemini-token"},
		},
	}
	for _, auth := range auths {
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("register %s: %v", auth.ID, errRegister)
		}
	}

	h := &Handler{authManager: manager}
	body := bytes.NewBufferString(`{"concurrency":1,"delay_ms":0}`)
	ctx, rec := newQuotaRefreshTestContext(http.MethodPost, "/v0/management/codex-quota/refresh-all", body)
	h.RefreshAllCodexQuota(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload codexQuotaRefreshAllResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &payload); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if payload.Total != 2 || payload.Succeeded != 2 || payload.Failed != 0 || payload.Skipped != 0 {
		t.Fatalf("summary = total %d succeeded %d failed %d skipped %d", payload.Total, payload.Succeeded, payload.Failed, payload.Skipped)
	}

	mu.Lock()
	gotAccounts := map[string]string{}
	for k, v := range seenAccounts {
		gotAccounts[k] = v
	}
	mu.Unlock()
	if gotAccounts["acc-one"] != "Bearer token-one" {
		t.Fatalf("acc-one Authorization = %q", gotAccounts["acc-one"])
	}
	if gotAccounts["acc-two"] != "Bearer token-two" {
		t.Fatalf("acc-two Authorization = %q", gotAccounts["acc-two"])
	}

	updatedOne, ok := manager.GetByID("codex-one")
	if !ok {
		t.Fatal("codex-one missing after refresh")
	}
	if updatedOne.Metadata["quota_remaining_percent"] != float64(91) {
		t.Fatalf("codex-one remaining = %#v, want 91", updatedOne.Metadata["quota_remaining_percent"])
	}
	updatedTwo, ok := manager.GetByID("codex-two")
	if !ok {
		t.Fatal("codex-two missing after refresh")
	}
	if updatedTwo.Metadata["quota_remaining_percent"] != float64(67) {
		t.Fatalf("codex-two remaining = %#v, want 67", updatedTwo.Metadata["quota_remaining_percent"])
	}
}

func TestRefreshAllCodexQuotaReportsSkippedAuths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	if _, errRegister := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-missing-account",
		Provider: "codex",
		Metadata: map[string]any{"access_token": "token"},
	}); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	h := &Handler{authManager: manager}
	ctx, rec := newQuotaRefreshTestContext(http.MethodPost, "/v0/management/codex-quota/refresh-all", bytes.NewBufferString(`{}`))
	h.RefreshAllCodexQuota(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload codexQuotaRefreshAllResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &payload); errDecode != nil {
		t.Fatalf("decode response: %v", errDecode)
	}
	if payload.Total != 1 || payload.Succeeded != 0 || payload.Failed != 0 || payload.Skipped != 1 {
		t.Fatalf("summary = total %d succeeded %d failed %d skipped %d", payload.Total, payload.Succeeded, payload.Failed, payload.Skipped)
	}
	if len(payload.Results) != 1 || payload.Results[0].Status != "skipped" || payload.Results[0].Error == "" {
		t.Fatalf("results = %#v, want one skipped result with error", payload.Results)
	}
}

func newQuotaRefreshTestContext(method, target string, body *bytes.Buffer) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, target, body)
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, rec
}

func fakeCodexIDToken(accountID string) string {
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}
