# Omniscient

Meeting transcript harvester: **Google Drive → LLM extraction → Confluence**.

Omniscient is a CLI tool that runs as a cron job, fetching Google Meet transcripts from Drive, extracting structured meeting notes via LLM, and publishing them to Confluence pages. It uses SQLite to deduplicate across runs.

## How It Works

1. **Fetch** — Pulls recent transcripts from a Google Drive folder (filtered by modification time)
2. **Classify** — LLM classifies each transcript by meeting type (engineering, customer success, planning, etc.)
3. **Extract** — Runs a type-specific prompt to produce YAML front-matter + markdown notes
4. **Publish** — Converts markdown to HTML and creates/updates a Confluence page
5. **Track** — Marks the transcript as processed in SQLite so it's skipped on the next run

## Installation

```bash
# Build from source
make build

# Install binary + example config to /opt/omniscient
sudo make install
```

Requires Go 1.23+.

## Setup

### 1. Google OAuth2 Credentials

1. Go to [Google Cloud Console](https://console.cloud.google.com/) → APIs & Services → Credentials
2. Create an OAuth 2.0 Client ID (Desktop application)
3. Download the JSON and save it as the `credentials_file` path in your config

### 2. Configuration

Copy the example config and fill in your values:

```bash
cp config.yaml.example /opt/omniscient/config.yaml
```

Key settings:

| Section | Field | Description |
|---------|-------|-------------|
| `google` | `folder_id` | Google Drive folder containing transcripts |
| `llm` | `provider` | `anthropic` or `openai-compatible` |
| `llm` | `model` | Model name (e.g., `claude-sonnet-4`, or a local model) |
| `confluence` | `base_url` | Your Atlassian URL (e.g., `https://company.atlassian.net`) |
| `confluence` | `space_key` | Target Confluence space |
| `sync` | `database_path` | SQLite database for dedup tracking |

See [`config.yaml.example`](config.yaml.example) for the full reference.

### 3. Authenticate

Run the auth command to complete the Google OAuth2 browser consent flow:

```bash
omniscient auth --config /opt/omniscient/config.yaml
```

This saves a `token.json` that auto-refreshes on subsequent runs.

### 4. Validate

```bash
omniscient config validate --config /opt/omniscient/config.yaml
```

## Usage

```bash
# Run the sync pipeline
omniscient sync --config /opt/omniscient/config.yaml

# Dry run (extract and print, don't publish)
# Set dry_run: true in config.yaml

# Check version
omniscient version
```

### Cron Job

```cron
*/30 * * * * /usr/local/bin/omniscient sync --config /opt/omniscient/config.yaml >> /var/log/omniscient/cron.log 2>&1
```

## LLM Providers

| Provider | Config | Notes |
|----------|--------|-------|
| **Anthropic** | `provider: anthropic` + `anthropic_api_key` | Claude models via Anthropic API |
| **OpenAI-compatible** | `provider: openai-compatible` + `openai_base_url` | Works with vLLM, Ollama, OpenAI, or any compatible endpoint |

## Meeting Type Templates

Omniscient classifies transcripts and applies type-specific extraction prompts. Default templates:

- **engineering** — Standups, design reviews, technical discussions
- **customer_success** — Customer calls, account reviews
- **planning** — Sprint planning, roadmap, strategy sessions

Custom templates can be defined in `config.yaml` under `prompts.templates`.

## Project Structure

```
cmd/omniscient/          CLI commands (sync, auth, config, version)
internal/
  config/                YAML config loading + validation
  drive/                 Google Drive API + OAuth2
  llm/                   LLM extraction (Anthropic, OpenAI-compatible)
  models/                Data types + YAML front-matter parser
  database/              SQLite deduplication store
  confluence/            Confluence REST API publisher
```

## Development

```bash
make build      # Build to bin/omniscient
make test       # Run all tests
make clean      # Remove build artifacts
```

## License

[Apache License 2.0](LICENSE)
