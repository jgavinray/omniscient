package models

import (
	"strings"
	"testing"
)

func TestParseExtractionOutput_Valid(t *testing.T) {
	raw := `---
date: "2026-02-21"
meeting_type: standup
participants:
  - Alice
  - Bob
---
## Summary

Quick standup covering project progress.`

	result, err := ParseExtractionOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FrontMatter["date"] != "2026-02-21" {
		t.Errorf("expected date %q, got %v", "2026-02-21", result.FrontMatter["date"])
	}
	if result.FrontMatter["meeting_type"] != "standup" {
		t.Errorf("expected meeting_type %q, got %v", "standup", result.FrontMatter["meeting_type"])
	}
	participants, ok := result.FrontMatter["participants"].([]any)
	if !ok {
		t.Fatalf("expected participants to be []any, got %T", result.FrontMatter["participants"])
	}
	if len(participants) != 2 {
		t.Errorf("expected 2 participants, got %d", len(participants))
	}
	if !strings.Contains(result.Markdown, "Quick standup") {
		t.Errorf("markdown body should contain summary text, got %q", result.Markdown)
	}
}

func TestParseExtractionOutput_MissingOpeningDelimiter(t *testing.T) {
	raw := `date: "2026-02-21"
---
## Summary`

	_, err := ParseExtractionOutput(raw)
	if err == nil {
		t.Fatal("expected error for missing opening delimiter")
	}
	if !strings.Contains(err.Error(), "missing opening ---") {
		t.Errorf("error = %q, want it to mention missing opening delimiter", err.Error())
	}
}

func TestParseExtractionOutput_MissingClosingDelimiter(t *testing.T) {
	raw := `---
date: "2026-02-21"
## Summary`

	_, err := ParseExtractionOutput(raw)
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
	if !strings.Contains(err.Error(), "missing closing ---") {
		t.Errorf("error = %q, want it to mention missing closing delimiter", err.Error())
	}
}

func TestParseExtractionOutput_InvalidYAML(t *testing.T) {
	raw := `---
invalid: yaml: [unbalanced
---
## Summary`

	_, err := ParseExtractionOutput(raw)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("error = %q, want it to mention invalid YAML", err.Error())
	}
}

func TestParseExtractionOutput_EmptyBody(t *testing.T) {
	raw := `---
date: "2026-02-21"
---`

	result, err := ParseExtractionOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Markdown != "" {
		t.Errorf("expected empty body, got %q", result.Markdown)
	}
	if result.FrontMatter["date"] != "2026-02-21" {
		t.Errorf("expected date in front-matter, got %v", result.FrontMatter["date"])
	}
}

func TestParseExtractionOutput_CodeFenceWrapped(t *testing.T) {
	raw := "```markdown\n---\ndate: \"2026-02-21\"\n---\n## Summary\n\nHello world.\n```"

	result, err := ParseExtractionOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FrontMatter["date"] != "2026-02-21" {
		t.Errorf("expected date %q, got %v", "2026-02-21", result.FrontMatter["date"])
	}
	if !strings.Contains(result.Markdown, "Hello world") {
		t.Errorf("markdown should contain body text, got %q", result.Markdown)
	}
}

func TestParseExtractionOutput_ComplexFrontMatter(t *testing.T) {
	raw := `---
date: "2026-02-21"
meeting_type: engineering
participants:
  - Alice
  - Bob
  - Charlie
decisions:
  - decision: "Use Go"
    owner: "Alice"
  - decision: "Add tests"
    owner: "Bob"
tags:
  - sprint-review
  - q1-planning
---
## Summary

Complex meeting with nested data.`

	result, err := ParseExtractionOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	participants, ok := result.FrontMatter["participants"].([]any)
	if !ok {
		t.Fatalf("expected participants to be []any, got %T", result.FrontMatter["participants"])
	}
	if len(participants) != 3 {
		t.Errorf("expected 3 participants, got %d", len(participants))
	}

	decisions, ok := result.FrontMatter["decisions"].([]any)
	if !ok {
		t.Fatalf("expected decisions to be []any, got %T", result.FrontMatter["decisions"])
	}
	if len(decisions) != 2 {
		t.Errorf("expected 2 decisions, got %d", len(decisions))
	}

	tags, ok := result.FrontMatter["tags"].([]any)
	if !ok {
		t.Fatalf("expected tags to be []any, got %T", result.FrontMatter["tags"])
	}
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(tags))
	}
}

func TestTranscriptKey(t *testing.T) {
	tr := &Transcript{ID: "abc123", Source: "googlemeet", Title: "Standup"}
	if got, want := tr.Key(), "googlemeet:abc123"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}
