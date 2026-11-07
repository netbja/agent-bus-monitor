package dispatch

import (
	"context"
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
)

// Client updates (spec 2026-10-21-client-reload-design.md): the observe pass
// tells whether the client an agent runs was updated on disk since it
// started, from file metadata only, and sentinel gets one directive per
// update episode. Nothing here runs a client, types, restarts or reads a
// credential.

// probeEvery bounds the probe by default: one look per agent and minute.
const probeEvery = time.Minute

type probeResult struct {
	at     time.Time
	pid    int
	update *bus.ClientUpdate
	known  bool
}

// probeClient is the throttled probe for one agent.
func (l *loop) probeClient(agent string, s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
	if l.probes == nil {
		l.probes = map[string]probeResult{}
	}
	every := l.cfg.ClientProbeEvery
	if every <= 0 {
		every = probeEvery
	}
	if p, ok := l.probes[agent]; ok && p.pid == s.PID && time.Since(p.at) < every {
		return copyUpdate(p.update), p.known
	}
	probe := l.cfg.ClientProbe
	if probe == nil {
		probe = realClientProbe
	}
	u, known := probe(s)
	l.probes[agent] = probeResult{at: time.Now(), pid: s.PID, update: copyUpdate(u), known: known}
	return u, known
}

func copyUpdate(u *bus.ClientUpdate) *bus.ClientUpdate {
	if u == nil {
		return nil
	}
	c := *u
	return &c
}

// realClientProbe: the launch fingerprint when the agent has one (any
// client), else the Claude Code layout of its running executable (Linux).
func realClientProbe(s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
	if fp, err := clientfp.Decode(s.ClientFP); err == nil {
		changed, known := fp.Changed()
		if !known || !changed {
			return nil, known
		}
		now, err := clientfp.Take(fp.Path)
		if err != nil {
			return nil, false
		}
		return &bus.ClientUpdate{Client: s.Client, Running: fp.Label, Installed: now.Label, Hash: now.Hash(), Source: "fingerprint"}, true
	}
	if runtime.GOOS != "linux" || s.PID <= 0 || s.Client != "" && s.Client != "claude-code" {
		return nil, false
	}
	exe, err := os.Readlink("/proc/" + strconv.Itoa(s.PID) + "/exe")
	if err != nil {
		return nil, false
	}
	running, installed, outdated, ok := clientfp.ClaudeLayout(exe)
	if !ok {
		return nil, false
	}
	if !outdated {
		return nil, true
	}
	return &bus.ClientUpdate{Client: "claude-code", Running: running, Installed: installed, Hash: "layout-" + installed, Source: "layout"}, true
}

// updateEpisode is what one update of a client is: its installed version's
// label, the same whichever source judged the agent (a launch fingerprint,
// the Claude Code layout, another resolved path); the hash only when no
// label is known.
func updateEpisode(u *bus.ClientUpdate) string {
	if u.Installed != "" {
		return "v" + u.Installed
	}
	return u.Hash
}

// proposeReloads sends one directive per update episode, with its once-mark
// in the same fenced script, to sentinel when it is registered, else to the
// escalation target. The mark is per episode (update:<client>:<episode>,
// value "sent"), so two episodes never overwrite each other's mark: a
// restarted dispatcher, another pass, another agent or another source on
// the same update sends nothing. The text follows the reload policy, read at
// each send (auto-reload spec): under on with sentinel registered, sentinel
// reloads the ready agents itself, and an agent that was not ready when the
// directive went out gets one hint once it is (retryReloads).
func (l *loop) proposeReloads(ctx context.Context, snaps map[string]bus.AgentSnapshot, obs map[string]Observation) error {
	byEpisode := map[[2]string][]string{}
	members := map[[2]string][]string{}
	labels := map[[2]string]*bus.ClientUpdate{}
	for a, o := range obs {
		if o.Update == nil {
			continue
		}
		k := [2]string{o.Update.Client, updateEpisode(o.Update)}
		byEpisode[k] = append(byEpisode[k], a+" ("+orUnknown(o.Update.Running)+")") // each agent's own version
		members[k] = append(members[k], a)
		labels[k] = o.Update
	}
	if len(byEpisode) == 0 {
		return nil
	}
	target := l.cfg.EscalateTo
	_, sentinel := snaps["sentinel"]
	if sentinel {
		target = "sentinel"
	}
	policy := "on"
	if l.cfg.ReloadPolicy != nil {
		policy = l.cfg.ReloadPolicy()
	}
	auto := policy == "on" && sentinel
	var rf *reloadReader
	if auto {
		rf = &reloadReader{l: l, snaps: snaps, obs: obs}
	}
	for _, k := range slices.SortedFunc(maps.Keys(byEpisode), func(a, b [2]string) int {
		return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1])
	}) {
		agents := byEpisode[k]
		slices.Sort(agents)
		slices.Sort(members[k])
		u := labels[k]
		client := k[0]
		if client == "" {
			client = "the client"
		}
		mark := "update:" + k[0] + ":" + k[1]
		head := fmt.Sprintf("client update: %s %s installed; still on an older one: %s.", client, orUnknown(u.Installed), strings.Join(agents, ", "))
		var extra []string
		if auto { // the episode is automatic; the agents this directive covers get no later hint
			extra = append(extra, autoMark(mark))
			for _, a := range members[k] {
				if a != "sentinel" && rf.ready(ctx, a) {
					extra = append(extra, mark+":"+a)
				}
			}
		}
		sent, err := l.b.FencedCmdOnceMarks(ctx, l.token, mark, "sent", extra, dispatchFrom, target, bus.CmdDirective,
			truncRunes(head+" "+reloadAction(policy, sentinel), bus.CmdMaxRunes), false)
		if err != nil {
			return err
		}
		if auto && !sent {
			if err := l.retryReloads(ctx, rf, mark, client, members[k], obs); err != nil {
				return err
			}
		}
	}
	return nil
}

// autoMark records, with the episode's send, that it went out under on with
// sentinel registered: only such an episode gets retry hints, so a policy
// changed after the send applies to the next episode (reviewer-codex). The
// suffix is no agent name (ValidName has no '='), so no agent's mark is it.
func autoMark(mark string) string { return mark + ":auto=on" }

// reloadAction is what the episode's directive asks for, by policy and
// recipient. Without a registered sentinel the escalation target may reload
// nobody but sentinel (agentbus reload's caller table), so on asks the
// person through it.
func reloadAction(policy string, sentinel bool) string {
	switch {
	case policy == "propose":
		return "Propose a reload (agent-bus-sentinel skill, Client update): propose only, never restart or type."
	case policy == "off" && sentinel:
		return "Reload nothing (the reload policy is off); report the agents to master once."
	case policy == "off":
		return "Reload nothing (the reload policy is off)."
	case sentinel:
		return "Reload the ready ones now (agent-bus-sentinel skill, Client update): agentbus reload <agent>, one at a time, master last; never type into a pane."
	}
	return "No sentinel registered, so nothing reloads by itself: ask the person to run agentbus reload <agent> --as <name> for each idle agent, one at a time; never type into a pane."
}

// retryReloads sends sentinel one hint per agent of an episode already sent
// automatically (autoMark) that is now ready and was not covered when the
// directive went out (no per-agent mark): its once-mark goes with it. The
// policy must still be on: a change to propose or off stops the hints. sentinel never gets one
// about itself: master reloads it.
func (l *loop) retryReloads(ctx context.Context, rf *reloadReader, mark, client string, agents []string, obs map[string]Observation) error {
	var marks map[string]string
	for _, a := range agents {
		if a == "sentinel" {
			continue
		}
		if marks == nil {
			var err error
			if marks, err = l.b.QuotaMarks(ctx); err != nil {
				return err
			}
		}
		if marks[autoMark(mark)] != "sent" {
			return nil // the episode was sent under another policy or without sentinel
		}
		if marks[mark+":"+a] == "sent" || !rf.ready(ctx, a) {
			continue
		}
		u := obs[a].Update
		msg := fmt.Sprintf("client update: %s is ready now (%s %s installed, it runs %s). Reload it (agent-bus-sentinel skill, Client update): agentbus reload %s, master last; never type into a pane.",
			a, client, orUnknown(u.Installed), orUnknown(u.Running), a)
		if _, err := l.b.FencedCmdOnce(ctx, l.token, mark+":"+a, "sent", dispatchFrom, "sentinel", bus.CmdDirective, truncRunes(msg, bus.CmdMaxRunes), false); err != nil {
			return err
		}
	}
	return nil
}

// ReloadFacts is what the dispatcher can read of an agent's readiness for a
// reload, without running anything: agentbus reload checks it all again.
type ReloadFacts struct {
	State    string   // declared
	Busy     bool     // its pane observed running a turn
	Accepted bool     // owns a board task in state accepted
	Gates    []string // open challenges on either side
	Swapping bool     // a swap or reload intent holds it
	Fallback bool     // runs on its fallback profile
	Unknown  bool     // a read failed: not ready
}

// reloadReady: idle, not busy, nothing in flight that agentbus reload would
// refuse on. Unknown is not ready.
func reloadReady(f ReloadFacts) bool {
	return !f.Unknown && f.State == "idle" && !f.Busy && !f.Accepted && len(f.Gates) == 0 && !f.Swapping && !f.Fallback
}

// reloadReader reads ReloadFacts for one pass, the board and fallbacks once.
type reloadReader struct {
	l         *loop
	snaps     map[string]bus.AgentSnapshot
	obs       map[string]Observation
	read      bool
	board     map[string]bus.BoardEntry
	fallbacks map[string]bus.FallbackRecord
	err       error
}

func (r *reloadReader) ready(ctx context.Context, a string) bool {
	if !r.read {
		r.read = true
		if r.board, r.err = r.l.b.Board(ctx); r.err == nil {
			r.fallbacks, r.err = r.l.b.Fallbacks(ctx)
		}
	}
	f := ReloadFacts{State: r.snaps[a].State, Busy: r.obs[a].Busy, Unknown: r.err != nil}
	for _, e := range r.board {
		f.Accepted = f.Accepted || e.Owner == a && e.State == "accepted"
	}
	_, f.Fallback = r.fallbacks[a]
	var err error
	if f.Gates, err = r.l.b.ReviewsOf(ctx, a); err != nil {
		f.Unknown = true
	}
	if f.Swapping, err = r.l.b.Swapping(ctx, a); err != nil {
		f.Unknown = true
	}
	return reloadReady(f)
}

func orUnknown(s string) string {
	if s == "" {
		return "(version unknown)"
	}
	return s
}
