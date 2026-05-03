package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

func newUsageTestContext(method, target string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	if body == nil {
		body = []byte{}
	}
	ctx.Request = httptest.NewRequest(method, target, bytes.NewReader(body))
	return ctx, rec
}

func TestGetUsageStatisticsReturnsEmptySnapshot(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	h.SetUsageStatistics(usage.NewRequestStatistics())

	ctx, rec := newUsageTestContext(http.MethodGet, "/v0/management/usage", nil)
	h.GetUsageStatistics(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if _, ok := payload["usage"].(map[string]any); !ok {
		t.Fatalf("usage object missing in %v", payload)
	}
	if payload["failed_requests"] == nil {
		t.Fatalf("failed_requests missing in %v", payload)
	}
}

func TestImportUsageStatisticsRejectsInvalidJSON(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	h.SetUsageStatistics(usage.NewRequestStatistics())

	ctx, rec := newUsageTestContext(http.MethodPost, "/v0/management/usage/import", []byte(`{"bad"`))
	h.ImportUsageStatistics(ctx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestImportUsageStatisticsMergesSnapshot(t *testing.T) {
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, nil)
	h.SetUsageStatistics(usage.NewRequestStatistics())

	body, err := json.Marshal(usageImportPayload{
		Version: 1,
		Usage: usage.StatisticsSnapshot{
			APIs: map[string]usage.APISnapshot{
				"test-key": {
					Models: map[string]usage.ModelSnapshot{
						"gpt-5.4": {
							Details: []usage.RequestDetail{{
								Timestamp: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
								Tokens:    usage.TokenStats{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
							}},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	ctx, rec := newUsageTestContext(http.MethodPost, "/v0/management/usage/import", body)
	h.ImportUsageStatistics(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if payload["added"].(float64) != 1 {
		t.Fatalf("added = %v, want 1", payload["added"])
	}
	if payload["total_requests"].(float64) != 1 {
		t.Fatalf("total_requests = %v, want 1", payload["total_requests"])
	}
}
