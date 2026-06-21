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
