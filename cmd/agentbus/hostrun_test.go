package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// call is one command the host runner ran: who, and with what words.
type call struct {
	self string
	args []string
}

// fakeHostCmds records each call and answers with code and text.
func fakeHostCmds(calls *[]call, code int, text string) map[string]hostCmd {
	m := map[string]hostCmd{}
	for _, op := range []string{bus.HostUp, bus.HostPop, bus.HostReload, bus.HostSwap, bus.HostSuspend, bus.HostResume, bus.HostRetire, bus.HostDrain} {
		m[op] = func(_ context.Context, self string, args []string, out, errw io.Writer) int {
			*calls = append(*calls, call{self, args})
			w := out
			if code != 0 {
				w = errw
			}
			fmt.Fprintf(w, "first line\n%s\n", text)
			return code
		}
	}
	return m
}

// Each op runs its command with the sender as the caller and the words that
// command takes: never more than the closed set carries.
func TestHostRunnerArgsPerOp(t *testing.T) {
	cases := []struct {
		op   bus.HostOp
		want []string
	}{
		{bus.HostOp{Op: bus.HostUp, From: "master", Resume: true}, []string{"demo", "--resume"}},
		{bus.HostOp{Op: bus.HostPop, Agent: "architect", From: "master"}, []string{"architect"}},
		{bus.HostOp{Op: bus.HostPop, Agent: "architect", Profile: "opus", From: "master"}, []string{"architect", "--profile", "opus"}},
		{bus.HostOp{Op: bus.HostReload, Agent: "coder", From: "sentinel"}, []string{"coder"}},
		{bus.HostOp{Op: bus.HostSwap, Agent: "coder", Profile: "kimi", From: "master"}, []string{"coder", "--profile", "kimi"}},
		{bus.HostOp{Op: bus.HostSwap, Agent: "coder", From: "master"}, []string{"coder", "--back"}},
		{bus.HostOp{Op: bus.HostSuspend, From: "master"}, nil},
		{bus.HostOp{Op: bus.HostResume, From: "master"}, nil},
		{bus.HostOp{Op: bus.HostRetire, Agent: "architect", From: "master"}, []string{"architect"}},
		{bus.HostOp{Op: bus.HostRetire, Agent: "security", From: "master", Forget: true, Episode: 1793647000000, Reason: "work done"},
			[]string{"security", "--forget", "--episode", "1793647000000", "--reason", "work done"}},
		{bus.HostOp{Op: bus.HostDrain, From: "master"}, []string{"demo"}},
	}
	for _, c := range cases {
		var calls []call
		h := hostRunner{Project: "demo", Cmds: fakeHostCmds(&calls, 0, "ok")}
		state, _ := h.Run(context.Background(), "tok", c.op)
		if state != bus.HostDone {
			t.Errorf("%+v: state %q, want done", c.op, state)
			continue
		}
		if len(calls) != 1 || calls[0].self != c.op.From || !slices.Equal(calls[0].args, c.want) {
			t.Errorf("%+v: ran %+v, want self %s args %q", c.op, calls, c.op.From, c.want)
		}
	}
}

// The outcome: 0 is done with the command's last line; 2 (its usage or its
// refusal before acting) is refused; any other code failed, with its last
// error line.
func TestHostRunnerOutcome(t *testing.T) {
	for _, c := range []struct {
		code  int
		state string
	}{{0, bus.HostDone}, {1, bus.HostFailed}, {2, bus.HostRefused}, {3, bus.HostFailed}} {
		var calls []call
		h := hostRunner{Project: "demo", Cmds: fakeHostCmds(&calls, c.code, "the last line")}
		state, text := h.Run(context.Background(), "tok", bus.HostOp{Op: bus.HostReload, Agent: "coder", From: "sentinel"})
		if state != c.state || text != "the last line" {
			t.Errorf("code %d: got %q %q, want %q %q", c.code, state, text, c.state, "the last line")
		}
	}
}

// What the relay cannot carry is refused before any command runs: an
// agent's own terminal is never closed from the relay (the command's
// own-pane check, which the dispatcher's environment cannot see),
// --ignore-background is never honoured (security S10a-3: the person's
// only, and the sender is self-attested), and relaunch
// is no command since the herdr exit.
func TestHostRunnerRefusesBeforeRunning(t *testing.T) {
	for _, o := range []bus.HostOp{
		{Op: bus.HostReload, Agent: "sentinel", From: "sentinel"},
		{Op: bus.HostSwap, Agent: "coder", Profile: "kimi", From: "coder"},
		{Op: bus.HostRetire, Agent: "coder", From: "coder"},
		{Op: bus.HostDrain, From: "coder", IgnoreBackground: true},
		{Op: bus.HostDrain, From: "master", IgnoreBackground: true},
		{Op: bus.HostRelaunch, Agent: "coder", From: "master"},
		{Op: "format-disk", From: "master"},
		// up bootstraps and starts a project: relayed, only master's up --resume
		// (its tool rules' form), never an agent's and never a bare up (reviewer-codex on 10b).
		{Op: bus.HostUp, From: "coder", Resume: true},
		{Op: bus.HostUp, From: "master"},
	} {
		var calls []call
		h := hostRunner{Project: "demo", Cmds: fakeHostCmds(&calls, 0, "ok")}
		state, text := h.Run(context.Background(), "tok", o)
		if state != bus.HostRefused || text == "" || len(calls) != 0 {
			t.Errorf("%+v: got %q %q after %d calls, want refused before any", o, state, text, len(calls))
		}
	}
}

// The real reload keeps its own table when relayed: sentinel's reload of
// itself and coder's of reviewer are refused by runReload, with its text.
func TestHostRunnerReloadKeepsItsTable(t *testing.T) {
	cmds := map[string]hostCmd{bus.HostReload: func(ctx context.Context, self string, args []string, out, errw io.Writer) int {
		return runReload(ctx, nil, reloadDeps{Env: func(string) string { return "" }}, self, args, out, errw)
	}}
	h := hostRunner{Project: "demo", Cmds: cmds}
	state, text := h.Run(context.Background(), "tok", bus.HostOp{Op: bus.HostReload, Agent: "reviewer", From: "coder"})
	if state != bus.HostFailed || !strings.Contains(text, "coder may not reload reviewer") {
		t.Fatalf("coder's reload reviewer: %q %q", state, text)
	}
}

// The person's commands, relayed, see the sender as their caller and refuse
// it before anything is read: suspend and holder drain.
func TestRealHostCmdsPersonsCommandsRefuseTheSender(t *testing.T) {
	t.Setenv("AGENT_BUS_AGENT", "")
	cmds := realHostCmds(nil, "demo", "")
	for op, want := range map[string]string{bus.HostSuspend: "person's act, not master's", bus.HostDrain: "AGENT_BUS_AGENT=\"master\""} {
		h := hostRunner{Project: "demo", Cmds: map[string]hostCmd{op: cmds[op]}}
		state, text := h.Run(context.Background(), "tok", bus.HostOp{Op: op, From: "master"})
		if state == bus.HostDone || !strings.Contains(text, want) {
			t.Errorf("%s from master: %q %q, want a refusal naming %q", op, state, text, want)
		}
	}
}
