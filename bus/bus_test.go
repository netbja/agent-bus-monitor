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
	got := SanitizeReportMessage(strings.Repeat("x", 200))
	r := []rune(got)
	if len(r) != maxReportLen+1 || r[len(r)-1] != '…' {
		t.Fatalf("truncation: got %d runes (last %q), want %d + …", len(r), string(r[len(r)-1]), maxReportLen)
	}
}
