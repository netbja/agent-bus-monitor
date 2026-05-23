package bus

import (
	"strings"
	"testing"
)

func TestReportChannel(t *testing.T) {
	if got := ReportChannel("claude1"); got != "hermes:report:claude1" {
		t.Fatalf("ReportChannel = %q, want hermes:report:claude1", got)
	}
}

func TestParseReport(t *testing.T) {
	cases := []struct {
		name, channel, data         string
		agent, kind, state, message string
	}{
		{"note", "hermes:report:claude1", "note|bug fixed", "claude1", "report", "note", "bug fixed"},
		{"auto", "hermes:report:claude2", "auto|deploy done", "claude2", "report", "auto", "deploy done"},
		{"pipe in message", "hermes:report:claude1", "note|a|b", "claude1", "report", "note", "a|b"},
		{"no message", "hermes:report:claude1", "note", "claude1", "report", "note", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agent, kind, state, message := Parse(c.channel, c.data)
			if agent != c.agent || kind != c.kind || state != c.state || message != c.message {
				t.Fatalf("Parse(%q, %q) = (%q,%q,%q,%q), want (%q,%q,%q,%q)",
					c.channel, c.data, agent, kind, state, message,
					c.agent, c.kind, c.state, c.message)
			}
		})
	}
}

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

func TestReportPayloadRoundTrip(t *testing.T) {
	agent, kind, state, message := Parse(ReportChannel("claude1"), reportPayload(ReportNote, "bug\nX|fixed"))
	if agent != "claude1" || kind != "report" || state != "note" || message != "bug X|fixed" {
		t.Fatalf("round-trip = (%q,%q,%q,%q), want (claude1,report,note,bug X|fixed)",
			agent, kind, state, message)
	}
}
