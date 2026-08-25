# Omniscient Project Context

## What This Is
Meeting transcript harvester: Google Drive → LLM extraction → multi-sink publish (Confluence, Slack, local markdown)

## Module
`github.com/jgavinray/omniscient`

## Architecture
- Cobra-based CLI tool (cron job, runs every 30min)
- Google OAuth2 for Drive API (interactive browser consent on first run, token.json stored locally for refresh)
- Multi-provider LLM support (Anthropic + OpenAI-compatible)
- SQLite deduplication (`modernc.org/sqlite`, pure Go, no CGO)
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
- **Cobra CLI**: Commands in `cmd/omniscient/` (sync, version, config validate, auth)
- **Interface-based LLM providers**: `internal/llm/extractor.go`
- **Google OAuth2**: `credentials.json` (OAuth client from Google Cloud Console) + `token.json` (auto-generated on first run via browser consent, auto-refreshes)
- **Config via YAML only**: `/opt/omniscient/config.yaml` — no environment variable overrides
- **Structured logging**: `slog` throughout
- **Retry transient errors**: 429, 5xx with exponential backoff (3 attempts)
- **Multi-sink publishing** (`internal/publish/`): Confluence (update-or-create by title), Slack incoming-webhook (off by default), local markdown (on by default, one `<date>_<name>.md` per transcript, atomic temp-file + rename). Each approved summary routes to ALL enabled sinks; opt out per sink via config
- **`omniscient sync --interactive`**: shows each extracted summary in the terminal and prompts per transcript (approve all / skip / pick sinks by number); without the flag it's fully automatic (cron-safe)

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
- No email notifications (the Slack incoming-webhook sink IS in scope)
- No Jira integration
- No multi-tenant support
- No environment variable config overrides
- Focus ONLY on: Download → Extract → Publish → Track

## When Making Changes
- Add tests for new providers
- Update `config.yaml.example` for any config changes
- Keep error handling consistent (see three categories above)
- Don't add features outside core pipeline scope