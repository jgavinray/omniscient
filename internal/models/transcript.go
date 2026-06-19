package models

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExtractionResult holds the parsed output from the LLM extraction: YAML front-matter
// and the markdown body.
type ExtractionResult struct {
	FrontMatter map[string]any // parsed YAML front-matter
	Markdown    string         // markdown body after closing ---
}

// ParseExtractionOutput parses LLM output in the format:
//
//	---
//	key: value
//	---
//	# Markdown body
//
// It strips leading code fences, splits on --- delimiters, parses YAML,
// and returns the markdown body.
func ParseExtractionOutput(raw string) (*ExtractionResult, error) {
	s := strings.TrimSpace(raw)

	// Strip leading code fences (LLMs may wrap output).
	if strings.HasPrefix(s, "```markdown") {
		s = strings.TrimPrefix(s, "```markdown")
	} else if strings.HasPrefix(s, "```yaml") {
		s = strings.TrimPrefix(s, "```yaml")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	s = strings.TrimSpace(s)

	// Must start with ---
	if !strings.HasPrefix(s, "---") {
		return nil, fmt.Errorf("missing opening --- delimiter")
	}

	// Find the closing ---
	rest := s[3:] // skip opening ---
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	closingIdx := strings.Index(rest, "\n---")
	if closingIdx == -1 {
		return nil, fmt.Errorf("missing closing --- delimiter")
	}

	yamlBlock := rest[:closingIdx]
	body := rest[closingIdx+4:] // skip \n---

	// Parse YAML front-matter.
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, fmt.Errorf("invalid YAML front-matter: %w", err)
	}

	body = strings.TrimSpace(body)

	return &ExtractionResult{
		FrontMatter: fm,
		Markdown:    body,
	}, nil
}

// MeetingData holds the structured data extracted from a meeting transcript by the LLM.
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
