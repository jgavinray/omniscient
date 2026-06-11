# Omniscient Project Context

## What This Is
Meeting transcript harvester: meeting platforms (Google Meet) → LLM extraction → knowledge bases (Confluence)

## Module
`github.com/jgavinray/omniscient`

## Architecture
- Cobra-based CLI tool (cron job, runs every 30min)
- **Pluggable providers**: `internal/source` (meeting platforms) + `internal/destination` (knowledge bases) interfaces, orchestrated by `internal/pipeline` — see `docs/ADDING_A_PROVIDER.md`
- Google OAuth2 for the Google Meet source (interactive browser consent via `omniscient auth googlemeet`, token auto-refreshes)
- Multi-provider LLM support (Anthropic + OpenAI-compatible, incl. local servers via `anthropic_base_url`/`openai_base_url`); temperature pinned to 0, outputs validated with one corrective retry — see `docs/LLM_SCOPE.md`
- SQLite deduplication (`modernc.org/sqlite`, pure Go, no CGO); dedup keys are source-prefixed (`googlemeet:<id>`), schema versioned in `schema_version` table
- Go 1.23+, clean interfaces

> **Note:** `docs/IMPLEMENTATION_SPEC.md` references service account auth — OAuth2 is the correct approach.

## Build / Run / Test
```bash
make build          # Build binary to bin/omniscient
make test           # Run all tests (go test -v ./...)
make install        # Install binary + example config to /opt/omniscient
make clean          # Remove build artifacts

# Run directly
go run ./cmd/omniscient sync

# After install
omniscient sync
```

## Key Patterns
- **Cobra CLI**: Commands in `cmd/omniscient/` (sync, version, config validate, auth <provider>, status, retry-failed, forget)
- **Provider interfaces**: `internal/source/source.go` (Source), `internal/destination/destination.go` (Destination — must be idempotent), `internal/llm/extractor.go` (Extractor)
- **Pipeline orchestration**: `internal/pipeline/pipeline.go` — multi-source fetch, dedup, classify/extract with validation + retry-with-feedback, multi-destination publish (all must succeed before mark-processed)
- **Google OAuth2**: `credentials.json` (OAuth client from Google Cloud Console) + `token.json` (auto-generated on first run via browser consent, auto-refreshes)
- **Config via YAML only**: `/opt/omniscient/config.yaml` — `sources:`/`destinations:` maps with per-provider `enabled:`; no environment variable overrides
- **Structured logging**: `slog` throughout
- **Retry transient errors**: 429, 5xx with exponential backoff (3 attempts)

## Error Handling
Three categories:
1. **Transient (retry)**: HTTP 429, 5xx, network timeouts → exponential backoff, 3 attempts
2. **Permanent (fail fast)**: Auth failure (401), invalid config, DB corruption → return error, exit
3. **Recoverable (log + continue)**: Single transcript extraction/publish failure → `slog.Error`, skip, continue processing remaining

## Commit Convention
[Conventional Commits](https://www.conventionalcommits.org/) enforced via git hooks:
- `feat:` new features
- `fix:` bug fixes
- `docs:` documentation
- `test:` tests
- `refactor:` refactoring
- `chore:` maintenance/tooling

## Testing
- Use `net/http/httptest` for HTTP API mocking (LLM providers, Confluence, Drive)
- Use temp directories for SQLite tests
- Add tests for every new provider
- See spec for per-package test cases

## Project Structure
See `docs/IMPLEMENTATION_SPEC.md` for full details

## Scope Boundaries (Out of Scope)
- No web UI or REST API
- No real-time processing (batch only)
- No Slack/email notifications
- No Jira integration
- No multi-tenant support
- No environment variable config overrides
- Focus ONLY on: Download → Extract → Publish → Track

## When Making Changes
- Add tests for new providers
- Update `config.yaml.example` for any config changes
- Keep error handling consistent (see three categories above)
- Don't add features outside core pipeline scope