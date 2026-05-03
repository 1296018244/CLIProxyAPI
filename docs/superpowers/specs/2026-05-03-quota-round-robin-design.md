# Quota-Aware Round-Robin Routing

Date: 2026-05-03
Repository: CLIProxyAPI
Status: Design approved for planning

## Goal

Add a routing strategy that keeps round-robin behavior but orders credentials by the refreshed quota percentage shown in quota inspection UIs, such as Codex weekly quota remaining percent. Accounts with more remaining quota should be tried earlier than accounts with less remaining quota.

The target operator behavior is:

```yaml
routing:
  strategy: "quota-round-robin"
```

When enabled, a set of available credentials with weekly remaining quotas `94%, 94%, 81%, 17%, 0%` should rotate in that descending quota order and then repeat.

## Current State

Credential selection currently lives mainly in:

- `sdk/cliproxy/auth/selector.go`
- `sdk/cliproxy/auth/scheduler.go`
- `sdk/cliproxy/builder.go`
- `sdk/cliproxy/service.go`
- `internal/api/handlers/management/config_basic.go`

Current `round-robin` behavior:

1. Filters unavailable, disabled, and cooling credentials.
2. Groups by numeric `priority` from auth attributes.
3. Selects the highest priority bucket.
4. Sorts that bucket by auth ID/name and round-robins through it.

Current `QuotaState` tracks cooldown/error state, not refreshed remaining quota percentages. The quota percentage shown by external quota inspector UIs may be stored in auth metadata/attributes after refresh, but the routing code does not read it yet.

## Recommended Approach

Add a new built-in strategy named `quota-round-robin` instead of changing the existing `round-robin` semantics.

This keeps existing deployments stable while allowing operators to opt into quota-aware ordering.

Supported strategy aliases:

- `quota-round-robin`
- `quota-roundrobin`
- `quota-rr`
- `qrr`

## Routing Semantics

For each provider/model shard:

1. Keep the existing availability filter unchanged.
2. Keep existing numeric `priority` semantics unchanged: the highest priority bucket wins first.
3. Within the selected priority bucket, sort ready credentials by quota score:
   - Known quota remaining percent first.
   - Higher remaining percent before lower remaining percent.
   - Equal remaining percent falls back to auth ID for stable ordering.
   - Unknown quota percent sorts after known quota percent.
4. Round-robin through that sorted view.
5. If no auth has quota information, behavior becomes equivalent to current ID-sorted round-robin.

For Gemini CLI virtual auth grouping, preserve the existing parent-first grouping model. The parent/child item order should be quota-sorted inside each relevant list so grouping behavior stays intact while higher-quota credentials are favored.

For session affinity, use the quota-aware selector as the fallback selector. Existing sticky-session behavior still wins when a session binding is valid.

## Quota Score Extraction

Add a helper in `sdk/cliproxy/auth` that extracts a remaining quota percentage from auth attributes or metadata.

The helper should prefer explicit remaining-percent fields, then derive remaining percent from used-percent fields when needed.

Candidate sources, checked in this order:

1. Nested weekly quota windows, because they are the value shown as Codex weekly quota:
   - `quota.windows[].id == "code-7d"` with `remaining_percent` or `remainingPercent`
   - `quota.windows[].label == "7d"` with `remaining_percent` or `remainingPercent`
   - `windows[].id == "code-7d"` with `remaining_percent` or `remainingPercent`
   - `windows[].label == "7d"` with `remaining_percent` or `remainingPercent`
2. Direct auth attributes/metadata, treated as remaining percent:
   - `quota_remaining_percent`
   - `remaining_percent`
   - `codex_quota_remaining_percent`
   - `quota_percent`
3. Used-percent fallback from the same locations:
   - `used_percent`
   - `usedPercent`

Clamp all parsed values to `[0, 100]`.

If a direct field looks like a fraction (`0.94`) rather than a percent (`94`), treat values in `[0, 1]` as fractions and multiply by 100.

## Configuration and Management API

Update config docs and management normalization to accept `quota-round-robin`.

Affected places:

- `internal/config/config.go`: comment should list the new strategy.
- `config.example.yaml`: route strategy comment should list the new option.
- `internal/api/handlers/management/config_basic.go`: normalize and validate the new aliases.
- `sdk/cliproxy/builder.go`: instantiate the new selector.
- `sdk/cliproxy/service.go`: handle hot reload from/to the new selector.

## Scheduler Changes

The scheduler fast path has its own built-in strategy enum, so it also needs quota-aware semantics.

Add a scheduler strategy value for quota round-robin.

When rebuilding ready views, sort entries with a strategy-aware comparator:

- fill-first: existing ID ordering.
- round-robin: existing ID ordering.
- quota-round-robin: quota comparator, then ID ordering.

Preserve cursor state across rebuilds as currently implemented.

## Error Handling and Fallbacks

- Invalid quota values are ignored and treated as unknown.
- Missing quota metadata should not make an auth unavailable.
- Unknown quota values sort after known values but still participate in rotation.
- Existing cooldown, disabled, model availability, pinned auth, websocket preference, and retry behavior remain unchanged.
- Do not log raw auth metadata or tokens.

## Testing Plan

Unit tests in `sdk/cliproxy/auth`:

- `QuotaRoundRobinSelector` selects known higher remaining percent before lower percent.
- Equal percentages fall back to stable ID order.
- Unknown percentages sort after known percentages.
- `used_percent` fallback derives remaining percent as `100 - used_percent`.
- Nested `quota.windows` with `code-7d` is preferred over direct generic values when applicable.
- Existing priority buckets still dominate quota ordering.
- Scheduler fast path matches selector behavior.
- Strategy normalization accepts aliases and rejects invalid strategy names.

Verification commands:

```powershell
go test ./sdk/cliproxy/auth -run "Quota|RoundRobin|Scheduler" -count=1
go test ./internal/api/handlers/management -run RoutingStrategy -count=1
gofmt -w .
go build -o test-output.exe ./cmd/server
Remove-Item -LiteralPath .\test-output.exe -Force
```

## Non-Goals

- Do not implement a new quota refresh/scraping UI in this change.
- Do not remove or change existing `round-robin` behavior.
- Do not persist new secrets or raw quota API payloads.
- Do not change provider translators.
- Do not force quota-aware routing for users who do not opt in.

## Rollout

1. Add tests for quota extraction and selector ordering.
2. Add the new selector and scheduler strategy.
3. Wire config, management normalization, builder, and hot reload.
4. Run targeted tests and compile verification.
5. Enable with `routing.strategy: "quota-round-robin"` after quota refresh metadata is present.

