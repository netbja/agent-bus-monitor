package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

// act applies one pass's decisions. now is the pass's Redis time.
func (c *selfRepairChore) act(ctx context.Context, token string, ds []selfrepair.Decision, r selfRepairReads, deaths map[string]repairDeath, now time.Time) error {
	master := c.master()
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, d := range ds {
		ep := deaths[d.Agent].episode
		switch {
		case d.Act && (d.Kind == selfrepair.KindRelaunch || d.Kind == selfrepair.KindCreate):
			keep(c.repair(ctx, token, d, r, ep, now.Sub(deaths[d.Agent].since), master))
		case d.Kind == selfrepair.KindBudget:
			fresh, err := c.b.MarkCrashLoop(ctx, token, d.Agent)
			if err != nil || !fresh { // marked before: escalated then
				keep(err)
				continue
			}
			text := fmt.Sprintf("self-repair: %s %s. Nothing more is relaunched for it until master or the person runs agentbus self-repair reset %s.", d.Agent, d.Reason, d.Agent)
			keep(c.tell(ctx, token, "selfrepair-budget:"+d.Agent, ep, master, text))
			c.ring(ctx, d.Agent+": self-repair budget spent", text)
		case d.Kind == selfrepair.KindReport && ep != "":
			keep(c.tell(ctx, token, "selfrepair-report:"+d.Agent, ep, master,
				fmt.Sprintf("self-repair: %s not relaunched: %s. Master relaunches it, or asks the person.", d.Agent, d.Reason)))
		}
	}
	return first
}

// repair is R1 (R2 when its tab is recreated): the role's intent taken,
// fenced; the relaunch through the service entry, its fence read right
// before the split; the proof; the finish; the report. A failure (an error
// or no proof) is final for that death: escalated to master and rung on
// the desktop, never retried (a failed episode reads failed next pass).
func (c *selfRepairChore) repair(ctx context.Context, token string, d selfrepair.Decision, r selfRepairReads, ep string, dead time.Duration, master string) error {
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
	if ok {
		fin := bus.SwapEvent{Role: role, Event: bus.SwapRelaunched, By: "dispatch", OldPID: snap.PID, OldPane: snap.Pane,
			NewPID: proved.PID, NewPane: proved.Pane, Episode: ep}
		if err := c.b.FinishSelfRepair(ctx, token, nonce, fin); err != nil {
			return fmt.Errorf("self-repair %s relaunched, its finish not written: %w", role, err)
		}
		resumed := "fresh"
		if session != "" {
			resumed = "resumed " + session[:min(8, len(session))]
		}
		return c.tell(ctx, token, "selfrepair-done:"+role, ep, master,
			fmt.Sprintf("self-repair: %s relaunched (pid %d -> %d, pane %s -> %s, %s), it was dead %s.",
				role, snap.PID, proved.PID, snap.Pane, proved.Pane, resumed, dead.Round(time.Second)))
	}
	why := fmt.Sprintf("failed its proof: no new process of %s/%s in a new pane after %d s", r.Own[role].Client, r.Own[role].Provider, c.tries())
	if err != nil {
		why = "failed: " + err.Error() + "; no proof"
	}
	fin := bus.SwapEvent{Role: role, Event: bus.SwapFailed, By: "dispatch", OldPID: snap.PID, OldPane: snap.Pane, Episode: ep, Error: why}
	if ferr := c.b.FinishSelfRepair(ctx, token, nonce, fin); ferr != nil {
		return fmt.Errorf("self-repair %s %s; its finish not written: %w", role, why, ferr)
	}
	recovery := "agent-launch " + role + " " + c.project
	if session != "" {
		recovery += " --resume " + session
	}
	text := fmt.Sprintf("self-repair: %s relaunch %s. No retry for this death: recover with %s in a new pane of its tab, or agentbus up %s --resume.",
		role, why, recovery, c.project)
	if terr := c.tell(ctx, token, "selfrepair-failed:"+role, ep, master, text); terr != nil {
		return terr
	}
	c.ring(ctx, role+": self-repair failed", text)
	return nil
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

// tell sends master one line, once per episode (mark), fenced.
func (c *selfRepairChore) tell(ctx context.Context, token, mark, episode, target, text string) error {
	if episode == "" {
		episode = "-"
	}
	_, err := c.b.FencedCmdOnce(ctx, token, mark, episode, "dispatch", target, bus.CmdDirective, text, false)
	if errors.Is(err, bus.ErrSwapInProgress) {
		return nil // master itself being repaired: the next pass tells it
	}
	return err
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
