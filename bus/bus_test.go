package bus

import "testing"

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
