# 08 Observability, Run IDs, and Sync Events

## Intent

Cron logs and SQLite state should explain each sync run without leaking
transcript contents or secrets.

## Requirements

- Generate a run ID for every sync invocation.
- Include run ID in sync logs.
- Record append-only sync events for meaningful stages:
  - run started,
  - transcripts fetched,
  - transcript discovered,
  - classification failed/succeeded,
  - extraction failed/succeeded,
  - publish failed/succeeded,
  - state persistence failed/succeeded,
  - run completed.
- Store bounded metadata, not raw transcript content.
- Log final summary counts by stage.
- Dry-run output preview must stay bounded.

## Non-Goals

- Do not add external telemetry.
- Do not log full transcript text.
- Do not log API keys, OAuth tokens, or Confluence tokens.

## Likely Files

- `cmd/omniscient/sync.go`
- Sync service files if introduced.
- `internal/database/store.go`
- `internal/database/store_test.go`
- Logging/event tests where practical.

## Acceptance Tests

- Sync logs include a run ID.
- Event rows can be queried by run ID or transcript ID.
- Normal logs/events do not contain transcript content.
- Final summary counts are emitted.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/08-observability-events.md`.
Work only in the current repo. Do not use parent paths. Keep event metadata
bounded and safe.
