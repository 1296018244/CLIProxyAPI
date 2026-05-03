# Quota-Aware Round-Robin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `quota-round-robin` routing strategy that round-robins credentials after sorting by refreshed remaining quota percentage, highest first.

**Architecture:** Add a new built-in auth selector strategy beside the existing round-robin and fill-first strategies. Keep availability filtering and priority buckets unchanged, but order ready credentials by quota score inside the selected bucket. Mirror the same ordering in the scheduler fast path so normal runtime routing and selector unit tests agree.

**Tech Stack:** Go 1.26, existing `sdk/cliproxy/auth` selector/scheduler, Gin management config handlers, YAML config comments, Go unit tests.

---

## File Structure

- Modify `sdk/cliproxy/auth/selector.go`: add `QuotaRoundRobinSelector`, quota percent extraction helpers, quota-aware sorting for selector path.
- Modify `sdk/cliproxy/auth/scheduler.go`: add scheduler strategy and quota-aware ready view ordering.
- Modify `sdk/cliproxy/auth/selector_test.go`: add selector-level quota ordering tests.
- Modify `sdk/cliproxy/auth/scheduler_test.go`: add scheduler fast-path quota ordering tests.
- Modify `sdk/cliproxy/builder.go`: instantiate the selector for `quota-round-robin` aliases.
- Modify `sdk/cliproxy/service.go`: normalize and hot-reload the new strategy.
- Modify `internal/api/handlers/management/config_basic.go`: accept the new strategy through management API.
- Modify `internal/api/handlers/management/config_basic_test.go`: add normalization coverage.
- Modify `internal/config/config.go`: document supported strategy values.
- Modify `config.example.yaml`: document the new strategy value.

---

### Task 1: Add failing selector tests for quota-aware ordering

**Files:**
- Modify: `sdk/cliproxy/auth/selector_test.go`

- [ ] **Step 1: Write failing tests**

Append these tests to `sdk/cliproxy/auth/selector_test.go`:

```go
func TestQuotaRoundRobinSelectorPick_OrdersByRemainingQuota(t *testing.T) {
	t.Parallel()

	selector := &QuotaRoundRobinSelector{}
	auths := []*Auth{
		{ID: "low", Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "remaining_percent": 17}}}}},
		{ID: "high-b", Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "remaining_percent": 94}}}}},
		{ID: "unknown"},
		{ID: "high-a", Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "remaining_percent": 94}}}}},
	}

	want := []string{"high-a", "high-b", "low", "unknown", "high-a"}
	for i, id := range want {
		got, err := selector.Pick(context.Background(), "codex", "gpt-5.1-codex", cliproxyexecutor.Options{}, auths)
		if err != nil {
			t.Fatalf("Pick() #%d error = %v", i, err)
		}
		if got == nil || got.ID != id {
			t.Fatalf("Pick() #%d auth.ID = %q, want %q", i, authID(got), id)
		}
	}
}

func TestQuotaRoundRobinSelectorPick_UsedPercentFallback(t *testing.T) {
	t.Parallel()

	selector := &QuotaRoundRobinSelector{}
	auths := []*Auth{
		{ID: "used-90", Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "used_percent": 90}}}}},
		{ID: "used-20", Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "used_percent": 20}}}}},
	}

	got, err := selector.Pick(context.Background(), "codex", "gpt-5.1-codex", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got == nil || got.ID != "used-20" {
		t.Fatalf("Pick() auth.ID = %q, want used-20", authID(got))
	}
}

func TestQuotaRoundRobinSelectorPick_PriorityOverridesQuota(t *testing.T) {
	t.Parallel()

	selector := &QuotaRoundRobinSelector{}
	auths := []*Auth{
		{ID: "low-priority-high-quota", Attributes: map[string]string{"priority": "0", "quota_remaining_percent": "100"}},
		{ID: "high-priority-low-quota", Attributes: map[string]string{"priority": "10", "quota_remaining_percent": "1"}},
	}

	got, err := selector.Pick(context.Background(), "codex", "gpt-5.1-codex", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got == nil || got.ID != "high-priority-low-quota" {
		t.Fatalf("Pick() auth.ID = %q, want high-priority-low-quota", authID(got))
	}
}

func authID(auth *Auth) string {
	if auth == nil {
		return ""
	}
	return auth.ID
}
```

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./sdk/cliproxy/auth -run QuotaRoundRobinSelector -count=1
```

Expected: FAIL because `QuotaRoundRobinSelector` is undefined.

- [ ] **Step 3: Commit tests after RED is observed**

No commit yet if the implementation will immediately follow in the same task cycle.

---

### Task 2: Implement quota-aware selector path

**Files:**
- Modify: `sdk/cliproxy/auth/selector.go`
- Modify: `sdk/cliproxy/auth/selector_test.go`

- [ ] **Step 1: Add selector type and helper functions**

In `sdk/cliproxy/auth/selector.go`, add `QuotaRoundRobinSelector` beside `RoundRobinSelector` and make it reuse the same cursor fields:

```go
// QuotaRoundRobinSelector round-robins credentials after sorting by remaining quota percentage.
type QuotaRoundRobinSelector struct {
	RoundRobinSelector
}
```

Add helper functions below `authPriority`:

```go
func authRemainingQuotaPercent(auth *Auth) (float64, bool) {
	if auth == nil {
		return 0, false
	}
	if value, ok := quotaPercentFromMap(auth.Metadata, true); ok {
		return value, true
	}
	attrMap := make(map[string]any, len(auth.Attributes))
	for key, value := range auth.Attributes {
		attrMap[key] = value
	}
	return quotaPercentFromMap(attrMap, true)
}

func quotaPercentFromMap(values map[string]any, allowDirect bool) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	for _, path := range [][]string{{"quota", "windows"}, {"windows"}} {
		if pct, ok := quotaPercentFromWindows(nestedValue(values, path...)); ok {
			return pct, true
		}
	}
	if allowDirect {
		for _, key := range []string{"quota_remaining_percent", "remaining_percent", "codex_quota_remaining_percent", "quota_percent"} {
			if pct, ok := parseQuotaPercent(values[key], true); ok {
				return pct, true
			}
		}
		for _, key := range []string{"used_percent", "usedPercent"} {
			if pct, ok := parseQuotaPercent(values[key], true); ok {
				return clampPercent(100 - pct), true
			}
		}
	}
	return 0, false
}

func quotaPercentFromWindows(raw any) (float64, bool) {
	items, ok := raw.([]any)
	if !ok {
		return 0, false
	}
	for _, item := range items {
		window, ok := item.(map[string]any)
		if !ok || !isWeeklyQuotaWindow(window) {
			continue
		}
		for _, key := range []string{"remaining_percent", "remainingPercent"} {
			if pct, ok := parseQuotaPercent(window[key], true); ok {
				return pct, true
			}
		}
		for _, key := range []string{"used_percent", "usedPercent"} {
			if pct, ok := parseQuotaPercent(window[key], true); ok {
				return clampPercent(100 - pct), true
			}
		}
	}
	return 0, false
}

func isWeeklyQuotaWindow(window map[string]any) bool {
	id := strings.TrimSpace(fmt.Sprint(window["id"]))
	label := strings.TrimSpace(fmt.Sprint(window["label"]))
	return strings.EqualFold(id, "code-7d") || strings.EqualFold(label, "7d")
}

func nestedValue(values map[string]any, path ...string) any {
	var current any = values
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[key]
	}
	return current
}

func parseQuotaPercent(raw any, fractionAllowed bool) (float64, bool) {
	switch value := raw.(type) {
	case nil:
		return 0, false
	case float64:
		return normalizeQuotaPercent(value, fractionAllowed), true
	case float32:
		return normalizeQuotaPercent(float64(value), fractionAllowed), true
	case int:
		return normalizeQuotaPercent(float64(value), fractionAllowed), true
	case int64:
		return normalizeQuotaPercent(float64(value), fractionAllowed), true
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, false
		}
		return normalizeQuotaPercent(parsed, fractionAllowed), true
	case string:
		trimmed := strings.TrimSpace(strings.TrimSuffix(value, "%"))
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return normalizeQuotaPercent(parsed, fractionAllowed), true
	default:
		return 0, false
	}
}

func normalizeQuotaPercent(value float64, fractionAllowed bool) float64 {
	if fractionAllowed && value >= 0 && value <= 1 {
		value *= 100
	}
	return clampPercent(value)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func sortAuthsByQuotaThenID(auths []*Auth) {
	sort.Slice(auths, func(i, j int) bool {
		leftQuota, leftOK := authRemainingQuotaPercent(auths[i])
		rightQuota, rightOK := authRemainingQuotaPercent(auths[j])
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && rightOK && leftQuota != rightQuota {
			return leftQuota > rightQuota
		}
		return auths[i].ID < auths[j].ID
	})
}
```

- [ ] **Step 2: Add quota-aware pick method**

In `sdk/cliproxy/auth/selector.go`, add:

```go
func (s *QuotaRoundRobinSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	return s.pick(ctx, provider, model, opts, auths, true)
}
```

Change `RoundRobinSelector.Pick` to call a shared method:

```go
func (s *RoundRobinSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	return s.pick(ctx, provider, model, opts, auths, false)
}
```

Move the current body into:

```go
func (s *RoundRobinSelector) pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth, quotaAware bool) (*Auth, error) {
	// current RoundRobinSelector.Pick body
}
```

Inside that body, after websocket preference and before cursor use, add:

```go
if quotaAware && len(available) > 1 {
	sortAuthsByQuotaThenID(available)
}
```

- [ ] **Step 3: Run GREEN test**

Run:

```powershell
gofmt -w sdk\cliproxy\auth\selector.go sdk\cliproxy\auth\selector_test.go
go test ./sdk/cliproxy/auth -run QuotaRoundRobinSelector -count=1
```

Expected: PASS.

---

### Task 3: Add scheduler fast-path quota ordering

**Files:**
- Modify: `sdk/cliproxy/auth/scheduler.go`
- Modify: `sdk/cliproxy/auth/scheduler_test.go`

- [ ] **Step 1: Write failing scheduler test**

Append this test to `sdk/cliproxy/auth/scheduler_test.go`:

```go
func TestSchedulerPick_QuotaRoundRobinOrdersByRemainingQuota(t *testing.T) {
	manager := NewManager(nil, &QuotaRoundRobinSelector{}, nil)
	manager.RegisterExecutor("codex", noopExecutor{})

	auths := []*Auth{
		{ID: "low", Provider: "codex", Status: StatusActive, Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "remaining_percent": 17}}}}},
		{ID: "high", Provider: "codex", Status: StatusActive, Metadata: map[string]any{"quota": map[string]any{"windows": []any{map[string]any{"id": "code-7d", "remaining_percent": 94}}}}},
	}
	for _, auth := range auths {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("Register(%s) error = %v", auth.ID, err)
		}
	}

	got, _, err := manager.pickNext(context.Background(), "codex", "", cliproxyexecutor.Options{}, nil)
	if err != nil {
		t.Fatalf("pickNext() error = %v", err)
	}
	if got == nil || got.ID != "high" {
		t.Fatalf("pickNext() auth.ID = %q, want high", authID(got))
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./sdk/cliproxy/auth -run TestSchedulerPick_QuotaRoundRobinOrdersByRemainingQuota -count=1
```

Expected before scheduler implementation: FAIL, because scheduler treats `QuotaRoundRobinSelector` as custom or does not sort by quota.

- [ ] **Step 3: Implement scheduler strategy**

In `sdk/cliproxy/auth/scheduler.go`:

Add enum value:

```go
schedulerStrategyQuotaRoundRobin
```

Update `selectorStrategy`:

```go
case *QuotaRoundRobinSelector:
	return schedulerStrategyQuotaRoundRobin
```

Update `isBuiltInSelector` in `sdk/cliproxy/auth/conductor.go`:

```go
case *RoundRobinSelector, *FillFirstSelector, *QuotaRoundRobinSelector:
	return true
```

Change `buildReadyBucket(entries)` to `buildReadyBucket(entries, strategy)` and `buildReadyView(entries)` to `buildReadyView(entries, strategy)`.

In `rebuildIndexesLocked`, pass the strategy from `modelScheduler`. Add a `strategy schedulerStrategy` field to `modelScheduler`, set it before rebuilding from `providerScheduler.ensureModelLocked`, and update it during `authScheduler.setSelector` rebuild.

For ready sorting, use:

```go
func sortScheduledAuths(entries []*scheduledAuth, strategy schedulerStrategy) {
	sort.Slice(entries, func(i, j int) bool {
		if strategy == schedulerStrategyQuotaRoundRobin {
			leftQuota, leftOK := authRemainingQuotaPercent(entries[i].auth)
			rightQuota, rightOK := authRemainingQuotaPercent(entries[j].auth)
			if leftOK != rightOK {
				return leftOK
			}
			if leftOK && rightOK && leftQuota != rightQuota {
				return leftQuota > rightQuota
			}
		}
		return entries[i].auth.ID < entries[j].auth.ID
	})
}
```

- [ ] **Step 4: Run scheduler GREEN test**

Run:

```powershell
gofmt -w sdk\cliproxy\auth\scheduler.go sdk\cliproxy\auth\conductor.go sdk\cliproxy\auth\scheduler_test.go
go test ./sdk/cliproxy/auth -run "QuotaRoundRobin|TestSchedulerPick_QuotaRoundRobin" -count=1
```

Expected: PASS.

---

### Task 4: Wire configuration and management strategy normalization

**Files:**
- Modify: `sdk/cliproxy/builder.go`
- Modify: `sdk/cliproxy/service.go`
- Modify: `internal/api/handlers/management/config_basic.go`
- Modify: `internal/api/handlers/management/config_basic_test.go`
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Write failing management normalization test**

Add to `internal/api/handlers/management/config_basic_test.go`:

```go
func TestNormalizeRoutingStrategyQuotaRoundRobin(t *testing.T) {
	for _, raw := range []string{"quota-round-robin", "quota-roundrobin", "quota-rr", "qrr"} {
		got, ok := normalizeRoutingStrategy(raw)
		if !ok {
			t.Fatalf("normalizeRoutingStrategy(%q) ok=false, want true", raw)
		}
		if got != "quota-round-robin" {
			t.Fatalf("normalizeRoutingStrategy(%q) = %q, want quota-round-robin", raw, got)
		}
	}
}
```

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/api/handlers/management -run TestNormalizeRoutingStrategyQuotaRoundRobin -count=1
```

Expected: FAIL because aliases are not accepted.

- [ ] **Step 3: Implement config wiring**

Update `normalizeRoutingStrategy` to return `quota-round-robin` for the aliases.

Update `sdk/cliproxy/builder.go` strategy switch:

```go
case "quota-round-robin", "quota-roundrobin", "quota-rr", "qrr":
	selector = &coreauth.QuotaRoundRobinSelector{}
```

Update `sdk/cliproxy/service.go` normalizeStrategy and selector switch similarly.

Update `internal/config/config.go` comment to list `quota-round-robin`.

Update `config.example.yaml` route strategy comment to list `quota-round-robin`.

- [ ] **Step 4: Run GREEN config tests**

Run:

```powershell
gofmt -w sdk\cliproxy\builder.go sdk\cliproxy\service.go internal\api\handlers\management\config_basic.go internal\api\handlers\management\config_basic_test.go internal\config\config.go
go test ./internal/api/handlers/management -run RoutingStrategy -count=1
```

Expected: PASS.

---

### Task 5: Final targeted verification

**Files:**
- All changed files.

- [ ] **Step 1: Run auth package tests**

```powershell
go test ./sdk/cliproxy/auth -run "Quota|RoundRobin|Scheduler" -count=1
```

Expected: PASS.

- [ ] **Step 2: Run management strategy tests**

```powershell
go test ./internal/api/handlers/management -run RoutingStrategy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run compile verification**

```powershell
go build -o test-output.exe ./cmd/server
Remove-Item -LiteralPath .\test-output.exe -Force
```

Expected: build exits 0 and test binary is removed.

- [ ] **Step 4: Inspect diff**

```powershell
git status --short
git diff --stat
```

Expected: only planned files changed.
