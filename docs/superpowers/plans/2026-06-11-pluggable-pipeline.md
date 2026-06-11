# Pluggable Transcript Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor omniscient into a pluggable Source/Destination pipeline (Google Meet + Confluence initially), harden the LLM stages for 14B-class local models, and ship junior-engineer documentation.

**Architecture:** Provider-neutral `models.Transcript` flows from `source.Source` implementations through `internal/pipeline.Service` (classify → extract → validate → publish) to `destination.Destination` implementations. Dedup keys become `source:id` with a versioned SQLite migration. Confluence-specific concepts move out of the pipeline into the Confluence destination.

**Tech Stack:** Go 1.23, Cobra, `modernc.org/sqlite`, `net/http/httptest` for tests, goldmark, google-api-go-client.

**Spec:** `docs/superpowers/specs/2026-06-11-pluggable-pipeline-design.md`

**Conventions for every task:** run tests with `go test ./... 2>&1 | tail -20` from `/archive/omniscient`. Commit messages use Conventional Commits. The repo has a commit hook enforcing this. TDD: write the failing test first, watch it fail, implement, watch it pass, commit.

---

### Task 1: Provider-neutral Transcript model

**Files:**
- Modify: `internal/models/transcript.go` (add type at top, after imports)
- Test: `internal/models/transcript_test.go` (append)

- [ ] **Step 1: Write the failing test** — append to `internal/models/transcript_test.go`:

```go
func TestTranscriptKey(t *testing.T) {
	tr := &Transcript{ID: "abc123", Source: "googlemeet", Title: "Standup"}
	if got, want := tr.Key(), "googlemeet:abc123"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/models/ -run TestTranscriptKey -v` — expect FAIL (undefined: Transcript).

- [ ] **Step 3: Implement** — add to `internal/models/transcript.go` (add `"time"` to imports):

```go
// Transcript is a provider-neutral meeting transcript produced by a Source.
type Transcript struct {
	ID         string    // provider-native ID (e.g. Drive file ID)
	Source     string    // source name, e.g. "googlemeet"
	Title      string    // human-readable title (e.g. Drive file name)
	ModifiedAt time.Time // last modification time at the provider
	Content    string    // plain-text transcript content
}

// Key returns the globally unique dedup key, e.g. "googlemeet:abc123".
// Source-prefixing prevents ID collisions between providers.
func (t *Transcript) Key() string {
	return t.Source + ":" + t.ID
}
```

- [ ] **Step 4: Run** the test again — expect PASS. Run `go build ./...` — expect clean.

- [ ] **Step 5: Commit** — `git add internal/models/ && git commit -m "feat: add provider-neutral Transcript model with source-prefixed dedup key"`

---

### Task 2: Source interface + googlemeet package (move internal/drive)

**Files:**
- Create: `internal/source/source.go`
- Move: `internal/drive/` → `internal/source/googlemeet/` (client.go, oauth.go, client_test.go)
- Test: `internal/source/googlemeet/client_test.go` (append)

- [ ] **Step 1: Move the package**

```bash
git mv internal/drive internal/source/googlemeet
```

In all three moved files change `package drive` → `package googlemeet`.

- [ ] **Step 2: Create `internal/source/source.go`:**

```go
// Package source defines the interface every meeting-platform provider
// implements. Implementations live in subpackages (e.g. googlemeet) and own
// their configuration and authentication. See docs/ADDING_A_PROVIDER.md.
package source

import (
	"context"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
)

// Source fetches recent meeting transcripts from a meeting platform.
type Source interface {
	// Name returns the provider name used in dedup keys and logs ("googlemeet").
	Name() string
	// ListRecent returns transcripts modified within the given duration.
	ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error)
}
```

- [ ] **Step 3: Write the failing test** — append to `internal/source/googlemeet/client_test.go`:

```go
func TestSourceName(t *testing.T) {
	s := &Source{}
	if got := s.Name(); got != "googlemeet" {
		t.Errorf("Name() = %q, want %q", got, "googlemeet")
	}
}
```

Run `go test ./internal/source/googlemeet/ -run TestSourceName -v` — expect FAIL (undefined: Source).

- [ ] **Step 4: Rework `internal/source/googlemeet/client.go`** — rename it to `googlemeet.go` (`git mv internal/source/googlemeet/client.go internal/source/googlemeet/googlemeet.go`). Replace the `Transcript` type, `Client` type, `NewClient`, and `GetRecentTranscripts` as follows (the body of the paging loop and `exportFileAsText` stay identical except where shown):

```go
// SourceName is the provider name used in dedup keys, config, and logs.
const SourceName = "googlemeet"

// Source provides authenticated access to Google Meet transcripts, which
// Google Meet saves as Google Docs in a Drive folder.
type Source struct {
	service  *drive.Service
	folderID string
}

// New creates an authenticated Google Meet source. It loads the OAuth config
// from credentialsPath and the saved token from tokenPath, then constructs a
// Drive service with an auto-refreshing token source.
//
// The token must already exist — run `omniscient auth googlemeet` first.
func New(ctx context.Context, credentialsPath, tokenPath, folderID string) (*Source, error) {
	config, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("loading OAuth config: %w", err)
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("loading token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token found at %s — run 'omniscient auth googlemeet' first to authorize", tokenPath)
	}

	tokenSource := getTokenSource(ctx, config, tokenPath, token)
	httpClient := oauth2.NewClient(ctx, tokenSource)

	service, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating Drive service: %w", err)
	}

	slog.Info("Google Meet source initialized")

	return &Source{service: service, folderID: folderID}, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return SourceName }

// ListRecent fetches transcript documents from the configured folder that
// were modified within the given duration. Each document is exported as
// plain text. If a single file export fails, the error is logged and that
// file is skipped — remaining files are still processed.
func (s *Source) ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error) {
```

Inside `ListRecent` (formerly `GetRecentTranscripts`): use `s.folderID` instead of the removed `folderID` parameter, `s.service` instead of `c.service`, import `github.com/jgavinray/omniscient/internal/models`, and build results as:

```go
			transcripts = append(transcripts, &models.Transcript{
				ID:         file.Id,
				Source:     SourceName,
				Title:      file.Name,
				ModifiedAt: modifiedAt,
				Content:    content,
			})
```

with `var transcripts []*models.Transcript` at the top. `exportFileAsText` keeps receiver rename `(s *Source)`. Do NOT change `oauth.go` logic — only its package clause.

- [ ] **Step 5: Run** `go test ./internal/source/... -v 2>&1 | tail -15` — expect PASS (oauth token tests + TestSourceName). `go build ./...` will fail because `cmd/omniscient` still imports `internal/drive` — that is expected until Task 7/8; verify only the source tree: `go vet ./internal/source/...` — expect clean.

- [ ] **Step 6: Commit** — `git add -A internal/source internal/drive && git commit -m "refactor: move drive package to source/googlemeet behind Source interface"`

> Note: `cmd/omniscient` is temporarily broken (imports `internal/drive`). Tasks 7–8 restore it. Run package-scoped tests until then.

---

### Task 3: Destination interface + Confluence publisher adapter

**Files:**
- Create: `internal/destination/destination.go`
- Move: `internal/confluence/` → `internal/destination/confluence/`
- Modify: `internal/destination/confluence/publisher.go` (append adapter)
- Test: `internal/destination/confluence/publisher_test.go` (append)

- [ ] **Step 1: Move the package**

```bash
git mv internal/confluence internal/destination/confluence
```

Package name stays `confluence`; only the import path changes.

- [ ] **Step 2: Create `internal/destination/destination.go`:**

```go
// Package destination defines the interface every knowledge-base provider
// implements. Implementations live in subpackages (e.g. confluence) and own
// their configuration and authentication. See docs/ADDING_A_PROVIDER.md.
package destination

import (
	"context"

	"github.com/jgavinray/omniscient/internal/models"
)

// Destination publishes extracted meeting notes to a knowledge base.
type Destination interface {
	// Name returns the provider name used in logs and published-URL maps.
	Name() string
	// Publish creates or updates a page for the transcript's notes and
	// returns its URL. Publish MUST be idempotent: publishing the same
	// transcript twice must update the same page, not create a duplicate —
	// the pipeline relies on this to retry safely after partial failures.
	Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (string, error)
}
```

- [ ] **Step 3: Write the failing test** — append to `internal/destination/confluence/publisher_test.go`:

```go
func TestPublisherImplementsDestination(t *testing.T) {
	p := NewPublisher("https://example.atlassian.net", "a@b.c", "tok", "ENG", "")
	if got := p.Name(); got != "confluence" {
		t.Errorf("Name() = %q, want %q", got, "confluence")
	}
}
```

Run `go test ./internal/destination/confluence/ -run TestPublisherImplementsDestination -v` — expect FAIL (undefined: NewPublisher).

- [ ] **Step 4: Implement the adapter** — append to `internal/destination/confluence/publisher.go`:

```go
// Publisher adapts Client to the destination.Destination interface, carrying
// the Confluence-specific target (space key, parent page) so those concepts
// stay out of the pipeline.
type Publisher struct {
	client       *Client
	spaceKey     string
	parentPageID string
}

// NewPublisher creates a Destination that publishes to the given Confluence
// space, optionally nesting pages under parentPageID.
func NewPublisher(baseURL, email, apiToken, spaceKey, parentPageID string) *Publisher {
	return &Publisher{
		client:       NewClient(baseURL, email, apiToken),
		spaceKey:     spaceKey,
		parentPageID: parentPageID,
	}
}

// Name implements destination.Destination.
func (p *Publisher) Name() string { return "confluence" }

// Publish implements destination.Destination. It is idempotent: PublishMarkdown
// updates an existing page with the same title instead of creating a duplicate.
func (p *Publisher) Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (string, error) {
	return p.client.PublishMarkdown(ctx, p.spaceKey, p.parentPageID, result, t.Title)
}
```

- [ ] **Step 5: Run** `go test ./internal/destination/... -v 2>&1 | tail -15` — expect PASS (existing publisher tests + new one). `go vet ./internal/destination/...` — clean.

- [ ] **Step 6: Commit** — `git add -A internal/destination internal/confluence && git commit -m "refactor: move confluence package behind Destination interface with Publisher adapter"`

---

### Task 4: Config schema v2 (sources/destinations maps)

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (rewrite fixtures, add tests)

- [ ] **Step 1: Update test fixtures and add failing tests.** In `internal/config/config_test.go`, the two fixture helpers (lines ~12 and ~40) currently emit old-schema YAML starting with `google:`. Replace their YAML with the new schema (keep whatever llm/sync/logging/prompts content they already have — only restructure google/confluence):

```yaml
sources:
  googlemeet:
    credentials_file: /opt/omniscient/credentials.json
    token_file: /opt/omniscient/token.json
    folder_id: abc123folderID
destinations:
  confluence:
    base_url: https://example.atlassian.net
    email: test@example.com
    api_token: token123
    space_key: ENG
```

Update every assertion referencing `cfg.Google.*` → `cfg.Sources.GoogleMeet.*` and `cfg.Confluence.*` → `cfg.Destinations.Confluence.*`, and expected error strings `google.credentials_file` → `sources.googlemeet.credentials_file`, `google.folder_id` → `sources.googlemeet.folder_id`, `confluence.*` → `destinations.confluence.*`. Then add:

```go
func TestLoadRejectsOldSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	old := "google:\n  credentials_file: /a/b.json\n  token_file: /a/t.json\n  folder_id: abc123folder\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected old-schema error, got nil")
	}
	if !strings.Contains(err.Error(), "sources.googlemeet") {
		t.Errorf("error = %q, want migration hint mentioning sources.googlemeet", err.Error())
	}
}

func TestValidateRequiresEnabledSourceAndDestination(t *testing.T) {
	cfg := validTestConfig(t) // helper: parse the valid fixture
	disabled := false
	cfg.Sources.GoogleMeet.Enabled = &disabled
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "at least one source") {
		t.Errorf("validate() = %v, want at-least-one-source error", err)
	}

	cfg = validTestConfig(t)
	cfg.Destinations.Confluence.Enabled = &disabled
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "at least one destination") {
		t.Errorf("validate() = %v, want at-least-one-destination error", err)
	}

	// Dry-run permits zero destinations.
	cfg = validTestConfig(t)
	cfg.Destinations.Confluence.Enabled = &disabled
	cfg.DryRun = true
	if err := cfg.validate(); err != nil {
		t.Errorf("validate() with dry_run = %v, want nil", err)
	}
}

func TestMaxTranscriptCharsDefault(t *testing.T) {
	cfg := validTestConfig(t)
	if cfg.LLM.MaxTranscriptChars != 100000 {
		t.Errorf("MaxTranscriptChars = %d, want 100000", cfg.LLM.MaxTranscriptChars)
	}
}
```

If no `validTestConfig` helper exists, add one that writes the valid fixture YAML to a temp file, calls `Load`, and fails the test on error. Run `go test ./internal/config/ 2>&1 | tail -10` — expect FAIL (undefined fields).

- [ ] **Step 2: Implement in `internal/config/config.go`.** Replace the `Config`, `GoogleConfig` declarations:

```go
// Config is the top-level configuration for the omniscient application.
type Config struct {
	Sources      SourcesConfig      `yaml:"sources"`
	Destinations DestinationsConfig `yaml:"destinations"`
	LLM          LLMConfig          `yaml:"llm"`
	Sync         SyncConfig         `yaml:"sync"`
	Logging      LoggingConfig      `yaml:"logging"`
	DryRun       bool               `yaml:"dry_run"`
	Prompts      PromptsConfig      `yaml:"prompts"`
}

// SourcesConfig holds one optional entry per supported meeting platform.
// To add a provider, add a field here and wire it in cmd/omniscient/sync.go.
type SourcesConfig struct {
	GoogleMeet GoogleMeetConfig `yaml:"googlemeet"`
}

// GoogleMeetConfig configures harvesting Google Meet transcripts, which Meet
// saves as Google Docs in a Drive folder.
type GoogleMeetConfig struct {
	Enabled         *bool  `yaml:"enabled"`
	CredentialsFile string `yaml:"credentials_file"`
	TokenFile       string `yaml:"token_file"`
	FolderID        string `yaml:"folder_id"`
}

// IsEnabled defaults to true when the field is unset.
func (c *GoogleMeetConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// DestinationsConfig holds one optional entry per supported knowledge base.
type DestinationsConfig struct {
	Confluence ConfluenceConfig `yaml:"confluence"`
}
```

`ConfluenceConfig` keeps its existing fields and `IsEnabled`. Add to `LLMConfig`:

```go
	// MaxTranscriptChars triggers a warning (not a failure) for transcripts
	// longer than this; very long inputs degrade small-model output quality.
	MaxTranscriptChars int `yaml:"max_transcript_chars"`
```

In `applyDefaults` add:

```go
	if c.LLM.MaxTranscriptChars == 0 {
		c.LLM.MaxTranscriptChars = 100000
	}
```

In `Load`, after unmarshal succeeds, detect the old schema:

```go
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err == nil {
		if _, ok := probe["google"]; ok {
			return nil, fmt.Errorf("config file %s uses the old schema: move google: to sources.googlemeet: and confluence: to destinations.confluence: (see config.yaml.example)", path)
		}
		if _, ok := probe["confluence"]; ok {
			return nil, fmt.Errorf("config file %s uses the old schema: move confluence: to destinations.confluence: (see config.yaml.example)", path)
		}
	}
```

In `validate()`: replace the Google block with a guarded block (field names in errors become `sources.googlemeet.*`), guard the Confluence block with `c.Destinations.Confluence.IsEnabled()` (error strings become `destinations.confluence.*`), and add before them:

```go
	enabledSources := 0
	if c.Sources.GoogleMeet.IsEnabled() {
		enabledSources++
	}
	if enabledSources == 0 {
		return fmt.Errorf("at least one source must be enabled under sources:")
	}

	enabledDestinations := 0
	if c.Destinations.Confluence.IsEnabled() {
		enabledDestinations++
	}
	if enabledDestinations == 0 && !c.DryRun {
		return fmt.Errorf("at least one destination must be enabled under destinations: (or set dry_run: true)")
	}
```

Validate GoogleMeet fields only when `c.Sources.GoogleMeet.IsEnabled()`.

- [ ] **Step 3: Run** `go test ./internal/config/ -v 2>&1 | tail -15` — expect PASS.

- [ ] **Step 4: Commit** — `git add internal/config/ && git commit -m "feat: multi-provider config schema with sources/destinations sections"`

---

### Task 5: LLM: temperature 0 + Anthropic-compatible base URL for local models

**Files:**
- Modify: `internal/llm/openai.go:70`, `internal/llm/anthropic.go`, `internal/llm/extractor.go`, `internal/config/config.go` (LLM defaults/validation)
- Test: `internal/llm/openai_test.go`, `internal/llm/anthropic_test.go`, `internal/config/config_test.go` (append)

- [ ] **Step 1: Write failing tests.** Both test files already use `httptest` servers; follow the same pattern. Append (adapting handler boilerplate to match the existing tests in each file):

```go
// openai_test.go
func TestOpenAITemperatureZero(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"engineering"}}]}`)
	}))
	defer srv.Close()
	e := NewOpenAIExtractor(srv.URL, "k", "m", 5*time.Second)
	if _, err := e.Classify(context.Background(), "p", []string{"engineering"}, "{{TEMPLATE_KEYS}} {{TRANSCRIPT_PREVIEW}}"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"temperature":0`) || strings.Contains(string(gotBody), `"temperature":0.1`) {
		t.Errorf("request body temperature not 0: %s", gotBody)
	}
}
```

For `anthropic_test.go`, the same shape: point `e.baseURL = srv.URL` (existing tests show how the base URL is overridden), respond with `{"content":[{"text":"engineering"}]}`, assert `"temperature":0` present. Also add base-URL tests:

```go
// anthropic_test.go
func TestNewAnthropicExtractorCustomBaseURL(t *testing.T) {
	e := NewAnthropicExtractor("http://localhost:8080", "", "local-model", 5*time.Second)
	if e.baseURL != "http://localhost:8080" {
		t.Errorf("baseURL = %q, want custom URL", e.baseURL)
	}
	e = NewAnthropicExtractor("", "sk-ant-x", "claude-sonnet-4", 5*time.Second)
	if e.baseURL != "https://api.anthropic.com" {
		t.Errorf("baseURL = %q, want default", e.baseURL)
	}
}
```

And config tests in `internal/config/config_test.go`:

```go
func TestAnthropicKeyOptionalForCustomBaseURL(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.AnthropicBaseURL = "http://localhost:8080"
	cfg.LLM.AnthropicAPIKey = "" // local servers don't need a real key
	if err := cfg.validate(); err != nil {
		t.Errorf("validate() = %v, want nil for custom anthropic base URL", err)
	}

	cfg.LLM.AnthropicBaseURL = "https://api.anthropic.com"
	if err := cfg.validate(); err == nil {
		t.Error("validate() = nil, want sk-ant- key error for default endpoint")
	}
}
```

Run `go test ./internal/llm/ ./internal/config/ -run 'Temperature|BaseURL|Anthropic' -v` — expect FAIL (openai sends 0.1; anthropic lacks the field/param).

- [ ] **Step 2: Implement.**
  1. `openai.go:70`: `Temperature: 0.1,` → `Temperature: 0,`.
  2. `anthropic.go`: add to `anthropicRequest`: `Temperature float64 \`json:"temperature"\`` and set `Temperature: 0` in the `callAPI` reqBody literal. Change the constructor to accept a base URL (mirrors `NewOpenAIExtractor`):

```go
// NewAnthropicExtractor creates an AnthropicExtractor. baseURL may point at
// any Anthropic-compatible Messages API server (llama.cpp, LM Studio, a
// proxy); empty means the official https://api.anthropic.com.
func NewAnthropicExtractor(baseURL, apiKey, model string, timeout time.Duration) *AnthropicExtractor {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicExtractor{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("unexpected redirect to %s", req.URL)
			},
		},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}
```

  In `callAPI`, send the key header only when present: wrap `req.Header.Set("x-api-key", e.apiKey)` in `if e.apiKey != ""`. Update existing anthropic tests that call the old constructor signature.
  3. `extractor.go`: `case "anthropic":` becomes `return NewAnthropicExtractor(cfg.AnthropicBaseURL, cfg.AnthropicAPIKey, cfg.Model, time.Duration(cfg.Timeout)*time.Second), nil`.
  4. `config.go`: add to `LLMConfig`: `AnthropicBaseURL string \`yaml:"anthropic_base_url"\`` with comment "Anthropic-compatible Messages API endpoint; empty = api.anthropic.com". In `applyDefaults`: `if c.LLM.AnthropicBaseURL == "" { c.LLM.AnthropicBaseURL = "https://api.anthropic.com" }`. In `validate()` replace the anthropic case body with:

```go
	case "anthropic":
		if err := validateURL("llm.anthropic_base_url", c.LLM.AnthropicBaseURL, []string{"http", "https"}); err != nil {
			return err
		}
		// Only the official endpoint requires a real Anthropic key; local
		// Anthropic-compatible servers accept any or no key.
		if c.LLM.AnthropicBaseURL == "https://api.anthropic.com" {
			if c.LLM.AnthropicAPIKey == "" {
				return fmt.Errorf("llm.anthropic_api_key is required when provider is anthropic")
			}
			if !strings.HasPrefix(c.LLM.AnthropicAPIKey, "sk-ant-") {
				return fmt.Errorf("llm.anthropic_api_key must start with \"sk-ant-\"")
			}
		}
```

- [ ] **Step 3: Run** `go test ./internal/llm/ ./internal/config/ -v 2>&1 | tail -10` — expect PASS.

- [ ] **Step 4: Commit** — `git add internal/llm/ internal/config/ && git commit -m "feat: configurable anthropic-compatible endpoint and temperature 0 for local LLMs"`

---

### Task 6: Database schema v2 (source-prefixed keys)

**Files:**
- Modify: `internal/database/store.go` (`ensureSchema` + new migration funcs)
- Test: `internal/database/store_test.go` (append)

- [ ] **Step 1: Write the failing test** — append to `internal/database/store_test.go`:

```go
func TestMigrationV2PrefixesUnprefixedKeys(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// First open creates the current schema; insert a legacy (unprefixed) row.
	s, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.MarkProcessed(ctx, "abc123", "Old Meeting", "https://x/page"); err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-v2 database: clear the version stamp.
	if _, err := s.db.Exec(`DELETE FROM schema_version`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Re-open: v2 migration must prefix the key.
	s2, err := NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	processed, err := s2.IsProcessed(ctx, "googlemeet:abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Error("expected googlemeet:abc123 to be processed after v2 migration")
	}
	processed, err = s2.IsProcessed(ctx, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Error("unprefixed key abc123 should no longer exist after migration")
	}
}
```

Run `go test ./internal/database/ -run TestMigrationV2 -v` — expect FAIL (no schema_version table).

- [ ] **Step 2: Implement.** In `store.go`, at the end of `ensureSchema` (all return paths that currently `return nil` should instead `return ensureVersion(db)`), add:

```go
// targetSchemaVersion is bumped whenever a data migration is added.
// Version 2 introduced source-prefixed transcript IDs ("googlemeet:<id>").
const targetSchemaVersion = 2

// ensureVersion creates the schema_version table if needed and runs any
// pending data migrations, stamping the target version when done.
func ensureVersion(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	var version int
	err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	if err == sql.ErrNoRows {
		version = 1 // pre-versioning databases are treated as v1
	} else if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version < 2 {
		if err := migrateToV2(db); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`DELETE FROM schema_version`); err != nil {
		return fmt.Errorf("clear schema version: %w", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, targetSchemaVersion); err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	return nil
}

// migrateToV2 prefixes legacy transcript IDs with "googlemeet:" — before v2
// the only source was Google Meet via Drive, so all unprefixed IDs belong to
// it. Prefixing keeps multi-source dedup keys collision-free.
func migrateToV2(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin v2 migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE processed_transcripts SET transcript_id = 'googlemeet:' || transcript_id WHERE transcript_id NOT LIKE '%:%'`); err != nil {
		return fmt.Errorf("prefix processed_transcripts ids: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sync_events SET transcript_id = 'googlemeet:' || transcript_id WHERE transcript_id != '' AND transcript_id NOT LIKE '%:%'`); err != nil {
		return fmt.Errorf("prefix sync_events ids: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit v2 migration: %w", err)
	}
	return nil
}
```

Fresh databases get the prefix-free migration run too (it's a no-op on empty tables), keeping one code path.

- [ ] **Step 3: Run** `go test ./internal/database/ -v 2>&1 | tail -15` — expect PASS (new + existing migration tests).

- [ ] **Step 4: Commit** — `git add internal/database/ && git commit -m "feat: versioned schema migration to source-prefixed transcript keys"`

---

### Task 7: Pipeline package (multi-source, multi-destination, 14B guards)

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Create: `internal/pipeline/pipeline_test.go` (port useful cases from `cmd/omniscient/sync_service_test.go`)

This is the heart of the refactor: `SyncService` moves out of `cmd/`, becomes provider-neutral, gains classification validation, extraction retry-with-feedback, a transcript length warning, and multi-destination publishing. It also fixes the hand-rolled (unescaped) JSON in `recordEvent` by using `json.Marshal`.

- [ ] **Step 1: Write the failing tests** — create `internal/pipeline/pipeline_test.go`:

```go
package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/models"
)

// --- fakes -----------------------------------------------------------------

type fakeSource struct {
	name        string
	transcripts []*models.Transcript
	err         error
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error) {
	return f.transcripts, f.err
}

type fakeExtractor struct {
	classifyResults []string // popped in order; repeats last
	classifyCalls   []string // prompts received
	extractResults  []string // popped in order; repeats last
	extractCalls    []string // prompts received
}

func (f *fakeExtractor) Classify(ctx context.Context, preview string, keys []string, prompt string) (string, error) {
	f.classifyCalls = append(f.classifyCalls, prompt)
	return f.pop(&f.classifyResults), nil
}
func (f *fakeExtractor) Extract(ctx context.Context, transcript string, prompt string) (string, error) {
	f.extractCalls = append(f.extractCalls, prompt)
	return f.pop(&f.extractResults), nil
}
func (f *fakeExtractor) pop(s *[]string) string {
	if len(*s) == 0 {
		return ""
	}
	v := (*s)[0]
	if len(*s) > 1 {
		*s = (*s)[1:]
	}
	return v
}

type fakeDestination struct {
	name      string
	url       string
	err       error
	published []*models.Transcript
}

func (f *fakeDestination) Name() string { return f.name }
func (f *fakeDestination) Publish(ctx context.Context, r *models.ExtractionResult, t *models.Transcript) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.published = append(f.published, t)
	return f.url, nil
}

type fakeStore struct {
	processed map[string]string // key -> urls JSON
	failed    map[string]string // key -> error message
}

func newFakeStore() *fakeStore {
	return &fakeStore{processed: map[string]string{}, failed: map[string]string{}}
}
func (f *fakeStore) IsProcessed(ctx context.Context, key string) (bool, error) {
	_, ok := f.processed[key]
	return ok, nil
}
func (f *fakeStore) MarkProcessed(ctx context.Context, key, name, urls string) error {
	f.processed[key] = urls
	return nil
}
func (f *fakeStore) MarkFailed(ctx context.Context, key, name, msg string) error {
	f.failed[key] = msg
	return nil
}
func (f *fakeStore) RecordSyncEvent(ctx context.Context, e *database.SyncEvent) error { return nil }

// --- helpers ---------------------------------------------------------------

const validOutput = "---\ndate: \"2026-06-11\"\n---\n## Summary\nNotes."

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Sync.MaxPerRun = 50
	cfg.Sync.LookbackHours = 24
	cfg.LLM.MaxTranscriptChars = 100000
	cfg.Prompts.ClassifyPrompt = "{{TEMPLATE_KEYS}} {{TRANSCRIPT_PREVIEW}}"
	cfg.Prompts.Templates = map[string]config.MeetingTemplate{
		"engineering": {ExtractionPrompt: "extract {{TRANSCRIPT}}"},
	}
	return cfg
}

func testTranscript(id string) *models.Transcript {
	return &models.Transcript{ID: id, Source: "fake", Title: "Meeting " + id, Content: "hello world"}
}

// --- tests -----------------------------------------------------------------

func TestRunPublishesToAllDestinations(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	d1 := &fakeDestination{name: "confluence", url: "https://c/1"}
	d2 := &fakeDestination{name: "notion", url: "https://n/1"}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{d1, d2}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	urls, ok := store.processed["fake:t1"]
	if !ok {
		t.Fatal("transcript not marked processed under source-prefixed key")
	}
	if !strings.Contains(urls, "https://c/1") || !strings.Contains(urls, "https://n/1") {
		t.Errorf("published URLs JSON missing destinations: %s", urls)
	}
	if len(d1.published) != 1 || len(d2.published) != 1 {
		t.Errorf("publish counts: confluence=%d notion=%d, want 1 and 1", len(d1.published), len(d2.published))
	}
}

func TestRunPartialPublishFailureMarksFailed(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	good := &fakeDestination{name: "confluence", url: "https://c/1"}
	bad := &fakeDestination{name: "notion", err: errors.New("boom")}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{good, bad}, store, testConfig())
	_ = svc.Run(context.Background())

	if _, ok := store.processed["fake:t1"]; ok {
		t.Error("transcript must NOT be marked processed when any destination fails")
	}
	if _, ok := store.failed["fake:t1"]; !ok {
		t.Error("transcript must be marked failed on partial publish failure")
	}
}

func TestClassifyInvalidRetriesThenFallsBack(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	// Both attempts return garbage -> pipeline falls back to the only template.
	ext := &fakeExtractor{classifyResults: []string{"banana", "still-banana"}, extractResults: []string{validOutput}}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{d}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ext.classifyCalls) != 2 {
		t.Errorf("classify calls = %d, want 2 (initial + corrective retry)", len(ext.classifyCalls))
	}
	if !strings.Contains(ext.classifyCalls[1], "banana") {
		t.Errorf("retry prompt should quote the bad answer, got: %s", ext.classifyCalls[1])
	}
	if _, ok := store.processed["fake:t1"]; !ok {
		t.Error("fallback template should still allow processing to complete")
	}
}

func TestExtractMalformedRetriesWithFeedback(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{
		classifyResults: []string{"engineering"},
		extractResults:  []string{"no front matter here", validOutput},
	}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{d}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(ext.extractCalls) != 2 {
		t.Fatalf("extract calls = %d, want 2 (initial + corrective retry)", len(ext.extractCalls))
	}
	if !strings.Contains(ext.extractCalls[1], "could not be parsed") {
		t.Errorf("retry prompt should explain the parse failure, got: %s", ext.extractCalls[1])
	}
	if _, ok := store.processed["fake:t1"]; !ok {
		t.Error("successful retry should mark transcript processed")
	}
}

func TestExtractMalformedTwiceMarksFailed(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{"junk", "more junk"}}
	store := newFakeStore()

	svc := New([]Source{src}, ext, []Destination{&fakeDestination{name: "confluence"}}, store, testConfig())
	_ = svc.Run(context.Background())

	if _, ok := store.failed["fake:t1"]; !ok {
		t.Error("transcript must be marked failed after two malformed extractions")
	}
}

func TestRunSkipsProcessedAndDryRun(t *testing.T) {
	src := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1"), testTranscript("t2")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()
	store.processed["fake:t1"] = "{}" // already done

	cfg := testConfig()
	cfg.DryRun = true
	svc := New([]Source{src}, ext, []Destination{d}, store, cfg)
	if err := svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(d.published) != 0 {
		t.Errorf("dry run must not publish, got %d publishes", len(d.published))
	}
	if len(store.processed) != 1 {
		t.Errorf("dry run must not mark new transcripts processed, processed=%v", store.processed)
	}
}

func TestRunContinuesWhenOneSourceFails(t *testing.T) {
	bad := &fakeSource{name: "bad", err: errors.New("api down")}
	good := &fakeSource{name: "fake", transcripts: []*models.Transcript{testTranscript("t1")}}
	ext := &fakeExtractor{classifyResults: []string{"engineering"}, extractResults: []string{validOutput}}
	d := &fakeDestination{name: "confluence", url: "https://c/1"}
	store := newFakeStore()

	svc := New([]Source{bad, good}, ext, []Destination{d}, store, testConfig())
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("one failing source must not abort the run: %v", err)
	}
	if _, ok := store.processed["fake:t1"]; !ok {
		t.Error("healthy source's transcript should still be processed")
	}
}
```

Run `go test ./internal/pipeline/ 2>&1 | tail -5` — expect FAIL (package does not exist).

- [ ] **Step 2: Implement** — create `internal/pipeline/pipeline.go`:

```go
// Package pipeline orchestrates the transcript flow: for every enabled
// source, fetch recent transcripts, classify the meeting type, extract
// structured notes via LLM, and publish to every enabled destination.
// Providers are plugged in via the source.Source and destination.Destination
// interfaces — see docs/ADDING_A_PROVIDER.md.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/models"
)

// Source fetches recent meeting transcripts from a meeting platform.
// It mirrors source.Source; declared locally so tests can fake it without
// importing provider packages.
type Source interface {
	Name() string
	ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error)
}

// Destination publishes extracted notes to a knowledge base (see
// destination.Destination for the idempotency contract).
type Destination interface {
	Name() string
	Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (string, error)
}

// Extractor classifies meeting types and extracts structured notes via LLM.
type Extractor interface {
	Classify(ctx context.Context, transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error)
	Extract(ctx context.Context, transcript string, extractionPrompt string) (string, error)
}

// StateStore tracks processed transcripts for idempotent pipeline runs.
// Keys are source-prefixed (models.Transcript.Key).
type StateStore interface {
	IsProcessed(ctx context.Context, key string) (bool, error)
	MarkProcessed(ctx context.Context, key, name, publishedURLs string) error
	MarkFailed(ctx context.Context, key, name, errorMessage string) error
	RecordSyncEvent(ctx context.Context, event *database.SyncEvent) error
}

// Service orchestrates the transcript processing pipeline.
type Service struct {
	sources      []Source
	extractor    Extractor
	destinations []Destination
	store        StateStore
	cfg          *config.Config
	templateKeys []string
	runID        string
	stageCounts  map[string]int
}

// New creates a Service with the given dependencies.
func New(sources []Source, extractor Extractor, destinations []Destination, store StateStore, cfg *config.Config) *Service {
	templateKeys := make([]string, 0, len(cfg.Prompts.Templates))
	for k := range cfg.Prompts.Templates {
		templateKeys = append(templateKeys, k)
	}
	sort.Strings(templateKeys)

	return &Service{
		sources:      sources,
		extractor:    extractor,
		destinations: destinations,
		store:        store,
		cfg:          cfg,
		templateKeys: templateKeys,
	}
}

// recordEvent appends a bounded metadata event for the given stage and status.
// On failure it logs a warning and continues — observability persistence
// failure must not break the pipeline.
func (s *Service) recordEvent(ctx context.Context, stage, status string, metadata map[string]string) {
	s.stageCounts[stage]++

	payload := map[string]string{"stage": stage, "status": status}
	for k, v := range database.TruncateMetadata(metadata) {
		payload[k] = v
	}
	metadataJSON, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("marshal sync event metadata failed", "run_id", s.runID, "stage", stage, "error", err)
		metadataJSON = []byte("{}")
	}

	event := &database.SyncEvent{
		ID:           fmt.Sprintf("evt-%s-%s", stage, time.Now().UTC().Format(time.RFC3339Nano)),
		RunID:        s.runID,
		TranscriptID: metadata["transcript_id"],
		Stage:        stage,
		Status:       status,
		MetadataJSON: string(metadataJSON),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	if err := s.store.RecordSyncEvent(ctx, event); err != nil {
		slog.Warn("record sync event failed", "run_id", s.runID, "stage", stage, "error", err)
	}
}

// Run executes the full pipeline across all sources. A failing source is
// logged and skipped (recoverable); the run only errors when state
// persistence fails or every pending transcript fails.
func (s *Service) Run(ctx context.Context) error {
	s.runID = fmt.Sprintf("run-%s", time.Now().UTC().Format(time.RFC3339Nano))
	s.stageCounts = make(map[string]int)
	slog.Info("sync run started", "run_id", s.runID)
	s.recordEvent(ctx, "run_started", "ok", nil)

	lookback := time.Duration(s.cfg.Sync.LookbackHours) * time.Hour

	var pending []*models.Transcript
	for _, src := range s.sources {
		transcripts, err := src.ListRecent(ctx, lookback)
		if err != nil {
			s.recordEvent(ctx, "source_fetch_failed", "error", map[string]string{"source": src.Name(), "error": err.Error()})
			slog.Error("source fetch failed, skipping source", "source", src.Name(), "error", err)
			continue
		}
		s.recordEvent(ctx, "transcripts_fetched", "ok", map[string]string{"source": src.Name(), "count": fmt.Sprintf("%d", len(transcripts))})
		slog.Info("fetched transcripts", "run_id", s.runID, "source", src.Name(), "count", len(transcripts))

		for _, t := range transcripts {
			processed, err := s.store.IsProcessed(ctx, t.Key())
			if err != nil {
				slog.Warn("check processed failed", "key", t.Key(), "error", err)
				continue
			}
			if !processed {
				pending = append(pending, t)
				s.recordEvent(ctx, "transcript_discovered", "ok", map[string]string{"transcript_id": t.Key()})
			}
		}
	}

	slog.Info("pending transcripts", "count", len(pending))

	if len(pending) > s.cfg.Sync.MaxPerRun {
		slog.Warn("limiting transcripts", "pending", len(pending), "max", s.cfg.Sync.MaxPerRun)
		pending = pending[:s.cfg.Sync.MaxPerRun]
	}

	successCount := 0
	persistenceFailures := 0
	publishFailures := 0
	for i, t := range pending {
		if err := ctx.Err(); err != nil {
			slog.Warn("sync cancelled", "processed", successCount, "remaining", len(pending)-i)
			break
		}

		slog.Info("processing transcript", "num", i+1, "total", len(pending), "source", t.Source, "title", t.Title)

		if len(t.Content) > s.cfg.LLM.MaxTranscriptChars {
			slog.Warn("transcript exceeds max_transcript_chars; small models may truncate or degrade",
				"key", t.Key(), "chars", len(t.Content), "max", s.cfg.LLM.MaxTranscriptChars)
		}

		meetingType, err := s.classifyValidated(ctx, t)
		if err != nil {
			s.recordEvent(ctx, "classification_failed", "error", map[string]string{"transcript_id": t.Key(), "error": err.Error()})
			slog.Error("classification failed", "key", t.Key(), "error", err)
			continue
		}
		s.recordEvent(ctx, "classification_succeeded", "ok", map[string]string{"transcript_id": t.Key(), "type": meetingType})

		result, err := s.extractValidated(ctx, t, s.cfg.Prompts.Templates[meetingType].ExtractionPrompt)
		if err != nil {
			s.recordEvent(ctx, "extraction_failed", "error", map[string]string{"transcript_id": t.Key(), "error": err.Error()})
			slog.Error("extraction failed", "key", t.Key(), "error", err)
			s.markFailed(ctx, t, "extraction failed: "+err.Error())
			continue
		}
		s.recordEvent(ctx, "extraction_succeeded", "ok", map[string]string{"transcript_id": t.Key()})

		if s.cfg.DryRun {
			preview := result.Markdown
			if len(preview) > 200 {
				preview = preview[:200] + "... [truncated]"
			}
			slog.Info("dry run, skipping publish", "title", t.Title, "output_preview", preview)
			continue
		}

		urls, err := s.publishAll(ctx, result, t)
		if err != nil {
			publishFailures++
			s.markFailed(ctx, t, "publish failed: "+err.Error())
			continue
		}

		urlsJSON, err := json.Marshal(urls)
		if err != nil {
			urlsJSON = []byte("{}")
		}
		if err := s.store.MarkProcessed(ctx, t.Key(), t.Title, string(urlsJSON)); err != nil {
			s.recordEvent(ctx, "state_persistence_failed", "error", map[string]string{"transcript_id": t.Key(), "error": err.Error()})
			persistenceFailures++
			slog.Error("mark processed failed", "key", t.Key(), "error", err)
			continue
		}
		s.recordEvent(ctx, "state_persistence_succeeded", "ok", map[string]string{"transcript_id": t.Key()})

		slog.Info("published", "key", t.Key(), "urls", string(urlsJSON))
		successCount++
	}

	slog.Info("sync complete", "run_id", s.runID, "success", successCount, "total", len(pending),
		"persistence_failures", persistenceFailures, "publish_failures", publishFailures,
		"event_stage_counts", s.stageCounts)

	if persistenceFailures > 0 {
		return fmt.Errorf("%d transcript publishes succeeded but failed to persist state", persistenceFailures)
	}
	if successCount == 0 && len(pending) > 0 && !s.cfg.DryRun {
		return fmt.Errorf("all %d transcripts failed to process", len(pending))
	}

	s.recordEvent(ctx, "run_completed", "ok", nil)
	return nil
}

// classifyValidated asks the LLM for a meeting type and validates it against
// the configured templates. An invalid answer gets one corrective retry
// (small models often add prose around the key); if that also fails, it
// falls back to the first template key.
func (s *Service) classifyValidated(ctx context.Context, t *models.Transcript) (string, error) {
	preview := t.Content
	if len(preview) > 1000 {
		preview = preview[:1000]
	}

	raw, err := s.extractor.Classify(ctx, preview, s.templateKeys, s.cfg.Prompts.ClassifyPrompt)
	if err != nil {
		return "", err
	}
	if key, ok := s.normalizeType(raw); ok {
		return key, nil
	}

	corrective := s.cfg.Prompts.ClassifyPrompt + fmt.Sprintf(
		"\n\nYour previous answer %q was not a valid type. Respond with exactly one of: {{TEMPLATE_KEYS}} — a single word, nothing else.",
		truncateForPrompt(raw, 64))
	raw2, err := s.extractor.Classify(ctx, preview, s.templateKeys, corrective)
	if err != nil {
		return "", err
	}
	if key, ok := s.normalizeType(raw2); ok {
		return key, nil
	}

	fallback := s.templateKeys[0]
	slog.Warn("classification invalid after retry, using fallback template",
		"key", t.Key(), "answer", truncateForPrompt(raw2, 64), "fallback", fallback)
	return fallback, nil
}

// normalizeType lowercases/trims an LLM classification answer and reports
// whether it matches a configured template key.
func (s *Service) normalizeType(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	_, ok := s.cfg.Prompts.Templates[key]
	return key, ok
}

// extractValidated runs the extraction prompt and parses the output. On a
// parse failure it retries once, appending the parse error as corrective
// feedback — enough to recover most malformed answers from small models.
func (s *Service) extractValidated(ctx context.Context, t *models.Transcript, extractionPrompt string) (*models.ExtractionResult, error) {
	raw, err := s.extractor.Extract(ctx, t.Content, extractionPrompt)
	if err != nil {
		return nil, err
	}
	result, perr := models.ParseExtractionOutput(raw)
	if perr == nil {
		return result, nil
	}

	slog.Warn("extraction output malformed, retrying with feedback", "key", t.Key(), "error", perr)
	corrective := extractionPrompt + fmt.Sprintf(
		"\n\nIMPORTANT: your previous response could not be parsed (%s). Respond with YAML front-matter between --- delimiters followed by the markdown body. Start your response with --- on its own line.",
		perr.Error())
	raw2, err := s.extractor.Extract(ctx, t.Content, corrective)
	if err != nil {
		return nil, err
	}
	result2, perr2 := models.ParseExtractionOutput(raw2)
	if perr2 != nil {
		return nil, fmt.Errorf("extraction output invalid after retry: %w", perr2)
	}
	return result2, nil
}

// publishAll publishes to every destination, collecting name → URL. It stops
// at the first failure; destinations are idempotent, so the whole transcript
// is retried next run without creating duplicates.
func (s *Service) publishAll(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (map[string]string, error) {
	urls := make(map[string]string, len(s.destinations))
	for _, dest := range s.destinations {
		u, err := dest.Publish(ctx, result, t)
		if err != nil {
			s.recordEvent(ctx, "publish_failed", "error", map[string]string{"transcript_id": t.Key(), "destination": dest.Name(), "error": err.Error()})
			slog.Error("publish failed", "key", t.Key(), "destination", dest.Name(), "error", err)
			return nil, fmt.Errorf("destination %s: %w", dest.Name(), err)
		}
		urls[dest.Name()] = u
		s.recordEvent(ctx, "publish_succeeded", "ok", map[string]string{"transcript_id": t.Key(), "destination": dest.Name()})
	}
	return urls, nil
}

// markFailed records a failure, logging (not returning) persistence errors.
func (s *Service) markFailed(ctx context.Context, t *models.Transcript, msg string) {
	if err := s.store.MarkFailed(ctx, t.Key(), t.Title, msg); err != nil {
		slog.Error("mark failed failed", "key", t.Key(), "error", err)
	}
}

// truncateForPrompt bounds untrusted LLM output before echoing it back.
func truncateForPrompt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
```

- [ ] **Step 3: Run** `go test ./internal/pipeline/ -v 2>&1 | tail -20` — expect PASS (all 7 tests).

- [ ] **Step 4: Commit** — `git add internal/pipeline/ && git commit -m "feat: provider-neutral pipeline with multi-destination publish and 14B output validation"`

---

### Task 8: CLI wiring (sync, auth subcommand, cleanup)

**Files:**
- Modify: `cmd/omniscient/sync.go` (replace entirely), `cmd/omniscient/auth.go` (replace entirely), `cmd/omniscient/main.go:25` (Short text)
- Delete: `cmd/omniscient/sync_service_test.go` (cases now live in `internal/pipeline/pipeline_test.go`)
- Modify: `cmd/omniscient/main_test.go:268-285` (config fixture → new schema)

- [ ] **Step 1: Replace `cmd/omniscient/sync.go`** with:

```go
package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/database"
	"github.com/jgavinray/omniscient/internal/destination"
	"github.com/jgavinray/omniscient/internal/destination/confluence"
	"github.com/jgavinray/omniscient/internal/llm"
	"github.com/jgavinray/omniscient/internal/pipeline"
	"github.com/jgavinray/omniscient/internal/source"
	"github.com/jgavinray/omniscient/internal/source/googlemeet"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch, extract, and publish recent meeting transcripts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			setupLogging(cfg.Logging.Level, cfg.Logging.File)

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return runSync(ctx, cfg)
		},
	}
}

// buildSources constructs every enabled source. To add a provider, append a
// block here — see docs/ADDING_A_PROVIDER.md.
func buildSources(ctx context.Context, cfg *config.Config) ([]source.Source, error) {
	var sources []source.Source
	if cfg.Sources.GoogleMeet.IsEnabled() {
		gm, err := googlemeet.New(ctx,
			cfg.Sources.GoogleMeet.CredentialsFile,
			cfg.Sources.GoogleMeet.TokenFile,
			cfg.Sources.GoogleMeet.FolderID,
		)
		if err != nil {
			return nil, fmt.Errorf("init googlemeet source: %w", err)
		}
		sources = append(sources, gm)
	}
	return sources, nil
}

// buildDestinations constructs every enabled destination. To add a provider,
// append a block here — see docs/ADDING_A_PROVIDER.md.
func buildDestinations(cfg *config.Config) []destination.Destination {
	var destinations []destination.Destination
	if cfg.Destinations.Confluence.IsEnabled() {
		destinations = append(destinations, confluence.NewPublisher(
			cfg.Destinations.Confluence.BaseURL,
			cfg.Destinations.Confluence.Email,
			cfg.Destinations.Confluence.APIToken,
			cfg.Destinations.Confluence.SpaceKey,
			cfg.Destinations.Confluence.ParentPageID,
		))
	}
	return destinations
}

// runSync wires up all dependencies and delegates to the pipeline service.
func runSync(ctx context.Context, cfg *config.Config) error {
	sources, err := buildSources(ctx, cfg)
	if err != nil {
		return err
	}

	llmExtractor, err := llm.NewExtractor(&cfg.LLM)
	if err != nil {
		return fmt.Errorf("init llm extractor: %w", err)
	}

	destinations := buildDestinations(cfg)

	store, err := database.NewStore(cfg.Sync.DatabasePath)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	defer store.Close()

	// Adapt typed slices to the pipeline's local interfaces.
	pipeSources := make([]pipeline.Source, len(sources))
	for i, s := range sources {
		pipeSources[i] = s
	}
	pipeDestinations := make([]pipeline.Destination, len(destinations))
	for i, d := range destinations {
		pipeDestinations[i] = d
	}

	return pipeline.New(pipeSources, llmExtractor, pipeDestinations, store, cfg).Run(ctx)
}
```

- [ ] **Step 2: Replace `cmd/omniscient/auth.go`** with:

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/jgavinray/omniscient/internal/config"
	"github.com/jgavinray/omniscient/internal/source/googlemeet"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth [provider]",
		Short: "Authenticate with a provider (run once; tokens auto-refresh afterwards)",
		// Bare `omniscient auth` keeps working while Google Meet is the only
		// OAuth provider.
		RunE: runGoogleMeetAuth,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "googlemeet",
		Short: "Authenticate with Google via OAuth2 browser consent flow",
		RunE:  runGoogleMeetAuth,
	})
	return cmd
}

func runGoogleMeetAuth(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	setupLogging(cfg.Logging.Level, cfg.Logging.File)

	slog.Info("starting Google OAuth2 authentication flow")

	_, err = googlemeet.RunAuthFlow(cfg.Sources.GoogleMeet.CredentialsFile, cfg.Sources.GoogleMeet.TokenFile)
	if err != nil {
		return fmt.Errorf("OAuth2 authentication failed: %w", err)
	}

	cmd.Println("Authentication successful! Token saved.")
	cmd.Printf("Token stored at: %s\n", cfg.Sources.GoogleMeet.TokenFile)

	return nil
}
```

- [ ] **Step 3: Cleanup.** `git rm cmd/omniscient/sync_service_test.go`. In `cmd/omniscient/main.go:25` change Short to `"Meeting transcript harvester: meeting platforms → LLM extraction → knowledge bases"`. In `cmd/omniscient/main_test.go` (fixture at ~line 268) restructure the YAML exactly as in Task 4 Step 1 (sources.googlemeet / destinations.confluence), keeping the `%s` printf substitutions it uses for temp paths.

- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./... 2>&1 | tail -20` — expect full build and ALL package tests PASS. This is the first fully-green checkpoint since Task 2.

- [ ] **Step 5: Commit** — `git add -A cmd/ && git commit -m "refactor: wire CLI to pluggable pipeline with per-provider auth subcommands"`

---

### Task 9: config.yaml.example

**Files:**
- Modify: `config.yaml.example`

- [ ] **Step 1: Restructure.** Move the `google:` block under `sources:` → `googlemeet:` (adding `enabled: true` and indenting; keep all comments), and the `confluence:` block under `destinations:` (keep all fields/comments). Add under `llm:`:

```yaml
  # For provider=anthropic:
  # Anthropic-compatible Messages API endpoint. Leave unset for the official
  # API. Point at a local server for local models, e.g.:
  #   anthropic_base_url: "http://localhost:8080"   # llama.cpp --api
  # With a custom endpoint the API key is optional.
  anthropic_base_url: ""

  # Warn when a transcript exceeds this many characters (small local models
  # degrade on very long inputs). Warning only — the transcript still runs.
  max_transcript_chars: 100000
```

Top of file, add a comment block:

```yaml
# Omniscient configuration.
# sources:      where meeting transcripts come from (enable at least one)
# destinations: where extracted notes are published (enable at least one,
#               or set dry_run: true)
# Adding a provider? See docs/ADDING_A_PROVIDER.md
```

`llm`, `sync`, `logging`, `prompts`, `dry_run` sections are otherwise unchanged.

- [ ] **Step 2: Verify** the example parses: write a tiny throwaway check — `go run ./cmd/omniscient config validate --config $(pwd)/config.yaml.example`; expect it to fail ONLY on placeholder values (e.g. folder ID pattern), not on schema/unmarshal errors. If `config validate` rejects placeholders, that is acceptable — confirm the error mentions a field value, not "old schema" or YAML parse failure.

- [ ] **Step 3: Commit** — `git add config.yaml.example && git commit -m "docs: update example config to sources/destinations schema"`

---

### Task 10: Documentation

**Files:**
- Modify: `README.md`, `CLAUDE.md`
- Create: `docs/ADDING_A_PROVIDER.md`, `docs/LLM_SCOPE.md`, `docs/SETUP.md`

- [ ] **Step 1: `docs/ADDING_A_PROVIDER.md`** — write with this structure and content (prose may be tightened, content must be complete):

```markdown
# Adding a Provider

Omniscient is a pipeline: **sources** (meeting platforms) produce transcripts,
**destinations** (knowledge bases) receive extracted notes. Each provider is
one package implementing one small interface. This guide walks through adding
either kind. `internal/source/googlemeet` and `internal/destination/confluence`
are the reference implementations — copy their shape.

## Adding a Source (e.g. Zoom)

1. **Create the package:** `internal/source/zoom/zoom.go`, `package zoom`.
2. **Implement the interface** (`internal/source/source.go`):
   - `Name() string` — return a short stable key, e.g. `"zoom"`. It becomes
     part of dedup keys (`zoom:<id>`); never change it once shipped.
   - `ListRecent(ctx, since)` — return `[]*models.Transcript` with `Source`
     set to your name, provider-native `ID`, `Title`, `ModifiedAt`, and
     plain-text `Content`. Skip-and-log individual fetch failures; only
     return an error when the whole listing fails.
3. **Add config:** new struct in `internal/config/config.go` under
   `SourcesConfig` with an `Enabled *bool` + `IsEnabled()` (copy
   `GoogleMeetConfig`). Validate its fields in `validate()` only when enabled.
4. **Wire it:** append a block in `buildSources()` in `cmd/omniscient/sync.go`.
5. **Test it:** `httptest.NewServer` faking the provider API (see
   `internal/destination/confluence/publisher_test.go` for the pattern).
   Cover: happy path field mapping, listing failure, individual-item failure.
6. **Document it:** add the section to `config.yaml.example` and the provider
   matrix in `README.md`.

## Adding a Destination (e.g. Notion)

Same steps with `internal/destination/`, `DestinationsConfig`, and
`buildDestinations()`. Two contract requirements:

- **Idempotency (required):** `Publish` must create-or-update keyed on a
  stable identity (Confluence uses the page title). The pipeline retries the
  whole transcript after partial failures and relies on this to avoid
  duplicate pages.
- Return the canonical page URL; it is stored in SQLite for `status` output.

## Error-handling rules (all providers)

- Transient (429, 5xx, timeouts): retry with `internal/retry` (3 attempts).
- Permanent (401, bad config): return the error immediately.
- Per-item failures during listing: log with `slog`, skip, continue.

## PR checklist

- [ ] Interface implemented in its own package under `internal/source/` or `internal/destination/`
- [ ] `Name()` is short, lowercase, stable
- [ ] Config struct + validation (only when enabled) + `config.yaml.example`
- [ ] Wired into `buildSources`/`buildDestinations`
- [ ] `httptest`-based tests, including failure cases
- [ ] README provider matrix updated
- [ ] `make test` green
```

- [ ] **Step 2: `docs/LLM_SCOPE.md`** — write with this content:

```markdown
# LLM Scope of Work

What the LLM is — and is not — responsible for in this pipeline, and how to
qualify a model (the pipeline is designed to work with local 14B-class models
on any OpenAI-compatible endpoint).

## Responsibilities

The LLM performs exactly two tasks per transcript:

### 1. Classify
- **Input:** first 1,000 chars of the transcript + the configured
  `classify_prompt` containing the allowed template keys.
- **Output contract:** exactly one of the configured template keys, one word.
- **Enforcement:** the pipeline lowercases/trims the answer and checks it
  against the configured templates. Invalid → one corrective retry quoting
  the bad answer → fallback to the first template key. The LLM cannot invent
  meeting types.

### 2. Extract
- **Input:** full transcript + the meeting-type extraction prompt.
- **Output contract:** YAML front-matter between `---` delimiters, then a
  markdown body. Parsed by `models.ParseExtractionOutput`; code fences are
  stripped automatically.
- **Enforcement:** parse failure → one corrective retry with the parse error
  appended → marked failed (visible in `omniscient status`, retryable with
  `omniscient retry-failed`).

## Out of scope for the LLM
Deduplication, page naming/placement, publishing, retry policy, and meeting-
type definitions are all code/config, not model judgment.

## Settings that matter for small models
- Temperature is pinned to 0 in both providers (deterministic structure
  beats creative prose here).
- Transcripts longer than `llm.max_transcript_chars` (default 100,000 chars
  ≈ 25k tokens) log a warning — check your model's context window. An
  hour-long meeting is typically 8–12k tokens and fits a 32k context.
- Extraction prompts must show the expected output skeleton (the defaults
  do); small models imitate structure far better than they follow abstract
  instructions.

## Qualifying a new model

1. Point `llm.openai_base_url`/`llm.model` at the candidate.
2. Set `dry_run: true` and run `omniscient sync` over a folder with ~10
   representative transcripts (use `omniscient forget` or a fresh
   `database_path` to re-run the same set).
3. Score from the logs:
   - **Classification accuracy:** `classification_succeeded` events with the
     type you'd assign by hand. Target ≥ 8/10, zero fallbacks.
   - **Parse rate:** extractions succeeding without the corrective retry.
     Target ≥ 9/10 first-try.
   - **Content spot-check:** action items/decisions present and attributed.
4. A model that needs the corrective retry often will work but doubles cost
   and latency; prefer one that passes first-try.
```

- [ ] **Step 3: `docs/SETUP.md`** — click-by-click setup; structure:

```markdown
# Setup Guide

## 1. Google (Meet transcripts)

### One-time: create an OAuth client
1. https://console.cloud.google.com/ → select/create a project
2. APIs & Services → Library → enable **Google Drive API**
3. APIs & Services → OAuth consent screen → User type **Internal** (Workspace)
   or **External** + add yourself as a test user
4. APIs & Services → Credentials → Create Credentials → **OAuth client ID** →
   Application type **Desktop app** → Create
5. Download the JSON → save it at the path in
   `sources.googlemeet.credentials_file`

### Authenticate (browser click, once)
    omniscient auth googlemeet --config /opt/omniscient/config.yaml
A browser opens; approve access. The token is saved and auto-refreshes —
you never handle it again.

### Find your transcripts folder
Google Meet (with transcription enabled) saves transcripts as Google Docs in
the organizer's Drive under **Meet Recordings**. Right-click the folder →
Share → Copy link → the ID between `/folders/` and `?` is
`sources.googlemeet.folder_id`.

## 2. Confluence

1. https://id.atlassian.com/manage-profile/security/api-tokens → Create API
   token → copy it into `destinations.confluence.api_token`
2. `email`: the Atlassian account email that owns the token
3. `base_url`: `https://<your-company>.atlassian.net`
4. `space_key`: visible in the space URL (`/wiki/spaces/<KEY>/...`)
5. Optional `parent_page_id`: open the parent page → the number in its URL

## 3. LLM
Three options:
- **Anthropic API:** `provider: anthropic` + `anthropic_api_key`
- **Local, OpenAI-compatible:** `provider: openai-compatible` +
  `openai_base_url` (vLLM, Ollama, LM Studio)
- **Local, Anthropic-compatible:** `provider: anthropic` +
  `anthropic_base_url` pointed at the local server (key optional)

Before trusting a new model, run the qualification checklist in
docs/LLM_SCOPE.md.

## 4. Validate and run
    omniscient config validate --config /opt/omniscient/config.yaml
    omniscient sync --config /opt/omniscient/config.yaml   # dry_run: true first!
Then schedule: `*/30 * * * * /opt/omniscient/omniscient sync --config /opt/omniscient/config.yaml`
```

- [ ] **Step 4: Rewrite `README.md`.** Keep installation/usage sections, restructure to: what it does (meeting platforms → LLM notes → knowledge bases, batch cron harvester — explicitly: it does not join or record calls; it harvests the transcripts platforms already produce); architecture ASCII diagram (sources → pipeline → destinations, from the spec); provider matrix:

```markdown
| Type | Provider | Status |
|------|----------|--------|
| Source | Google Meet (via Drive) | ✅ implemented |
| Source | Zoom | 🔜 planned — see docs/ADDING_A_PROVIDER.md |
| Destination | Confluence | ✅ implemented |
| Destination | Notion | 🔜 planned — see docs/ADDING_A_PROVIDER.md |
```

plus links to `docs/SETUP.md`, `docs/ADDING_A_PROVIDER.md`, `docs/LLM_SCOPE.md`, and the config table updated to `sources.googlemeet.*` / `destinations.confluence.*` paths.

- [ ] **Step 5: Update `CLAUDE.md`:** "What This Is" → "Meeting transcript harvester: meeting platforms (Google Meet) → LLM extraction → knowledge bases (Confluence)"; Architecture bullets add "Pluggable providers: `internal/source` + `internal/destination` interfaces — see docs/ADDING_A_PROVIDER.md"; Key Patterns: interfaces list updated (`internal/source/source.go`, `internal/destination/destination.go`, pipeline in `internal/pipeline`); note config schema is sources/destinations maps.

- [ ] **Step 6: Commit** — `git add README.md CLAUDE.md docs/ && git commit -m "docs: provider guide, LLM scope of work, setup guide, README rewrite"`

---

### Task 11: Final verification

- [ ] **Step 1:** `make build && make test 2>&1 | tail -25` — expect build success, all tests PASS.
- [ ] **Step 2:** `go vet ./...` — clean.
- [ ] **Step 3:** Smoke-test the CLI surface: `./bin/omniscient --help` (commands listed), `./bin/omniscient auth --help` (shows googlemeet subcommand), `./bin/omniscient version`.
- [ ] **Step 4:** Write a minimal valid config to a temp dir (new schema, dry_run true, fake-but-well-formed values) and run `./bin/omniscient config validate --config <path>` — expect "valid" output.
- [ ] **Step 5:** Update spec status line to `Implemented` and commit any stragglers: `git add -A && git commit -m "chore: final verification pass for pluggable pipeline"` (only if there are changes).
