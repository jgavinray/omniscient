# 07 Operator Commands

## Intent

Operators should inspect and recover sync state without manually editing SQLite.

## Requirements

- Add a status command that shows counts by status and recent transcript rows.
- Add a retry-failed command that moves failed transcripts back to an eligible
  state and reports the count.
- Add a forget/reprocess command by transcript ID.
- Commands must load the database path from `--config`.
- Output should be plain text and usable in scripts.

## Non-Goals

- Do not build an interactive TUI.
- Do not delete Confluence pages.
- Do not mutate non-failed rows with retry-failed.

## Likely Files

- `cmd/omniscient/main.go`
- New `cmd/omniscient/status.go` or `cmd/omniscient/ops.go`
- `internal/database/store.go`
- `internal/database/store_test.go`
- CLI command tests.

## Acceptance Tests

- Status works on an empty DB.
- Status shows inserted test states.
- Retry-failed updates only failed rows and reports count.
- Forget by ID removes or resets exactly one transcript.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/07-operator-commands.md`.
Work only in the current repo. Do not use parent paths. Run database and CLI
tests.
