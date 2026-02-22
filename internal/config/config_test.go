package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validOpenAICompatibleYAML returns a valid config YAML string using the openai-compatible provider.
func validOpenAICompatibleYAML() string {
	return `google:
  credentials_file: /opt/omniscient/credentials.json
  token_file: /opt/omniscient/token.json
  folder_id: abc123folderID
llm:
  provider: openai-compatible
  openai_base_url: http://localhost:11434/v1
  openai_api_key: test-key-123
  model: llama3:70b
  timeout: 90
confluence:
  base_url: https://mycompany.atlassian.net/wiki
  email: user@example.com
  api_token: confluence-token-xyz
  space_key: ENG
  parent_page_id: "12345"
sync:
  lookback_hours: 48
  database_path: /opt/omniscient/data.db
  max_per_run: 25
logging:
  level: debug
  file: /var/log/omniscient.log
`
}

// validAnthropicYAML returns a valid config YAML string using the anthropic provider.
func validAnthropicYAML() string {
	return `google:
  credentials_file: /opt/omniscient/credentials.json
  token_file: /opt/omniscient/token.json
  folder_id: folder-xyz-789
llm:
  provider: anthropic
  anthropic_api_key: sk-ant-api03-testkey123
  model: claude-sonnet-4-20250514
  timeout: 180
confluence:
  base_url: https://wiki.example.com
  email: admin@example.com
  api_token: conf-token-abc
  space_key: MEETINGS
  parent_page_id: "99999"
sync:
  lookback_hours: 12
  database_path: /tmp/omniscient-test.db
  max_per_run: 100
logging:
  level: warn
`
}

// baseValidConfig returns a Config struct that passes validation (openai-compatible).
// Tests can mutate individual fields to trigger specific validation errors.
func baseValidConfig() Config {
	return Config{
		Google: GoogleConfig{
			CredentialsFile: "/opt/omniscient/credentials.json",
			TokenFile:       "/opt/omniscient/token.json",
			FolderID:        "abc123",
		},
		LLM: LLMConfig{
			Provider:      "openai-compatible",
			OpenAIBaseURL: "http://localhost:11434/v1",
			Model:         "llama3:70b",
			Timeout:       90,
		},
		Confluence: ConfluenceConfig{
			BaseURL:      "https://mycompany.atlassian.net/wiki",
			Email:        "user@example.com",
			APIToken:     "token-abc",
			SpaceKey:     "ENG",
			ParentPageID: "12345",
		},
		Sync: SyncConfig{
			LookbackHours: 24,
			DatabasePath:  "/opt/omniscient/data.db",
			MaxPerRun:     50,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

// writeYAML writes content to a file named "config.yaml" in the given directory
// and returns the full path.
func writeYAML(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, validOpenAICompatibleYAML())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	// Google fields.
	if cfg.Google.CredentialsFile != "/opt/omniscient/credentials.json" {
		t.Errorf("Google.CredentialsFile = %q, want %q", cfg.Google.CredentialsFile, "/opt/omniscient/credentials.json")
	}
	if cfg.Google.TokenFile != "/opt/omniscient/token.json" {
		t.Errorf("Google.TokenFile = %q, want %q", cfg.Google.TokenFile, "/opt/omniscient/token.json")
	}
	if cfg.Google.FolderID != "abc123folderID" {
		t.Errorf("Google.FolderID = %q, want %q", cfg.Google.FolderID, "abc123folderID")
	}

	// LLM fields.
	if cfg.LLM.Provider != "openai-compatible" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "openai-compatible")
	}
	if cfg.LLM.OpenAIBaseURL != "http://localhost:11434/v1" {
		t.Errorf("LLM.OpenAIBaseURL = %q, want %q", cfg.LLM.OpenAIBaseURL, "http://localhost:11434/v1")
	}
	if cfg.LLM.OpenAIAPIKey != "test-key-123" {
		t.Errorf("LLM.OpenAIAPIKey = %q, want %q", cfg.LLM.OpenAIAPIKey, "test-key-123")
	}
	if cfg.LLM.Model != "llama3:70b" {
		t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, "llama3:70b")
	}
	if cfg.LLM.Timeout != 90 {
		t.Errorf("LLM.Timeout = %d, want %d", cfg.LLM.Timeout, 90)
	}

	// Confluence fields.
	if cfg.Confluence.BaseURL != "https://mycompany.atlassian.net/wiki" {
		t.Errorf("Confluence.BaseURL = %q, want %q", cfg.Confluence.BaseURL, "https://mycompany.atlassian.net/wiki")
	}
	if cfg.Confluence.Email != "user@example.com" {
		t.Errorf("Confluence.Email = %q, want %q", cfg.Confluence.Email, "user@example.com")
	}
	if cfg.Confluence.APIToken != "confluence-token-xyz" {
		t.Errorf("Confluence.APIToken = %q, want %q", cfg.Confluence.APIToken, "confluence-token-xyz")
	}
	if cfg.Confluence.SpaceKey != "ENG" {
		t.Errorf("Confluence.SpaceKey = %q, want %q", cfg.Confluence.SpaceKey, "ENG")
	}
	if cfg.Confluence.ParentPageID != "12345" {
		t.Errorf("Confluence.ParentPageID = %q, want %q", cfg.Confluence.ParentPageID, "12345")
	}

	// Sync fields.
	if cfg.Sync.LookbackHours != 48 {
		t.Errorf("Sync.LookbackHours = %d, want %d", cfg.Sync.LookbackHours, 48)
	}
	if cfg.Sync.DatabasePath != "/opt/omniscient/data.db" {
		t.Errorf("Sync.DatabasePath = %q, want %q", cfg.Sync.DatabasePath, "/opt/omniscient/data.db")
	}
	if cfg.Sync.MaxPerRun != 25 {
		t.Errorf("Sync.MaxPerRun = %d, want %d", cfg.Sync.MaxPerRun, 25)
	}

	// Logging fields.
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
	if cfg.Logging.File != "/var/log/omniscient.log" {
		t.Errorf("Logging.File = %q, want %q", cfg.Logging.File, "/var/log/omniscient.log")
	}
}

func TestLoad_ValidAnthropicConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, validAnthropicYAML())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "anthropic")
	}
	if cfg.LLM.AnthropicAPIKey != "sk-ant-api03-testkey123" {
		t.Errorf("LLM.AnthropicAPIKey = %q, want %q", cfg.LLM.AnthropicAPIKey, "sk-ant-api03-testkey123")
	}
	if cfg.LLM.Model != "claude-sonnet-4-20250514" {
		t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, "claude-sonnet-4-20250514")
	}
	if cfg.LLM.Timeout != 180 {
		t.Errorf("LLM.Timeout = %d, want %d", cfg.LLM.Timeout, 180)
	}
	if cfg.Google.FolderID != "folder-xyz-789" {
		t.Errorf("Google.FolderID = %q, want %q", cfg.Google.FolderID, "folder-xyz-789")
	}
	if cfg.Confluence.SpaceKey != "MEETINGS" {
		t.Errorf("Confluence.SpaceKey = %q, want %q", cfg.Confluence.SpaceKey, "MEETINGS")
	}
	if cfg.Sync.LookbackHours != 12 {
		t.Errorf("Sync.LookbackHours = %d, want %d", cfg.Sync.LookbackHours, 12)
	}
	if cfg.Sync.MaxPerRun != 100 {
		t.Errorf("Sync.MaxPerRun = %d, want %d", cfg.Sync.MaxPerRun, 100)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "warn")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "reading config file") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "reading config file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeYAML(t, dir, `{{{invalid yaml: [unbalanced`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing config file") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parsing config file")
	}
}

func TestValidate_MissingCredentialsFile(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Google.CredentialsFile = ""

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for empty credentials_file, got nil")
	}
	if !strings.Contains(err.Error(), "google.credentials_file") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "google.credentials_file")
	}
}

func TestValidate_MissingFolderID(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Google.FolderID = ""

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for empty folder_id, got nil")
	}
	if !strings.Contains(err.Error(), "google.folder_id") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "google.folder_id")
	}
}

func TestValidate_InvalidProvider(t *testing.T) {
	cfg := baseValidConfig()
	cfg.LLM.Provider = "gemini"

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for invalid provider, got nil")
	}
	if !strings.Contains(err.Error(), "llm.provider") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "llm.provider")
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("error = %q, want it to mention the invalid value %q", err.Error(), "gemini")
	}
}

func TestValidate_AnthropicMissingKey(t *testing.T) {
	cfg := baseValidConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.AnthropicAPIKey = ""

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for missing anthropic API key, got nil")
	}
	if !strings.Contains(err.Error(), "llm.anthropic_api_key") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "llm.anthropic_api_key")
	}
}

func TestValidate_AnthropicInvalidKeyPrefix(t *testing.T) {
	cfg := baseValidConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.AnthropicAPIKey = "bad-prefix-key-12345"

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for anthropic key with wrong prefix, got nil")
	}
	if !strings.Contains(err.Error(), "sk-ant-") {
		t.Errorf("error = %q, want it to mention the required prefix %q", err.Error(), "sk-ant-")
	}
}

func TestValidate_OpenAIMissingBaseURL(t *testing.T) {
	cfg := baseValidConfig()
	cfg.LLM.Provider = "openai-compatible"
	cfg.LLM.OpenAIBaseURL = ""

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for missing openai base URL, got nil")
	}
	if !strings.Contains(err.Error(), "llm.openai_base_url") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "llm.openai_base_url")
	}
}

func TestValidate_MissingModel(t *testing.T) {
	cfg := baseValidConfig()
	cfg.LLM.Model = ""

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for empty model, got nil")
	}
	if !strings.Contains(err.Error(), "llm.model") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "llm.model")
	}
}

func TestValidate_MissingConfluenceFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "missing base_url",
			mutate: func(c *Config) {
				c.Confluence.BaseURL = ""
			},
			wantErr: "confluence.base_url",
		},
		{
			name: "missing email",
			mutate: func(c *Config) {
				c.Confluence.Email = ""
			},
			wantErr: "confluence.email",
		},
		{
			name: "missing api_token",
			mutate: func(c *Config) {
				c.Confluence.APIToken = ""
			},
			wantErr: "confluence.api_token",
		},
		{
			name: "missing space_key",
			mutate: func(c *Config) {
				c.Confluence.SpaceKey = ""
			},
			wantErr: "confluence.space_key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			tc.mutate(&cfg)

			err := cfg.validate()
			if err == nil {
				t.Fatalf("validate() expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidate_Defaults(t *testing.T) {
	cfg := baseValidConfig()
	// Zero out the fields that should receive defaults.
	cfg.LLM.Timeout = 0
	cfg.Sync.LookbackHours = 0
	cfg.Sync.MaxPerRun = 0
	cfg.Logging.Level = ""

	cfg.applyDefaults()
	err := cfg.validate()
	if err != nil {
		t.Fatalf("validate() returned unexpected error: %v", err)
	}

	if cfg.LLM.Timeout != 120 {
		t.Errorf("LLM.Timeout = %d, want default %d", cfg.LLM.Timeout, 120)
	}
	if cfg.Sync.LookbackHours != 24 {
		t.Errorf("Sync.LookbackHours = %d, want default %d", cfg.Sync.LookbackHours, 24)
	}
	if cfg.Sync.MaxPerRun != 50 {
		t.Errorf("Sync.MaxPerRun = %d, want default %d", cfg.Sync.MaxPerRun, 50)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want default %q", cfg.Logging.Level, "info")
	}
}

func TestValidate_DryRunParsed(t *testing.T) {
	dir := t.TempDir()
	yaml := validOpenAICompatibleYAML() + "dry_run: true\n"
	path := writeYAML(t, dir, yaml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if !cfg.DryRun {
		t.Error("expected DryRun to be true")
	}
}

func TestValidate_ConfluenceDisabledSkipsValidation(t *testing.T) {
	cfg := baseValidConfig()
	f := false
	cfg.Confluence.Enabled = &f
	// Clear required confluence fields — should not trigger validation error.
	cfg.Confluence.BaseURL = ""
	cfg.Confluence.Email = ""
	cfg.Confluence.APIToken = ""
	cfg.Confluence.SpaceKey = ""

	err := cfg.validate()
	if err != nil {
		t.Fatalf("validate() should pass when confluence is disabled, got: %v", err)
	}
}

func TestValidate_ConfluenceEnabledByDefault(t *testing.T) {
	cfg := baseValidConfig()
	// Enabled is nil (default) — should still validate confluence fields.
	if !cfg.Confluence.IsEnabled() {
		t.Error("IsEnabled() should return true when Enabled is nil")
	}
}

func TestValidate_PromptsLoadedFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := validOpenAICompatibleYAML() + `prompts:
  classify_prompt: "Custom classify prompt {{TEMPLATE_KEYS}} {{TRANSCRIPT_PREVIEW}}"
  templates:
    custom_type:
      description: "A custom meeting type"
      extraction_prompt: "Custom extraction prompt {{TRANSCRIPT}}"
`
	path := writeYAML(t, dir, yaml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if !strings.Contains(cfg.Prompts.ClassifyPrompt, "Custom classify prompt") {
		t.Errorf("expected custom classify prompt, got %q", cfg.Prompts.ClassifyPrompt)
	}
	if _, ok := cfg.Prompts.Templates["custom_type"]; !ok {
		t.Error("expected custom_type template to be present")
	}
	if cfg.Prompts.Templates["custom_type"].Description != "A custom meeting type" {
		t.Errorf("expected template description, got %q", cfg.Prompts.Templates["custom_type"].Description)
	}
}

func TestValidate_DefaultPromptsPopulated(t *testing.T) {
	cfg := baseValidConfig()
	// No prompts section — defaults should be populated by applyDefaults.
	cfg.applyDefaults()
	err := cfg.validate()
	if err != nil {
		t.Fatalf("validate() returned unexpected error: %v", err)
	}

	if cfg.Prompts.ClassifyPrompt == "" {
		t.Error("expected default classify prompt to be populated")
	}
	if len(cfg.Prompts.Templates) == 0 {
		t.Error("expected default templates to be populated")
	}
	for _, key := range []string{"engineering", "customer_success", "planning"} {
		if _, ok := cfg.Prompts.Templates[key]; !ok {
			t.Errorf("expected default template %q to be present", key)
		}
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := baseValidConfig()
	cfg.Logging.Level = "trace"

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error for invalid log level, got nil")
	}
	if !strings.Contains(err.Error(), "logging.level") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "logging.level")
	}
	if !strings.Contains(err.Error(), "trace") {
		t.Errorf("error = %q, want it to mention the invalid value %q", err.Error(), "trace")
	}
}
