# 04 Transcript State Lifecycle

## Intent

Replace binary processed tracking with explicit, recoverable transcript state.

## Requirements

- Use bounded statuses such as:
  - `discovered`
  - `extracted`
  - `published`
  - `skipped`
  - `failed`
- `IsProcessed` or successor logic must return true only for `published`.
- Dry-run and Confluence-disabled transcripts must not become `published`.
- Store transcript ID, name, status, Confluence URL, last error, attempt count,
  first seen timestamp, last updated timestamp, and published timestamp when
  applicable.
- Existing SQLite databases must migrate safely; old processed rows are treated
  as `published`.

## Non-Goals

- Do not introduce an external database.
- Do not build a generic workflow engine.
- Do not delete existing processed rows.

## Likely Files

- `internal/database/store.go`
- `internal/database/store_test.go`

## Acceptance Tests

- Published transcripts are processed/skipped on future runs.
- Skipped transcripts are not considered processed.
- Failed transcripts record error and attempts.
- Existing `MarkProcessed` tests still pass or are updated compatibly.
- Migration compatibility is covered where practical.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/04-transcript-state-lifecycle.md`.
Work only in the current repo. Do not use parent paths. Touch only the database
package unless tests prove otherwise. Run `go test ./internal/database`.
