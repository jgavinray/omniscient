package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the omniscient application.
type Config struct {
	Google     GoogleConfig     `yaml:"google"`
	LLM        LLMConfig        `yaml:"llm"`
	Confluence ConfluenceConfig `yaml:"confluence"`
	Sync       SyncConfig       `yaml:"sync"`
	Logging    LoggingConfig    `yaml:"logging"`
	DryRun     bool             `yaml:"dry_run"`
	Prompts    PromptsConfig    `yaml:"prompts"`
}

// GoogleConfig holds Google Drive OAuth2 and folder settings.
type GoogleConfig struct {
	CredentialsFile string `yaml:"credentials_file"`
	TokenFile       string `yaml:"token_file"`
	FolderID        string `yaml:"folder_id"`
}

// LLMConfig holds LLM provider settings for transcript extraction.
type LLMConfig struct {
	Provider        string `yaml:"provider"`
	AnthropicAPIKey string `yaml:"anthropic_api_key"`
	OpenAIBaseURL   string `yaml:"openai_base_url"`
	OpenAIAPIKey    string `yaml:"openai_api_key"`
	Model           string `yaml:"model"`
	Timeout         int    `yaml:"timeout"`
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

// Load reads a YAML configuration file from the given path, unmarshals it into
// a Config struct, applies defaults, and validates all required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
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
	// Google config validation.
	if c.Google.CredentialsFile == "" {
		return fmt.Errorf("google.credentials_file must not be empty")
	}
	if c.Google.TokenFile == "" {
		return fmt.Errorf("google.token_file must not be empty")
	}
	if c.Google.FolderID == "" {
		return fmt.Errorf("google.folder_id must not be empty")
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
		if _, err := url.ParseRequestURI(c.LLM.OpenAIBaseURL); err != nil {
			return fmt.Errorf("llm.openai_base_url is not a valid URL: %w", err)
		}
	default:
		return fmt.Errorf("llm.provider must be \"anthropic\" or \"openai-compatible\", got %q", c.LLM.Provider)
	}

	if c.LLM.Model == "" {
		return fmt.Errorf("llm.model must not be empty")
	}

	// Confluence config validation (skip when disabled).
	if c.Confluence.IsEnabled() {
		if c.Confluence.BaseURL == "" {
			return fmt.Errorf("confluence.base_url must not be empty")
		}
		if _, err := url.ParseRequestURI(c.Confluence.BaseURL); err != nil {
			return fmt.Errorf("confluence.base_url is not a valid URL: %w", err)
		}
		if c.Confluence.Email == "" {
			return fmt.Errorf("confluence.email must not be empty")
		}
		if c.Confluence.APIToken == "" {
			return fmt.Errorf("confluence.api_token must not be empty")
		}
		if c.Confluence.SpaceKey == "" {
			return fmt.Errorf("confluence.space_key must not be empty")
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
