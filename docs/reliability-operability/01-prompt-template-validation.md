# 01 Prompt and Template Validation

## Intent

Bad prompt configuration should fail during `config validate` or startup, before
cron sync starts external Drive, LLM, or Confluence work.

## Requirements

- After defaults/custom config are applied, at least one template must exist.
- `prompts.classify_prompt` must contain both:
  - `{{TEMPLATE_KEYS}}`
  - `{{TRANSCRIPT_PREVIEW}}`
- Every template key must be non-empty after trimming whitespace.
- Every template extraction prompt must contain `{{TRANSCRIPT}}`.
- Validation errors must identify the field or template key.
- Preserve existing defaults and existing valid configs.

## Non-Goals

- Do not judge prompt quality.
- Do not require every default template when a user provides custom templates.
- Do not call an LLM during validation.
- Do not change config field names.

## Likely Files

- `internal/config/config.go`
- `internal/config/config_test.go`

## Acceptance Tests

- Config with classify prompt missing `{{TEMPLATE_KEYS}}` fails.
- Config with classify prompt missing `{{TRANSCRIPT_PREVIEW}}` fails.
- Config with a template extraction prompt missing `{{TRANSCRIPT}}` fails.
- Config with an empty/whitespace template key fails.
- Existing valid config tests still pass.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/01-prompt-template-validation.md`.
Work only in the current repo. Do not use parent paths. Touch only
`internal/config/config.go` and `internal/config/config_test.go` unless tests
prove another file is required. Run `gofmt` and `go test ./internal/config`.
