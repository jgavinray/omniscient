# Reliability and Operability Requirements

This document defines the reliability and operability work required for
Omniscient, a cron-oriented pipeline that fetches Google Drive transcripts,
extracts meeting notes with an LLM, publishes to Confluence, and stores durable
state in SQLite.

The implementation goal is not to add a broad policy engine. The goal is a
small, auditable sync system with explicit transcript state, recoverable
operator actions, deterministic configuration validation, and tests that cover
the real orchestration path.

These requirements are intentionally written as implementation source material
for local Claude Code sessions. Each feature set should be implemented in a
small phase with focused tests before moving to the next phase.

## Design Principles

- Keep state explicit. A transcript should not be treated as fully processed
  unless it was actually published and that publish state was persisted.
- Prefer bounded labels. Transcript states, event types, and command outputs
  should use a small vocabulary that can be tested and documented.
- Preserve an append-only event trail. Current state is useful for decisions;
  event history is useful for audit and recovery.
- Make recovery operator-friendly. Common failures should be recoverable with
  CLI commands, not manual SQLite editing.
- Keep the implementation local to this CLI. Do not introduce a generic policy
  engine, background service, or external database.
- Test the production path. Integration-style tests should exercise the sync
  service itself, not duplicate its logic in test code.

## 1. Explicit Publish State and Transcript Lifecycle

### Intent

Replace the binary "processed or not" model with a recoverable transcript
lifecycle. Dry runs, disabled publishing, extraction failures, publish failures,
and persistence failures must be distinguishable.

### Functional Requirements

- Store transcript state using bounded status values:
  - `discovered`: transcript was seen in Drive and is eligible for processing.
  - `extracted`: LLM extraction and parsing succeeded, but publish is not
    complete.
  - `published`: Confluence publish succeeded and durable state was written.
  - `skipped`: processing intentionally skipped, such as dry run or Confluence
    disabled.
  - `failed`: processing failed and should be visible to operators.
- `IsProcessed` or its replacement must return true only for `published`.
- Dry-run transcripts must not become `published`.
- Confluence-disabled transcripts must not become `published`.
- Record at least:
  - transcript ID,
  - transcript name,
  - status,
  - Confluence URL when published,
  - last error,
  - attempt count,
  - first seen timestamp,
  - last updated timestamp,
  - processed/published timestamp when applicable.
- Existing databases with the old `processed_transcripts` table should migrate
  safely without data loss. Existing rows should be interpreted as `published`.

### Non-Goals and Guardrails

- Do not add a separate external state database.
- Do not build a generalized workflow engine.
- Do not delete old processed rows during migration.
- Do not require users to manually migrate SQLite files.

### Acceptance Criteria

- A published transcript is skipped on the next sync.
- A dry-run transcript is visible as skipped or dry-run state and is eligible
  for real publish later.
- A Confluence-disabled transcript is not permanently suppressed from later
  publishing.
- A failed transcript records the error and attempt count.
- Existing store tests still pass with updated semantics.
- New tests cover `published`, `skipped`, `failed`, and migration behavior.

### Likely Files

- `internal/database/store.go`
- `internal/database/store_test.go`
- `cmd/omniscient/sync.go`
- New sync orchestration tests under `cmd/omniscient` or an internal service
  package.

## 2. Confluence Base URL Normalization and Validation

### Intent

Prevent malformed Confluence API URLs when users configure either the Atlassian
site root or a URL containing `/wiki`.

### Functional Requirements

- Accept `https://company.atlassian.net` and normalize it to the site root.
- Accept `https://company.atlassian.net/wiki` if we decide to preserve
  compatibility, but normalize it to `https://company.atlassian.net`.
- Ensure API calls build exactly one `/wiki/rest/api/content...` prefix.
- Returned page URLs should remain valid browser URLs.
- Keep HTTPS-only validation for enabled Confluence publishing.

### Non-Goals and Guardrails

- Do not support arbitrary non-Atlassian path prefixes unless explicitly needed.
- Do not silently accept URLs with unrelated paths like `/foo`; reject or
  normalize only documented forms.
- Do not change the public config field name.

### Acceptance Criteria

- Tests prove `https://example.atlassian.net` calls `/wiki/rest/api/content`.
- Tests prove `https://example.atlassian.net/wiki` also calls
  `/wiki/rest/api/content`, not `/wiki/wiki/rest/api/content`.
- Config validation rejects unsupported pathful Confluence base URLs when
  publishing is enabled.

### Likely Files

- `internal/confluence/publisher.go`
- `internal/confluence/publisher_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `config.yaml.example` if wording needs clarification.

## 3. Correct Sync Success Accounting

### Intent

The sync command must not report success when the pipeline produced side
effects but failed to persist durable state. A publish followed by a database
failure is not a successful transcript; it is a recovery risk.

### Functional Requirements

- If Confluence publishing succeeds but state persistence fails:
  - log the Confluence URL and transcript ID,
  - record the failure if possible,
  - do not increment the successful transcript count,
  - return an error when appropriate.
- `successCount` should represent transcripts fully completed and durably
  recorded as `published`.
- Summary logging should separately count publish successes, persistence
  failures, skipped transcripts, and failed transcripts.
- When all pending non-dry-run work fails, `sync` must return a non-zero error.

### Non-Goals and Guardrails

- Do not attempt to delete Confluence pages after a DB failure.
- Do not hide partial side effects; log enough information for reconciliation.
- Do not mark a transcript published unless the database write succeeds.

### Acceptance Criteria

- A test with a store that fails on publish-state write causes sync to return an
  error and does not count the transcript as successful.
- A successful publish and state write increments the success count.
- Logs or returned errors include enough context to find the affected transcript
  and page URL.

### Likely Files

- `cmd/omniscient/sync.go`
- Sync orchestration service file if introduced.
- Tests for sync orchestration.

## 4. Testable Sync Orchestration

### Intent

Make the sync pipeline testable without reimplementing the production loop in
tests. External systems should be represented by small interfaces.

### Functional Requirements

- Introduce a small sync service or orchestration type that accepts
  dependencies through interfaces:
  - transcript fetcher,
  - LLM classifier/extractor,
  - publisher,
  - state store.
- Keep CLI command construction thin:
  - load config,
  - set up logging/context,
  - construct real dependencies,
  - invoke the service.
- Existing behavior should remain recognizable from the CLI user's perspective.
- Tests should use fake dependencies to exercise the actual sync service.

### Non-Goals and Guardrails

- Do not introduce a large dependency injection framework.
- Do not make every package public.
- Do not require real Google OAuth, LLM, Confluence, or SQLite network behavior
  in sync service tests.

### Acceptance Criteria

- The current integration test that manually duplicates the sync loop is
  replaced or supplemented by tests that call the actual sync service.
- Tests cover:
  - already-published transcripts are skipped,
  - dry-run does not publish or mark published,
  - Confluence-disabled does not mark published,
  - extraction failure records failure,
  - publish failure records failure,
  - DB mark failure affects success accounting.

### Likely Files

- `cmd/omniscient/sync.go`
- Possibly new `cmd/omniscient/sync_service.go`
- `cmd/omniscient/main_test.go`
- New focused sync service tests.

## 5. Context-Aware Retry and Backoff Hygiene

### Intent

Retries should respect cancellation and external API rate-limit signals. Cron
jobs should shut down promptly and avoid synchronized retry spikes.

### Functional Requirements

- Retry sleeps must be context-aware.
- HTTP 429 responses should respect `Retry-After` when present and parseable.
- Retry backoff should include bounded jitter.
- Retry behavior should remain deterministic enough to test. Use injectable
  sleep/backoff helpers or narrow unit tests where appropriate.
- Keep retry limits modest by default.

### Non-Goals and Guardrails

- Do not add a global scheduler.
- Do not retry permanent errors like 401/403.
- Do not sleep after the context is canceled.
- Do not make tests wait on real multi-second sleeps.

### Acceptance Criteria

- A canceled context stops retry sleep promptly.
- 429 with `Retry-After` uses that delay within configured bounds.
- 5xx still retries.
- 401 still does not retry.
- Tests avoid long wall-clock waits.

### Likely Files

- `internal/retry/retry.go`
- `internal/retry/retry_test.go`
- `internal/confluence/publisher.go`
- `internal/llm/anthropic.go`
- `internal/llm/openai.go`

## 6. Operator Commands

### Intent

Operators should be able to inspect and recover sync state without manually
editing SQLite.

### Functional Requirements

- Add `omniscient status` or `omniscient sync status` to show:
  - counts by status,
  - recent transcript states,
  - transcript ID,
  - name,
  - status,
  - attempts,
  - last error,
  - Confluence URL when present,
  - updated timestamp.
- Add `retry-failed` to move failed transcripts back to an eligible state.
- Add `forget --id <transcript-id>` or equivalent to remove/reset one
  transcript so it can be reprocessed.
- Commands should load the configured database path from `--config`.
- Command output should be plain text and script-readable enough for cron/admin
  use.

### Non-Goals and Guardrails

- Do not build an interactive TUI.
- Do not require direct SQL knowledge from the operator.
- Do not mutate non-failed transcripts with `retry-failed`.
- Do not delete Confluence pages.

### Acceptance Criteria

- `status` works with an empty database.
- `status` displays counts and recent rows after test data is inserted.
- `retry-failed` resets failed rows and reports how many were affected.
- `forget --id` removes or resets exactly one transcript.
- CLI tests cover command behavior using a temp database.

### Likely Files

- New `cmd/omniscient/status.go` or similar.
- `cmd/omniscient/main.go`
- `internal/database/store.go`
- `internal/database/store_test.go`
- CLI command tests.

## 7. Prompt and Template Validation

### Intent

Bad prompt configuration should fail during config validation, not during a
cron sync after external calls have already started.

### Functional Requirements

- Require at least one prompt template after defaults/custom config are applied.
- Require every extraction prompt to include `{{TRANSCRIPT}}`.
- Require classify prompt to include:
  - `{{TEMPLATE_KEYS}}`,
  - `{{TRANSCRIPT_PREVIEW}}`.
- Require template keys to be non-empty after trimming.
- Prefer clear validation errors that identify the offending template key.

### Non-Goals and Guardrails

- Do not validate prompt quality or model suitability.
- Do not require all default templates when custom templates are supplied.
- Do not call the LLM during config validation.

### Acceptance Criteria

- Config with empty custom template map fails or receives defaults only when the
  prompts section is absent.
- Config with an extraction prompt missing `{{TRANSCRIPT}}` fails.
- Config with classify prompt missing either required placeholder fails.
- Validation error messages include the field or template key.

### Likely Files

- `internal/config/config.go`
- `internal/config/config_test.go`
- `config.yaml.example` if wording needs clarification.

## 8. Observability, Run IDs, and Safe Logging

### Intent

Cron logs and SQLite state should explain what happened in each run without
leaking transcript contents.

### Functional Requirements

- Generate a run ID for each sync invocation.
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
- Store event metadata as bounded structured data, not raw transcript content.
- Log summary counts by stage at run completion.
- Dry-run preview may include bounded output preview, but transcript content
  should not be logged by default.

### Non-Goals and Guardrails

- Do not add external telemetry services.
- Do not log full transcript text.
- Do not log API keys, OAuth tokens, or Confluence tokens.
- Do not make event metadata unbounded.

### Acceptance Criteria

- A sync run logs a run ID and final summary counts.
- Event rows can be queried by run ID and transcript ID.
- Tests verify transcript content is not included in normal logs/events.
- Dry-run preview remains bounded.

### Likely Files

- `cmd/omniscient/sync.go`
- Sync service file if introduced.
- `internal/database/store.go`
- `internal/database/store_test.go`
- Logging tests where practical.

## Phased Implementation Plan for Local Claude Code

The local implementation model has limited throughput and context. Do not ask it
to implement all features in one pass. Use this document as the source of truth
and run one phase at a time.

### Phase 1: Config and Confluence URL Safety

Scope:
- Implement prompt/template validation.
- Implement Confluence base URL normalization or rejection.
- Update docs/example comments only if needed.

Files:
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/confluence/publisher.go`
- `internal/confluence/publisher_test.go`

Tests:
- Config validation placeholder tests.
- Confluence `/wiki` normalization tests.
- Existing package tests.

Why first:
- Small surface area.
- No state migration yet.
- Easy for a local model to reason about.

### Phase 2: Retry Context Awareness

Scope:
- Make retry helper context-aware.
- Add Retry-After support and jitter/backoff bounds.
- Wire Confluence and LLM callers to the updated retry API.

Files:
- `internal/retry/retry.go`
- `internal/retry/retry_test.go`
- `internal/confluence/publisher.go`
- `internal/llm/anthropic.go`
- `internal/llm/openai.go`

Tests:
- Cancellation test.
- 429 Retry-After test.
- Permanent error no-retry test.
- Existing LLM and Confluence retry tests.

### Phase 3: Database State Model and Event Ledger

Scope:
- Add explicit transcript statuses.
- Migrate old processed rows to `published`.
- Add append-only sync events.
- Add store APIs for status, retry failed, forget, mark failed/skipped/published.

Files:
- `internal/database/store.go`
- `internal/database/store_test.go`

Tests:
- Status lifecycle tests.
- Migration compatibility test if practical.
- Event append/query tests.
- Retry-failed and forget tests.

### Phase 4: Sync Service Refactor and Success Accounting

Scope:
- Introduce small interfaces and a sync service.
- Update `runSync` to construct real dependencies and delegate.
- Use explicit state APIs.
- Fix DB mark failure accounting.
- Add run ID and summary counts.

Files:
- `cmd/omniscient/sync.go`
- New `cmd/omniscient/sync_service.go` if useful.
- `cmd/omniscient/main_test.go` or new sync service tests.

Tests:
- Real sync service with fakes.
- Dry-run not published.
- Confluence-disabled not published.
- Publish success plus DB failure returns failure.
- All-failed run returns error.

### Phase 5: Operator Commands

Scope:
- Add status, retry-failed, and forget commands.
- Keep output plain text.
- Use configured SQLite path.

Files:
- `cmd/omniscient/main.go`
- New `cmd/omniscient/status.go` or `cmd/omniscient/ops.go`
- `internal/database/store.go` if missing APIs remain.

Tests:
- Command behavior with temp DB.
- Empty DB status.
- Retry-failed count.
- Forget by ID.

### Phase 6: Final Observability and End-to-End Test Pass

Scope:
- Ensure run ID appears consistently.
- Ensure event metadata is bounded and safe.
- Ensure final summary counts are accurate.
- Run full suite and vet/tidy checks.

Files:
- Any files touched by prior phases.
- README or docs updates if CLI behavior changed.

Tests:
- `go test ./...`
- `go vet ./...`
- `go mod tidy -diff`

## Prompting Guidance for Local Claude Code

For each phase, give the local Claude session:

- This document path.
- The exact phase section.
- A hard boundary: current repo only, no parent paths.
- A short file list.
- A requirement to inspect before editing.
- A requirement to run only the relevant package tests first, then `go test
  ./...` at phase end.

Avoid asking the local model to keep all eight feature sets active in one
prompt. Each phase should finish with a small diff, passing tests, and a concise
report of changed files.
