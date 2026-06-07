# 06 Testable Sync Orchestration

## Intent

Tests should exercise the real sync orchestration path rather than a hand-copied
loop.

## Requirements

- Introduce a small sync service or orchestration type.
- Use small interfaces for:
  - transcript fetching,
  - LLM classification/extraction,
  - publishing,
  - state storage.
- Keep Cobra command code thin.
- Use fakes in tests; no real Google OAuth, LLM, or Confluence calls.

## Non-Goals

- Do not introduce a dependency injection framework.
- Do not make all internals public.
- Do not rewrite unrelated CLI commands.

## Likely Files

- `cmd/omniscient/sync.go`
- New `cmd/omniscient/sync_service.go` if useful.
- `cmd/omniscient/main_test.go`
- New sync service tests.

## Acceptance Tests

- Already-published transcripts are skipped.
- Dry-run does not publish or mark published.
- Confluence-disabled does not mark published.
- Extraction failure records failure.
- Publish failure records failure.
- DB mark failure affects success accounting.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/06-testable-sync-orchestration.md`.
Work only in the current repo. Do not use parent paths. Keep the service small
and preserve current CLI behavior.
