package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// `agentbus reload <role> [--fresh] [--dry-run] [--reason <text>] [--as <person>]`
// (lifecycle spec, part C): relaunch one agent on the same role, profile and
// client, resuming its proved session, without typing into any pane. It
// signals the verified pid, then v0.21's relaunch runs the role's template
// command in a new pane and closes the old one once the new agent is proven.
// Everything is checked before anything changes, under the swap intent
// (kind=reload), and every failure after the intent is in the ledger.

// reloadDeps are the outside world of a reload; tests replace every one.
type reloadDeps struct {
	Env       func(string) string
	Manifest  func() (*rolecfg.Manifest, error)
	LookPath  func(string) (string, error)
	ProcStart func(pid int) (uint64, error)
	Terminate func(pid int) error // SIGTERM, never SIGKILL
	// Relaunch runs the role's template command in a new pane of the open
	// workspace, resuming session ("" = fresh), and closes the old pane once
	// the new agent is proven.
	Relaunch    func(ctx context.Context, role, session string) error
	AfterIntent func() // tests: work that opens between the two readiness reads
	Now         func() time.Time
	Sleep       func(time.Duration)
	PIDGone     time.Duration // wait for the old process (15 s)
	Proof       time.Duration // wait for the new identity (60 s)
	Recent      time.Duration // unused by the rule (idle is required); kept for parity with swap
}

func realReloadDeps(env []string, project string, stdout, stderr io.Writer) reloadDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	return reloadDeps{
		// The policy comes from the caller's environment, else the project's
		// env file, where up --reload records it for the services.
		Env: func(k string) string {
			v := os.Getenv(k)
			if v == "" && k == service.ReloadEnv {
				if env, err := service.LoadEnvFile(paths.EnvFile(project)); err == nil {
					v = env[k]
				}
			}
			return v
		},
		Manifest: func() (*rolecfg.Manifest, error) {
			path, err := manifestPath(env)
			if err != nil {
				return nil, err
			}
			return rolecfg.Load(path)
		},
		LookPath:  exec.LookPath,
		ProcStart: dispatch.ProcStart,
		Terminate: func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) },
		Relaunch: func(_ context.Context, role, session string) error {
			return relaunchRole(realUpDeps(stdout, stderr), project, role, session, stdout)
		},
		Now: time.Now, Sleep: time.Sleep,
		PIDGone: 15 * time.Second, Proof: 60 * time.Second, Recent: 2 * time.Minute,
	}
}

const reloadUsage = "usage: agentbus reload <role> [--fresh] [--dry-run] [--reason <text>] [--as <person>]"

func runReload(ctx context.Context, b *bus.Bus, d reloadDeps, self string, args []string, out, errw io.Writer) int {
	args, fresh := extractBool(args, "--fresh")
	args, dry := extractBool(args, "--dry-run")
	args, reason := extractFlag(args, "--reason")
	args, person := extractFlag(args, "--as")
	if len(args) != 1 || !bus.ValidName(args[0]) {
		fmt.Fprintln(errw, reloadUsage)
		return 2
	}
	role := args[0]
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errw, "reload %s: "+format+"\n", append([]any{role}, a...)...)
		return 1
	}

	// Who may reload whom (part C's table). The CLI cannot prove who runs it,
	// as with gate close --as; a person names themself explicitly.
	by := self
	if person != "" {
		if !bus.ValidName(person) {
			return fail("--as %q is not a person's name", person)
		}
		by = person
	}
	mode := d.Env(service.ReloadEnv) // "" (on), propose, off
	if person == "" {
		switch {
		case mode == "off":
			return fail("AGENT_BUS_RELOAD=off: only a person reloads an agent (--as <person>)")
		case self == "sentinel" && role == "sentinel":
			return fail("sentinel cannot reload itself: master reloads sentinel")
		case mode == "propose": // the person approves every reload, master's of sentinel included
			return fail("AGENT_BUS_RELOAD=propose: a person runs the reload (--as <person>) once they approve; propose it to master, who asks them")
		case self == "master" && role != "sentinel":
			return fail("master may reload only sentinel; sentinel reloads the others, a person anyone (--as)")
		case self != "sentinel" && self != "master":
			return fail("%s may not reload %s: sentinel reloads agents, master reloads sentinel, a person anyone (--as)", self, role)
		}
	}

	// Preflight: nothing changes before every check has passed.
	m, err := d.Manifest()
	if err != nil {
		return fail("role manifest: %v", err)
	}
	if _, ok := m.Roles[role]; !ok {
		return fail("unknown role in ROLES_TOML")
	}
	own, err := m.Resolve(role)
	if err != nil {
		return fail("%v", err)
	}
	if exe := rolecfg.ClientExecutable(own); exe == "" {
		return fail("profile %s has no client executable", own.Profile)
	} else if _, err := d.LookPath(exe); err != nil {
		return fail("client executable %s not found on PATH", exe)
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return fail("%v", err)
	}
	snap, ok := snaps[role]
	if !ok || snap.Pane == "" || snap.PID == 0 {
		return fail("no verified pane recorded for %s (agentbus agents): nothing to reload", role)
	}
	if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
		return fail("its recorded process (pid %d) is not running: agentbus up %s --resume relaunches it", snap.PID, b.Project())
	}
	if pane := d.Env("HERDR_PANE_ID"); pane != "" && pane == snap.Pane {
		return fail("an agent cannot be reloaded from its own pane: run it from another pane or a terminal")
	}
	fallbacks, err := b.Fallbacks(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if f, on := fallbacks[role]; on {
		return fail("it runs on its fallback profile %s, and its template carries %s: a reload would undo that swap; use agentbus swap %s --back",
			f.Profile, own.Profile, role)
	}
	obs, err := b.Observed(ctx)
	if err != nil {
		return fail("%v", err)
	}
	o, seen := obs[role]
	seen = seen && o.Fresh(d.Now().UnixMilli())
	if person == "" && self == "sentinel" {
		if !seen || o.Update == nil {
			return fail("no client update is observed for %s: sentinel reloads only on an update episode", role)
		}
		if role == "master" {
			driver, err := b.PilotDriver(ctx)
			if err != nil {
				return fail("%v", err)
			}
			if driver != "" && driver != "master" {
				return fail("the pilot lease is held by %s: master is reloaded only when no other driver holds it", driver)
			}
		}
	}
	if code := reloadOpenWork(ctx, b, role, fail); code != 0 {
		return code
	}
	if seen && o.Busy {
		return fail("its pane runs a turn right now; wait until it is idle")
	}
	if snap.State != "idle" {
		return fail("it declared %s; a reload needs it idle (no turn running, nothing in flight)", snap.State)
	}
	if w, ok, err := b.Wake(ctx, role); err != nil {
		return fail("%v", err)
	} else if ok && w.State == "prompting" {
		return fail("a wake is being typed into its pane; retry in a minute")
	}
	session := ""
	if !fresh {
		if snap.Client != "claude-code" || !bus.ValidSessionID(snap.IdentSession) {
			return fail("no proved Claude Code session to resume (client %s); pass --fresh to relaunch without one (its conversation is lost)",
				dash(snap.Client))
		}
		session = snap.IdentSession
	}
	resumeWord := "resuming session " + session
	if session == "" {
		resumeWord = "fresh (its conversation is lost)"
	}
	if dry {
		fmt.Fprintf(out, "would reload %s: SIGTERM pid %d in pane %s, relaunch on %s %s, then close the old pane once the new agent is proven\n",
			role, snap.PID, snap.Pane, own.Profile, resumeWord)
		return 0
	}

	// The intent (kind=reload): the dispatcher neither prompts, escalates,
	// resumes nor clears the agent while it holds.
	closeInterrupted(ctx, b, role, by)
	if err := b.BeginSwap(ctx, role, bus.SwapIntent{Kind: bus.SwapKindReload, ToProfile: own.Profile, By: by, OldPID: snap.PID, OldStart: snap.PIDStart},
		bus.SwapEvent{Kind: bus.SwapKindReload, FromProfile: own.Profile, ToProfile: own.Profile, FromProvider: snap.Provider, ToProvider: own.Provider,
			By: by, Reason: reason, OldPID: snap.PID, OldPane: snap.Pane}); err != nil {
		return fail("%v", err)
	}
	defer func() { _ = b.EndSwap(context.Background(), role) }()
	failed := func(step, format string, a ...any) int {
		msg := fmt.Sprintf(format, a...)
		_ = b.LogSwap(context.Background(), bus.SwapEvent{Role: role, Event: bus.SwapFailed, Kind: bus.SwapKindReload, ToProfile: own.Profile, By: by,
			Error: step + ": " + msg})
		return fail("%s", msg)
	}
	if d.AfterIntent != nil {
		d.AfterIntent()
	}
	if code := reloadOpenWork(ctx, b, role, func(f string, a ...any) int { return failed("open work", f, a...) }); code != 0 {
		return code
	}
	for deadline := d.Now().Add(10 * time.Second); ; d.Sleep(200 * time.Millisecond) {
		w, ok, err := b.Wake(ctx, role)
		if err != nil {
			return failed("wake record", "cannot read its wake record (%v); nothing was signalled", err)
		}
		if !ok || w.State != "prompting" {
			break
		}
		if d.Now().After(deadline) {
			return failed("wake in progress", "a wake is still being typed into its pane; retry in a minute")
		}
	}

	// Stop: the same process as checked, or nothing is signalled.
	cur, err := b.Agents(ctx)
	if err != nil {
		return failed("stop", "%v; nothing was signalled", err)
	}
	if s := cur[role]; s.PID != snap.PID || s.PIDStart != snap.PIDStart {
		return failed("stop", "its process changed since the check; nothing was signalled")
	} else if s.State != "idle" {
		return failed("not idle", "it declared %s since the check; nothing was signalled", s.State)
	}
	obsNow, err := b.Observed(ctx)
	if err != nil { // unknown is not idle: fail closed, as the preflight does
		return failed("not idle", "cannot read its observed pane (%v); nothing was signalled", err)
	}
	oNow, okNow := obsNow[role]
	freshNow := okNow && oNow.Fresh(d.Now().UnixMilli())
	if seen && !freshNow { // observed at the check, not now: whether a turn runs is unknown
		return failed("not idle", "its pane is no longer observed (the reading aged out); nothing was signalled")
	}
	if freshNow && oNow.Busy {
		return failed("not idle", "its pane started a turn since the check; nothing was signalled")
	}
	if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
		return failed("stop", "pid %d is no longer the checked process; nothing was signalled", snap.PID)
	}
	if err := d.Terminate(snap.PID); err != nil {
		return failed("stop", "SIGTERM refused (%v); nothing was replaced", err)
	}
	for deadline := d.Now().Add(d.PIDGone); ; d.Sleep(200 * time.Millisecond) {
		if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
			break
		}
		if d.Now().After(deadline) {
			return failed("stop", "the client did not exit within %s of SIGTERM; nothing was replaced (no SIGKILL: stop it by hand)", d.PIDGone)
		}
	}

	// Relaunch and proof.
	recovery := fmt.Sprintf("agent-launch %s %s", role, b.Project())
	if session != "" {
		recovery += " --resume " + session
	}
	if err := d.Relaunch(ctx, role, session); err != nil {
		return failed("relaunch", "the old client is gone and the relaunch failed: %v; recover with %s in pane %s, or agentbus up %s --resume",
			err, recovery, snap.Pane, b.Project())
	}
	var proved bus.AgentSnapshot
	for deadline := d.Now().Add(d.Proof); ; d.Sleep(500 * time.Millisecond) {
		if now, err := b.Agents(ctx); err == nil {
			if s, ok := now[role]; ok && replaced(s, snap, own, d.ProcStart) {
				proved = s
				break
			}
		}
		if d.Now().After(deadline) {
			return failed("proof", "no proof of the new process after %s (a new pid, alive, in a new pane, on %s/%s); check agentbus agents and agentbus up %s --resume --dry-run",
				d.Proof, own.Client, own.Provider, b.Project())
		}
	}
	// An update episode's reload must run the client installed now (its
	// fingerprint's hash is the episode), or it only restarted the old one.
	if seen && o.Update != nil && o.Update.Source == "fingerprint" {
		if fp, err := clientfp.Decode(proved.ClientFP); err != nil || fp.Hash() != o.Update.Hash {
			return failed("proof", "the new process (pid %d) does not run the installed client %s (its client_fp %s); check the launcher's PATH, then reload again",
				proved.PID, dash(o.Update.Installed), dash(fpHash(proved.ClientFP)))
		}
	}
	if err := b.FinishReload(ctx, bus.SwapEvent{Role: role, FromProfile: own.Profile, ToProfile: own.Profile, ToProvider: own.Provider,
		By: by, Reason: reason, NewPID: proved.PID, NewPane: proved.Pane}); err != nil {
		return fail("reloaded (pane %s, pid %d), but the ledger was not written: %v; check agentbus swaps", proved.Pane, proved.PID, err)
	}
	fmt.Fprintf(out, "reloaded %s: pid %d -> %d, pane %s -> %s, %s\n", role, snap.PID, proved.PID, snap.Pane, proved.Pane, resumeWord)
	return 0
}

// fpHash is an encoded fingerprint's hash, or "" when there is none.
func fpHash(encoded string) string {
	fp, err := clientfp.Decode(encoded)
	if err != nil {
		return ""
	}
	return fp.Hash()
}

// reloadOpenWork is reload's one readiness rule (part C): swap's open work (a
// challenge on either side, a mission attempt) plus an open ask by it and a
// board task it owns in state accepted, which is work in flight even while
// it is idle between turns. Sentinel's reload pass and master's last reload
// apply this same rule, through this command.
func reloadOpenWork(ctx context.Context, b *bus.Bus, agent string, fail func(string, ...any) int) int {
	if code := openWork(ctx, b, agent, fail); code != 0 {
		return code
	}
	var open []string
	asks, err := b.Asks(ctx)
	if err != nil {
		return fail("%v", err)
	}
	for _, a := range asks {
		if a.From == agent && a.Open() > 0 {
			open = append(open, "ask "+a.ID+" ("+a.Title+")")
		}
	}
	board, err := b.Board(ctx)
	if err != nil {
		return fail("%v", err)
	}
	for task, e := range board {
		if e.Owner == agent && e.State == "accepted" {
			open = append(open, "board task "+task+" (accepted)")
		}
	}
	if len(open) == 0 {
		return 0
	}
	sort.Strings(open)
	return fail("it has work in flight; finish or hand it over first:\n  %s", strings.Join(open, "\n  "))
}

// relaunchRole is `agentbus up`'s relaunch for one role of the open
// workspace: the role's tab only, its template command resuming session (""
// = fresh) in a new pane, the old pane closed once the new agent is proven.
// It takes up's lock, so it never splits a tab while an up does.
func relaunchRole(d upDeps, project, role, session string, stdout io.Writer) error {
	if d.Env("HERDR_ENV") != "1" {
		return fmt.Errorf("not inside herdr: run it in the project's herdr session")
	}
	if d.Lock != nil {
		unlock, err := d.Lock(project, false)
		if err != nil {
			return err
		}
		defer unlock()
	}
	var tpl upTemplate
	if _, err := toml.DecodeFile(templatePath(d, project), &tpl); err != nil {
		return fmt.Errorf("template: %w", err)
	}
	tpl.Tabs = slices.DeleteFunc(tpl.Tabs, func(t upTab) bool {
		r, ok := launchRole(t.Command, project)
		return !ok || r != role
	})
	if len(tpl.Tabs) != 1 {
		return fmt.Errorf("the template has %d tabs launching %s, want 1", len(tpl.Tabs), role)
	}
	ws, err := findWorkspace(d, project)
	if err != nil {
		return err
	}
	if ws == "" {
		return fmt.Errorf("the workspace of %s is not open", project)
	}
	if d.Agents == nil {
		return fmt.Errorf("no bus to prove the new agent on")
	}
	snaps, err := d.Agents(project)
	if err != nil {
		return err
	}
	// The relaunch resumes the session the reload chose (the proved one),
	// not the snapshot's unverified Session.
	if s, ok := snaps[role]; ok {
		s.Session = session
		snaps[role] = s
	}
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	o := relaunchOptions{Resume: session != "", Create: true,
		Dir:   os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Env("HOME"), 1)),
		Prove: &relaunchProof{Agents: func() (map[string]bus.AgentSnapshot, error) { return d.Agents(project) }, Sleep: sleep, Tries: 60}}
	return relaunchOpen(d.Herdr, snaps, true, d.ProcStart, project, ws, tpl, o, stdout)
}
