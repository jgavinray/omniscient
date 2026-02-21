# Omniscient Phase 2 — Flexible Templates, Classification & Publish Controls

## Overview

Phase 2 adds three capabilities to the existing pipeline:

1. **Meeting-type-aware prompt templates** — different meeting types produce different markdown documents with different structure, fields, and prose style.
2. **LLM-based meeting classification** — every transcript is classified by the LLM before extraction, selecting the appropriate template automatically.
3. **Publish controls** — `confluence.enabled` toggle and top-level `dry_run` flag.

### Core Design Principle: Markdown-First

The LLM produces **markdown with YAML front-matter** — not JSON, not HTML. Markdown is the universal intermediate format:

- Renders natively in Confluence (via markdown-to-HTML conversion at publish time)
- Works for file archival, Slack, email, GitHub wikis, stdout
- LLMs produce high-quality markdown naturally
- Decouples extraction from any specific publishing destination
- Human-readable without any tooling

The YAML front-matter block carries structured metadata (date, participants, meeting type) needed for page titles, database records, and programmatic use. The markdown body is the human-readable content.

---

## 1. Output Format

Each template tells the LLM to produce a markdown document like this:

### Engineering Example

```markdown
---
date: "2026-02-21"
time: "09:00"
duration_min: 30
meeting_type: standup
participants:
  - Alice
  - Bob
projects:
  - Omniscient
  - Platform API
sentiment: calm
---

## Summary

- Pipeline is feature-complete, moving to testing phase
- Bob blocked on Drive API quota — escalating to Google support
- Decision to ship v1 without Slack integration

## Decisions

- **Ship v1 without Slack integration** (Owner: Alice)
  Rationale: Keeps scope tight. Can add in v2 based on user feedback.

- **Switch to WAL mode for SQLite** (Owner: Bob)
  Rationale: Better concurrent read performance for future monitoring.

## Blockers

- **Google Drive API quota exceeded** — Ticket: OPS-1234
  Impact: Cannot test with production folder until quota resets.
  Escalation: Alice filing support ticket with Google.

## Action Items

- [ ] Write integration tests for sync pipeline (Bob, due 2026-02-22)
- [ ] File Google support ticket for quota increase (Alice, due 2026-02-21)
- [ ] Update deployment runbook (Bob, due 2026-02-24)

## Key Quotes

> "We should ship what we have and iterate. Perfect is the enemy of done." — Alice, on v1 scope
```

### Customer Success Example

```markdown
---
date: "2026-02-21"
time: "14:00"
duration_min: 45
meeting_type: qbr
customer_name: "Acme Corp"
participants:
  - Sarah (CSM)
  - Mike (Acme VP Eng)
  - Lisa (Acme PM)
customer_sentiment: concerned
churn_risk: medium
renewal_status: at_risk
---

## Executive Summary

Acme Corp's QBR revealed growing concerns about API latency impacting their
checkout flow. Mike specifically cited three incidents in the past month where
p99 response times exceeded their 200ms SLA. While they remain committed to
the platform, Lisa indicated they are evaluating a competitor as a fallback.
Renewal is in 90 days and is at risk without visible performance improvements.

## Health Signals

| Signal | Direction | Detail |
|--------|-----------|--------|
| API latency p99 | Negative | Exceeded 200ms SLA 3x in past month |
| Support ticket volume | Negative | Up 40% quarter over quarter |
| Feature adoption | Positive | Rolled out webhooks to 3 new teams |
| NPS score | Negative | Dropped from 8 to 6 |

## Commitments

| Commitment | Owner | Party | Due |
|------------|-------|-------|-----|
| Root cause analysis on latency spikes | Sarah | Us | 2026-02-28 |
| Share performance roadmap | Sarah | Us | 2026-03-07 |
| Provide API usage patterns for profiling | Mike | Customer | 2026-02-25 |

## Feature Requests

- **[High]** Dedicated API endpoint with guaranteed SLA — checkout flow is their #1 revenue path
- **[Medium]** Bulk import API — currently uploading records one at a time

## Escalations

- **[High]** Latency SLA breaches — 3 incidents in past month (Owner: Sarah, escalate to Engineering)

## Next Steps

- [ ] Sarah: Schedule follow-up in 2 weeks with latency RCA results
- [ ] Mike: Send API call logs from last 3 incident windows
- [ ] Sarah: Loop in Engineering lead for dedicated endpoint feasibility
```

### Planning Example

```markdown
---
date: "2026-02-21"
time: "10:00"
duration_min: 60
meeting_type: sprint_planning
participants:
  - Alice
  - Bob
  - Charlie
sprint_goal: "Complete sync pipeline testing and deploy to staging"
capacity_notes: "Charlie out Thursday/Friday. Bob at 80% — supporting on-call."
---

## Summary

Sprint 14 planning focused on getting the sync pipeline to staging. Team
committed to 21 story points across 8 items. Deferred the Slack integration
and advanced search to keep scope realistic given reduced capacity.

## Committed Work

| Priority | Item | Owner | Estimate |
|----------|------|-------|----------|
| P0 | Integration tests for sync pipeline | Bob | 5 pts |
| P0 | Deploy to staging environment | Alice | 3 pts |
| P1 | Error handling improvements | Charlie | 3 pts |
| P1 | Config validation CLI command | Bob | 2 pts |
| P2 | Logging improvements | Alice | 3 pts |
| P2 | SQLite backup script | Charlie | 2 pts |

## Deferred

- Slack notification integration — descoped from v1, revisit after launch
- Advanced transcript search — needs design review first

## Risks

| Risk | Mitigation | Owner |
|------|-----------|-------|
| Charlie's reduced availability | Front-load his work items Mon-Wed | Alice |
| Staging env not provisioned yet | Alice to request by EOD Monday | Alice |

## Dependencies

| Dependency | Team | Status |
|------------|------|--------|
| Staging environment provisioning | DevOps | Pending |
| Google Drive API quota increase | Google Support | Pending |

## Decisions

- **Defer Slack integration to v2** (Owner: Alice)
  Rationale: Reduced capacity this sprint, core pipeline is higher priority.
```

---

## 2. Config Schema Changes

### New top-level fields

```yaml
# Skip all side effects (no publish, no marking processed).
# Extracted markdown is logged to stdout.
dry_run: false
```

### Updated confluence section

```yaml
confluence:
  enabled: true           # false = skip publishing, still marks processed, logs markdown
  base_url: "https://your-company.atlassian.net"
  email: "your-email@example.com"
  api_token: "your-confluence-api-token"
  space_key: "ENG"
  parent_page_id: ""
```

### New prompts section

```yaml
prompts:
  # Template used when the LLM classifier returns an unrecognized type
  default_template: "engineering"

  # Prompt sent to the LLM to classify the meeting type.
  # {{.TemplateKeys}} is replaced with the available template names.
  # {{.TranscriptPreview}} is replaced with the first ~1000 chars.
  classification_prompt: |
    Classify this meeting transcript into exactly one of these categories: {{.TemplateKeys}}

    Respond with ONLY the category name, nothing else.

    TRANSCRIPT (first 1000 characters):
    {{.TranscriptPreview}}

  templates:
    engineering:
      description: "Standups, design reviews, retrospectives, incident reviews"
      extraction_prompt: |
        Analyze this engineering meeting transcript and produce a structured
        markdown document with YAML front-matter.

        TRANSCRIPT:
        {{.Transcript}}

        Produce a markdown document in EXACTLY this format:

        ---
        date: "YYYY-MM-DD"
        time: "HH:MM"
        duration_min: <integer>
        meeting_type: "standup|design_review|retrospective|incident_review"
        participants:
          - name1
          - name2
        projects:
          - project1
        sentiment: "urgent|calm|frustrated|excited"
        ---

        ## Summary
        Concise bullet points of what was covered.

        ## Decisions
        Each decision as: **Decision text** (Owner: name)
        Followed by rationale on next line.

        ## Blockers
        Each blocker as: **Blocker text** — Ticket: XXX
        With Impact and Escalation details below.

        ## Action Items
        Markdown task list: - [ ] Task (owner, due YYYY-MM-DD)

        ## Key Quotes
        Notable quotes as blockquotes: > "quote" — Speaker, context

        INSTRUCTIONS:
        - Be thorough. Extract ALL decisions, blockers, and action items.
        - Use "None identified." for empty sections — do not omit sections.
        - Produce ONLY the markdown document. No preamble, no explanation.

    customer_success:
      description: "Customer calls, QBRs, renewals, onboarding check-ins"
      extraction_prompt: |
        Analyze this customer meeting transcript and produce a structured
        markdown document with YAML front-matter. Focus on customer health
        signals, commitments, and risk indicators.

        TRANSCRIPT:
        {{.Transcript}}

        Produce a markdown document in EXACTLY this format:

        ---
        date: "YYYY-MM-DD"
        time: "HH:MM"
        duration_min: <integer>
        meeting_type: "qbr|check_in|onboarding|renewal|escalation"
        customer_name: "Customer Name"
        participants:
          - name1 (role)
          - name2 (role)
        customer_sentiment: "happy|neutral|concerned|frustrated|at_risk"
        churn_risk: "low|medium|high"
        renewal_status: "on_track|at_risk|churned|unknown"
        ---

        ## Executive Summary
        Narrative paragraph suitable for executive review.

        ## Health Signals
        Markdown table: | Signal | Direction | Detail |

        ## Commitments
        Markdown table: | Commitment | Owner | Party (us/customer) | Due |

        ## Feature Requests
        Bulleted list: - **[priority]** Request — context

        ## Escalations
        Bulleted list: - **[severity]** Issue (Owner: name)

        ## Next Steps
        Markdown task list: - [ ] Step (owner, due date)

        INSTRUCTIONS:
        - Write the Executive Summary as a narrative, not bullet points.
        - Distinguish between commitments WE made vs the CUSTOMER made.
        - Flag any churn risk signals explicitly.
        - Use "None identified." for empty sections.
        - Produce ONLY the markdown document.

    planning:
      description: "Sprint planning, roadmap reviews, capacity planning"
      extraction_prompt: |
        Analyze this planning meeting transcript and produce a structured
        markdown document with YAML front-matter.

        TRANSCRIPT:
        {{.Transcript}}

        Produce a markdown document in EXACTLY this format:

        ---
        date: "YYYY-MM-DD"
        time: "HH:MM"
        duration_min: <integer>
        meeting_type: "sprint_planning|roadmap|capacity|backlog_grooming"
        participants:
          - name1
          - name2
        sprint_goal: "One-line sprint goal"
        capacity_notes: "Capacity constraints or PTO"
        ---

        ## Summary
        Structured overview paragraph of the planning session.

        ## Committed Work
        Markdown table: | Priority | Item | Owner | Estimate |

        ## Deferred
        Bulleted list: - Item — reason for deferral

        ## Risks
        Markdown table: | Risk | Mitigation | Owner |

        ## Dependencies
        Markdown table: | Dependency | Team | Status (resolved/pending/blocked) |

        ## Decisions
        Each as: **Decision** (Owner: name)
        With rationale.

        INSTRUCTIONS:
        - Focus on what was committed vs deferred and why.
        - Capture priority levels and estimates where discussed.
        - Use "None identified." for empty sections.
        - Produce ONLY the markdown document.
```

---

## 3. Architecture Changes

### 3a. Two-Pass LLM Pipeline

```
Transcript → Classify (LLM #1, ~1000 chars, 32 max_tokens)
           → Select template
           → Extract (LLM #2, full transcript)
           → Markdown document (with YAML front-matter)
           → Parse front-matter for metadata
           → Convert markdown body → Confluence HTML (if publishing)
           → Publish
```

### 3b. New Data Model

```go
// internal/models/transcript.go

// ExtractionResult holds the LLM output after parsing.
type ExtractionResult struct {
    TemplateName string            // which template was used
    FrontMatter  map[string]any    // parsed YAML front-matter (date, participants, etc.)
    Markdown     string            // the markdown body (everything after front-matter)
    RawOutput    string            // the complete LLM output (front-matter + markdown)
}
```

Front-matter is parsed with `gopkg.in/yaml.v3` — split on `---` delimiters,
unmarshal the YAML block into `map[string]any`, keep the rest as markdown body.

### 3c. Extractor Interface Changes

```go
type Extractor interface {
    // Classify determines the meeting type from a transcript preview.
    Classify(transcriptPreview string, templateKeys []string) (string, error)

    // Extract sends the full transcript through the given prompt and
    // returns the raw LLM output (markdown with front-matter).
    Extract(transcript string, extractionPrompt string) (string, error)
}
```

`Extract` now returns a raw string — the caller parses front-matter and markdown
separately. This keeps the LLM layer simple (send prompt, get text back).

### 3d. Markdown-to-HTML Conversion for Confluence

The Confluence publisher needs a `markdownToHTML` function. Options:

1. **`github.com/yuin/goldmark`** — mature, extensible Go markdown parser.
   Handles tables, task lists, blockquotes, code blocks. Best option.
2. **`github.com/gomarkdown/markdown`** — simpler API, fewer extensions.
3. **`github.com/russross/blackfriday/v2`** — fast, well-known, good table support.

Recommendation: **goldmark** — it supports GFM (GitHub Flavored Markdown) extensions
including tables and task lists, which are heavily used in our templates.

```go
// internal/confluence/publisher.go

import "github.com/yuin/goldmark"

func markdownToHTML(md string) (string, error) {
    var buf bytes.Buffer
    parser := goldmark.New(
        goldmark.WithExtensions(
            extension.GFM,        // tables, strikethrough, task lists
        ),
    )
    if err := parser.Convert([]byte(md), &buf); err != nil {
        return "", fmt.Errorf("convert markdown to HTML: %w", err)
    }
    return buf.String(), nil
}
```

The existing `renderHTML()` function is replaced entirely — no more manual
string building. The LLM controls the document structure through markdown,
and goldmark handles the HTML conversion.

### 3e. Front-Matter Parser

```go
// internal/models/parse.go

import (
    "strings"
    "gopkg.in/yaml.v3"
)

// ParseExtractionOutput splits LLM output into front-matter and markdown body.
func ParseExtractionOutput(raw string, templateName string) (*ExtractionResult, error) {
    raw = strings.TrimSpace(raw)

    // Split on "---" delimiters
    if !strings.HasPrefix(raw, "---") {
        // No front-matter — treat entire output as markdown
        return &ExtractionResult{
            TemplateName: templateName,
            FrontMatter:  map[string]any{},
            Markdown:     raw,
            RawOutput:    raw,
        }, nil
    }

    // Find closing "---"
    rest := raw[3:] // skip opening ---
    idx := strings.Index(rest, "\n---")
    if idx < 0 {
        return nil, fmt.Errorf("malformed front-matter: no closing ---")
    }

    yamlBlock := strings.TrimSpace(rest[:idx])
    markdownBody := strings.TrimSpace(rest[idx+4:])

    var frontMatter map[string]any
    if err := yaml.Unmarshal([]byte(yamlBlock), &frontMatter); err != nil {
        return nil, fmt.Errorf("parse front-matter YAML: %w", err)
    }

    return &ExtractionResult{
        TemplateName: templateName,
        FrontMatter:  frontMatter,
        Markdown:     markdownBody,
        RawOutput:    raw,
    }, nil
}
```

### 3f. Updated Sync Pipeline

```go
func runSync(cfg *config.Config) error {
    // ... init clients ...

    for _, transcript := range pending {
        // 1. Classify
        preview := truncate(transcript.Content, 1000)
        templateKeys := cfg.Prompts.TemplateKeys()
        templateName, err := extractor.Classify(preview, templateKeys)
        if err != nil {
            slog.Warn("classification failed, using default", "error", err)
            templateName = cfg.Prompts.DefaultTemplate
        }

        tmpl := cfg.Prompts.Templates[templateName]
        slog.Info("classified transcript",
            "name", transcript.Name, "template", templateName)

        // 2. Extract — LLM returns markdown with YAML front-matter
        rawOutput, err := extractor.Extract(transcript.Content, tmpl.ExtractionPrompt)
        if err != nil {
            slog.Error("extraction failed", "id", transcript.ID, "error", err)
            continue
        }

        // 3. Parse front-matter + markdown body
        result, err := models.ParseExtractionOutput(rawOutput, templateName)
        if err != nil {
            slog.Error("parse extraction output failed", "id", transcript.ID, "error", err)
            continue
        }

        // 4. Dry run: log markdown and skip
        if cfg.DryRun {
            slog.Info("dry run — extracted output",
                "name", transcript.Name,
                "template", templateName)
            fmt.Println(result.RawOutput)
            continue // don't mark processed, don't publish
        }

        // 5. Publish (if enabled)
        var confluenceURL string
        if cfg.Confluence.Enabled {
            confluenceURL, err = confluenceClient.PublishMarkdown(
                cfg.Confluence.SpaceKey,
                cfg.Confluence.ParentPageID,
                result,
                transcript.Name,
            )
            if err != nil {
                slog.Error("publish failed", "id", transcript.ID, "error", err)
                continue
            }
        } else {
            slog.Info("confluence disabled — extracted output",
                "name", transcript.Name)
            fmt.Println(result.RawOutput)
            confluenceURL = "not-published"
        }

        // 6. Mark processed
        if err := store.MarkProcessed(transcript.ID, transcript.Name, confluenceURL); err != nil {
            slog.Error("mark processed failed", "id", transcript.ID, "error", err)
        }
    }
}
```

---

## 4. Config Struct Changes

```go
type Config struct {
    DryRun     bool             `yaml:"dry_run"`
    Google     GoogleConfig     `yaml:"google"`
    LLM        LLMConfig        `yaml:"llm"`
    Confluence ConfluenceConfig `yaml:"confluence"`
    Sync       SyncConfig       `yaml:"sync"`
    Logging    LoggingConfig    `yaml:"logging"`
    Prompts    PromptsConfig    `yaml:"prompts"`
}

type ConfluenceConfig struct {
    Enabled      bool   `yaml:"enabled"`   // default true
    BaseURL      string `yaml:"base_url"`
    Email        string `yaml:"email"`
    APIToken     string `yaml:"api_token"`
    SpaceKey     string `yaml:"space_key"`
    ParentPageID string `yaml:"parent_page_id"`
}

type PromptsConfig struct {
    DefaultTemplate      string                       `yaml:"default_template"`
    ClassificationPrompt string                       `yaml:"classification_prompt"`
    Templates            map[string]MeetingTemplate   `yaml:"templates"`
}

type MeetingTemplate struct {
    Description      string `yaml:"description"`
    ExtractionPrompt string `yaml:"extraction_prompt"`
}

// TemplateKeys returns sorted list of template names for the classifier.
func (p *PromptsConfig) TemplateKeys() []string
```

Note: `MeetingTemplate` no longer has a `confluence_template` field. The markdown
output is the template — the LLM controls the document structure, and goldmark
converts it to HTML mechanically.

---

## 5. Validation Changes

New validations:

- `prompts.default_template` must reference an existing template key
- `prompts.templates` must have at least one entry
- Each template must have non-empty `extraction_prompt`
- `classification_prompt` must contain `{{.TemplateKeys}}` and `{{.TranscriptPreview}}`
- When `confluence.enabled` is false, confluence auth fields (email, api_token, base_url, space_key) are not required
- `confluence.enabled` defaults to `true` if not specified
- `dry_run` defaults to `false`

---

## 6. Behavioral Summary

| `dry_run` | `confluence.enabled` | Behavior |
|-----------|---------------------|----------|
| `false`   | `true`              | Full pipeline: classify → extract → publish → mark processed |
| `false`   | `false`             | Classify → extract → log markdown → mark processed (no publish) |
| `true`    | `true` or `false`   | Classify → extract → log markdown → stop (no publish, no mark) |

---

## 7. New Dependency

```
github.com/yuin/goldmark  // GFM-compatible markdown → HTML
```

---

## 8. Files Modified

| File | Change |
|------|--------|
| `go.mod` | Add `github.com/yuin/goldmark` |
| `internal/config/config.go` | Add `DryRun`, `Confluence.Enabled`, `PromptsConfig`, `MeetingTemplate`. Update validation. |
| `internal/models/transcript.go` | Add `ExtractionResult` struct. |
| `internal/models/parse.go` | New file: `ParseExtractionOutput` — splits YAML front-matter from markdown body. |
| `internal/llm/extractor.go` | Add `Classify` to interface. Change `Extract` to accept prompt string, return raw string. |
| `internal/llm/util.go` | Remove `buildPrompt()` (prompts now come from config). Keep `retryable`, `cleanJSONResponse` → rename to `cleanResponse`. |
| `internal/llm/anthropic.go` | Implement `Classify`. Update `Extract` — accept prompt, return string (no JSON parsing). |
| `internal/llm/openai.go` | Implement `Classify`. Update `Extract` — accept prompt, return string (no JSON parsing). |
| `internal/confluence/publisher.go` | Replace `renderHTML()` with `markdownToHTML()` using goldmark. Add `PublishMarkdown` method. Page title derived from front-matter `date` field. |
| `cmd/omniscient/sync.go` | Two-pass pipeline. Handle `dry_run` and `confluence.enabled`. |
| `config.yaml.example` | Add `prompts` section with all three templates. Add `dry_run` and `confluence.enabled`. |
| All `*_test.go` | Update for new signatures and add template/classification/parsing tests. |

---

## 9. New Tests

| Test | Description |
|------|-------------|
| `TestClassify_ReturnsValidTemplate` | Mock LLM returns "customer_success", verify mapped correctly |
| `TestClassify_FallbackToDefault` | Mock LLM returns garbage, verify default_template used |
| `TestExtract_ReturnsRawString` | Mock LLM returns markdown, verify raw string returned |
| `TestParseExtractionOutput_WithFrontMatter` | Parse full output, verify front-matter fields and markdown body separated |
| `TestParseExtractionOutput_NoFrontMatter` | Parse markdown without `---`, verify empty front-matter, full body |
| `TestParseExtractionOutput_MalformedFrontMatter` | Missing closing `---`, verify error |
| `TestMarkdownToHTML_Tables` | Convert GFM table to HTML, verify `<table>` output |
| `TestMarkdownToHTML_TaskLists` | Convert `- [ ]` items to HTML checkboxes |
| `TestMarkdownToHTML_Blockquotes` | Convert `>` quotes to `<blockquote>` |
| `TestPublishMarkdown_TitleFromFrontMatter` | Verify page title uses front-matter date field |
| `TestSyncDryRun` | Verify no publish calls, no mark processed, markdown printed to stdout |
| `TestSyncConfluenceDisabled` | Verify no publish but mark processed happens |
| `TestPromptsValidation` | Missing templates, bad default_template ref, empty extraction_prompt |

---

## 10. Migration Path

Phase 1 code continues to work as-is. Phase 2 changes are additive:

1. If `prompts` section is absent from config, fall back to current behavior (hardcoded prompt, JSON output, `renderHTML()`).
2. If `prompts` section is present, use the new pipeline (classification → template-driven extraction → markdown → goldmark HTML).
3. Once Phase 2 is validated, remove the Phase 1 fallback path and the `MeetingData` struct.
