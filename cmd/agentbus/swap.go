package main

import (
	"context"

	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// `agentbus swap <role> --profile <profile> [--now] [--force]` (spec
// 2026-10-16-quota-resume-and-fallback-design.md §6): replace an agent by the
// same role on another profile, checking everything that can be checked
// before anything is closed, and proving the replacement before it reports
// success. Run inside the project's herdr session, by master after a
// person's yes, or by the person.

// swapDeps are the outside world of a swap; tests replace every one.
type swapDeps struct {
	Env       func(string) string
	Manifest  func() (*rolecfg.Manifest, error)
	LookPath  func(string) (string, error)
	ProcStart func(pid int) (uint64, error)
	ClosePane func(ctx context.Context, pane string) error
	Spawn     func(ctx context.Context, role, project, profile string) error
	Now       func() time.Time
	Sleep     func(time.Duration)
	PIDGone   time.Duration // wait for the old process (15 s)
	Proof     time.Duration // wait for the new identity (60 s)
	Recent    time.Duration // a working declaration this recent is a running turn (2 min)
}

func realSwapDeps(env []string) swapDeps {
	return swapDeps{
		Env: os.Getenv,
		Manifest: func() (*rolecfg.Manifest, error) {
			path, err := manifestPath(env)
			if err != nil {
				return nil, err
			}
			return rolecfg.Load(path)
		},
		LookPath:  exec.LookPath,
		ProcStart: dispatch.ProcStart,
		ClosePane: func(ctx context.Context, pane string) error {
			out, err := exec.CommandContext(ctx, "herdr", "pane", "close", pane).CombinedOutput()
			if err != nil {
				return fmt.Errorf("herdr pane close %s: %v: %s", pane, err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		Spawn: func(ctx context.Context, role, project, profile string) error {
			repo, err := repoDir(os.Getenv)
			if err != nil {
				return err
			}
			c := exec.CommandContext(ctx, filepath.Join(repo, "scripts", "agent-spawn"), role, project, "--profile", profile)
			c.Env = append(os.Environ(), "AGENT_LAUNCH_REQUIRE_BIND=1")
			out, err := c.CombinedOutput()
			if err != nil {
				return fmt.Errorf("agent-spawn: %v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		Now: time.Now, Sleep: time.Sleep,
		PIDGone: 15 * time.Second, Proof: 60 * time.Second, Recent: 2 * time.Minute,
	}
}

const swapUsage = "usage: agentbus swap <role> --profile <profile> [--now] [--force]"

func runSwap(ctx context.Context, b *bus.Bus, d swapDeps, self string, args []string, out, errw io.Writer) int {
	args, now := extractBool(args, "--now")
	args, force := extractBool(args, "--force")
	var role, profile string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--profile" && i+1 < len(args):
			profile = args[i+1]
			i++
		case role == "" && !strings.HasPrefix(args[i], "-"):
			role = args[i]
		default:
			fmt.Fprintln(errw, swapUsage)
			return 2
		}
	}
	if role == "" || profile == "" || !bus.ValidName(role) || !bus.ValidName(profile) {
		fmt.Fprintln(errw, swapUsage)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errw, "swap %s: "+format+"\n", append([]any{role}, a...)...)
		return 1
	}

	// 6.1 Preflight: nothing changes before every check has passed.
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
	allowed := profile == own.Profile
	for _, f := range m.Fallbacks(role) {
		allowed = allowed || f == profile
	}
	if !allowed && !force {
		return fail("profile %s is neither the role's profile (%s) nor one of its fallback_profiles (agentbus roles fallbacks %s); --force accepts any defined profile",
			profile, own.Profile, role)
	}
	target, err := m.ResolveWith(role, profile)
	if err != nil {
		return fail("profile %s: %v", profile, err)
	}
	if !allowed {
		fmt.Fprintf(errw, "notice: --force: %s is not a fallback of %s\n", profile, role)
	}
	if exe := rolecfg.ClientExecutable(target); exe == "" {
		return fail("profile %s has no client executable", profile)
	} else if _, err := d.LookPath(exe); err != nil {
		return fail("client executable %s not found on PATH", exe)
	}

	snaps, err := b.Agents(ctx)
	if err != nil {
		return fail("%v", err)
	}
	snap, ok := snaps[role]
	if !ok || snap.Pane == "" || snap.PID == 0 {
		return fail("no verified pane recorded for %s (agentbus agents): nothing to replace", role)
	}
	if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
		return fail("its recorded process (pid %d) is not running any more: start it with agent-spawn %s %s --profile %s",
			snap.PID, role, b.Project(), profile)
	}
	if pane := d.Env("HERDR_PANE_ID"); pane != "" && pane == snap.Pane {
		return fail("an agent cannot swap itself: run it from master's pane or a terminal")
	}

	var open []string
	reviews, err := b.ReviewsOf(ctx, role)
	if err != nil {
		return fail("%v", err)
	}
	open = append(open, reviews...)
	missions, err := missionsOf(ctx, b, role)
	if err != nil {
		return fail("%v", err)
	}
	open = append(open, missions...)
	if len(open) > 0 {
		sort.Strings(open)
		return fail("it takes part in open work that is keyed by its name; close it first (verdict, mission reassign):\n  %s",
			strings.Join(open, "\n  "))
	}
	if !now {
		obs, err := b.Observed(ctx)
		if err != nil {
			return fail("%v", err)
		}
		if o, ok := obs[role]; ok && o.Busy && o.Fresh(d.Now().UnixMilli()) {
			return fail("its pane runs a turn right now; wait, or pass --now (the turn is lost)")
		}
		if snap.State == "working" && d.Now().Sub(time.UnixMilli(snap.TS)) < d.Recent {
			return fail("it declared working %s ago; wait, or pass --now (the turn is lost)", d.Now().Sub(time.UnixMilli(snap.TS)).Round(time.Second))
		}
	}

	// 6.2 Handoff.
	if err := b.BeginSwap(ctx, role, bus.SwapIntent{ToProfile: profile, By: self, OldPID: snap.PID, OldStart: snap.PIDStart}); err != nil {
		return fail("%v", err)
	}
	ended := false
	end := func() {
		if !ended {
			ended = true
			_ = b.EndSwap(context.Background(), role)
		}
	}
	defer end()
	recovery := fmt.Sprintf("recover with: agent-spawn %s %s (its own profile, %s)", role, b.Project(), own.Profile)
	if err := d.ClosePane(ctx, snap.Pane); err != nil {
		return fail("%v; nothing was replaced", err)
	}
	for deadline := d.Now().Add(d.PIDGone); ; d.Sleep(200 * time.Millisecond) {
		if start, err := d.ProcStart(snap.PID); err != nil || start != snap.PIDStart {
			break
		}
		if d.Now().After(deadline) {
			return fail("pane %s closed but pid %d is still running after %s; %s", snap.Pane, snap.PID, d.PIDGone, recovery)
		}
	}

	// 6.3 Launch and proof.
	if err := d.Spawn(ctx, role, b.Project(), profile); err != nil {
		return fail("the old pane is closed and the launch failed: %v\n%s", err, recovery)
	}
	var proved bus.AgentSnapshot
	for deadline := d.Now().Add(d.Proof); ; d.Sleep(500 * time.Millisecond) {
		cur, err := b.Agents(ctx)
		if err == nil {
			if s, ok := cur[role]; ok && replaced(s, snap, target, d.ProcStart) {
				proved = s
				break
			}
		}
		if d.Now().After(deadline) {
			return fail("no proof of replacement after %s (a new pid, alive, in a new pane, on %s/%s)\n%s",
				d.Proof, target.Client, target.Provider, recovery)
		}
	}
	end()
	fmt.Fprintf(out, "swapped %s: %s (%s, %s) -> %s (%s, %s), pane %s, pid %d\n",
		role, own.Profile, own.Provider, own.Model, profile, target.Provider, target.Model, proved.Pane, proved.PID)

	// The work it had in flight, sent only now, so the old process never
	// read it; through the hold's resume mark when its old provider is held.
	if text, ok := swapResumeText(ctx, b, role, snap, profile); ok {
		marks, _ := b.QuotaMarks(ctx)
		if episode := marks["hold:"+snap.Provider]; episode != "" && snap.Provider != "" {
			if _, err := b.CmdOnce(ctx, "resumed:"+snap.Provider+":"+role, episode, self, role, text); err != nil {
				fmt.Fprintf(errw, "swap %s: resume directive: %v\n", role, err)
			}
		} else if _, err := b.Cmd(ctx, self, role, bus.CmdDirective, "", text); err != nil {
			fmt.Fprintf(errw, "swap %s: resume directive: %v\n", role, err)
		}
	}
	return 0
}

// replaced: the snapshot proves a new process of the chosen profile.
func replaced(s, old bus.AgentSnapshot, p rolecfg.Profile, procStart func(int) (uint64, error)) bool {
	if s.PID == 0 || s.PID == old.PID && s.PIDStart == old.PIDStart || s.Pane == "" || s.Pane == old.Pane {
		return false
	}
	if start, err := procStart(s.PID); err != nil || start != s.PIDStart {
		return false
	}
	return s.Client == p.Client && s.Provider == p.Provider
}

// swapResumeText is the directive of the agent's in-flight work, if any.
func swapResumeText(ctx context.Context, b *bus.Bus, role string, old bus.AgentSnapshot, profile string) (string, bool) {
	board, err := b.Board(ctx)
	if err != nil {
		return "", false
	}
	var tasks []string
	for task, e := range board {
		if e.Owner == role && e.State == "accepted" {
			tasks = append(tasks, task)
		}
	}
	sort.Strings(tasks)
	if len(tasks) == 0 && old.State != "working" {
		return "", false
	}
	what := "the turn you were running"
	if len(tasks) > 0 {
		if len(tasks) > 5 {
			tasks = append(tasks[:5], strconv.Itoa(len(tasks)-5)+" more")
		}
		what = strings.Join(tasks, ", ")
	}
	return fmt.Sprintf("You replace %s on the profile %s: the previous process stopped mid-work. "+
		"Resume where it stopped (%s): read the board and your thread, then report as usual.", role, profile, what), true
}

// missionsOf lists the mission attempts agent is assigned to, in progress.
func missionsOf(ctx context.Context, b *bus.Bus, agent string) ([]string, error) {
	st := mission.Store{B: b}
	changes, err := st.Changes(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range changes {
		ms, err := st.Missions(ctx, c)
		if err != nil {
			return nil, err
		}
		for id, r := range ms {
			switch r.State {
			case mission.Requested, mission.Accepted, mission.Delivered, mission.Checking:
				if r.Current.Agent == agent {
					out = append(out, fmt.Sprintf("mission %s/%s (%s)", c, id, r.State))
				}
			}
		}
	}
	return out, nil
}
