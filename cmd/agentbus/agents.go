package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/termhost"
)

const (
	agentIdleAfter  = 2 * time.Minute
	agentStaleAfter = 10 * time.Minute
)

// humanAge renders a duration as a compact "Ns/Nm/Nh ago".
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// agentPane returns the agent's registered terminal id and whether the agent
// is known with a non-empty one.
func agentPane(m map[string]bus.AgentSnapshot, agent string) (string, bool) {
	s, ok := m[agent]
	if !ok || s.Pane == "" {
		return "", false
	}
	return s.Pane, true
}

// agentsTable renders the current state of every agent, one aged line each,
// sorted by name. Entries older than agentIdleAfter/agentStaleAfter are marked
// idle/offline (never deleted — age is the staleness signal).
// selectAgents is m narrowed to names (agent-brief: an agent reads only the
// peers it asks about), and the names m does not hold; no names = all of m.
func selectAgents(m map[string]bus.AgentSnapshot, names []string) (map[string]bus.AgentSnapshot, []string) {
	if len(names) == 0 {
		return m, nil
	}
	out := map[string]bus.AgentSnapshot{}
	var missing []string
	for _, n := range names {
		if s, ok := m[n]; ok {
			out[n] = s
		} else {
			missing = append(missing, n)
		}
	}
	return out, missing
}

// listedAgents is what `agentbus agents` shows: selectAgents, and with no
// names, every entry but the person's (identity.as), which is never an agent
// whatever terminal it names (person-never-agent). Named, it is shown: the
// operator inspects it before agents forget. all is never changed.
func listedAgents(all map[string]bus.AgentSnapshot, names []string, person string) (map[string]bus.AgentSnapshot, []string) {
	if len(names) > 0 || person == "" {
		return selectAgents(all, names)
	}
	if _, ok := all[person]; !ok {
		return all, nil
	}
	out := make(map[string]bus.AgentSnapshot, len(all)-1)
	for n, s := range all {
		if n != person {
			out[n] = s
		}
	}
	return out, nil
}

// liveTerminals are the agents of project with a live terminal on the holder
// list (item 22).
func liveTerminals(list []termhost.Term, project string) map[string]bool {
	out := map[string]bool{}
	for _, t := range list {
		if t.Project == project && t.Alive {
			out[t.Name] = true
		}
	}
	return out
}

// agentPresence is an agent's presence and, when lost, why
// (cleaning-up-agents §3, §10).
type agentPresence struct{ State, Reason string }

// presences judges each agent of m (bus.Presence): live is the holder's
// agents with a live terminal (nil: the holder was not read), owes what
// each owes (bus.ReadOwes), man the roles file (nil: not read, so whether a
// role is required is unknown: fail closed). No park record exists yet.
func presences(m map[string]bus.AgentSnapshot, live map[string]bool, owes map[string]bus.Owes, man *rolecfg.Manifest, selfRepair bool) map[string]agentPresence {
	out := make(map[string]agentPresence, len(m))
	for n, s := range m {
		o, ok := owes[n]
		if !ok {
			o.Unknown = append(o.Unknown, "owes")
		}
		in := bus.PresenceIn{Pane: s.Pane, TermsRead: live != nil, Live: live[n], SelfRepair: selfRepair, Owes: o}
		if man == nil { // first: whether it is required is judged before what it owes (reviewer-codex)
			in.Owes.Unknown = append([]string{"roles"}, o.Unknown...)
		} else {
			in.Required = man.RoleRequired(n)
		}
		st, why := bus.Presence(in)
		out[n] = agentPresence{st, why}
	}
	return out
}

// readPresences judges each agent of m as agentbus agents shows it: the
// holder's live terminals, what each owes, the roles file (nil when it cannot
// be read: required unknown, fail closed, cleaning-up-agents §3.2) and
// dispatch.self_repair. agentbus summary's Team reads it too.
func readPresences(ctx context.Context, b *bus.Bus, project string, m map[string]bus.AgentSnapshot) map[string]agentPresence {
	var man *rolecfg.Manifest
	if path, err := manifestPath(os.Environ()); err == nil && path != "" {
		man, _ = rolecfg.Load(path)
	}
	return presences(m, holderLive(ctx, project), b.ReadOwes(ctx, m), man, selfRepairOn(os.Getenv))
}

// agentJSON is agents --json's row: the snapshot, plus its presence
// (additive, cleaning-up-agents §10).
type agentJSON struct {
	bus.AgentSnapshot
	Presence       string `json:"presence"`
	PresenceReason string `json:"presence_reason,omitempty"`
}

func agentsJSON(m map[string]bus.AgentSnapshot, pres map[string]agentPresence) map[string]agentJSON {
	out := make(map[string]agentJSON, len(m))
	for n, s := range m {
		out[n] = agentJSON{s, pres[n].State, pres[n].Reason}
	}
	return out
}

// agentsTable prints the agents with their presence (presences): a running
// agent its declared state, any other its presence word and, when lost, its
// reason; never its last state, never 'no terminal'.
func agentsTable(m map[string]bus.AgentSnapshot, now time.Time, pres map[string]agentPresence) string {
	if len(m) == 0 { // stranger-test: say so rather than print nothing
		return "no agent has declared a status yet (each appears with its first agentbus status)\n"
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		s := m[n]
		age := now.Sub(time.UnixMilli(s.TS))
		when := humanAge(age)
		marker := ""
		switch {
		case s.TS == 0: // bound (agent-launch, bind) but no status declared yet
			when = "never"
		case age > agentStaleAfter:
			marker = "  · bus activity stale"
		case age > agentIdleAfter:
			marker = "  · bus activity quiet"
		}
		msg := ""
		if s.Message != "" {
			msg = "  (" + s.Message + ")"
		}
		pane := ""
		if s.Pane != "" {
			pane = "  ⧉" + s.Pane
		}
		p, ok := pres[n]
		if !ok {
			p.State = bus.PresenceRunning
		}
		state := bus.Shown(s.State, p.State)
		if p.Reason != "" {
			marker += "  · " + p.Reason
		}
		fmt.Fprintf(&sb, "%-12s %-8s %-9s%s%s%s\n", n, state, when, marker, pane, msg)
	}
	return sb.String()
}

// forgetDeps is what `agents forget` reads and writes, injected for tests.
type forgetDeps struct {
	Agents func(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Armed  func(ctx context.Context, agent string) (bool, error)
	Forget func(ctx context.Context, agent string) (bool, error)
	Now    func() time.Time
}

// runAgentsForget retires agents that are gone (a replaced team, a renamed
// role): their snapshot, usage, wake record, armed/gate/receiver keys and cmd
// group. It refuses an agent that is listening or declared a status in the
// last agentStaleAfter, unless --force: forgetting a live agent only makes it
// reappear at its next status, without its consumer group.
func runAgentsForget(ctx context.Context, d forgetDeps, args []string, out, errw io.Writer) int {
	args, force := extractBool(args, "--force")
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: agents forget <agent>... [--force]")
		return 1
	}
	for _, a := range args {
		if !bus.ValidName(a) {
			fmt.Fprintf(errw, "agents forget: invalid agent name %q\n", a)
			return 1
		}
	}
	snaps, err := d.Agents(ctx)
	if err != nil {
		fmt.Fprintf(errw, "agents forget: %v\n", err)
		return 1
	}
	code := 0
	for _, a := range args {
		if !force {
			if armed, err := d.Armed(ctx, a); err != nil || armed {
				fmt.Fprintf(errw, "%s: still listening on the bus, not forgotten (--force to override)\n", a)
				code = 1
				continue
			}
			if s, ok := snaps[a]; ok && s.TS != 0 && d.Now().Sub(time.UnixMilli(s.TS)) < agentStaleAfter {
				fmt.Fprintf(errw, "%s: declared a status %s, not forgotten (--force to override)\n", a, humanAge(d.Now().Sub(time.UnixMilli(s.TS))))
				code = 1
				continue
			}
		}
		found, err := d.Forget(ctx, a)
		switch {
		case err != nil:
			fmt.Fprintf(errw, "%s: %v\n", a, err)
			code = 1
		case found:
			fmt.Fprintf(out, "%s: forgotten\n", a)
		default:
			fmt.Fprintf(out, "%s: nothing to forget\n", a)
		}
	}
	return code
}

// runAgentsWait (A10) waits for agent's first verified status at or after
// --since (Unix ms): a declared state from a bound process. It reads once a
// second until --timeout (default 30s) and returns 0 once it has one, 1 if
// none came (its client may be waiting on a dialog in its pane), 2 on usage.
// Read-only: a launcher uses it to say which terminal to look at.
func runAgentsWait(ctx context.Context, read func(context.Context) (map[string]bus.AgentSnapshot, error),
	sleep func(time.Duration), args []string, out, errw io.Writer) int {
	args, sinceS := extractFlag(args, "--since")
	args, timeoutS := extractFlag(args, "--timeout")
	if len(args) != 1 || !bus.ValidName(args[0]) {
		fmt.Fprintln(errw, "usage: agents wait <agent> [--since <unix-ms>] [--timeout <dur>]")
		return 2
	}
	agent := args[0]
	since, err := strconv.ParseInt(sinceS, 10, 64)
	if sinceS != "" && err != nil {
		fmt.Fprintln(errw, "agents wait: --since takes Unix milliseconds")
		return 2
	}
	timeout := 30 * time.Second
	if timeoutS != "" {
		if timeout, err = time.ParseDuration(timeoutS); err != nil || timeout <= 0 {
			fmt.Fprintln(errw, "agents wait: --timeout takes a positive duration (30s)")
			return 2
		}
	}
	for i, n := 0, max(1, int(timeout/time.Second)); i < n; i++ {
		if snaps, err := read(ctx); err == nil {
			if s, ok := snaps[agent]; ok && s.PID > 0 && s.State != "" && s.TS >= since {
				fmt.Fprintf(out, "%s: %s\n", agent, s.State)
				return 0
			}
		}
		if i < n-1 {
			sleep(time.Second)
		}
	}
	fmt.Fprintf(errw, "agents wait: no status from %s within %s\n", agent, timeout)
	return 1
}

// holderLive reads the project's holder for liveTerminals; nil when it
// cannot be read (another host, or down): the declared states show.
func holderLive(ctx context.Context, project string) map[string]bool {
	h, err := holder.NewHost(project, holder.BuildID(), os.Getenv)
	if err != nil {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return liveFrom(c, h, project)
}

// termLister is the part of a holder liveFrom reads.
type termLister interface {
	List(ctx context.Context, project string) ([]termhost.Term, error)
	Describe(ctx context.Context) (termhost.HostInfo, error)
}

// liveFrom is liveTerminals of h's list; nil unless a holder of the project
// answers (an empty list from no holder proves nothing) and the list is read.
func liveFrom(ctx context.Context, h termLister, project string) map[string]bool {
	if info, err := h.Describe(ctx); err != nil || len(info.Builds) == 0 {
		return nil
	}
	list, err := h.List(ctx, project)
	if err != nil {
		return nil
	}
	return liveTerminals(list, project)
}

// selfRepairOn: dispatch.self_repair is on (shadow and off relaunch nothing).
func selfRepairOn(getenv func(string) string) bool {
	return config.SelfRepairMode(config.Machine(getenv)) == "on"
}
