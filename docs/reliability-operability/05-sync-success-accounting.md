# 05 Sync Success Accounting

## Intent

The sync command must not report success when publishing succeeded but durable
state persistence failed.

## Requirements

- A transcript counts as successful only after publish and state persistence
  both succeed.
- If publishing succeeds but state persistence fails:
  - log transcript ID and Confluence URL,
  - do not increment success count,
  - return an error when all real work failed or the run has unrecoverable
    persistence failures.
- Summary counts should distinguish success, skipped, failed, publish failure,
  and persistence failure where practical.

## Non-Goals

- Do not delete or roll back Confluence pages.
- Do not hide partial side effects.
- Do not mark a transcript published unless the DB write succeeds.

## Likely Files

- `cmd/omniscient/sync.go`
- Sync service files if introduced.
- Sync tests.

## Acceptance Tests

- Store mark failure after publish does not count as success.
- All failed pending work returns an error.
- Successful publish plus state write increments success.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/05-sync-success-accounting.md`.
Work only in the current repo. Do not use parent paths. Prefer tests against
the real sync service if Phase 6 has already introduced one.
