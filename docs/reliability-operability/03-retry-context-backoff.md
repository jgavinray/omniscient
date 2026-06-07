# 03 Context-Aware Retry and Backoff

## Intent

Retries should respect cancellation and API rate-limit signals. Cron jobs should
shut down promptly and avoid synchronized retry spikes.

## Requirements

- Retry sleeps must accept and observe `context.Context`.
- HTTP 429 should respect `Retry-After` when present and parseable.
- Backoff should include bounded jitter.
- Permanent errors such as 401/403 must not be retried.
- Tests must not wait on real multi-second sleeps.

## Non-Goals

- Do not add a global scheduler.
- Do not add external retry dependencies unless clearly justified.
- Do not change public CLI behavior.

## Likely Files

- `internal/retry/retry.go`
- `internal/retry/retry_test.go`
- `internal/confluence/publisher.go`
- `internal/llm/anthropic.go`
- `internal/llm/openai.go`
- Related LLM/Confluence tests if signatures change.

## Acceptance Tests

- Canceled context stops retry sleep promptly.
- 429 with `Retry-After` uses the retry-after delay path.
- 5xx still retries.
- 401 still does not retry.
- Existing LLM and Confluence retry tests pass.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/03-retry-context-backoff.md`.
Work only in the current repo. Do not use parent paths. Keep API changes small.
Run focused retry, LLM, and Confluence tests before `go test ./...`.
