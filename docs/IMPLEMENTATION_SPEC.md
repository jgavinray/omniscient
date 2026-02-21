Here's the complete specification for Sonnet:

───

Omniscient: Meeting Transcript Harvester - Implementation Specification

You are tasked with building Omniscient, a Go application that automatically harvests Google Meet transcripts, extracts structured data via LLM, and publishes to Confluence.

───

Project Requirements

Purpose

Automate the extraction of meeting context (decisions, blockers, action items) from Google Meet transcripts and make them searchable in Confluence.

Execution Model

• Type: Command-line tool designed to run as a cron job
• Invocation: omniscient sync (process recent transcripts)
• Frequency: Intended to run every 30 minutes via cron
• Exit behavior: Process all pending transcripts and exit (no long-running daemon)

Constraints

• Narrow scope: ONLY the core 3-step pipeline (download → extract → publish)
• No web UI, monitoring dashboard, or real-time processing
• Single-team deployment (no multi-tenant support)
• Focus on reliability over features

───

Architecture

Google Drive (Transcripts)
↓ (1) Download via Drive API
Omniscient Go Application
↓ (2) Extract via LLM
LLM Provider (Anthropic or OpenAI-compatible)
↓ (3) Publish
Confluence (formatted pages)

State tracking: SQLite database (deduplication)

───

Technical Stack

Language & Libraries

Language: Go 1.23+

Required Go packages:

// Google Drive API (OAuth2)
"google.golang.org/api/drive/v3"
"google.golang.org/api/option"
"golang.org/x/oauth2"
"golang.org/x/oauth2/google"

// HTTP client for LLM and Confluence APIs
"net/http"
"encoding/json"

// SQLite (pure Go, no CGO)
"modernc.org/sqlite"

// CLI framework
"github.com/spf13/cobra"

// Configuration
"gopkg.in/yaml.v3"

// Logging
"log/slog" (Go 1.21+ standard library)

// Standard library
"os"
"time"
"fmt"
"io"
"strings"

───

Configuration

File Location

/opt/omniscient/config.yaml

Schema

# /opt/omniscient/config.yaml

google:
# Path to Google OAuth2 client credentials JSON
# Download from: Google Cloud Console → APIs & Services → Credentials → OAuth 2.0 Client IDs
credentials_file: "/opt/omniscient/credentials/credentials.json"

# Path where the OAuth2 token will be stored after first-run browser consent
# Auto-generated — do not create manually
token_file: "/opt/omniscient/credentials/token.json"

# Google Drive folder ID containing transcripts
# Find via: Right-click folder in Drive → "Get link" → Extract ID from URL
folder_id: "1abc...xyz"

llm:
# Provider: "anthropic" or "openai-compatible"
provider: "openai-compatible"

# For provider=anthropic:
# Anthropic API key (get from: https://console.anthropic.com/settings/keys)
anthropic_api_key: ""

# For provider=openai-compatible:
# Base URL for OpenAI-compatible API (vLLM, Ollama, OpenAI, etc.)
openai_base_url: "http://spark:8000/v1"
# API key (optional for local endpoints, required for OpenAI)
openai_api_key: "local"

# Model name
# For anthropic: "claude-opus-4", "claude-sonnet-4", "claude-haiku-4"
# For openai-compatible: depends on endpoint (e.g., "Qwen/Qwen2.5-72B-Instruct-AWQ")
model: "Qwen/Qwen2.5-72B-Instruct-AWQ"

# Timeout for LLM extraction (seconds)
timeout: 120

confluence:
# Confluence Cloud URL (e.g., https://your-company.atlassian.net)
base_url: "https://your-company.atlassian.net"

# Confluence API authentication
# Email of API token owner
email: "your-email@example.com"

# API token (create at: https://id.atlassian.com/manage/api-tokens)
api_token: "your-confluence-api-token"

# Target Confluence space key (e.g., "ENG", "TEAM")
space_key: "ENG"

# Optional: Parent page ID (all transcript pages created under this)
# Leave empty for root-level pages
parent_page_id: ""

sync:
# How far back to look for transcripts (hours)
lookback_hours: 24

# SQLite database path (tracks processed transcripts)
database_path: "/opt/omniscient/data/omniscient.db"

# Maximum transcripts to process per run
# (prevents runaway processing on first run)
max_per_run: 50

logging:
# Log level: debug, info, warn, error
level: "info"

# Log file path (leave empty for stdout only)
file: "/var/log/omniscient/omniscient.log"

Example Configurations

Example 1: Using local vLLM (Spark 72B):

llm:

provider: "openai-compatible"
openai_base_url: "http://spark:8000/v1"
openai_api_key: "local"
model: "Qwen/Qwen2.5-72B-Instruct-AWQ"
timeout: 120

Example 2: Using Anthropic Claude:

llm:
provider: "anthropic"
anthropic_api_key: "sk-ant-..."
model: "claude-sonnet-4"
timeout: 120

Example 3: Using OpenAI:

llm:
provider: "openai-compatible"
openai_base_url: "https://api.openai.com/v1"
openai_api_key: "sk-..."
model: "gpt-4"
timeout: 120

Example 4: Using Ollama (Jetson):

llm:
provider: "openai-compatible"
openai_base_url: "http://jetson:11434/v1"
openai_api_key: "ollama"
model: "qwen2.5:3b"
timeout: 120

───

Project Structure

omniscient/
├── cmd/
│   └── omniscient/
│       ├── main.go              # Entry point (Cobra root command)
│       ├── sync.go              # sync command
│       ├── auth.go              # auth command (OAuth2 browser flow)
│       └── config.go            # config validate command
├── internal/
│   ├── config/
│   │   └── config.go            # Load and validate config.yaml
│   ├── drive/
│   │   ├── client.go            # Google Drive API client
│   │   └── oauth.go             # OAuth2 token management (consent flow, token refresh)
│   ├── llm/
│   │   ├── extractor.go         # LLM extractor interface
│   │   ├── anthropic.go         # Anthropic provider implementation
│   │   └── openai.go            # OpenAI-compatible provider implementation
│   ├── confluence/
│   │   └── publisher.go         # Confluence REST API client
│   ├── database/
│   │   └── store.go             # SQLite operations
│   └── models/
│       └── transcript.go        # Shared data structures
├── config.yaml.example          # Example configuration
├── go.mod
├── go.sum
├── Makefile                     # Build targets
└── README.md

───

Component Specifications

1. Configuration Loader (internal/config/config.go)

Responsibilities:

• Load YAML from /opt/omniscient/config.yaml
• Validate required fields
• Return typed Config struct

Interface:

type Config struct {
Google     GoogleConfig
LLM        LLMConfig
Confluence ConfluenceConfig
Sync       SyncConfig
Logging    LoggingConfig
}

type GoogleConfig struct {
CredentialsFile string `yaml:"credentials_file"` // OAuth2 client credentials JSON
TokenFile       string `yaml:"token_file"`        // OAuth2 token (auto-generated)
FolderID        string `yaml:"folder_id"`
}

type LLMConfig struct {
Provider         string `yaml:"provider"` // "anthropic" or "openai-compatible"
AnthropicAPIKey  string `yaml:"anthropic_api_key"`
OpenAIBaseURL    string `yaml:"openai_base_url"`
OpenAIAPIKey     string `yaml:"openai_api_key"`
Model            string `yaml:"model"`
Timeout          int    `yaml:"timeout"`
}

type ConfluenceConfig struct {
BaseURL      string `yaml:"base_url"`
Email        string `yaml:"email"`
APIToken     string `yaml:"api_token"`
SpaceKey     string `yaml:"space_key"`
ParentPageID string `yaml:"parent_page_id"`
}

type SyncConfig struct {
LookbackHours int    `yaml:"lookback_hours"`
DatabasePath  string `yaml:"database_path"`
MaxPerRun     int    `yaml:"max_per_run"`
}

type LoggingConfig struct {
Level string `yaml:"level"`
File  string `yaml:"file"`
}

// Load reads and validates configuration
func Load() (*Config, error)

Validation requirements:

• All required fields present (based on provider)
• File paths exist
• URLs are valid
• lookback_hours > 0
• Provider must be "anthropic" or "openai-compatible"
• If provider=anthropic, require anthropic_api_key and validate format (starts with "sk-ant-")
• If provider=openai-compatible, require openai_base_url

Error handling:

• Return descriptive error if config invalid
• Log which field failed validation

───

2. Google Drive Client (internal/drive/client.go)

Responsibilities:

• Authenticate via OAuth2 (browser consent on first run, token refresh on subsequent runs)
• List transcript files from specified folder
• Filter by modification time (lookback window)
• Download transcript content as plain text

Interface:

type Transcript struct {
ID         string    // Drive file ID
Name       string    // File name
ModifiedAt time.Time // Last modified timestamp
Content    string    // Transcript text content
}

type Client struct {
service *drive.Service
}

// NewClient creates authenticated Drive client using OAuth2
func NewClient(credentialsPath, tokenPath string) (*Client, error)

// GetRecentTranscripts fetches transcripts modified within lookback window
func (c *Client) GetRecentTranscripts(folderID string, since time.Duration) ([]*Transcript, error)

OAuth2 Flow (internal/drive/oauth.go):

• Read OAuth2 client credentials from credentials.json (downloaded from Google Cloud Console)
• On first run (no token.json exists):
  1. Generate authorization URL with scope drive.readonly
  2. Open user's browser to Google consent screen
  3. Start temporary local HTTP server (e.g., localhost:8085) to receive the callback
  4. Exchange authorization code for access + refresh tokens
  5. Save token to token.json
• On subsequent runs:
  1. Load token.json
  2. If access token expired, use refresh token to obtain new one (automatic via oauth2.TokenSource)
  3. Save updated token back to token.json
• Required OAuth2 scopes: https://www.googleapis.com/auth/drive.readonly

Standalone auth command:

• `omniscient auth` triggers the OAuth2 consent flow independently
• Useful for initial setup and re-authentication if token is revoked

Implementation notes:

• Search query: mimeType='application/vnd.google-apps.document' and modifiedTime > '<timestamp>' and '<folderID>' in parents
• Export as plain text: drive.Files.Export(fileID, "text/plain")
• Sort by modifiedTime descending (newest first)

Error handling:

• Retry transient API errors (exponential backoff, 3 attempts)
• Log warning if file can't be downloaded (continue with others)
• Return error only if authentication fails

───

3. LLM Extractor (internal/llm/)

Architecture:

Use interface-based design with provider-specific implementations:

// internal/llm/extractor.go

package llm

// Extractor defines the interface for LLM extraction
type Extractor interface {
Extract(transcript string) (*MeetingData, error)
}

// NewExtractor creates appropriate extractor based on config
func NewExtractor(cfg *config.LLMConfig) (Extractor, error) {
switch cfg.Provider {
case "anthropic":
return NewAnthropicExtractor(cfg.AnthropicAPIKey, cfg.Model, time.Duration(cfg.Timeout)*time.Second), nil
case "openai-compatible":
return NewOpenAIExtractor(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.Model, time.Duration(cfg.Timeout)*time.Second), nil
default:
return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
}
}

type MeetingData struct {
MeetingType        string     `json:"meeting_type"`
Date               string     `json:"date"`
Time               string     `json:"time"`
DurationMin        int        `json:"duration_min"`
Participants       []string   `json:"participants"`
ProjectsDiscussed  []string   `json:"projects_discussed"`
DecisionsMade      []Decision `json:"decisions_made"`
BlockersIdentified []Blocker  `json:"blockers_identified"`
ActionItems        []Action   `json:"action_items"`
KeyQuotes          []Quote    `json:"key_quotes"`
Sentiment          string     `json:"sentiment"`
Summary            string     `json:"summary"`
}

type Decision struct {
Decision  string `json:"decision"`
Owner     string `json:"owner"`
Rationale string `json:"rationale"`
}

type Blocker struct {
Blocker    string `json:"blocker"`
Ticket     string `json:"ticket"`
Impact     string `json:"impact"`
Escalation string `json:"escalation"`
}

type Action struct {
Task    string `json:"task"`
Owner   string `json:"owner"`
DueDate string `json:"due_date"`
}

type Quote struct {
Speaker string `json:"speaker"`
Quote   string `json:"quote"`
Context string `json:"context"`
}

Extraction Prompt (shared by all providers):

Extract structured information from this meeting transcript.

TRANSCRIPT:
{{TRANSCRIPT_TEXT}}

OUTPUT (JSON):
{
"meeting_type": "standup|design_review|retrospective|planning|1on1|all_hands",
"date": "YYYY-MM-DD",
"time": "HH:MM",
"duration_min": <integer>,
"participants": ["name1", "name2", ...],
"projects_discussed": ["project1", "project2", ...],
"decisions_made": [
{"decision": "...", "owner": "...", "rationale": "..."}
],
"blockers_identified": [
{"blocker": "...", "ticket": "...", "impact": "...", "escalation": "..."}
],
"action_items": [
{"task": "...", "owner": "...", "due_date": "..."}
],
"key_quotes": [

{"speaker": "...", "quote": "...", "context": "..."}
],
"sentiment": "urgent|calm|frustrated|excited",
"summary": "One paragraph summary"
}

INSTRUCTIONS:
- Be thorough. Extract all decisions, blockers, and action items.
- If information is not present, use null for that field.
- Return ONLY valid JSON. No markdown, no explanation, no preamble.
- Ensure JSON is properly escaped and parseable.

───

3a. Anthropic Provider (internal/llm/anthropic.go)

package llm

import (
"bytes"
"encoding/json"
"fmt"
"net/http"
"time"
)

type AnthropicExtractor struct {
apiKey     string
model      string
httpClient *http.Client
}

func NewAnthropicExtractor(apiKey, model string, timeout time.Duration) *AnthropicExtractor {
return &AnthropicExtractor{
apiKey: apiKey,
model:  model,
httpClient: &http.Client{
Timeout: timeout,
},
}
}

func (e *AnthropicExtractor) Extract(transcript string) (*MeetingData, error) {
prompt := buildPrompt(transcript)

    reqBody := map[string]interface{}{
        "model": e.model,
        "max_tokens": 4096,
        "messages": []map[string]string{
            {"role": "user", "content": prompt},
        },
    }
    
    jsonData, _ := json.Marshal(reqBody)
    
    req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonData))
    req.Header.Set("x-api-key", e.apiKey)
    req.Header.Set("anthropic-version", "2023-06-01")
    req.Header.Set("Content-Type", "application/json")
    
    resp, err := e.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("anthropic api request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("anthropic api error: status %d", resp.StatusCode)
    }
    
    var apiResp struct {
        Content []struct {
            Text string `json:"text"`
        } `json:"content"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
        return nil, fmt.Errorf("decode anthropic response: %w", err)
    }
    
    if len(apiResp.Content) == 0 {
        return nil, fmt.Errorf("empty response from anthropic")
    }
    
    // Parse JSON from response text
    var meetingData MeetingData
    if err := json.Unmarshal([]byte(apiResp.Content[0].Text), &meetingData); err != nil {
        return nil, fmt.Errorf("parse meeting data json: %w", err)
    }
    
    return &meetingData, nil
}

───

3b. OpenAI-Compatible Provider (internal/llm/openai.go)

package llm

import (
"bytes"
"encoding/json"
"fmt"
"net/http"
"time"
)

type OpenAIExtractor struct {
baseURL    string
apiKey     string
model      string
httpClient *http.Client
}

func NewOpenAIExtractor(baseURL, apiKey, model string, timeout time.Duration) *OpenAIExtractor {
return &OpenAIExtractor{
baseURL: baseURL,
apiKey:  apiKey,
model:   model,
httpClient: &http.Client{
Timeout: timeout,
},
}
}

func (e *OpenAIExtractor) Extract(transcript string) (*MeetingData, error) {
prompt := buildPrompt(transcript)

    reqBody := map[string]interface{}{
        "model": e.model,
        "messages": []map[string]string{
            {"role": "user", "content": prompt},
        },
        "temperature": 0.1,
        "max_tokens": 4096,
    }
    
    jsonData, _ := json.Marshal(reqBody)
    
    url := e.baseURL + "/chat/completions"
    req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
    req.Header.Set("Authorization", "Bearer "+e.apiKey)
    req.Header.Set("Content-Type", "application/json")
    
    resp, err := e.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("openai api request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("openai api error: status %d", resp.StatusCode)

}

    var apiResp struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
        return nil, fmt.Errorf("decode openai response: %w", err)
    }
    
    if len(apiResp.Choices) == 0 {
        return nil, fmt.Errorf("empty response from openai")
    }
    
    // Parse JSON from response
    var meetingData MeetingData
    if err := json.Unmarshal([]byte(apiResp.Choices[0].Message.Content), &meetingData); err != nil {
        return nil, fmt.Errorf("parse meeting data json: %w", err)
    }
    
    return &meetingData, nil
}

───

3c. Shared Utilities (internal/llm/util.go)

package llm

import "fmt"

func buildPrompt(transcript string) string {
return fmt.Sprintf(`Extract structured information from this meeting transcript.

TRANSCRIPT:
%s

OUTPUT (JSON):
{
"meeting_type": "standup|design_review|retrospective|planning|1on1|all_hands",
"date": "YYYY-MM-DD",
"time": "HH:MM",
"duration_min": <integer>,
"participants": ["name1", "name2", ...],
"projects_discussed": ["project1", "project2", ...],
"decisions_made": [
{"decision": "...", "owner": "...", "rationale": "..."}
],
"blockers_identified": [
{"blocker": "...", "ticket": "...", "impact": "...", "escalation": "..."}
],
"action_items": [
{"task": "...", "owner": "...", "due_date": "..."}
],
"key_quotes": [
{"speaker": "...", "quote": "...", "context": "..."}
],
"sentiment": "urgent|calm|frustrated|excited",
"summary": "One paragraph summary"
}

INSTRUCTIONS:
- Be thorough. Extract all decisions, blockers, and action items.
- If information is not present, use null for that field.
- Return ONLY valid JSON. No markdown, no explanation, no preamble.
- Ensure JSON is properly escaped and parseable.`, transcript)
  }

Error handling:

• Retry on HTTP 429 (rate limit - use Retry-After header if available)
• Retry on HTTP 5xx (3 attempts, exponential backoff)
• If JSON parsing fails, log full response and return error
• If LLM returns invalid structure, log and return error
• Timeout handled by http.Client

───

4. Confluence Publisher (internal/confluence/publisher.go)

Responsibilities:

• Create Confluence page from MeetingData
• Format as readable HTML (Confluence storage format)
• Check if page already exists (by title)
• Update if exists, create if new

Interface:

type Client struct {
baseURL    string
email      string
apiToken   string
httpClient *http.Client
}

// NewClient creates Confluence API client
func NewClient(baseURL, email, apiToken string) *Client

// Publish creates or updates Confluence page
// Returns page URL
func (c *Client) Publish(spaceKey, parentPageID string, data *llm.MeetingData, transcriptName string) (string, error)

Page Title Format:

[Meeting Name] - YYYY-MM-DD

Extract meeting name from transcript filename (e.g., "Team Standup.gdoc" → "Team Standup")

Page Content (Confluence Storage Format HTML):

<h2>Summary</h2>
<p>{{.Summary}}</p>

<h2>Meeting Details</h2>
<table>
  <tr><th>Date</th><td>{{.Date}} {{.Time}}</td></tr>
  <tr><th>Duration</th><td>{{.DurationMin}} minutes</td></tr>
  <tr><th>Type</th><td>{{.MeetingType}}</td></tr>
  <tr><th>Participants</th><td>{{range .Participants}}{{.}}, {{end}}</td></tr>
</table>

<h2>Decisions Made</h2>
<ul>
  {{range .DecisionsMade}}
  <li><strong>{{.Decision}}</strong> (Owner: {{.Owner}})<br/>
      <em>Rationale: {{.Rationale}}</em></li>
  {{end}}
</ul>

<h2>Blockers Identified</h2>
<ul>
  {{range .BlockersIdentified}}
  <li><strong>{{.Blocker}}</strong><br/>
      Impact: {{.Impact}}<br/>
      Ticket: {{.Ticket}}<br/>
      Escalation: {{.Escalation}}</li>
  {{end}}
</ul>

<h2>Action Items</h2>
<ul>
  {{range .ActionItems}}
  <li><ac:task><ac:task-status>incomplete</ac:task-status>

<ac:task-body>{{.Task}} (Owner: {{.Owner}}, Due: {{.DueDate}})</ac:task-body>
</ac:task></li>
{{end}}
</ul>

<h2>Projects Discussed</h2>
<ul>
  {{range .ProjectsDiscussed}}
  <li>{{.}}</li>
  {{end}}
</ul>

<ac:structured-macro ac:name="info">
<ac:rich-text-body>
<p>Generated from Google Meet transcript: <strong>{{transcriptName}}</strong></p>
<p>Sentiment: {{.Sentiment}}</p>
</ac:rich-text-body>
</ac:structured-macro>

Confluence API Usage:

1. Check if page exists:

GET /wiki/rest/api/content?spaceKey={space}&title={title}

2. Create new page:

POST /wiki/rest/api/content
{
"type": "page",
"title": "Meeting Name - 2026-02-21",
"space": {"key": "ENG"},
"ancestors": [{"id": "parent-page-id"}],  // Optional
"body": {
"storage": {
"value": "<html content>",
"representation": "storage"
}
}
}

3. Update existing page:

PUT /wiki/rest/api/content/{pageId}
{
"version": {"number": currentVersion + 1},
"title": "Meeting Name - 2026-02-21",
"type": "page",
"body": {
"storage": {
"value": "<html content>",
"representation": "storage"
}
}
}

Authentication:

• Basic Auth: base64(email:api_token)
• Header: Authorization: Basic <base64>

Error handling:

• Retry on 429 (rate limit) with Retry-After header
• Retry on 5xx (3 attempts)
• Log error if page creation fails (don't halt entire run)
• Return page URL on success for logging

───

5. Database Store (internal/database/store.go)

Responsibilities:

• Track which transcripts have been processed
• Prevent duplicate processing
• Simple SQLite-based state storage

Interface:

type Store struct {
db *sql.DB
}

// NewStore opens SQLite database and creates schema
func NewStore(dbPath string) (*Store, error)

// IsProcessed checks if transcript ID already processed
func (s *Store) IsProcessed(transcriptID string) (bool, error)

// MarkProcessed records successful processing
func (s *Store) MarkProcessed(transcriptID, transcriptName, confluenceURL string) error

// Close closes database connection
func (s *Store) Close() error

Database Schema:

CREATE TABLE IF NOT EXISTS processed_transcripts (
transcript_id TEXT PRIMARY KEY,
transcript_name TEXT NOT NULL,
processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
confluence_url TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_processed_at ON processed_transcripts(processed_at);

Implementation notes:

• Create database file and schema on first run
• Use prepared statements for all queries
• Transaction for MarkProcessed (ensure atomicity)

Error handling:

• Return error if database can't be created/opened
• Log warning if query fails (don't crash)

───

6. Main Application Flow (cmd/omniscient/main.go)

CLI Interface (Cobra):

# Process recent transcripts
omniscient sync

# Authenticate with Google (OAuth2 browser flow)
omniscient auth

# Show version
omniscient version

# Validate configuration
omniscient config validate

# Show help
omniscient --help

Sync Command Implementation:

func runSync(cfg *config.Config) error {
// 1. Initialize clients
driveClient, err := drive.NewClient(cfg.Google.CredentialsFile, cfg.Google.TokenFile)
if err != nil {
return fmt.Errorf("init drive client: %w", err)
}

    llmExtractor, err := llm.NewExtractor(&cfg.LLM)
    if err != nil {
        return fmt.Errorf("init llm extractor: %w", err)
    }
    
    confluenceClient := confluence.NewClient(
        cfg.Confluence.BaseURL,
        cfg.Confluence.Email,
        cfg.Confluence.APIToken,
    )
    
    store, err := database.NewStore(cfg.Sync.DatabasePath)
    if err != nil {
        return fmt.Errorf("init database: %w", err)
    }
    defer store.Close()
    
    // 2. Fetch recent transcripts from Google Drive
    lookback := time.Duration(cfg.Sync.LookbackHours) * time.Hour
    transcripts, err := driveClient.GetRecentTranscripts(cfg.Google.FolderID, lookback)
    if err != nil {
        return fmt.Errorf("fetch transcripts: %w", err)
    }
    
    slog.Info("fetched transcripts", "count", len(transcripts))

// 3. Filter out already processed
var pending []*drive.Transcript
for _, t := range transcripts {
processed, err := store.IsProcessed(t.ID)
if err != nil {
slog.Warn("check processed failed", "id", t.ID, "error", err)
continue
}
if !processed {
pending = append(pending, t)
}
}

    slog.Info("pending transcripts", "count", len(pending))
    
    // 4. Limit to max_per_run (safety)
    if len(pending) > cfg.Sync.MaxPerRun {
        slog.Warn("limiting transcripts", "pending", len(pending), "max", cfg.Sync.MaxPerRun)
        pending = pending[:cfg.Sync.MaxPerRun]
    }
    
    // 5. Process each transcript
    successCount := 0
    for i, transcript := range pending {
        slog.Info("processing transcript", 
            "num", i+1, 
            "total", len(pending), 
            "name", transcript.Name)
        
        // Extract structured data via LLM
        meetingData, err := llmExtractor.Extract(transcript.Content)
        if err != nil {
            slog.Error("extraction failed", "id", transcript.ID, "error", err)
            continue  // Skip this transcript, continue with others
        }
        
        // Publish to Confluence
        confluenceURL, err := confluenceClient.Publish(
            cfg.Confluence.SpaceKey,
            cfg.Confluence.ParentPageID,
            meetingData,
            transcript.Name,
        )
        if err != nil {
            slog.Error("publish failed", "id", transcript.ID, "error", err)
            continue
        }
        
        // Mark as processed
        if err := store.MarkProcessed(transcript.ID, transcript.Name, confluenceURL); err != nil {
            slog.Error("mark processed failed", "id", transcript.ID, "error", err)
            // Continue anyway (idempotent publish will handle duplicate)
        }
        
        slog.Info("published", "url", confluenceURL)
        successCount++
    }
    
    slog.Info("sync complete", "success", successCount, "total", len(pending))
    
    if successCount == 0 && len(pending) > 0 {
        return fmt.Errorf("all transcripts failed to process")
    }
    
    return nil
}

Error handling philosophy:

• Individual transcript failures should NOT halt entire run
• Log errors but continue processing remaining transcripts
• Exit code 0 if at least one transcript succeeds OR no pending transcripts
• Exit code 1 only if catastrophic failure (can't init clients, config invalid, all transcripts failed)

Logging:

• Use structured logging (slog)
• Include context in all log messages (transcript ID, count, etc.)
• Log levels:
• INFO: Normal operations (fetched N transcripts, published page)
• WARN: Recoverable errors (skipped transcript, rate limit)
• ERROR: Processing failures (extraction failed, publish failed)
• DEBUG: Detailed info (API requests, response sizes)

───

Testing Requirements

Unit Tests

Each package should have tests:

internal/config/config_test.go:

• Valid config loads successfully
• Missing required field returns error
• Environment variable override works
• Invalid provider returns error
• Provider-specific validation (API keys, base URLs)

internal/drive/client_test.go:

• Mock Google Drive API
• Test transcript filtering by time
• Test plain text export

internal/llm/anthropic_test.go:

• Mock Anthropic API response
• Test JSON parsing edge cases
• Test retry logic on failures

internal/llm/openai_test.go:

• Mock OpenAI-compatible API response
• Test JSON parsing edge cases
• Test retry logic on failures

internal/confluence/publisher_test.go:

• Mock Confluence API
• Test HTML generation from MeetingData
• Test create vs update logic

internal/database/store_test.go:

• Test IsProcessed with empty database
• Test MarkProcessed and subsequent IsProcessed
• Test concurrent access (if needed)

Integration Test

cmd/omniscient/main_test.go:

• End-to-end test with mocked external APIs
• Verify complete pipeline (drive → llm → confluence)
• Test both Anthropic and OpenAI providers

Manual Testing Checklist

1. ✅ Config loads from /opt/omniscient/config.yaml
2. ✅ Google Drive authentication succeeds
3. ✅ Can fetch transcripts from folder
4. ✅ LLM extraction returns valid JSON (both providers)
5. ✅ Confluence page created successfully
6. ✅ Database tracks processed transcripts
7. ✅ Re-running sync doesn't duplicate pages
8. ✅ Failed extraction doesn't crash (logs error, continues)
9. ✅ Logs written to configured file
10. ✅ Can switch providers via config

───

Build & Deployment

Makefile

.PHONY: build test install clean

build:
go build -o bin/omniscient cmd/omniscient/main.go

test:
go test -v ./...

install:
sudo mkdir -p /opt/omniscient/{data,credentials}
sudo cp bin/omniscient /usr/local/bin/
sudo cp config.yaml.example /opt/omniscient/config.yaml
@echo "Edit /opt/omniscient/config.yaml before running"

clean:
rm -rf bin/

Installation Steps

1. Build:

make build

2. Install:

sudo make install

3. Configure:

sudo nano /opt/omniscient/config.yaml
# Fill in all required fields:
# - Google credentials path
# - Google Drive folder ID
# - LLM provider and credentials
# - Confluence credentials

4. Place Google OAuth2 client credentials:

# Download OAuth 2.0 Client ID JSON from Google Cloud Console:
# APIs & Services → Credentials → Create Credentials → OAuth client ID → Desktop app
sudo cp ~/Downloads/credentials.json /opt/omniscient/credentials/

5. Authenticate with Google:

omniscient auth
# Opens browser for Google consent. Token saved to token_file path in config.

6. Test:

omniscient sync

7. Set up cron:

# Run every 30 minutes
*/30 * * * * /usr/local/bin/omniscient sync >> /var/log/omniscient/cron.log 2>&1

───

Error Handling Patterns

Transient Errors (Retry)

• HTTP 429 (rate limit)
• HTTP 5xx (server errors)
• Network timeouts

Pattern:

func retryable(fn func() error, maxAttempts int) error {
var err error
for i := 0; i < maxAttempts; i++ {
err = fn()
if err == nil {
return nil
}

        if !isTransient(err) {
            return err  // Don't retry permanent errors
        }
        
        backoff := time.Duration(math.Pow(2, float64(i))) * time.Second
        slog.Warn("retrying after error", "attempt", i+1, "backoff", backoff, "error", err)
        time.Sleep(backoff)
    }
    return fmt.Errorf("max retries exceeded: %w", err)
}

Permanent Errors (Fail Fast)

• Authentication failure (401)
• Invalid configuration
• Database corruption

Pattern:

• Log error with full context
• Return error to caller
• Exit application (if in main)

Recoverable Errors (Log and Continue)

• Single transcript extraction fails
• Single Confluence publish fails
• Database mark processed fails

Pattern:

• Log error with slog.Error
• Continue processing remaining items
• Report summary at end (X succeeded, Y failed)

───

Security Considerations

Credential Storage

• Store Google OAuth2 client credentials (credentials.json) in /opt/omniscient/credentials/
• OAuth2 token (token.json) is auto-generated after first auth — contains refresh token
• Store API keys in config.yaml only (no environment variable overrides)
• File permissions: 600 (readable only by omniscient user)
• Never log credentials, API keys, or tokens

API Tokens

• Store in config.yaml with restricted permissions (chmod 600)
• Rotate tokens periodically (document in README)
• If Google OAuth2 token is revoked, re-run `omniscient auth`

Network Security

• All APIs use HTTPS (Google, Anthropic, Confluence)
• For local LLM endpoints (HTTP), ensure network isolation
• Validate TLS certificates for HTTPS endpoints
• No plaintext credential transmission

───

Output & Success Criteria

What Success Looks Like

After running omniscient sync:

1. ✅ Log shows:

INFO fetched transcripts count=5
INFO pending transcripts count=3
INFO processing transcript num=1 total=3 name="Team Standup.gdoc"
INFO published url="https://company.atlassian.net/wiki/spaces/ENG/pages/12345"
INFO processing transcript num=2 total=3 name="Design Review.gdoc"
INFO published url="https://company.atlassian.net/wiki/spaces/ENG/pages/12346"
INFO processing transcript num=3 total=3 name="Planning Meeting.gdoc"
INFO published url="https://company.atlassian.net/wiki/spaces/ENG/pages/12347"
INFO sync complete success=3 total=3

2. ✅ Confluence pages created with:
   • Proper formatting (headings, lists, tables)
   • All extracted data present
   • Readable layout
3. ✅ Database tracks processed transcripts:

SELECT * FROM processed_transcripts;
-- Shows 3 rows with transcript IDs and Confluence URLs

4. ✅ Re-running omniscient sync:

INFO fetched transcripts count=5
INFO pending transcripts count=0
INFO sync complete success=0 total=0

5. ✅ Handles errors gracefully:

INFO fetched transcripts count=5
INFO pending transcripts count=3
ERROR extraction failed id="abc123" error="JSON parse error: unexpected token"
INFO processing transcript num=2 total=3 name="Valid Meeting.gdoc"
INFO published url="https://company.atlassian.net/wiki/spaces/ENG/pages/12348"
INFO sync complete success=1 total=2

6. ✅ Can switch providers via config:

# Test with local vLLM
llm:
provider: "openai-compatible"
openai_base_url: "http://spark:8000/v1"
model: "Qwen/Qwen2.5-72B-Instruct-AWQ"

# Switch to Anthropic
llm:
provider: "anthropic"
anthropic_api_key: "sk-ant-..."
model: "claude-sonnet-4"

───

Questions to Ask During Implementation

If you encounter ambiguity, make reasonable assumptions and document them in comments:

Example ambiguities:

• Q: Should we deduplicate participants list?
A: Yes, use map[string]bool to deduplicate
• Q: What if transcript has no decisions/blockers?
A: Display empty section with "None identified"
• Q: Should we preserve original transcript text in Confluence?
A: No, only show extracted structured data (reduces noise)

Document assumptions in code:

// Assumption: Transcript names are unique within the folder
// If duplicates exist, only the newest is processed

───

Deliverables

Provide these files:

1. Complete Go code for all components
2. go.mod with all dependencies
3. Makefile with build/test/install targets
4. config.yaml.example with commented examples for both providers
5. README.md with:
   • Installation instructions
   • Configuration guide (both Anthropic and OpenAI-compatible)
   • Cron setup
   • Troubleshooting section
   • Provider comparison table
6. Basic tests for critical paths

───

Scope Boundaries (What NOT to Include)

❌ Web UI or REST API
❌ Real-time processing (batch only)
❌ Multi-tenant support
❌ User authentication
❌ Monitoring/alerting dashboard
❌ Slack/email notifications
❌ Advanced search functionality
❌ Jira integration (out of scope for v1)
❌ Markdown backup (Confluence is source of truth)
❌ Version history tracking
❌ Additional LLM providers beyond Anthropic and OpenAI-compatible

Focus ONLY on: Download → Extract (multi-provider) → Publish → Track

───

Summary

Build a reliable, maintainable Go application that:

• Runs as a cron job every 30 minutes
• Fetches Google Meet transcripts from Drive
• Extracts structured meeting data via configurable LLM provider (Anthropic or OpenAI-compatible)
• Publishes formatted pages to Confluence
• Tracks processed transcripts in SQLite
• Handles errors gracefully (log and continue)
• Configurable via YAML
• Production-ready code quality

Target: Complete, tested, deployable application ready for cron scheduling.

Key feature: Clean provider abstraction supporting both Anthropic API and OpenAI-compatible endpoints (vLLM, Ollama, OpenAI, etc.) with zero code changes required to switch providers.
    
