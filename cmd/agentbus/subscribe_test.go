package main

import (
	"bytes"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestRearmSentinel(t *testing.T) {
	cases := []struct {
		name           string
		outcome        watchOutcome
		ref, from, msg string
		wantLine       string
		wantCode       int
	}{
		{"cmd", outcomeCmd, "C1", "hermes", "", "__AGENTBUS__ event=cmd rearm=1 ref=C1 from=hermes", 0},
		{"heartbeat", outcomeHeartbeat, "", "", "", "__AGENTBUS__ event=heartbeat rearm=1", 64},
		{"transient", outcomeTransient, "", "", "broker down", "__AGENTBUS__ event=error rearm=1 msg=broker down", 75},
		{"fatal", outcomeFatal, "", "", "invalid agent", "__AGENTBUS__ event=fatal rearm=0 msg=invalid agent", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, code := rearmSentinel(c.outcome, c.ref, c.from, c.msg)
			if line != c.wantLine {
				t.Errorf("line = %q, want %q", line, c.wantLine)
			}
			if code != c.wantCode {
				t.Errorf("code = %d, want %d", code, c.wantCode)
			}
		})
	}
}

func TestPrintCmd(t *testing.T) {
	var buf bytes.Buffer
	printCmd(&buf, bus.Event{Type: "directive", From: "hermes", Target: "dev", Message: "run it"})
	if got := buf.String(); got != "[directive hermes->dev] run it\n" {
		t.Fatalf("printCmd = %q", got)
	}
	buf.Reset()
	printCmd(&buf, bus.Event{Type: "challenge", From: "review", Target: "dev", Ref: "C1", Message: "justify"})
	if got := buf.String(); got != "[challenge review->dev ref=C1] justify\n" {
		t.Fatalf("printCmd with ref = %q", got)
	}
}
