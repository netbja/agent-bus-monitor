package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

type fakeHandoffs struct {
	declared []string
	err      error
	records  map[string]bus.Handoff
	snaps    map[string]bus.AgentSnapshot
}

func (f *fakeHandoffs) deps(self string) handoffDeps {
	return handoffDeps{
		Self: self,
		Ident: func(context.Context) bus.AgentIdent {
			return bus.AgentIdent{PID: 42, PIDStart: 7, Session: "session-0001"}
		},
		Declare: func(_ context.Context, agent, report string, who bus.AgentIdent) (bus.Handoff, error) {
			f.declared = append(f.declared, agent+" "+report+" "+who.Session)
			if f.err != nil {
				return bus.Handoff{}, f.err
			}
			return bus.Handoff{Report: report, Session: who.Session, PID: who.PID, PIDStart: who.PIDStart, At: 1000}, nil
		},
		List:   func(context.Context) (map[string]bus.Handoff, error) { return f.records, nil },
		Agents: func(context.Context) (map[string]bus.AgentSnapshot, error) { return f.snaps, nil },
		Now:    func() time.Time { return time.UnixMilli(61000).In(time.FixedZone("CET", 3600)) },
	}
}

func runHandoffT(f *fakeHandoffs, self string, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := runHandoff(context.Background(), f.deps(self), args, &out, &errw)
	return code, out.String(), errw.String()
}

func TestHandoffDeclaresTheCallersOwn(t *testing.T) {
	f := &fakeHandoffs{}
	code, out, errOut := runHandoffT(f, "coder", "coder", "--report", "1793-0")
	if code != 0 || strings.Join(f.declared, ",") != "coder 1793-0 session-0001" || !strings.Contains(out, "1793-0") {
		t.Fatalf("declare: %d %q %q %v", code, out, errOut, f.declared)
	}
	// Only an agent declares its own hand-off.
	f.declared = nil
	if code, _, errOut := runHandoffT(f, "master", "coder", "--report", "1793-0"); code == 0 || len(f.declared) != 0 || !strings.Contains(errOut, "own") {
		t.Fatalf("another's: %d %q %v", code, errOut, f.declared)
	}
	if code, _, errOut := runHandoffT(f, "coder", "coder"); code != 2 || !strings.Contains(errOut, "--report") {
		t.Fatalf("no report: %d %q", code, errOut)
	}
	f.err = bus.ErrHandoffNotVerified
	if code, _, errOut := runHandoffT(f, "coder", "coder", "--report", "1793-0"); code == 0 || !strings.Contains(errOut, "verified") {
		t.Fatalf("refused: %d %q", code, errOut)
	}
}

func TestHandoffListsPendingAndVoidRecords(t *testing.T) {
	f := &fakeHandoffs{
		records: map[string]bus.Handoff{
			"coder":    {Report: "1793-0", Session: "session-0001", PID: 42, PIDStart: 7, At: 1001},
			"foureyes": {Report: "1794-0", Session: "session-0002", PID: 50, PIDStart: 9, At: 1000},
		},
		snaps: map[string]bus.AgentSnapshot{
			"coder":    {State: "idle", TS: 2000, PID: 42, PIDStart: 7, IdentSession: "session-0001"},
			"foureyes": {State: "idle", TS: 2000, PID: 50, PIDStart: 9, IdentSession: "session-0003"},
		},
	}
	code, out, _ := runHandoffT(f, "master")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 2 ||
		!strings.HasPrefix(lines[0], "coder") || !strings.Contains(lines[0], "1793-0") || !strings.Contains(lines[0], "pending") || !strings.Contains(lines[0], "1970-01-01T01:00:01.001+01:00 (1m ago)") ||
		!strings.HasPrefix(lines[1], "foureyes") || !strings.Contains(lines[1], "void: another session") {
		t.Fatalf("list %d:\n%s", code, out)
	}
	f.records = nil
	if code, out, _ := runHandoffT(f, "master"); code != 0 || !strings.Contains(out, "no hand-off") {
		t.Fatalf("empty: %d %q", code, out)
	}
}

func TestHandoffPassesTheIdentityGuardOnlyToList(t *testing.T) {
	env := map[string]string{"HERDR_PANE_ID": "w1:p2"}
	get := func(k string) string { return env[k] }
	if err := identityGuard(get, "handoff", nil); err != nil {
		t.Fatalf("list refused: %v", err)
	}
	if err := identityGuard(get, "handoff", []string{"coder", "--report", "1-0"}); err == nil {
		t.Fatal("a declaration from a pane without AGENT_BUS_AGENT passed the guard")
	}
}
