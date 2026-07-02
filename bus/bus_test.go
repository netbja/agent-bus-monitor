package bus

import (
	"strings"
	"testing"
)

func TestSanitizeReportMessage(t *testing.T) {
	if got := SanitizeReportMessage("line1\nline2\r\tend"); got != "line1 line2 end" {
		t.Fatalf("control chars: got %q, want %q", got, "line1 line2 end")
	}
	if got := SanitizeReportMessage("  spaced   out  "); got != "spaced out" {
		t.Fatalf("whitespace: got %q, want %q", got, "spaced out")
	}
	// default cap is 500 runes, then an ellipsis
	got := SanitizeReportMessage(strings.Repeat("x", 600))
	if r := []rune(got); len(r) != 501 || r[len(r)-1] != '…' {
		t.Fatalf("default truncation: got %d runes (last %q), want 501 + …", len(r), string(r[len(r)-1]))
	}
}

func TestReportMaxLenEnv(t *testing.T) {
	t.Setenv("AGENT_BUS_REPORT_MAX", "10")
	got := SanitizeReportMessage(strings.Repeat("y", 50))
	if r := []rune(got); len(r) != 11 || r[len(r)-1] != '…' {
		t.Fatalf("env cap: got %d runes, want 11 + …", len(r))
	}
}

func TestSanitizeReportFull(t *testing.T) {
	// keeps newlines and tabs, maps other control chars to space, no collapse
	got := SanitizeReportFull("a\nb\tc\x00d   e")
	if got != "a\nb\tc d   e" {
		t.Fatalf("SanitizeReportFull = %q, want %q", got, "a\nb\tc d   e")
	}
	// short input under the cap is returned unchanged (no truncation, no collapse)
	if got := SanitizeReportFull("short  text"); got != "short  text" {
		t.Fatalf("no-truncation path = %q, want %q", got, "short  text")
	}
	// truncates at the cap with an ellipsis
	t.Setenv("AGENT_BUS_REPORT_FULL_MAX", "5")
	if got := SanitizeReportFull("abcdefghij"); got != "abcde…" {
		t.Fatalf("truncated = %q, want %q", got, "abcde…")
	}
}
