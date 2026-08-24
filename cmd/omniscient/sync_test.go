package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPromptForSinks_All(t *testing.T) {
	var out bytes.Buffer
	choice, skip, err := promptForSinks(strings.NewReader("a\n"), &out, []string{"confluence", "slack", "local"})
	if err != nil {
		t.Fatalf("promptForSinks: %v", err)
	}
	if skip {
		t.Error("skip = true, want false")
	}
	want := []int{0, 1, 2}
	if len(choice) != 3 || choice[0] != 0 || choice[1] != 1 || choice[2] != 2 {
		t.Errorf("choice = %v, want %v", choice, want)
	}
}

func TestPromptForSinks_AllKeywordCaseInsensitive(t *testing.T) {
	var out bytes.Buffer
	choice, skip, err := promptForSinks(strings.NewReader("ALL\n"), &out, []string{"confluence", "local"})
	if err != nil {
		t.Fatalf("promptForSinks: %v", err)
	}
	if skip {
		t.Error("skip = true, want false")
	}
	if len(choice) != 2 || choice[0] != 0 || choice[1] != 1 {
		t.Errorf("choice = %v, want [0 1]", choice)
	}
}

func TestPromptForSinks_EmptyMeansAll(t *testing.T) {
	var out bytes.Buffer
	choice, skip, err := promptForSinks(strings.NewReader("\n"), &out, []string{"confluence", "local"})
	if err != nil {
		t.Fatalf("promptForSinks: %v", err)
	}
	if skip {
		t.Error("skip = true, want false")
	}
	if len(choice) != 2 {
		t.Errorf("choice = %v, want [0 1]", choice)
	}
}

func TestPromptForSinks_Skip(t *testing.T) {
	var out bytes.Buffer
	choice, skip, err := promptForSinks(strings.NewReader("n\n"), &out, []string{"confluence", "local"})
	if err != nil {
		t.Fatalf("promptForSinks: %v", err)
	}
	if !skip {
		t.Error("skip = false, want true")
	}
	if choice != nil {
		t.Errorf("choice = %v, want nil", choice)
	}
}

func TestPromptForSinks_SkipKeyword(t *testing.T) {
	var out bytes.Buffer
	_, skip, err := promptForSinks(strings.NewReader("skip\n"), &out, []string{"confluence", "local"})
	if err != nil {
		t.Fatalf("promptForSinks: %v", err)
	}
	if !skip {
		t.Error("skip = false, want true")
	}
}

func TestPromptForSinks_Indices(t *testing.T) {
	var out bytes.Buffer
	choice, skip, err := promptForSinks(strings.NewReader("1, 3\n"), &out, []string{"confluence", "slack", "local"})
	if err != nil {
		t.Fatalf("promptForSinks: %v", err)
	}
	if skip {
		t.Error("skip = true, want false")
	}
	if len(choice) != 2 || choice[0] != 0 || choice[1] != 2 {
		t.Errorf("choice = %v, want [0 2]", choice)
	}
}

func TestPromptForSinks_InvalidInput(t *testing.T) {
	cases := []string{
		"abc\n",   // not a recognized keyword or number
		"2,9\n",   // out of range
		",,\n",    // no valid selections
		"0\n",     // indices are 1-based
	}
	for _, in := range cases {
		var out bytes.Buffer
		_, _, err := promptForSinks(strings.NewReader(in), &out, []string{"confluence", "local"})
		if err == nil {
			t.Errorf("input %q: expected error, got nil", in)
		}
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	// Shorter than max: unchanged.
	if got := truncateRuneSafe("hello", 100); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	// Exact boundary: unchanged.
	if got := truncateRuneSafe("hello", 5); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	// Plain ASCII cut.
	if got := truncateRuneSafe("hello", 3); got != "hel" {
		t.Errorf("got %q, want %q", got, "hel")
	}
	// "héllo" is 6 bytes: h(1) é(2) l(1) l(1) o(1).
	// Cutting at byte 2 lands inside "é" → must back off to 1.
	if got := truncateRuneSafe("héllo", 2); got != "h" {
		t.Errorf("got %q, want %q", got, "h")
	}
	// Cutting at byte 4 is on a rune boundary (after "él") → "hél" (4 bytes).
	if got := truncateRuneSafe("héllo", 4); got != "hél" {
		t.Errorf("got %q, want %q", got, "hél")
	}
	// Cutting at byte 5 is on a rune boundary → "héll" (h + é + l + l = 5 bytes).
	if got := truncateRuneSafe("héllo", 5); got != "héll" {
		t.Errorf("got %q, want %q", got, "héll")
	}
	// Result must always be valid UTF-8 (αβγ are 2 bytes each).
	s := strings.Repeat("αβγ", 100)
	for _, max := range []int{0, 1, 2, 3, 4, 5, 7} {
		if got := truncateRuneSafe(s, max); !utf8.ValidString(got) {
			t.Errorf("max=%d: result not valid UTF-8: %q", max, got)
		}
	}
}
