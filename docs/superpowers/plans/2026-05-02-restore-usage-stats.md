# Restore Usage Statistics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the legacy built-in usage statistics API and make the supplied management panel's `/usage` page work again, with optional file-backed persistence for Render.

**Architecture:** Reintroduce the `internal/usage` aggregation plugin from `v6.9.49`, wire it into the current `sdk/cliproxy/usage` event stream, expose legacy-compatible management endpoints, and add a small optional JSON snapshot persistence layer. Keep the existing `internal/redisqueue` feature intact and continue honoring `usage-statistics-enabled`.

**Tech Stack:** Go 1.26, Gin management handlers, existing `sdk/cliproxy/usage` plugin API, JSON snapshots, existing management panel asset system.

---

## File Structure

- Create `internal/usage/logger_plugin.go`: in-memory usage aggregation, legacy snapshot types, import merge logic, and usage plugin registration.
- Create `internal/usage/logger_plugin_test.go`: aggregation, latency/token detail capture, import deduplication, disabled switch tests.
- Create `internal/usage/persistence.go`: optional JSON snapshot load/save, atomic file writes, periodic autosave, shutdown save.
- Create `internal/usage/persistence_test.go`: snapshot save/load and missing-file behavior tests.
- Create `internal/api/handlers/management/usage.go`: `/usage`, `/usage/export`, and `/usage/import` handlers.
- Create `internal/api/handlers/management/usage_test.go`: usage management response and import validation tests.
- Modify `internal/api/handlers/management/handler.go`: add `usageStats *usage.RequestStatistics` and setter.
- Modify `internal/api/server.go`: add usage routes and hot-reload usage toggle wiring.
- Modify `cmd/server/main.go`: initialize usage toggle and optional `USAGE_STATS_FILE` persistence.
- Modify `sdk/cliproxy/service.go`: blank-import `internal/usage` so embedded SDK service users get the restored plugin.
- Modify `config.example.yaml`: document `USAGE_STATS_FILE` for Render persistence.
- Deployment asset action: copy `C:\Users\xwk\Downloads\management.html` to runtime `management.html` and set `remote-management.disable-auto-update-panel: true` in deployed config.

---

### Task 1: Restore the in-memory usage aggregation module

**Files:**
- Create: `internal/usage/logger_plugin.go`
- Create: `internal/usage/logger_plugin_test.go`

- [ ] **Step 1: Write the failing tests for legacy aggregation behavior**

Create `internal/usage/logger_plugin_test.go` with:

```go
package usage

import (
    "context"
    "testing"
    "time"

    coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestRequestStatisticsRecordIncludesLatencyAndTokens(t *testing.T) {
    prev := StatisticsEnabled()
    SetStatisticsEnabled(true)
    t.Cleanup(func() { SetStatisticsEnabled(prev) })

    stats := NewRequestStatistics()
    stats.Record(context.Background(), coreusage.Record{
        APIKey:      "test-key",
        Model:       "gpt-5.4",
        Source:      "user@example.com",
        AuthIndex:   "2",
        RequestedAt: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
        Latency:     1500 * time.Millisecond,
        Detail: coreusage.Detail{
            InputTokens:     10,
            OutputTokens:    20,
            ReasoningTokens: 3,
            CachedTokens:    4,
            TotalTokens:     37,
        },
    })

    snapshot := stats.Snapshot()
    if snapshot.TotalRequests != 1 {
        t.Fatalf("total requests = %d, want 1", snapshot.TotalRequests)
    }
    if snapshot.SuccessCount != 1 {
        t.Fatalf("success count = %d, want 1", snapshot.SuccessCount)
    }
    if snapshot.TotalTokens != 37 {
        t.Fatalf("total tokens = %d, want 37", snapshot.TotalTokens)
    }

    apiSnapshot := snapshot.APIs["test-key"]
    modelSnapshot := apiSnapshot.Models["gpt-5.4"]
    if modelSnapshot.TotalRequests != 1 {
        t.Fatalf("model requests = %d, want 1", modelSnapshot.TotalRequests)
    }
    if len(modelSnapshot.Details) != 1 {
        t.Fatalf("details len = %d, want 1", len(modelSnapshot.Details))
    }

    detail := modelSnapshot.Details[0]
    if detail.LatencyMs != 1500 {
        t.Fatalf("latency_ms = %d, want 1500", detail.LatencyMs)
    }
    if detail.Source != "user@example.com" || detail.AuthIndex != "2" {
        t.Fatalf("source/auth index = %q/%q, want user@example.com/2", detail.Source, detail.AuthIndex)
    }
    if detail.Tokens.InputTokens != 10 || detail.Tokens.OutputTokens != 20 || detail.Tokens.ReasoningTokens != 3 || detail.Tokens.CachedTokens != 4 || detail.Tokens.TotalTokens != 37 {
        t.Fatalf("unexpected tokens: %+v", detail.Tokens)
    }
}

func TestRequestStatisticsRecordRespectsDisabledSwitch(t *testing.T) {
    prev := StatisticsEnabled()
    SetStatisticsEnabled(false)
    t.Cleanup(func() { SetStatisticsEnabled(prev) })

    stats := NewRequestStatistics()
    stats.Record(context.Background(), coreusage.Record{
        APIKey:      "test-key",
        Model:       "gpt-5.4",
        RequestedAt: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
        Detail:      coreusage.Detail{InputTokens: 1, OutputTokens: 2},
    })

    snapshot := stats.Snapshot()
    if snapshot.TotalRequests != 0 {
        t.Fatalf("total requests = %d, want 0", snapshot.TotalRequests)
    }
}

func TestRequestStatisticsMergeSnapshotDeduplicatesDetails(t *testing.T) {
    prev := StatisticsEnabled()
    SetStatisticsEnabled(true)
    t.Cleanup(func() { SetStatisticsEnabled(prev) })

    stats := NewRequestStatistics()
    timestamp := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
    snapshot := StatisticsSnapshot{
        APIs: map[string]APISnapshot{
            "test-key": {
                Models: map[string]ModelSnapshot{
                    "gpt-5.4": {
                        Details: []RequestDetail{{
                            Timestamp: timestamp,
                            LatencyMs: 2500,
                            Source:    "user@example.com",
                            AuthIndex: "0",
                            Tokens: TokenStats{
                                InputTokens:  10,
                                OutputTokens: 20,
                                TotalTokens:  30,
                            },
                        }},
                    },
                },
            },
        },
    }

    first := stats.MergeSnapshot(snapshot)
    if first.Added != 1 || first.Skipped != 0 {
        t.Fatalf("first merge = %+v, want added=1 skipped=0", first)
    }
    second := stats.MergeSnapshot(snapshot)
    if second.Added != 0 || second.Skipped != 1 {
        t.Fatalf("second merge = %+v, want added=0 skipped=1", second)
    }
    got := stats.Snapshot()
    if got.TotalRequests != 1 || got.TotalTokens != 30 {
        t.Fatalf("snapshot totals = requests %d tokens %d, want 1/30", got.TotalRequests, got.TotalTokens)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail before implementation**

Run:

```powershell
go test ./internal/usage
```

Expected: FAIL because `internal/usage` does not exist or exported symbols such as `NewRequestStatistics` are undefined.

- [ ] **Step 3: Restore the legacy implementation from tag `v6.9.49`**

Run:

```powershell
New-Item -ItemType Directory -Force internal\usage | Out-Null
git show v6.9.49:internal/usage/logger_plugin.go | Set-Content -Encoding UTF8 internal\usage\logger_plugin.go
gofmt -w internal\usage\logger_plugin.go internal\usage\logger_plugin_test.go
```

The restored file must contain:

```go
func SetStatisticsEnabled(enabled bool)
func StatisticsEnabled() bool
func GetRequestStatistics() *RequestStatistics
func NewRequestStatistics() *RequestStatistics
func (s *RequestStatistics) Record(ctx context.Context, record coreusage.Record)
func (s *RequestStatistics) Snapshot() StatisticsSnapshot
func (s *RequestStatistics) MergeSnapshot(snapshot StatisticsSnapshot) MergeResult
```

- [ ] **Step 4: Run tests to verify aggregation passes**

Run:

```powershell
go test ./internal/usage
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```powershell
git add internal/usage/logger_plugin.go internal/usage/logger_plugin_test.go
git commit -m "feat: restore usage statistics aggregator"
```

---

### Task 2: Restore management usage API handlers and routes

**Files:**
- Create: `internal/api/handlers/management/usage.go`
- Create: `internal/api/handlers/management/usage_test.go`
- Modify: `internal/api/handlers/management/handler.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write failing handler tests**

Create `internal/api/handlers/management/usage_test.go` with:

```go
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
                                Tokens: usage.TokenStats{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
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
```

- [ ] **Step 2: Run tests to verify missing handler failures**

Run:

```powershell
go test ./internal/api/handlers/management -run Usage -count=1
```

Expected: FAIL because `GetUsageStatistics`, `ImportUsageStatistics`, `usageImportPayload`, and `SetUsageStatistics` are undefined.

- [ ] **Step 3: Restore usage handlers**

Create `internal/api/handlers/management/usage.go` with:

```go
package management

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type usageExportPayload struct {
    Version    int                      `json:"version"`
    ExportedAt time.Time                `json:"exported_at"`
    Usage      usage.StatisticsSnapshot `json:"usage"`
}

type usageImportPayload struct {
    Version int                      `json:"version"`
    Usage   usage.StatisticsSnapshot `json:"usage"`
}

func (h *Handler) GetUsageStatistics(c *gin.Context) {
    var snapshot usage.StatisticsSnapshot
    if h != nil && h.usageStats != nil {
        snapshot = h.usageStats.Snapshot()
    }
    c.JSON(http.StatusOK, gin.H{"usage": snapshot, "failed_requests": snapshot.FailureCount})
}

func (h *Handler) ExportUsageStatistics(c *gin.Context) {
    var snapshot usage.StatisticsSnapshot
    if h != nil && h.usageStats != nil {
        snapshot = h.usageStats.Snapshot()
    }
    c.JSON(http.StatusOK, usageExportPayload{Version: 1, ExportedAt: time.Now().UTC(), Usage: snapshot})
}

func (h *Handler) ImportUsageStatistics(c *gin.Context) {
    if h == nil || h.usageStats == nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
        return
    }
    data, err := c.GetRawData()
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
        return
    }
    var payload usageImportPayload
    if err := json.Unmarshal(data, &payload); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
        return
    }
    if payload.Version != 0 && payload.Version != 1 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported version"})
        return
    }
    result := h.usageStats.MergeSnapshot(payload.Usage)
    snapshot := h.usageStats.Snapshot()
    c.JSON(http.StatusOK, gin.H{"added": result.Added, "skipped": result.Skipped, "total_requests": snapshot.TotalRequests, "failed_requests": snapshot.FailureCount})
}
```

- [ ] **Step 4: Wire usage stats into the management handler**

Modify `internal/api/handlers/management/handler.go`:

```go
import "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
```

Add field:

```go
usageStats *usage.RequestStatistics
```

Set in `NewHandler`:

```go
usageStats: usage.GetRequestStatistics(),
```

Add method:

```go
func (h *Handler) SetUsageStatistics(stats *usage.RequestStatistics) {
    if h == nil {
        return
    }
    h.mu.Lock()
    h.usageStats = stats
    h.mu.Unlock()
}
```

- [ ] **Step 5: Wire management routes and hot-reload toggle**

Modify `internal/api/server.go`:

```go
import "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
```

Add routes:

```go
mgmt.GET("/usage", s.mgmt.GetUsageStatistics)
mgmt.GET("/usage/export", s.mgmt.ExportUsageStatistics)
mgmt.POST("/usage/import", s.mgmt.ImportUsageStatistics)
```

Update usage toggle block:

```go
usage.SetStatisticsEnabled(cfg.UsageStatisticsEnabled)
redisqueue.SetUsageStatisticsEnabled(cfg.UsageStatisticsEnabled)
```

- [ ] **Step 6: Format and run handler tests**

Run:

```powershell
gofmt -w internal\api\handlers\management\handler.go internal\api\handlers\management\usage.go internal\api\handlers\management\usage_test.go internal\api\server.go
go test ./internal/api/handlers/management -run Usage -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```powershell
git add internal/api/handlers/management/handler.go internal/api/handlers/management/usage.go internal/api/handlers/management/usage_test.go internal/api/server.go
git commit -m "feat: restore usage management API"
```

---

### Task 3: Register the restored usage plugin in server and SDK startup

**Files:**
- Modify: `cmd/server/main.go`
- Modify: `sdk/cliproxy/service.go`

- [ ] **Step 1: Run compile-focused package tests before wiring**

Run:

```powershell
go test ./cmd/server ./sdk/cliproxy
```

Expected before implementation: FAIL or compile error if `internal/usage` is referenced by server code from Task 2 but not imported consistently.

- [ ] **Step 2: Add startup toggle wiring in `cmd/server/main.go`**

Add import:

```go
"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
```

After:

```go
redisqueue.SetUsageStatisticsEnabled(cfg.UsageStatisticsEnabled)
```

add:

```go
usage.SetStatisticsEnabled(cfg.UsageStatisticsEnabled)
```

- [ ] **Step 3: Add blank import in `sdk/cliproxy/service.go`**

In the import block, add:

```go
_ "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
```

- [ ] **Step 4: Run package tests**

Run:

```powershell
gofmt -w cmd\server\main.go sdk\cliproxy\service.go
go test ./cmd/server ./sdk/cliproxy ./internal/usage ./internal/api/handlers/management
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```powershell
git add cmd/server/main.go sdk/cliproxy/service.go
git commit -m "feat: enable usage statistics plugin at startup"
```

---

### Task 4: Add optional file persistence for Render deployments

**Files:**
- Create: `internal/usage/persistence.go`
- Create: `internal/usage/persistence_test.go`
- Modify: `cmd/server/main.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Write failing persistence tests**

Create `internal/usage/persistence_test.go` with:

```go
package usage

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestSaveAndLoadSnapshotFile(t *testing.T) {
    path := filepath.Join(t.TempDir(), "usage.json")
    stats := NewRequestStatistics()
    stats.MergeSnapshot(StatisticsSnapshot{APIs: map[string]APISnapshot{"test-key": {Models: map[string]ModelSnapshot{"gpt-5.4": {Details: []RequestDetail{{Timestamp: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC), Tokens: TokenStats{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}}}}}})

    if err := SaveSnapshotFile(path, stats); err != nil {
        t.Fatalf("SaveSnapshotFile() error = %v", err)
    }

    loaded := NewRequestStatistics()
    result, err := LoadSnapshotFile(path, loaded)
    if err != nil {
        t.Fatalf("LoadSnapshotFile() error = %v", err)
    }
    if result.Added != 1 || result.Skipped != 0 {
        t.Fatalf("load result = %+v, want added=1 skipped=0", result)
    }
    snapshot := loaded.Snapshot()
    if snapshot.TotalRequests != 1 || snapshot.TotalTokens != 3 {
        t.Fatalf("loaded totals = %d/%d, want 1/3", snapshot.TotalRequests, snapshot.TotalTokens)
    }
}

func TestLoadSnapshotFileMissingIsNoop(t *testing.T) {
    stats := NewRequestStatistics()
    result, err := LoadSnapshotFile(filepath.Join(t.TempDir(), "missing.json"), stats)
    if err != nil {
        t.Fatalf("LoadSnapshotFile() missing error = %v", err)
    }
    if result.Added != 0 || result.Skipped != 0 {
        t.Fatalf("missing result = %+v, want zero", result)
    }
}

func TestStartSnapshotPersistenceSavesOnCancel(t *testing.T) {
    path := filepath.Join(t.TempDir(), "usage.json")
    stats := NewRequestStatistics()
    stats.MergeSnapshot(StatisticsSnapshot{APIs: map[string]APISnapshot{"test-key": {Models: map[string]ModelSnapshot{"gpt-5.4": {Details: []RequestDetail{{Timestamp: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC), Tokens: TokenStats{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}}}}}})

    ctx, cancel := context.WithCancel(context.Background())
    done := StartSnapshotPersistence(ctx, path, stats, time.Hour)
    cancel()

    select {
    case <-done:
    case <-time.After(2 * time.Second):
        t.Fatal("persistence did not stop after cancel")
    }

    if _, err := os.Stat(path); err != nil {
        t.Fatalf("snapshot file not written on cancel: %v", err)
    }
}
```

- [ ] **Step 2: Run tests to verify missing persistence functions**

Run:

```powershell
go test ./internal/usage -run Snapshot -count=1
```

Expected: FAIL because `SaveSnapshotFile`, `LoadSnapshotFile`, and `StartSnapshotPersistence` are undefined.

- [ ] **Step 3: Implement `internal/usage/persistence.go`**

Create `internal/usage/persistence.go` with:

```go
package usage

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"

    log "github.com/sirupsen/logrus"
)

type SnapshotFilePayload struct {
    Version    int                `json:"version"`
    ExportedAt time.Time          `json:"exported_at"`
    Usage      StatisticsSnapshot `json:"usage"`
}

func SaveSnapshotFile(path string, stats *RequestStatistics) error {
    path = strings.TrimSpace(path)
    if path == "" || stats == nil {
        return nil
    }
    payload := SnapshotFilePayload{Version: 1, ExportedAt: time.Now().UTC(), Usage: stats.Snapshot()}
    data, err := json.MarshalIndent(payload, "", "  ")
    if err != nil {
        return fmt.Errorf("marshal usage snapshot: %w", err)
    }
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return fmt.Errorf("create usage snapshot directory: %w", err)
    }
    tmp, err := os.CreateTemp(dir, ".usage-*.tmp")
    if err != nil {
        return fmt.Errorf("create usage snapshot temp file: %w", err)
    }
    tmpName := tmp.Name()
    defer func() {
        if errRemove := os.Remove(tmpName); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
            log.WithError(errRemove).Debug("failed to remove temporary usage snapshot file")
        }
    }()
    if _, err := tmp.Write(data); err != nil {
        _ = tmp.Close()
        return fmt.Errorf("write usage snapshot temp file: %w", err)
    }
    if err := tmp.Close(); err != nil {
        return fmt.Errorf("close usage snapshot temp file: %w", err)
    }
    if err := os.Rename(tmpName, path); err != nil {
        return fmt.Errorf("replace usage snapshot file: %w", err)
    }
    return nil
}

func LoadSnapshotFile(path string, stats *RequestStatistics) (MergeResult, error) {
    var zero MergeResult
    path = strings.TrimSpace(path)
    if path == "" || stats == nil {
        return zero, nil
    }
    data, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return zero, nil
        }
        return zero, fmt.Errorf("read usage snapshot file: %w", err)
    }
    var payload SnapshotFilePayload
    if err := json.Unmarshal(data, &payload); err != nil {
        return zero, fmt.Errorf("parse usage snapshot file: %w", err)
    }
    if payload.Version != 0 && payload.Version != 1 {
        return zero, fmt.Errorf("unsupported usage snapshot version %d", payload.Version)
    }
    return stats.MergeSnapshot(payload.Usage), nil
}

func StartSnapshotPersistence(ctx context.Context, path string, stats *RequestStatistics, interval time.Duration) <-chan struct{} {
    done := make(chan struct{})
    path = strings.TrimSpace(path)
    if ctx == nil {
        ctx = context.Background()
    }
    if path == "" || stats == nil {
        close(done)
        return done
    }
    if interval <= 0 {
        interval = 5 * time.Minute
    }
    go func() {
        defer close(done)
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                if err := SaveSnapshotFile(path, stats); err != nil {
                    log.WithError(err).Warn("failed to save usage snapshot during shutdown")
                }
                return
            case <-ticker.C:
                if err := SaveSnapshotFile(path, stats); err != nil {
                    log.WithError(err).Warn("failed to save usage snapshot")
                }
            }
        }
    }()
    return done
}
```

- [ ] **Step 4: Wire persistence into `cmd/server/main.go`**

Add after env parsing starts:

```go
usageStatsFile := strings.TrimSpace(os.Getenv("USAGE_STATS_FILE"))
```

After `usage.SetStatisticsEnabled(cfg.UsageStatisticsEnabled)`, add:

```go
if usageStatsFile != "" {
    if result, errLoadUsage := usage.LoadSnapshotFile(usageStatsFile, usage.GetRequestStatistics()); errLoadUsage != nil {
        log.WithError(errLoadUsage).Warn("failed to load usage statistics snapshot")
    } else if result.Added > 0 || result.Skipped > 0 {
        log.Infof("usage statistics snapshot loaded: added=%d skipped=%d", result.Added, result.Skipped)
    }
}
```

Before each service start call, add:

```go
if usageStatsFile != "" {
    ctxUsage, cancelUsage := context.WithCancel(context.Background())
    doneUsage := usage.StartSnapshotPersistence(ctxUsage, usageStatsFile, usage.GetRequestStatistics(), 5*time.Minute)
    defer func() {
        cancelUsage()
        <-doneUsage
    }()
}
```

Apply this before `cmd.StartServiceBackground(...)` and before `cmd.StartService(...)`.

- [ ] **Step 5: Document the Render env var in `config.example.yaml`**

Above `usage-statistics-enabled`, add:

```yaml
# For cloud deployments with ephemeral filesystems, set USAGE_STATS_FILE to a path on persistent storage
# such as /data/cliproxy/usage.json to preserve this in-memory snapshot across restarts.
```

- [ ] **Step 6: Run persistence tests and targeted packages**

Run:

```powershell
gofmt -w internal\usage\persistence.go internal\usage\persistence_test.go cmd\server\main.go
go test ./internal/usage -run Snapshot -count=1
go test ./cmd/server ./internal/usage
```

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```powershell
git add internal/usage/persistence.go internal/usage/persistence_test.go cmd/server/main.go config.example.yaml
git commit -m "feat: persist usage statistics snapshots"
```

---

### Task 5: Verify management panel compatibility and deployment asset behavior

**Files:**
- Runtime asset input: `C:\Users\xwk\Downloads\management.html`
- Runtime target: `managementasset.FilePath(configFilePath)`, usually `<config-dir>/static/management.html` or a writable-path static directory.
- Create: `docs/superpowers/plans/2026-05-02-restore-usage-stats-deploy-notes.md`

- [ ] **Step 1: Confirm the supplied panel calls restored endpoints**

Run:

```powershell
python -c "from pathlib import Path; text=Path(r'C:\Users\xwk\Downloads\management.html').read_text(encoding='utf-8', errors='replace'); [print(endpoint, endpoint in text) for endpoint in ['/usage','/usage/export','/usage/import']]"
```

Expected output:

```text
/usage True
/usage/export True
/usage/import True
```

- [ ] **Step 2: Add deployment instructions to a local note file**

Create `docs/superpowers/plans/2026-05-02-restore-usage-stats-deploy-notes.md` with:

````markdown
# Usage Stats Panel Deployment Notes

For the custom CLIProxyAPI build that restores `/v0/management/usage`, use the supplied panel:

- Source: `C:\Users\xwk\Downloads\management.html`
- Runtime target: the `management.html` file returned by `managementasset.FilePath(configFilePath)`.

Recommended Render config:

```yaml
remote-management:
  allow-remote: true
  secret-key: "<set through MANAGEMENT_PASSWORD or your config secret>"
  disable-control-panel: false
  disable-auto-update-panel: true
```

Recommended Render environment:

```text
MANAGEMENT_PASSWORD=<strong password>
USAGE_STATS_FILE=/data/cliproxy/usage.json
```

If using a persistent disk, mount it at `/data` and keep `USAGE_STATS_FILE` under `/data`.
````

- [ ] **Step 3: Commit Task 5**

```powershell
git add -f docs/superpowers/plans/2026-05-02-restore-usage-stats-deploy-notes.md
git commit -m "docs: add usage stats deployment notes"
```

---

### Task 6: Final verification

**Files:**
- Verify all files changed by Tasks 1-5.

- [ ] **Step 1: Run targeted tests**

```powershell
go test ./internal/usage ./internal/api/handlers/management ./cmd/server ./sdk/cliproxy
```

Expected: PASS.

- [ ] **Step 2: Run full test suite if time allows**

```powershell
go test ./...
```

Expected: PASS. If the full suite fails in an unrelated long-running or environment-dependent package, capture the failing package and exact error before proceeding.

- [ ] **Step 3: Build the server binary**

```powershell
go build -o test-output.exe ./cmd/server
Remove-Item -LiteralPath .\test-output.exe -Force
```

Expected: build exits 0 and `test-output.exe` is removed.

- [ ] **Step 4: Inspect final diff**

```powershell
git status --short
git log --oneline -6
git diff origin/main...HEAD --stat
```

Expected:

- Only planned usage/statistics/docs files are changed.
- Working tree is clean after commits.
- Recent commits include the design, plan, and feature commits.

- [ ] **Step 5: Final summary for the user**

Report:

- Restored endpoints.
- Whether targeted tests passed.
- Whether full test suite passed or which package failed.
- Build verification result.
- Render env/config needed:
  - `MANAGEMENT_PASSWORD`
  - `USAGE_STATS_FILE=/data/cliproxy/usage.json`
  - `usage-statistics-enabled: true`
  - `remote-management.disable-auto-update-panel: true`
```
