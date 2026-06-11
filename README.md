# Omniscient

Meeting transcript harvester: **meeting platforms → LLM extraction → knowledge bases**.

Omniscient is a CLI tool that runs as a cron job, harvesting meeting
transcripts from the platforms that already record them, extracting
structured meeting notes via LLM, and publishing them to your knowledge base.
It uses SQLite to deduplicate across runs.

It does **not** join or record calls — meeting platforms already do that.
Google Meet (with transcription enabled) saves transcripts as Google Docs in
Drive; Omniscient harvests them on a schedule.

## Providers

| Type | Provider | Status |
|------|----------|--------|
| Source | Google Meet (via Drive) | ✅ implemented |
| Source | Zoom | 🔜 planned — see [docs/ADDING_A_PROVIDER.md](docs/ADDING_A_PROVIDER.md) |
| Destination | Confluence | ✅ implemented |
| Destination | Notion | 🔜 planned — see [docs/ADDING_A_PROVIDER.md](docs/ADDING_A_PROVIDER.md) |

Adding a provider means implementing one small interface in one package —
the guide walks through it step by step.

## Architecture

```
 sources (1..n)          pipeline                 destinations (1..n)
┌──────────────┐   ┌──────────────────────┐   ┌──────────────────┐
│ googlemeet   │──▶│ dedup → classify →   │──▶│ confluence       │
│ (zoom …)     │   │ extract → validate → │   │ (notion …)       │
└──────────────┘   │ publish → mark       │   └──────────────────┘
                   └──────────────────────┘
                        SQLite state + events
```

1. **Fetch** — every enabled source lists transcripts modified within the
   lookback window
2. **Classify** — the LLM picks a meeting type (engineering, customer
   success, planning, …) from your configured templates; answers are
   validated, retried once on garbage, and fall back safely
3. **Extract** — a type-specific prompt produces YAML front-matter + markdown
   notes; malformed output gets one corrective retry
4. **Publish** — the notes go to every enabled destination (idempotent
   create-or-update, so retries never duplicate pages)
5. **Track** — the transcript is marked processed in SQLite under a
   source-prefixed key (`googlemeet:<id>`) and skipped on later runs

Works with the Anthropic API or **fully local LLMs** — any OpenAI-compatible
(vLLM, Ollama, LM Studio) or Anthropic-compatible (llama.cpp) endpoint. The
pipeline is hardened for 14B-class models: temperature 0, validated outputs,
corrective retries. See [docs/LLM_SCOPE.md](docs/LLM_SCOPE.md) for what the
LLM is responsible for and how to qualify a model.

## Installation

```bash
# Build from source
make build

# Install binary + example config to /opt/omniscient
sudo make install
```

Requires Go 1.23+.

## Setup

Follow the click-by-click guide in [docs/SETUP.md](docs/SETUP.md). The short
version:

1. **Google:** create an OAuth client (Desktop app) in Google Cloud Console,
   save the JSON, then `omniscient auth googlemeet` — one browser click,
   tokens auto-refresh forever
2. **Confluence:** create an API token at id.atlassian.com, paste it into the
   config
3. **LLM:** point at Anthropic or your local model server
4. Copy and fill the config:

```bash
cp config.yaml.example /opt/omniscient/config.yaml
```

Key settings:

| Section | Field | Description |
|---------|-------|-------------|
| `sources.googlemeet` | `folder_id` | Drive folder containing transcripts ("Meet Recordings") |
| `destinations.confluence` | `base_url` | Your Atlassian URL (e.g., `https://company.atlassian.net`) |
| `destinations.confluence` | `space_key` | Target Confluence space |
| `llm` | `provider` | `anthropic` or `openai-compatible` |
| `llm` | `model` | Model name (e.g., `claude-sonnet-4`, or a local model) |
| `sync` | `database_path` | SQLite database for dedup tracking |

See [`config.yaml.example`](config.yaml.example) for the full reference.

## Usage

```bash
# Validate config
omniscient config validate --config /opt/omniscient/config.yaml

# Authenticate with Google (once)
omniscient auth googlemeet --config /opt/omniscient/config.yaml

# Run the sync pipeline (set dry_run: true in config for a test run)
omniscient sync --config /opt/omniscient/config.yaml

# Operations
omniscient status         # pipeline counts + recent transcripts
omniscient retry-failed   # re-queue everything marked failed
omniscient forget <key>   # forget one transcript (googlemeet:<id>)
omniscient version
```

### Cron Job

```cron
*/30 * * * * /usr/local/bin/omniscient sync --config /opt/omniscient/config.yaml >> /var/log/omniscient/cron.log 2>&1
```

## Meeting Type Templates

Omniscient classifies transcripts and applies type-specific extraction
prompts. Default templates:

- **engineering** — Standups, design reviews, technical discussions
- **customer_success** — Customer calls, account reviews
- **planning** — Sprint planning, roadmap, strategy sessions

Custom templates can be defined in `config.yaml` under `prompts.templates`.

## Project Structure

```
cmd/omniscient/                CLI commands (sync, auth, config, status, …)
internal/
  pipeline/                    Orchestration: dedup → classify → extract → publish
  source/                      Source interface
    googlemeet/                Google Meet source (Drive API + OAuth2)
  destination/                 Destination interface
    confluence/                Confluence REST API publisher
  llm/                         LLM extraction (Anthropic + OpenAI-compatible)
  config/                      YAML config loading + validation
  models/                      Neutral Transcript type + front-matter parser
  database/                    SQLite dedup store + sync events
  retry/                       Transient-error retry with backoff
```

## Documentation

- [docs/SETUP.md](docs/SETUP.md) — click-by-click provider setup
- [docs/ADDING_A_PROVIDER.md](docs/ADDING_A_PROVIDER.md) — add a source or destination
- [docs/LLM_SCOPE.md](docs/LLM_SCOPE.md) — LLM responsibilities + model qualification

## Development

```bash
make build      # Build to bin/omniscient
make test       # Run all tests
make clean      # Remove build artifacts
```

## License

[Apache License 2.0](LICENSE)
