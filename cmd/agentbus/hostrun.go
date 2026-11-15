package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
)

// The relay's operations (herdr exit, spec 2026-11-10-herdr-exit-design.md
// §4.2): the dispatcher runs an accepted host directive with the very
// command a shell would run, in its own process (outside every agent's
// terminal, so the holder accepts it), with the directive's sender as the
// caller: each command keeps its own authorisation, unchanged. The closed
// set carries no flags beyond a profile and --ignore-background, so a
// relayed command runs with its defaults. --ignore-background, which the
// set carries, is refused (security S10a-3).

// hostCmd runs one command as self with its words; its exit code as the CLI's.
type hostCmd func(ctx context.Context, self string, args []string, out, errw io.Writer) int

// hostRunner is the dispatcher's HostOps.Run: Cmds by op.
type hostRunner struct {
	Project string
	Cmds    map[string]hostCmd
}

// ownTerminal: the ops whose command refuses its target's own terminal.
var ownTerminal = map[string]bool{bus.HostReload: true, bus.HostSwap: true, bus.HostRetire: true}

// Run runs o and returns its outcome: done (exit 0), refused (exit 2, or a
// refusal before any command), failed otherwise; the text is the command's
// last line.
func (h hostRunner) Run(ctx context.Context, _ string, o bus.HostOp) (string, string) {
	if err := o.Validate(); err != nil {
		return bus.HostRefused, err.Error()
	}
	switch {
	case o.Op == bus.HostRelaunch:
		return bus.HostRefused, "relaunch: no such command since the herdr exit: reload <role> relaunches an agent"
	case ownTerminal[o.Op] && o.Agent == o.From:
		return bus.HostRefused, o.Op + " " + o.Agent + ": an agent cannot " + o.Op + " itself from its own terminal: master or a person runs it"
	case o.IgnoreBackground: // security S10a-3: the person's only, and the sender is self-attested
		return bus.HostRefused, o.Op + ": --ignore-background is the person's, from their own shell: the relay never honours it"
	}
	run, ok := h.Cmds[o.Op]
	if !ok {
		return bus.HostRefused, o.Op + ": not run by this dispatcher"
	}
	var out, errw bytes.Buffer
	code := run(ctx, o.From, h.args(o), &out, &errw)
	switch code {
	case 0:
		return bus.HostDone, outcomeLine(out.String(), o.Op)
	case 2:
		return bus.HostRefused, outcomeLine(errw.String(), o.Op)
	}
	return bus.HostFailed, outcomeLine(errw.String(), o.Op)
}

// args are the command's words for o.
func (h hostRunner) args(o bus.HostOp) []string {
	var a []string
	switch o.Op {
	case bus.HostUp, bus.HostDrain:
		a = []string{h.Project}
	case bus.HostPop, bus.HostReload, bus.HostSwap, bus.HostRetire:
		a = []string{o.Agent}
	}
	switch {
	case o.Profile != "":
		a = append(a, "--profile", o.Profile)
	case o.Op == bus.HostSwap:
		a = append(a, "--back")
	}
	if o.Forget {
		a = append(a, "--forget", "--episode", strconv.FormatInt(o.Episode, 10))
	}
	if o.Reason != "" {
		a = append(a, "--reason", o.Reason)
	}
	return a
}

// outcomeLine is s's last non-blank line, or def.
func outcomeLine(s, def string) string {
	lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return def
}

// getenvAs is the dispatcher's environment seen by a relayed command: the
// sender is the caller (AGENT_BUS_AGENT), so a person's command (suspend,
// a suspended project's resume, holder drain) refuses it as it refuses any
// agent; and no terminal of the dispatcher's own.
func getenvAs(self string, getenv func(string) string) func(string) string {
	return func(k string) string {
		switch k {
		case "AGENT_BUS_AGENT":
			return self
		case "AGENTBUS_TERM_ID":
			return ""
		}
		return getenv(k)
	}
}

// realHostCmds are the commands the relay runs, built as main builds them,
// each writing to the runner's buffers and reading no terminal.
func realHostCmds(b *bus.Bus, project, host string) map[string]hostCmd {
	return map[string]hostCmd{
		bus.HostUp: func(_ context.Context, _ string, args []string, out, errw io.Writer) int {
			return runUp(args, realUpDepsIn(strings.NewReader(""), out, errw), out, errw)
		},
		bus.HostPop: func(ctx context.Context, _ string, args []string, out, errw io.Writer) int {
			h, err := holder.NewHost(project, holder.BuildID(), os.Getenv)
			if err != nil {
				fmt.Fprintf(errw, "pop: %v\n", err)
				return 1
			}
			return runPop(ctx, h, project, args, out, errw)
		},
		bus.HostReload: func(ctx context.Context, self string, args []string, out, errw io.Writer) int {
			return runReload(ctx, b, realReloadDeps(os.Environ(), project, out, errw), self, args, out, errw)
		},
		bus.HostSwap: func(ctx context.Context, self string, args []string, out, errw io.Writer) int {
			return runSwap(ctx, b, realSwapDeps(os.Environ(), project), self, args, out, errw)
		},
		bus.HostRetire: func(ctx context.Context, self string, args []string, out, errw io.Writer) int {
			return runRetire(ctx, b, realSwapDeps(os.Environ(), project), self, args, out, errw)
		},
		bus.HostSuspend: func(ctx context.Context, self string, args []string, out, errw io.Writer) int {
			d := realSuspendDeps(host)
			d.Getenv, d.TTY, d.Stdin = getenvAs(self, os.Getenv), false, strings.NewReader("")
			return runSuspend(ctx, args, project, d, out, errw)
		},
		bus.HostResume: func(ctx context.Context, self string, args []string, out, errw io.Writer) int {
			full := realResumeDeps(host)
			full.Getenv, full.TTY, full.Stdin = getenvAs(self, os.Getenv), false, strings.NewReader("")
			d := pauseDeps{Set: b.SetPause, Get: b.Pause, Resume: b.Resume, Budgets: b.Budgets, Now: time.Now, Env: getenvAs(self, os.Getenv),
				Full: func(ctx context.Context, args []string) int {
					return runSuspendedResume(ctx, project, parseResumeArgs(args), full, out, errw)
				}}
			return runResume(ctx, d, self, args, out, errw)
		},
		bus.HostDrain: func(_ context.Context, self string, args []string, out, errw io.Writer) int {
			return runHolderDrain(project, host, args, getenvAs(self, os.Getenv), out, errw)
		},
	}
}
