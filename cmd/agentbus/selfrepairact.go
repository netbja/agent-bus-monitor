package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// act applies one pass's decisions, then delivers what is owed to master
// and not written yet. now is the pass's Redis time.
func (c *selfRepairChore) act(ctx context.Context, token string, ds []selfrepair.Decision, r selfRepairReads, deaths map[string]repairDeath, now time.Time) error {
	master := c.master()
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	network := map[string][]string{} // provider|since: the agents its hold keeps
	var networkOrder []string
	workspace := false
	for _, d := range ds {
		ep := deaths[d.Agent].episode
		switch {
		case d.Kind == selfrepair.KindHold && d.Hold == selfrepair.HoldNetwork:
			k := d.Provider + "|" + sinceKey(d.Since)
			if _, seen := network[k]; !seen {
				networkOrder = append(networkOrder, k)
			}
			network[k] = append(network[k], d.Agent)
		case d.Kind == selfrepair.KindHold && d.Hold == selfrepair.HoldWorkspace:
			workspace = true
		case d.Act && (d.Kind == selfrepair.KindRelaunch || d.Kind == selfrepair.KindCreate):
			keep(c.repair(ctx, token, d, r, ep, now.Sub(deaths[d.Agent].since)))
		case d.Kind == selfrepair.KindBudget:
			keep(c.crashLoop(ctx, token, d, master))
		case d.Kind == selfrepair.KindReport && ep != "":
			_, err := c.sender()(ctx, token, "selfrepair-report:"+d.Agent, ep, master,
				fmt.Sprintf("self-repair: %s not relaunched: %s. Master relaunches it, or asks the person.", d.Agent, d.Reason))
			if !errors.Is(err, errNotYet) { // a report is sent again at the next pass of the same death
				keep(err)
			}
		}
	}
	for _, k := range networkOrder { // R3: once per provider and last good reading
		provider, ms, _ := strings.Cut(k, "|")
		since := "it was never read"
		if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n > 0 {
			since = "since " + time.UnixMilli(n).Local().Format("15:04")
		}
		_, err := c.sender()(ctx, token, "selfrepair-network:"+provider, ms, master,
			fmt.Sprintf("self-repair: holding relaunches, %s unreachable %s (%s dead, relaunched once it answers, oldest first).",
				provider, since, strings.Join(network[k], ", ")))
		if !errors.Is(err, errNotYet) {
			keep(err)
		}
	}
	if workspace { // R4: once per spell of a closed workspace
		c.mu.Lock()
		if c.wsClosed.IsZero() { // after a restart, the outstanding spell's episode (its durable mark)
			c.wsClosed = now
			if ms, err := strconv.ParseInt(r.Marks[workspaceMark], 10, 64); err == nil && ms > 0 {
				c.wsClosed = time.UnixMilli(ms)
			}
		}
		ep := strconv.FormatInt(c.wsClosed.UnixMilli(), 10)
		c.mu.Unlock()
		_, err := c.sender()(ctx, token, workspaceMark, ep, master,
			fmt.Sprintf("self-repair: the workspace of %s is not open: nothing is relaunched until it is. Run agentbus up %s --resume (or open it in herdr).",
				c.project, c.project))
		if !errors.Is(err, errNotYet) {
			keep(err)
		}
	}
	keep(c.deliver(ctx, token, master))
	return first
}

// workspaceMark is R4's notice mark in {p}:quota: its value, the spell's
// episode, outlives a dispatcher restart (reviewer-codex); cleared once
// herdr says the workspace is open.
const workspaceMark = "selfrepair-workspace"

// sinceKey is a last good reading as an episode ("0": none).
func sinceKey(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
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
	if errors.Is(err, errNotYet) {
		return nil // the mark stands: sent at a later pass
	}
	return err
}

// owedMsg is a message a self-repair owes master, recorded with its finish
// (FinishSelfRepairOwing) and kept until written: of any age, through a
// lost lease, a broker away or master being swapped (reviewer-codex on 7b).
type owedMsg struct {
	Mark    string `json:"mark"`
	Episode string `json:"episode"`
	Text    string `json:"text"`
	Ring    bool   `json:"ring,omitempty"` // a failure: the desktop rings when it is written
}

// deliver sends every owed message: written now, or already written by an
// earlier send (FencedCmdOnce's mark holds its episode), it is acknowledged;
// a send that failed, or waits on a swap, keeps it for the next pass. Only
// the send that writes rings the desktop.
func (c *selfRepairChore) deliver(ctx context.Context, token, master string) error {
	owed, err := c.b.SelfRepairOwed(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, o := range owed { // oldest death first
		var m owedMsg
		if json.Unmarshal([]byte(o.Value), &m) != nil || m.Mark == "" {
			_ = c.b.AckSelfRepairOwed(ctx, token, o) // unreadable: nothing to send
			continue
		}
		wrote, err := c.sender()(ctx, token, m.Mark, o.ID, master, m.Text) // once per end: its ledger id
		if errors.Is(err, errNotYet) {
			continue
		}
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if wrote && m.Ring {
			c.ring(ctx, o.Role+": self-repair failed", m.Text)
		}
		if err := c.b.AckSelfRepairOwed(ctx, token, o); err != nil && first == nil {
			first = err
		}
	}
	return first
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
	msg := owedMsg{Mark: "selfrepair-done:" + role, Episode: ep,
		Text: fmt.Sprintf("self-repair: %s relaunched (pid %d -> %d, pane %s -> %s, %s).", role, snap.PID, fin.NewPID, snap.Pane, fin.NewPane, fin.Reason)}
	if !ok {
		msg = owedMsg{Mark: "selfrepair-failed:" + role, Episode: ep, Ring: true,
			Text: fmt.Sprintf("self-repair: %s relaunch %s. No retry for this death: %s.", role, fin.Error, fin.Reason)}
	}
	if msg.Episode == "" {
		msg.Episode = "-"
	}
	raw, _ := json.Marshal(msg)
	if err := c.b.FinishSelfRepairOwing(ctx, token, nonce, fin, string(raw)); err != nil {
		return fmt.Errorf("self-repair %s: its finish not written: %w", role, err)
	}
	return nil // deliver, in this same pass, tells master
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

// errNotYet: the target is being swapped; the message waits for a later
// pass (never counted written).
var errNotYet = errors.New("self-repair: master is being swapped: the message waits")

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
			return false, errNotYet
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
