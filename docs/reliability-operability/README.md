# Reliability and Operability Feature Specs

This directory splits the reliability and operability requirements into small
feature specs for local Claude Code implementation sessions.

Use one spec per implementation prompt. Do not ask the local model to carry all
eight feature sets at once.

Recommended order:

1. `01-prompt-template-validation.md`
2. `02-confluence-url-normalization.md`
3. `03-retry-context-backoff.md`
4. `04-transcript-state-lifecycle.md`
5. `05-sync-success-accounting.md`
6. `06-testable-sync-orchestration.md`
7. `07-operator-commands.md`
8. `08-observability-events.md`

Global guardrails for every implementation prompt:

- Work only in `/archive/omniscient`.
- Do not use parent-directory paths.
- Inspect before editing.
- Touch only the files named in the active feature spec unless a test or
  compiler error proves another file is required.
- Run focused package tests before broader tests.
- Keep each diff small and complete before moving to the next spec.
