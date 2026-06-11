package models

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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

	// Cap YAML front-matter size to prevent resource exhaustion.
	const maxFrontMatterBytes = 64 * 1024 // 64 KB
	if len(yamlBlock) > maxFrontMatterBytes {
		return nil, fmt.Errorf("YAML front-matter block exceeds %d bytes", maxFrontMatterBytes)
	}

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
