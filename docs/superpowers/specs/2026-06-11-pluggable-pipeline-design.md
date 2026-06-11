# Omniscient v2: Pluggable Transcript Pipeline — Design

**Date:** 2026-06-11
**Status:** Approved (initial scope: Google Meet + Confluence)

## Intent

Record meetings, summarize the transcript with an LLM, and post notes to a
knowledge base. Long-term: multiple meeting platforms (Google Meet, Zoom, …)
and multiple knowledge bases (Confluence, Notion, …). The codebase must be
structured so a junior software engineer can add a new provider by
implementing one small interface and copying an existing test pattern.

**Initial release scope: Google Meet (source) + Confluence (destination) only.**
Zoom and Notion are explicitly deferred; the architecture must support them
as additive work.

## Current State and Gaps

The existing tool already implements the Google Meet → Confluence path:
Google Meet (with transcription enabled) saves transcripts as Google Docs in
Drive; the tool harvests that folder, classifies/extracts via LLM, and
publishes to Confluence with SQLite dedup. Gaps:

1. The source is named and typed as "drive" — the pipeline's `fetcher`
   interface returns `[]*drive.Transcript`, leaking the provider type.
2. Confluence concepts (space key, parent page ID) are baked into the
   pipeline's `publisher` interface signature, so no other destination can
   implement it.
3. The orchestration interfaces are private to `cmd/omniscient`, so they are
   not real extension points.
4. Config is shaped around exactly one source and one sink.
5. Prompts trust the LLM output; a 14B-class local model needs validation
   and constrained choices.

## Architecture

```
 sources (1..n)          pipeline                 destinations (1..n)
┌──────────────┐   ┌──────────────────────┐   ┌──────────────────┐
│ googlemeet   │──▶│ dedup → classify →   │──▶│ confluence       │
│ (zoom later) │   │ extract → validate → │   │ (notion later)   │
└──────────────┘   │ publish → mark       │   └──────────────────┘
                   └──────────────────────┘
                        SQLite state + events
```

### Neutral domain types (`internal/models`)

```go
// Transcript is a provider-neutral meeting transcript.
type Transcript struct {
    ID         string    // provider-native ID
    Source     string    // provider name, e.g. "googlemeet"
    Title      string
    ModifiedAt time.Time
    Content    string
}

// Key returns the globally unique dedup key, e.g. "googlemeet:abc123".
func (t *Transcript) Key() string
```

`ExtractionResult` (YAML front-matter + markdown) stays as the neutral note
format all destinations consume.

### Source interface (`internal/source`)

```go
type Source interface {
    Name() string
    ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error)
}
```

- `internal/source/googlemeet` — the existing Drive client, moved and
  renamed to say what it is. Each source owns its config (credentials file,
  token file, folder ID). No behavior change.

### Destination interface (`internal/destination`)

```go
type Destination interface {
    Name() string
    Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (url string, err error)
}
```

- `internal/destination/confluence` — existing client behind the interface.
  Space key and parent page ID move into the Confluence implementation's own
  config. Publishing stays idempotent (create-or-update by title) so retries
  after partial failure are safe.

### Pipeline (`internal/pipeline`)

`SyncService` moves from `cmd/omniscient/sync.go` to `internal/pipeline`.
It loops over all enabled sources, and for each new transcript runs:
dedup check → classify → extract → validate → publish to **all enabled
destinations** → mark processed. A transcript is marked processed only when
all enabled destinations succeed; otherwise it is marked failed and retried
on the next run. Existing error categories are unchanged:

1. Transient (retry w/ backoff): 429, 5xx, timeouts
2. Permanent (fail fast): auth failure, invalid config, DB corruption
3. Recoverable (log + continue): single-transcript failure

Sync-event observability and dry-run behavior carry over unchanged.

### Config

Breaking change: provider sections become maps under `sources:` and
`destinations:`, each entry with `enabled:`. Loading the old schema produces
a clear error pointing at the new `config.yaml.example`. No legacy shim.

```yaml
sources:
  googlemeet:
    enabled: true
    credentials_file: "/opt/omniscient/credentials/credentials.json"
    token_file: "/opt/omniscient/credentials/token.json"
    folder_id: "..."

destinations:
  confluence:
    enabled: true
    base_url: "https://company.atlassian.net"
    email: "..."
    api_token: "..."
    space_key: "ENG"
    parent_page_id: ""

llm:      # unchanged (provider, model, timeout, keys)
sync:     # unchanged (lookback_hours, database_path, max_per_run)
logging:  # unchanged
prompts:  # unchanged (classify_prompt, templates)
dry_run:  # unchanged
```

Validation rule: at least one source and one destination enabled (dry-run
allows zero destinations).

### Database migration

`processed_transcripts` keys become `source:id` (e.g. `googlemeet:<driveID>`).
A one-time startup migration prefixes existing rows with `googlemeet:` so
nothing reprocesses. Schema version is tracked in a `schema_version` table.

### Auth

- **Google Meet:** existing browser OAuth2 consent flow (`omniscient auth`),
  token stored and auto-refreshed; the user never handles tokens. Command
  becomes `omniscient auth googlemeet` (bare `auth` kept as alias while
  there is one OAuth source).
- **Confluence:** API token (single paste from id.atlassian.com). Decision:
  Atlassian OAuth 2.0 (3LO) requires creating a developer-console app and
  pasting a client secret anyway, so it removes no setup steps while adding
  rotating-refresh-token fragility. Deferred as an additive ticket; the
  destination owns its auth config so this swaps cleanly later.

## LLM Scope of Work (14B-class models)

The pipeline must work with a local 14B model on an OpenAI-compatible
endpoint (already supported in config) **or an Anthropic-compatible
endpoint**: the `anthropic` provider gains `anthropic_base_url`
(default `https://api.anthropic.com`), so local servers exposing the
Anthropic Messages API work too. The `sk-ant-` API-key requirement applies
only to the default endpoint; custom endpoints accept any (or no) key.
Two LLM responsibilities:

1. **Classify** — pick a meeting type. Constrained choice: the prompt lists
   the exact template keys; the response is validated against that list
   (case-insensitive). On mismatch, retry once with corrective feedback,
   then fall back to the default template (current fallback behavior).
2. **Extract** — produce YAML front-matter + markdown notes. Output is
   validated by `ParseExtractionOutput`; on parse failure, retry once with
   the parse error appended as corrective feedback, then mark failed.

Requirements that make 14B viable:

- Temperature 0 (or provider default minimum) for both calls.
- Classification prompt contains the allowed keys verbatim and demands a
  one-word answer.
- Extraction prompt templates include one worked example of the expected
  output format.
- One retry-with-feedback on malformed output (bounded; no loops).
- Transcript length guard: log a warning above a configurable size
  (default ~100k chars); no chunking in this release (an hour-long meeting
  is ~8–12k tokens and fits a 32k context).

A `docs/LLM_SCOPE.md` document defines the model's responsibilities,
input/output contracts, prompt requirements, and a manual evaluation
checklist (run N sample transcripts in dry-run, check classification
accuracy and front-matter parse rate) so a junior can qualify a new model.

## Documentation Deliverables

1. `README.md` — rewritten: intent, architecture diagram, provider matrix
   (implemented / planned), setup, usage.
2. `docs/ADDING_A_PROVIDER.md` — junior guide: implement the interface, add
   the config struct, register in the factory, copy the `httptest` test
   pattern; includes a PR checklist.
3. `docs/LLM_SCOPE.md` — LLM scope of work (above).
4. `docs/SETUP.md` — click-by-click Google Cloud Console and Atlassian
   token setup.

## Testing

- Existing style continues: `httptest` for HTTP APIs, temp dirs for SQLite.
- Pipeline tests use fake `Source`/`Destination` implementations.
- New tests: multi-destination publish (incl. partial failure → not marked
  processed), classification validation/retry, extraction retry-with-
  feedback, config migration error, DB key migration.
- `make test` green is the bar for every step.

## Out of Scope

- Zoom source, Notion destination (deferred; architecture supports them)
- Atlassian OAuth 3LO (deferred; additive)
- Recording bots, webhooks/real-time, web UI, REST API, chunked
  map-reduce summarization, env-var config, multi-tenant
