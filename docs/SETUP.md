# Setup Guide

Click-by-click setup for each provider. After this one-time setup, the tool
runs unattended on cron — tokens refresh themselves.

## 1. Google (Meet transcripts)

### One-time: create an OAuth client

1. Go to <https://console.cloud.google.com/> and select (or create) a project
2. **APIs & Services → Library** → search "Google Drive API" → **Enable**
3. **APIs & Services → OAuth consent screen** → User type **Internal**
   (Google Workspace) or **External** — if External, add yourself under
   **Test users**
4. **APIs & Services → Credentials → Create Credentials → OAuth client ID** →
   Application type **Desktop app** → **Create**
5. **Download JSON** → save it at the path configured in
   `sources.googlemeet.credentials_file`

### Authenticate (browser click, once)

```bash
omniscient auth googlemeet --config /opt/omniscient/config.yaml
```

A browser opens; approve access. The token is saved to
`sources.googlemeet.token_file` and auto-refreshes — you never handle it
again.

### Find your transcripts folder

Google Meet (with transcription turned on for the meeting) saves transcripts
as Google Docs in the organizer's Drive under **Meet Recordings**. In Drive:
right-click the folder → **Share → Copy link**. The folder ID is the part
between `/folders/` and `?` in the URL — put it in
`sources.googlemeet.folder_id`.

> Transcription must be enabled in the meeting (host control). No transcript
> in Drive means nothing to harvest.

## 2. Confluence

1. Go to <https://id.atlassian.com/manage-profile/security/api-tokens> →
   **Create API token** → copy it into `destinations.confluence.api_token`
2. `email`: the Atlassian account email that owns the token
3. `base_url`: `https://<your-company>.atlassian.net`
4. `space_key`: visible in the space URL (`/wiki/spaces/<KEY>/...`)
5. Optional `parent_page_id`: open the intended parent page — the number in
   its URL (`/pages/<number>/...`)

## 3. LLM

Three options:

- **Anthropic API:** `provider: anthropic` + `anthropic_api_key` from
  <https://console.anthropic.com/settings/keys>
- **Local, OpenAI-compatible:** `provider: openai-compatible` +
  `openai_base_url` (vLLM, Ollama, LM Studio)
- **Local, Anthropic-compatible:** `provider: anthropic` +
  `anthropic_base_url` pointed at the local server (API key optional)

Before trusting a new model — especially a small local one — run the
qualification checklist in [LLM_SCOPE.md](LLM_SCOPE.md).

## 4. Validate and run

```bash
omniscient config validate --config /opt/omniscient/config.yaml

# First run with dry_run: true in config — check the extracted output in logs
omniscient sync --config /opt/omniscient/config.yaml
```

When the dry-run output looks right, set `dry_run: false` and schedule it:

```cron
*/30 * * * * /opt/omniscient/omniscient sync --config /opt/omniscient/config.yaml
```

Operational commands:

```bash
omniscient status         # pipeline counts + recent transcripts
omniscient retry-failed   # re-queue everything marked failed
omniscient forget <key>   # forget one transcript (key looks like googlemeet:<id>)
```
