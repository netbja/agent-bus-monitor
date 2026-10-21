package main

import (
	"strings"
	"testing"
)

func TestReportShapeNote(t *testing.T) {
	flat := strings.Repeat("word ", 61)[:301] // 301 characters, no line break
	if got := reportShapeNote(flat); !strings.Contains(got, "busmon renders Markdown") {
		t.Fatalf("flat 301: %q", got)
	}
	for _, msg := range []string{
		strings.Repeat("a", 300),                                   // at the limit
		strings.Repeat("a", 200) + "\n" + strings.Repeat("b", 200), // has a line break
		"short",
	} {
		if got := reportShapeNote(msg); got != "" {
			t.Errorf("note for %d chars: %q", len(msg), got)
		}
	}
	// Characters, not bytes: 300 accented letters are not over the limit.
	if got := reportShapeNote(strings.Repeat("é", 300)); got != "" {
		t.Fatalf("300 runes noted: %q", got)
	}
}
