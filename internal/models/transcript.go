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
	s := StripCodeFences(raw)

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

// StripCodeFences removes markdown code fences (```json, ```markdown, ```yaml, ```)
// from text and trims surrounding whitespace.
func StripCodeFences(s string) string {
	s = strings.TrimSpace(s)

	// Remove leading ```json or ```markdown or ```yaml or ``` fence.
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```markdown") {
		s = strings.TrimPrefix(s, "```markdown")
	} else if strings.HasPrefix(s, "```yaml") {
		s = strings.TrimPrefix(s, "```yaml")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}

	// Remove trailing ``` fence.
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}

	return strings.TrimSpace(s)
}
