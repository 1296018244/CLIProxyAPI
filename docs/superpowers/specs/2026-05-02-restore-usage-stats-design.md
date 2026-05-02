# Restore Built-in Usage Statistics

Date: 2026-05-02
Repository: CLIProxyAPI
Status: Design approved for planning

## Goal

Restore the full built-in usage statistics experience that the bundled/legacy management UI expects, including dashboard summaries, the detailed `/usage` page, request events, token totals, model breakdowns, import/export, and model price estimation support in the frontend.

The immediate compatibility target is the supplied `C:\Users\xwk\Downloads\management.html`, which calls these management endpoints:

- `GET /v0/management/usage`
- `GET /v0/management/usage/export`
- `POST /v0/management/usage/import`
- Existing `GET/PUT/PATCH /v0/management/usage-statistics-enabled`

## Current State

The current branch still emits runtime usage records through `sdk/cliproxy/usage` and includes lightweight queue support in `internal/redisqueue`, but it no longer exposes the legacy aggregated usage API consumed by the management UI.

The latest backend currently has:

- `usage-statistics-enabled` config and management toggle.
- A usage plugin path for queue-style events.
- `/v0/management/api-key-usage` for recent API-key request buckets.

It is missing:

- The `internal/usage` in-memory aggregation module from `v6.9.49`.
- `Handler.usageStats` integration.
- `/v0/management/usage`, `/usage/export`, and `/usage/import` routes.

## Recommended Approach

Restore the old in-process statistics module from `v6.9.49` and wire it into the current runtime as a normal `sdk/cliproxy/usage.Plugin`.

This is preferred over rebuilding the feature from `redisqueue` because:

- The legacy management UI already expects the old snapshot shape.
- The old module consumes the current `sdk/cliproxy/usage.Record` type, so integration remains small.
- It avoids coupling browser statistics to the transient Redis-like queue retention window.

## Architecture

### 1. Usage Aggregation Module

Reintroduce `internal/usage` with a shared `RequestStatistics` store.

Responsibilities:

- Register a usage plugin at package initialization.
- Respect `usage-statistics-enabled` through `SetStatisticsEnabled`.
- Aggregate every `sdk/cliproxy/usage.Record` into:
  - total requests
  - success count
  - failure count
  - total tokens
  - per API/account/source stats
  - per model stats
  - per-request details
  - requests/tokens by day and hour
- Preserve token breakdown fields:
  - `input_tokens`
  - `output_tokens`
  - `reasoning_tokens`
  - `cached_tokens`
  - `total_tokens`
- Preserve request metadata needed by the UI:
  - timestamp
  - latency
  - source
  - auth index
  - failed flag

### 2. Management API

Add back `internal/api/handlers/management/usage.go` with:

- `GetUsageStatistics`
- `ExportUsageStatistics`
- `ImportUsageStatistics`

Response contract:

```json
{
  "usage": {
    "total_requests": 0,
    "success_count": 0,
    "failure_count": 0,
    "total_tokens": 0,
    "apis": {},
    "requests_by_day": {},
    "requests_by_hour": {},
    "tokens_by_day": {},
    "tokens_by_hour": {}
  },
  "failed_requests": 0
}
```

Export/import should use versioned JSON payloads so old backups remain compatible:

```json
{
  "version": 1,
  "exported_at": "2026-05-02T00:00:00Z",
  "usage": {}
}
```

### 3. Route Wiring

In `internal/api/server.go`, restore management routes under the existing management middleware:

- `GET /v0/management/usage`
- `GET /v0/management/usage/export`
- `POST /v0/management/usage/import`

Also update config hot-reload to call `usage.SetStatisticsEnabled(cfg.UsageStatisticsEnabled)` in addition to any queue toggle that should remain.

### 4. Handler State

Update `internal/api/handlers/management/handler.go` to hold a `*usage.RequestStatistics` reference initialized with `usage.GetRequestStatistics()`.

Expose a setter for tests:

```go
func (h *Handler) SetUsageStatistics(stats *usage.RequestStatistics)
```

### 5. Management Panel Asset

The supplied `management.html` should be used as the management panel asset for this custom build. To avoid the official panel overwriting it, deployment config should disable panel auto-update or point `panel-github-repository` to the custom panel release source.

Implementation can use either of these deployment choices:

- Bundle the supplied HTML into the custom image/build output.
- Mount/copy it as the management panel asset at startup.

The backend API compatibility is the critical part; the panel asset replacement can be handled as a deployment patch after API tests pass.

### 6. Render Persistence

The restored legacy module is in-memory. For Render, that means usage data resets after restart unless a persistence path is added.

The implementation should keep persistence small and reversible:

- Add an optional usage snapshot path controlled by env/config, for example `USAGE_STATS_FILE=/data/cliproxy/usage.json`.
- On startup, if the file exists, import it into the shared statistics store.
- Periodically and on shutdown, export the snapshot atomically to the file.
- If no file is configured, preserve legacy in-memory behavior.

This keeps local/default operation simple while making Render deployments durable when paired with a persistent disk.

## Data Flow

1. A proxy request is executed by a provider executor.
2. The executor publishes a `sdk/cliproxy/usage.Record`.
3. The global usage manager dispatches the record to registered plugins.
4. `internal/usage.LoggerPlugin` records it into `RequestStatistics` when enabled.
5. The management UI calls `/v0/management/usage`.
6. The handler returns a snapshot in the legacy UI-compatible shape.
7. Optional persistence imports/exports the same snapshot shape.

## Error Handling

- If the usage store is unavailable, `/usage` should return an empty valid snapshot, not a server error.
- `/usage/import` should reject invalid JSON and unsupported versions with HTTP 400.
- Import should deduplicate request details so repeated imports do not inflate totals.
- Persistence failures should be logged without preventing the proxy from serving requests.
- Secrets and raw tokens must not be logged.

## Testing Plan

Unit tests:

- Recording one usage event increments totals and stores latency/token details.
- Failed events increment `failure_count` and set detail `failed=true`.
- Snapshot maps use the UI-compatible JSON field names.
- Import/export round trip preserves totals.
- Re-importing the same snapshot deduplicates details.
- `usage-statistics-enabled=false` suppresses aggregation.

Integration/handler tests:

- `GET /v0/management/usage` returns a valid empty snapshot.
- `GET /v0/management/usage/export` returns versioned payload.
- `POST /v0/management/usage/import` merges valid payload and rejects invalid payload.
- Management middleware continues to protect the routes.

Build verification:

- Run targeted Go tests for `internal/usage` and management handlers.
- Run `go build -o test-output ./cmd/server` and remove the binary afterwards.

## Non-Goals

- Do not scrape provider-side remaining quota from external websites.
- Do not replace the full management frontend source project.
- Do not remove the current `internal/redisqueue` usage queue.
- Do not change provider translators unless a usage record shape bug is proven.

## Rollout

1. Restore backend usage aggregation and API routes.
2. Run tests and compile verification.
3. Add or document panel asset replacement using `management.html`.
4. Add optional Render persistence if the backend compatibility tests are passing.
5. Deploy custom fork/image to Render with `usage-statistics-enabled: true`, a management password, and a persistent disk or `USAGE_STATS_FILE` path.
