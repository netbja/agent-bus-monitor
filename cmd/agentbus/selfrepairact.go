package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"github.com/netbja/agent-bus-monitor/selfrepair"
)

// The self-repair watcher's acts, on mode (spec
// docs/superpowers/specs/2026-11-09-self-repair-watcher-design.md 4.1, 4.2,
// 4.5 and 5; step 7b). The pass decided (selfrepair.Decide); this applies
// its decisions: the one relaunch it allows, a crash loop's mark, and the
// reports. Every write is fenced on the dispatch lease; every message to
// master is sent once per episode (FencedCmdOnce).

const proofEvery = time.Second

// act applies one pass's decisions, then sends what is owed to master and
// not written yet (replay). now is the pass's Redis time.
func (c *selfRepairChore) act(ctx context.Context, token string, ds []selfrepair.Decision, r selfRepairReads, deaths map[string]repairDeath, now time.Time) error {
	master := c.master()
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	acted := false
	for _, d := range ds {
		ep := deaths[d.Agent].episode
		switch {
		case d.Act && (d.Kind == selfrepair.KindRelaunch || d.Kind == selfrepair.KindCreate):
			acted = true
			keep(c.repair(ctx, token, d, r, ep, now.Sub(deaths[d.Agent].since)))
		case d.Kind == selfrepair.KindBudget:
			keep(c.crashLoop(ctx, token, d, master))
		case d.Kind == selfrepair.KindReport && ep != "":
			_, err := c.sender()(ctx, token, "selfrepair-report:"+d.Agent, ep, master,
				fmt.Sprintf("self-repair: %s not relaunched: %s. Master relaunches it, or asks the person.", d.Agent, d.Reason))
			keep(err)
		}
	}
	keep(c.replay(ctx, token, r, now, master, acted))
	return first
}

// crashLoop marks a spent day (fenced, once) and tells master, its episode
// the mark's own time: a message not written is sent at a later pass (the
// mark stays until reset), and only the send that writes it rings the
// desktop.
func (c *selfRepairChore) crashLoop(ctx context.Context, token string, d selfrepair.Decision, master string) error {
	if _, err := c.b.MarkCrashLoop(ctx, token, d.Agent); err != nil {
		return err
	}
	past, err := c.b.SelfRepairPast(ctx)
	if err != nil {
		return err
	}
	at := past[d.Agent].CrashLoopAt
	text := fmt.Sprintf("self-repair: %s %s. Nothing more is relaunched for it until master or the person runs agentbus self-repair reset %s.", d.Agent, d.Reason, d.Agent)
	wrote, err := c.sender()(ctx, token, "selfrepair-budget:"+d.Agent, strconv.FormatInt(at, 10), master, text)
	if wrote {
		c.ring(ctx, d.Agent+": self-repair budget spent", text)
	}
	return err
}

// replay sends master what a relaunch or a failure owes it, read from the
// ledger's newest self-repair end of each role that made an attempt in the
// last day: the send is once per death (FencedCmdOnce), so one that failed
// is written now, and one written is not repeated.
func (c *selfRepairChore) replay(ctx context.Context, token string, r selfRepairReads, now time.Time, master string, acted bool) error {
	recent := acted // this pass's own relaunch is told in this pass
	for _, p := range r.Past {
		for _, at := range p.Attempts {
			recent = recent || now.Sub(at) < 24*time.Hour
		}
	}
	if !recent {
		return nil
	}
	log, err := c.b.SwapLog(ctx, "", 0)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var first error
	for _, e := range log { // newest first
		if e.Kind != bus.SwapKindSelfRepair || (e.Event != bus.SwapRelaunched && e.Event != bus.SwapFailed) || seen[e.Role] {
			continue
		}
		seen[e.Role] = true
		if ms, _, ok := strings.Cut(e.ID, "-"); ok {
			if n, err := strconv.ParseInt(ms, 10, 64); err == nil && now.Sub(time.UnixMilli(n)) > 24*time.Hour {
				continue
			}
		}
		if err := c.announce(ctx, token, e, master); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// announce is a self-repair's end told to master, built from its ledger
// entry alone (so a replay says the same), once per death; a failure rings
// the desktop when this send wrote it.
func (c *selfRepairChore) announce(ctx context.Context, token string, e bus.SwapEvent, master string) error {
	ep := e.Episode
	if ep == "" {
		ep = e.ID
	}
	if e.Event == bus.SwapRelaunched {
		_, err := c.sender()(ctx, token, "selfrepair-done:"+e.Role, ep, master,
			fmt.Sprintf("self-repair: %s relaunched (pid %d -> %d, pane %s -> %s, %s).", e.Role, e.OldPID, e.NewPID, e.OldPane, e.NewPane, e.Reason))
		return err
	}
	text := fmt.Sprintf("self-repair: %s relaunch %s. No retry for this death: %s.", e.Role, e.Error, e.Reason)
	wrote, err := c.sender()(ctx, token, "selfrepair-failed:"+e.Role, ep, master, text)
	if wrote {
		c.ring(ctx, e.Role+": self-repair failed", text)
	}
	return err
}

// repair is R1 (R2 when its tab is recreated): the role's intent taken,
// fenced; the relaunch through the service entry, its fence read right
// before the split; the proof, which decides; the finish, which records
// what master is told (announce). A failure is final for that death:
// escalated, rung on the desktop, never retried (a failed episode reads
// failed next pass).
func (c *selfRepairChore) repair(ctx context.Context, token string, d selfrepair.Decision, r selfRepairReads, ep string, dead time.Duration) error {
	role, snap := d.Agent, r.Snaps[d.Agent]
	nonce := newNonce()
	begin := bus.SwapEvent{By: "dispatch", Reason: string(d.Kind) + ": " + d.Reason, OldPID: snap.PID, OldPane: snap.Pane, Episode: ep}
	if err := c.b.BeginSelfRepair(ctx, token, role, nonce, begin); err != nil {
		if errors.Is(err, bus.ErrSwapInProgress) {
			return nil // another relauncher holds the role: never next to it
		}
		return fmt.Errorf("self-repair %s: %w", role, err)
	}
	fence := func(string) error {
		held, err := c.b.SelfRepairHeld(ctx, token, role, nonce)
		switch {
		case err != nil:
			return err
		case !held:
			return errors.New("the self-repair is no longer its own (the dispatch lease or the role's intent changed)")
		}
		return nil
	}
	session := relaunch.ProvedSession(snap)
	err := c.relaunch(ctx, role, session, fence)
	// The proof decides, the relaunch's error or not: Role can fail after
	// the new agent came up (the old pane not closed), and an error before
	// any split simply finds no proof.
	proved, ok := c.prove(ctx, role, snap, r)
	var fin bus.SwapEvent
	if ok {
		resumed := "fresh"
		if session != "" {
			resumed = "resumed " + session[:min(8, len(session))]
		}
		fin = bus.SwapEvent{Role: role, Event: bus.SwapRelaunched, By: "dispatch", OldPID: snap.PID, OldPane: snap.Pane,
			NewPID: proved.PID, NewPane: proved.Pane, Episode: ep, Reason: resumed + ", it was dead " + dead.Round(time.Second).String()}
	} else {
		why := fmt.Sprintf("failed its proof: no new process of %s/%s in a new pane after %d s", r.Own[role].Client, r.Own[role].Provider, c.tries())
		if err != nil {
			why = "failed: " + err.Error() + "; no proof"
		}
		recovery := "agent-launch " + role + " " + c.project
		if session != "" {
			recovery += " --resume " + session
		}
		fin = bus.SwapEvent{Role: role, Event: bus.SwapFailed, By: "dispatch", OldPID: snap.PID, OldPane: snap.Pane, Episode: ep, Error: why,
			Reason: "recover with " + recovery + " in a new pane of its tab, or agentbus up " + c.project + " --resume"}
	}
	if err := c.b.FinishSelfRepair(ctx, token, nonce, fin); err != nil {
		return fmt.Errorf("self-repair %s: its finish not written: %w", role, err)
	}
	return nil // the replay of this same pass tells master (announce)
}

func (c *selfRepairChore) tries() int {
	if c.proofTries > 0 {
		return c.proofTries
	}
	return 60
}

// prove reads the snapshots until role runs a new process, alive (its pid
// and start time), in a new pane, on its own profile's client and provider
// (reload's replaced rule, spec 4.1 Proof); proofTries reads, a second apart.
func (c *selfRepairChore) prove(ctx context.Context, role string, old bus.AgentSnapshot, r selfRepairReads) (bus.AgentSnapshot, bool) {
	sleep := c.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	own := r.Own[role]
	for i := 0; i < c.tries(); i++ {
		if i > 0 {
			sleep(proofEvery)
		}
		if ctx.Err() != nil {
			return bus.AgentSnapshot{}, false
		}
		snaps, err := c.agents(ctx)
		if err != nil {
			continue
		}
		if s, ok := snaps[role]; ok && own.Client != "" && replaced(s, old, own, c.procStart) {
			return s, true
		}
	}
	return bus.AgentSnapshot{}, false
}

// sender is send, or FencedCmdOnce: one message to master per (mark,
// episode), fenced; a message to an agent being swapped waits for a later
// pass.
func (c *selfRepairChore) sender() func(ctx context.Context, token, mark, episode, target, text string) (bool, error) {
	if c.send != nil {
		return c.send
	}
	return func(ctx context.Context, token, mark, episode, target, text string) (bool, error) {
		if episode == "" {
			episode = "-"
		}
		wrote, err := c.b.FencedCmdOnce(ctx, token, mark, episode, "dispatch", target, bus.CmdDirective, text, false)
		if errors.Is(err, bus.ErrSwapInProgress) {
			return false, nil
		}
		return wrote, err
	}
}

// ring rings the desktop (a failed proof, a spent budget: spec 5), best
// effort.
func (c *selfRepairChore) ring(ctx context.Context, title, body string) {
	if c.notify != nil {
		_ = c.notify(ctx, "agentbus "+c.project+": "+title, body)
	}
}

func newNonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// serviceRelaunch is the service entry of a relaunch (spec 4.1): herdr on
// the session the services' env file names, up's project lock (never next
// to an up), the template's role tab, its fence read right before the
// split.
func serviceRelaunch(ctx context.Context, b *bus.Bus, getenv func(string) string, procStart func(int) (uint64, error),
	role, session string, fence func(string) error) error {
	herdr, err := relaunch.ServiceHerdr(getenv)
	if err != nil {
		return err
	}
	tpl, err := relaunch.ReadTemplate(getenv, b.Project())
	if err != nil {
		return err
	}
	d := relaunch.Deps{Herdr: herdr, ProcStart: procStart, Home: getenv("HOME"), Fence: fence,
		Agents: func(string) (map[string]bus.AgentSnapshot, error) { return b.Agents(ctx) }}
	return relaunch.ServiceRole(d, relaunch.ProjectLock(relaunch.CacheDir(getenv)), b.Project(), role, session, tpl, io.Discard)
}
