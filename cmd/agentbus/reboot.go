package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/lifecycle"
	"github.com/netbja/agent-bus-monitor/service"
)

// After a reboot (spec docs/superpowers/specs/2026-11-15-reboot-resume-design.md
// §3, amended by holder hardening H1b): once it holds the lease, before any
// other loop, the dispatch service judges the run record once, records the
// verdict in {p}:reboot (fenced), tells the person once per boot, and only
// then writes running for this boot, so a stop in between judges again.

// The holder's readiness phase (H1b): only for an unplanned stop whose
// conditions 1 and 2 held, the holder is polled this often, for at most
// this long; nothing is written, counted or relaunched meanwhile.
const (
	rebootHolderWait  = 2 * time.Minute
	rebootHolderEvery = 2 * time.Second
)

// rebootSettings are the machine's settings the judgement reads (§6.3).
type rebootSettings struct {
	Restart    bool          // services.unplanned_restart
	Max        int           // services.unplanned_max
	Window     time.Duration // services.unplanned_window
	SelfRepair string        // dispatch.self_repair: on, off, shadow
}

// rebootJudge judges one start (each input replaced by tests).
type rebootJudge struct {
	b           *bus.Bus
	dir         string // the project's state directory: run.json, unplanned.json
	boot        string // this host's boot ("": unknown, nothing judged, §2.3)
	host        string
	escalate    func() string
	settings    func() rebootSettings
	holderReady func(ctx context.Context) bool   // the current build's holder answers
	repo        func(ctx context.Context) string // why the repository is not intact; "" intact
	services    func() map[string]bool           // the services running now (the suspend's record)
	now         func() time.Time                 // the readiness phase's clock (this host's)
	sleep       func(ctx context.Context, d time.Duration) error
	log         io.Writer
}

// run is the dispatcher's judgement at start (dispatch.Config.Reboot). An
// error leaves running unwritten: the next start judges again.
func (j *rebootJudge) run(ctx context.Context, token string) error {
	if j.boot == "" {
		return nil // unknown boot: nothing can be judged, nothing is written (§2.3)
	}
	// A judgement of this boot is already recorded (a stop after it, before
	// running was written): it stands, nothing is judged or counted again;
	// its notice and directive, once per boot, are given again in case the
	// stop came before them (reviewer-codex).
	if rec, ok, err := j.b.Reboot(ctx); err != nil {
		return err
	} else if ok && rec.Boot == j.boot {
		if err := j.tell(ctx, token, rec); err != nil {
			return err
		}
		return j.started()
	}
	prev, err := lifecycle.ReadRun(j.dir)
	var p *lifecycle.Record
	if err == nil {
		p = &prev
	}
	var rec bus.RebootRecord
	switch lifecycle.Judge(p, err, j.boot) {
	case lifecycle.Intentional:
		rec.Verdict = bus.RebootIntentional
	case lifecycle.Suspended:
		rec.Verdict = bus.RebootSuspended
	case lifecycle.Unplanned:
		if rec, err = j.unplanned(ctx, token); err != nil {
			return err
		}
	default:
		return j.started()
	}
	rec.Boot = j.boot
	if err := j.b.FencedPutReboot(ctx, token, rec); err != nil {
		return err
	}
	if err := j.tell(ctx, token, rec); err != nil {
		return err
	}
	return j.started()
}

// unplanned is §3.1's conditions in order; the first that fails is why.
func (j *rebootJudge) unplanned(ctx context.Context, token string) (bus.RebootRecord, error) {
	s := j.settings()
	not := func(why string) (bus.RebootRecord, error) {
		return bus.RebootRecord{Verdict: bus.RebootUnplanned, Why: why}, nil
	}
	switch {
	case !s.Restart:
		return not("setting off")
	case s.SelfRepair == "shadow":
		return not("self-repair in shadow mode")
	case s.SelfRepair != "on":
		return not("self-repair off")
	}
	// 2b, after H1b's readiness phase: the holder must answer.
	if !j.waitHolder(ctx) {
		if err := ctx.Err(); err != nil {
			return bus.RebootRecord{}, err // stopped during the phase: nothing written
		}
		return not("holder not answering")
	}
	// 3: the repository intact, or the project stops; nothing counted.
	if why := j.repo(ctx); why != "" {
		if err := ctx.Err(); err != nil {
			return bus.RebootRecord{}, err
		}
		if err := j.stop(ctx, token, "stopped: the repository needs a check ("+why+")"); err != nil {
			return bus.RebootRecord{}, err
		}
		return bus.RebootRecord{Verdict: bus.RebootStopped, Why: why}, nil
	}
	// 4 and 5: the count, durably, before anything is relaunched (§3.2).
	es, err := lifecycle.ReadCounter(j.dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return not("counter missing")
	case err != nil:
		return not("counter not written")
	}
	now, err := j.b.ServerTime(ctx)
	if err != nil {
		return bus.RebootRecord{}, err
	}
	es = lifecycle.Count(es, j.boot, now.UnixMilli(), s.Window)
	n, win := len(es), windowText(s.Window)
	counted := bus.RebootRecord{N: n, Max: s.Max, Window: s.Window.Milliseconds()}
	if n > s.Max {
		_ = lifecycle.WriteCounter(j.dir, es) // its failure does not undo the stop
		if err := j.stop(ctx, token, fmt.Sprintf("stopped after %d unplanned restarts of this machine in %s", n, win)); err != nil {
			return bus.RebootRecord{}, err
		}
		counted.Verdict, counted.Why = bus.RebootStopped, fmt.Sprintf("limit: %d in %s", n, win)
		return counted, nil
	}
	if err := lifecycle.WriteCounter(j.dir, es); err != nil {
		return not("counter not written")
	}
	counted.Verdict = bus.RebootRestarting
	return counted, nil
}

// waitHolder is H1b's readiness phase: true once the holder answers, false
// after rebootHolderWait (or when ctx ends).
func (j *rebootJudge) waitHolder(ctx context.Context) bool {
	deadline := j.now().Add(rebootHolderWait)
	for {
		if j.holderReady(ctx) {
			return true
		}
		if !j.now().Before(deadline) || j.sleep(ctx, rebootHolderEvery) != nil {
			return false
		}
	}
}

// stop is §3.2's stop: the dispatcher's suspend (an existing one, the
// person's, is left as it is); the services keep running.
func (j *rebootJudge) stop(ctx context.Context, token, reason string) error {
	_, _, err := j.b.FencedSuspend(ctx, token, bus.SuspendSpec{By: "dispatch", Notify: j.escalate(), Reason: reason,
		Host: j.host, Boot: j.boot, Services: j.services()})
	return err
}

// tell is §3.3's one notice per reboot, and for a restart the escalation
// target's directive.
func (j *rebootJudge) tell(ctx context.Context, token string, rec bus.RebootRecord) error {
	p := j.b.Project()
	count := fmt.Sprintf("%d of %d in %s", rec.N, rec.Max, windowText(time.Duration(rec.Window)*time.Millisecond))
	var text string
	switch rec.Verdict {
	case bus.RebootIntentional:
		text = fmt.Sprintf("%s: this machine restarted (a clean restart); the agents of %s are not running. Relaunch: agentbus up %s --resume", p, p, p)
	case bus.RebootSuspended:
		text = fmt.Sprintf("%s was suspended before this machine restarted. Resume: agentbus resume -p %s --yes", p, p)
	case bus.RebootUnplanned:
		why := rec.Why
		if why == "holder not answering" {
			why = "its holder did not answer within 2 minutes of the dispatcher's start"
		}
		text = fmt.Sprintf("%s: this machine stopped unexpectedly, not restarted: %s; the agents of %s are not running. Relaunch: agentbus up %s --resume", p, why, p, p)
	case bus.RebootStopped:
		text = fmt.Sprintf("%s is stopped: %s. Check the repository first (git status, git fsck), then resume: agentbus resume -p %s --yes", p, rec.Why, p)
	case bus.RebootRestarting:
		text = fmt.Sprintf("%s restarted after an unexpected stop of this machine (%s)", p, count)
		if _, err := j.b.FencedCmdOnce(ctx, token, "reboot-told", j.boot, "dispatch", j.escalate(), bus.CmdDirective,
			fmt.Sprintf("restarted after an unplanned stop of this machine (%s): self-repair relaunches the team, master first.", count), false); err != nil {
			return err
		}
	}
	_, err := j.b.FencedNotifyOnceForPerson(ctx, token, "reboot", j.boot, "dispatch", text)
	return err
}

// started writes running for this boot, the judgement recorded (§2.1). A
// failed write is said: at worst the next start judges this boot again.
func (j *rebootJudge) started() error {
	if err := lifecycle.Started(j.dir, j.boot, time.Now()); err != nil {
		fmt.Fprintf(j.log, "dispatch: run record: %v\n", err)
	}
	return nil
}

// windowText is a window as busmon and the notices say it ("24 h").
func windowText(d time.Duration) string {
	if d > 0 && d%time.Hour == 0 {
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return d.String()
}

// serviceRebootJudge is the dispatch service's judge for project: its state
// directory, this host's boot, the machine's settings read at the
// judgement, the current build's holder, the repository in workdir (the
// unit's working directory), and the service's escalation target (master
// when nil or empty).
func serviceRebootJudge(b *bus.Bus, project string, paths service.Paths, getenv func(string) string, workdir string, escalateTo *string, log io.Writer) *rebootJudge {
	host, _ := os.Hostname()
	return &rebootJudge{b: b, dir: paths.StateDir(project), boot: runBootID(), host: host, log: log,
		escalate: func() string {
			if escalateTo != nil && *escalateTo != "" {
				return *escalateTo
			}
			return "master"
		},
		settings: func() rebootSettings {
			r := config.Machine(getenv)
			return rebootSettings{Restart: r.Bool("services.unplanned_restart"), Max: r.Int("services.unplanned_max"),
				Window: r.Duration("services.unplanned_window"), SelfRepair: config.SelfRepairMode(r)}
		},
		holderReady: func(context.Context) bool {
			return holderReady(getenv, project, service.BuildKey(holder.BuildID()), 2*time.Second)
		},
		repo: func(ctx context.Context) string { return repoIntegrity(ctx, workdir, gitRunner(workdir)) },
		services: func() map[string]bool {
			out := map[string]bool{service.Dispatch: true, service.Holder: true, service.Missions: false}
			if m, _ := service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, getenv)), getenv); m != nil {
				if st, err := m.State(project, service.Missions); err == nil {
					out[service.Missions] = st.Active
				}
			}
			return out
		},
		now: time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}}
}
