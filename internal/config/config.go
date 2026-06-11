package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

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
// To add a provider, add a field here and wire it in cmd/omniscient/sync.go —
// see docs/ADDING_A_PROVIDER.md.
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

// LLMConfig holds LLM provider settings for transcript extraction.
type LLMConfig struct {
	Provider        string `yaml:"provider"`
	AnthropicAPIKey string `yaml:"anthropic_api_key"`
	OpenAIBaseURL   string `yaml:"openai_base_url"`
	OpenAIAPIKey    string `yaml:"openai_api_key"`
	Model           string `yaml:"model"`
	Timeout         int    `yaml:"timeout"`
	// MaxTranscriptChars triggers a warning (not a failure) for transcripts
	// longer than this; very long inputs degrade small-model output quality.
	MaxTranscriptChars int `yaml:"max_transcript_chars"`
}

// ConfluenceConfig holds Atlassian Confluence publishing settings.
type ConfluenceConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	BaseURL      string `yaml:"base_url"`
	Email        string `yaml:"email"`
	APIToken     string `yaml:"api_token"`
	SpaceKey     string `yaml:"space_key"`
	ParentPageID string `yaml:"parent_page_id"`
}

// IsEnabled returns whether Confluence publishing is enabled.
// Defaults to true when the Enabled field is nil (not specified).
func (c *ConfluenceConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// PromptsConfig holds LLM prompt templates for classification and extraction.
type PromptsConfig struct {
	ClassifyPrompt string                     `yaml:"classify_prompt"`
	Templates      map[string]MeetingTemplate `yaml:"templates"`
}

// MeetingTemplate defines the extraction prompt for a specific meeting type.
type MeetingTemplate struct {
	Description      string `yaml:"description"`
	ExtractionPrompt string `yaml:"extraction_prompt"`
}

// SyncConfig holds synchronization behavior settings.
type SyncConfig struct {
	LookbackHours int    `yaml:"lookback_hours"`
	DatabasePath  string `yaml:"database_path"`
	MaxPerRun     int    `yaml:"max_per_run"`
}

// LoggingConfig holds structured logging settings.
type LoggingConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// validateURL checks that u is a valid URL with an allowed scheme.
func validateURL(field, u string, allowedSchemes []string) error {
	parsed, err := url.ParseRequestURI(u)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", field, err)
	}
	for _, s := range allowedSchemes {
		if parsed.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("%s scheme must be one of %v, got %q", field, allowedSchemes, parsed.Scheme)
}

// validateConfluencePath ensures the base URL path is either empty or exactly
// "/wiki", rejecting unrelated pathful URLs such as https://host/foo.
func validateConfluencePath(u string) error {
	parsed, err := url.ParseRequestURI(u)
	if err != nil {
		return fmt.Errorf("destinations.confluence.base_url is not a valid URL: %w", err)
	}
	path := parsed.Path
	// Strip trailing slash for comparison.
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	if path != "" && path != "/wiki" {
		return fmt.Errorf("destinations.confluence.base_url path must be empty or \"/wiki\", got %q", parsed.Path)
	}
	return nil
}

// validateFilePath checks that a config path field is non-empty and absolute.
func validateFilePath(field, path string) error {
	if path == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("%s must be an absolute path, got %q", field, path)
	}
	return nil
}

// driveIDPattern matches valid Google Drive folder/file IDs.
var driveIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-]{10,}$`)

// Load reads a YAML configuration file from the given path, unmarshals it into
// a Config struct, applies defaults, and validates all required fields.
func Load(path string) (*Config, error) {
	const maxConfigBytes = 1 * 1024 * 1024 // 1 MB
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes))
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	if int64(len(data)) >= maxConfigBytes {
		return nil, fmt.Errorf("config file %s exceeds %d byte limit", path, maxConfigBytes)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	// Detect the pre-v2 single-provider schema and fail with a migration hint
	// instead of confusing field-level validation errors.
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err == nil {
		if _, ok := probe["google"]; ok {
			return nil, fmt.Errorf("config file %s uses the old schema: move google: to sources.googlemeet: and confluence: to destinations.confluence: (see config.yaml.example)", path)
		}
		if _, ok := probe["confluence"]; ok {
			return nil, fmt.Errorf("config file %s uses the old schema: move confluence: to destinations.confluence: (see config.yaml.example)", path)
		}
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// applyDefaults sets default values for optional fields that were not specified.
func (c *Config) applyDefaults() {
	if c.LLM.Timeout == 0 {
		c.LLM.Timeout = 120
	}
	if c.LLM.MaxTranscriptChars == 0 {
		c.LLM.MaxTranscriptChars = 100000
	}
	if c.Sync.LookbackHours == 0 {
		c.Sync.LookbackHours = 24
	}
	if c.Sync.MaxPerRun == 0 {
		c.Sync.MaxPerRun = 50
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Prompts.ClassifyPrompt == "" {
		c.Prompts.ClassifyPrompt = defaultClassifyPrompt
	}
	if len(c.Prompts.Templates) == 0 {
		c.Prompts.Templates = defaultTemplates()
	}
}

// validate checks all required fields and returns a descriptive error for the
// first validation failure encountered. Defaults must be applied before calling.
func (c *Config) validate() error {
	// At least one source must be enabled; destinations may all be disabled
	// only in dry-run mode (extraction output goes to logs instead).
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

	// Google Meet source validation (skip when disabled).
	if c.Sources.GoogleMeet.IsEnabled() {
		if err := validateFilePath("sources.googlemeet.credentials_file", c.Sources.GoogleMeet.CredentialsFile); err != nil {
			return err
		}
		if err := validateFilePath("sources.googlemeet.token_file", c.Sources.GoogleMeet.TokenFile); err != nil {
			return err
		}
		if c.Sources.GoogleMeet.FolderID == "" {
			return fmt.Errorf("sources.googlemeet.folder_id must not be empty")
		}
		if !driveIDPattern.MatchString(c.Sources.GoogleMeet.FolderID) {
			return fmt.Errorf("sources.googlemeet.folder_id contains unexpected characters; expected an alphanumeric Google Drive ID")
		}
	}

	// LLM config validation.
	switch c.LLM.Provider {
	case "anthropic":
		if c.LLM.AnthropicAPIKey == "" {
			return fmt.Errorf("llm.anthropic_api_key is required when provider is anthropic")
		}
		if !strings.HasPrefix(c.LLM.AnthropicAPIKey, "sk-ant-") {
			return fmt.Errorf("llm.anthropic_api_key must start with \"sk-ant-\"")
		}
	case "openai-compatible":
		if c.LLM.OpenAIBaseURL == "" {
			return fmt.Errorf("llm.openai_base_url is required when provider is openai-compatible")
		}
		if err := validateURL("llm.openai_base_url", c.LLM.OpenAIBaseURL, []string{"http", "https"}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("llm.provider must be \"anthropic\" or \"openai-compatible\", got %q", c.LLM.Provider)
	}

	if c.LLM.Model == "" {
		return fmt.Errorf("llm.model must not be empty")
	}

	// Confluence destination validation (skip when disabled).
	if c.Destinations.Confluence.IsEnabled() {
		if c.Destinations.Confluence.BaseURL == "" {
			return fmt.Errorf("destinations.confluence.base_url must not be empty")
		}
		if err := validateURL("destinations.confluence.base_url", c.Destinations.Confluence.BaseURL, []string{"https"}); err != nil {
			return err
		}
		if err := validateConfluencePath(c.Destinations.Confluence.BaseURL); err != nil {
			return err
		}
		if c.Destinations.Confluence.Email == "" {
			return fmt.Errorf("destinations.confluence.email must not be empty")
		}
		if c.Destinations.Confluence.APIToken == "" {
			return fmt.Errorf("destinations.confluence.api_token must not be empty")
		}
		if c.Destinations.Confluence.SpaceKey == "" {
			return fmt.Errorf("destinations.confluence.space_key must not be empty")
		}
	}

	// Sync config validation.
	if c.Sync.LookbackHours < 0 {
		return fmt.Errorf("sync.lookback_hours must be > 0, got %d", c.Sync.LookbackHours)
	}
	if c.Sync.DatabasePath == "" {
		return fmt.Errorf("sync.database_path must not be empty")
	}
	if c.Sync.MaxPerRun < 0 {
		return fmt.Errorf("sync.max_per_run must be > 0, got %d", c.Sync.MaxPerRun)
	}

	// Logging config validation.
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("logging.level must be one of debug, info, warn, error; got %q", c.Logging.Level)
	}

	// Prompt template validation.
	if err := c.validatePrompts(); err != nil {
		return err
	}

	return nil
}

// validatePrompts checks that classify_prompt and all template extraction
// prompts contain the required placeholder tokens.
func (c *Config) validatePrompts() error {
	// classify_prompt must contain both required placeholders.
	if !strings.Contains(c.Prompts.ClassifyPrompt, "{{TEMPLATE_KEYS}}") {
		return fmt.Errorf("prompts.classify_prompt must contain {{TEMPLATE_KEYS}}")
	}
	if !strings.Contains(c.Prompts.ClassifyPrompt, "{{TRANSCRIPT_PREVIEW}}") {
		return fmt.Errorf("prompts.classify_prompt must contain {{TRANSCRIPT_PREVIEW}}")
	}

	// At least one template must exist.
	if len(c.Prompts.Templates) == 0 {
		return fmt.Errorf("prompts.templates must contain at least one entry")
	}

	// Every template key must be non-empty after trimming whitespace.
	for key := range c.Prompts.Templates {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("prompts.templates must not have an empty key")
		}
	}

	// Every template extraction prompt must contain {{TRANSCRIPT}}.
	for key, tmpl := range c.Prompts.Templates {
		if !strings.Contains(tmpl.ExtractionPrompt, "{{TRANSCRIPT}}") {
			return fmt.Errorf("prompts.templates[%s].extraction_prompt must contain {{TRANSCRIPT}}", key)
		}
	}

	return nil
}

const defaultClassifyPrompt = `Classify this meeting transcript into one of the following types: {{TEMPLATE_KEYS}}

Read the first portion of the transcript below and respond with ONLY the type key (a single word, no explanation).

TRANSCRIPT PREVIEW:
{{TRANSCRIPT_PREVIEW}}`

func defaultTemplates() map[string]MeetingTemplate {
	return map[string]MeetingTemplate{
		"engineering": {
			Description: "Engineering standups, design reviews, and technical discussions",
			ExtractionPrompt: `You are a meeting notes assistant. Extract structured notes from this engineering meeting transcript.

Output format: YAML front-matter (between --- delimiters) followed by markdown body.

---
date: "YYYY-MM-DD"
meeting_type: engineering
participants: [list of names]
---

## Summary
One paragraph summary.

## Decisions
- **Decision** (Owner: name) — rationale

## Action Items
- [ ] Task description (Owner: name, Due: date)

## Blockers
- Blocker description (Impact: description, Ticket: ID)

## Key Discussion Points
- Point with context

TRANSCRIPT:
{{TRANSCRIPT}}`,
		},
		"customer_success": {
			Description: "Customer calls, success reviews, and account meetings",
			ExtractionPrompt: `You are a meeting notes assistant. Extract structured notes from this customer meeting transcript.

Output format: YAML front-matter (between --- delimiters) followed by markdown body.

---
date: "YYYY-MM-DD"
meeting_type: customer_success
participants: [list of names]
customer: "customer name"
---

## Summary
One paragraph summary of the customer interaction.

## Customer Feedback
- Feedback point with sentiment

## Action Items
- [ ] Task description (Owner: name, Due: date)

## Commitments Made
- Commitment with timeline

## Follow-up Required
- Follow-up item with priority

TRANSCRIPT:
{{TRANSCRIPT}}`,
		},
		"planning": {
			Description: "Sprint planning, roadmap reviews, and strategy sessions",
			ExtractionPrompt: `You are a meeting notes assistant. Extract structured notes from this planning meeting transcript.

Output format: YAML front-matter (between --- delimiters) followed by markdown body.

---
date: "YYYY-MM-DD"
meeting_type: planning
participants: [list of names]
sprint: "sprint identifier if mentioned"
---

## Summary
One paragraph summary of planning outcomes.

## Goals & Objectives
- Goal with priority and owner

## Decisions
- **Decision** (Owner: name) — rationale

## Action Items
- [ ] Task description (Owner: name, Due: date)

## Risks & Dependencies
- Risk or dependency with mitigation plan

## Timeline
- Milestone with target date

TRANSCRIPT:
{{TRANSCRIPT}}`,
		},
	}
}
