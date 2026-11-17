package main

// liveProject is the live check's disposable project on this machine: its
// own name (lc + 6 hex), a working folder of the check's (stable, so a real
// client's trust of it is given once), the team master and coder, started
// by agentbus up on this host's service manager (systemd, launchd, or the
// detached supervisors), and removed at the end: its services and holder,
// its broker keys, its template and state.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/trust"
)

// liveText is the wake's directive: a real agent reads it too.
const liveText = "Holder live check: nothing to do; this message only proves that a wake reaches you."

type liveProject struct {
	project, clients string
	exe              string // this agentbus, installed (up refuses a build cache's)
	work, dir        string // the team's working folder; the check's files (the fake's PATH, its Claude config, logs)
	roles            []string
	environ          []string
	getenv           func(string) string
	paths            service.Paths
	trusted          func(client, dir string) bool
	run              func(ctx context.Context, argv, env []string, out io.Writer) error
	procStart        func(pid int) (uint64, error)
	kill             func(pid int) error
	connect          func() (*redis.Client, error)
	build            string // the holder's build (this binary's)

	r    *redis.Client
	b    *bus.Bus
	host *holder.Host
}

// newLiveProject is the check's project, on this machine.
func newLiveProject(clients string, getenv func(string) string, environ []string) (*liveProject, error) {
	paths, err := service.PathsFrom(getenv)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	var n [3]byte
	rand.Read(n[:])
	root := filepath.Join(paths.StateHome, "agentbus", ".livecheck") // a dot: no project's state directory
	project := "lc" + hex.EncodeToString(n[:])
	return &liveProject{project: project, clients: clients, exe: exe, work: filepath.Join(root, "work"), dir: filepath.Join(root, project),
		roles: []string{"master", "coder"}, environ: environ, getenv: getenv, paths: paths, trusted: trust.Reader(getenv),
		run: func(ctx context.Context, argv, env []string, out io.Writer) error {
			c := exec.CommandContext(ctx, argv[0], argv[1:]...)
			c.Env, c.Stdout, c.Stderr = env, out, out
			return c.Run()
		},
		procStart: dispatch.ProcStart,
		kill:      func(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) },
		connect:   func() (*redis.Client, error) { return bus.Connect("") },
		build:     holder.BuildID()}, nil
}

// withEnv is env with key set to value.
func withEnv(env []string, key, value string) []string {
	out := slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, key+"=") })
	return append(out, key+"="+value)
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func (p *liveProject) Roles() []string { return p.roles }

// Up runs agentbus up for the project, self-repair on; with fake clients,
// agentbus fake-client is `claude` on the project's PATH and the working
// folder trusted in a Claude config of the check's own (up's trust gate
// reads it; the person's is not touched). A real client's trust of the
// folder is the person's to give, once.
func (p *liveProject) Up(ctx context.Context) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(p.work, 0o755); err != nil {
		return err
	}
	env := withEnv(p.environ, config.EnvName("dispatch.self_repair"), "on")
	switch p.clients {
	case "fake":
		bin := filepath.Join(p.dir, "bin")
		if err := os.MkdirAll(bin, 0o700); err != nil {
			return err
		}
		shim := "#!/bin/sh\nexec " + rolecfg.ShellJoin([]string{p.exe, "fake-client", "claude"}) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(shim), 0o700); err != nil {
			return err
		}
		env = withEnv(env, "PATH", bin+string(os.PathListSeparator)+envValue(env, "PATH"))
		cfg := filepath.Join(p.dir, "claude")
		if err := os.MkdirAll(cfg, 0o700); err != nil {
			return err
		}
		key, _ := trust.Key(p.work)
		b, _ := json.Marshal(map[string]any{"projects": map[string]any{key: map[string]bool{"hasTrustDialogAccepted": true}}})
		if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), b, 0o600); err != nil {
			return err
		}
		env = withEnv(env, "CLAUDE_CONFIG_DIR", cfg)
	case "real":
		if !p.trusted("claude-code", p.work) {
			return fmt.Errorf("Claude Code does not trust %s yet: run claude there once (cd %s && claude), accept its question, quit, then run the check again", p.work, p.work)
		}
	default:
		return fmt.Errorf("clients %q: fake or real", p.clients)
	}
	logPath := filepath.Join(p.dir, "up.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	argv := []string{p.exe, "up", p.project, p.work, "--yes", "--no-missions", "--roles", strings.Join(p.roles, ",")}
	if err := p.run(ctx, argv, env, log); err != nil {
		return fmt.Errorf("agentbus up %s: %v (its output: %s)", p.project, err, logPath)
	}
	return p.open()
}

// open connects to the project's broker and holder.
func (p *liveProject) open() error {
	if p.b != nil {
		return nil
	}
	r, err := p.connect()
	if err != nil {
		return err
	}
	b, err := bus.Open(r, p.project)
	if err != nil {
		r.Close()
		return err
	}
	h, err := holder.NewHost(p.project, p.build, p.getenv)
	if err != nil {
		r.Close()
		return err
	}
	p.r, p.b, p.host = r, b, h
	return nil
}

// Down removes the project: its services and holder (which ends its
// agents), its broker keys, its template, its state and the check's files;
// the working folder stays (a real client's trust of it is kept).
func (p *liveProject) Down(ctx context.Context) error {
	var errs []error
	log, err := os.OpenFile(filepath.Join(p.dir, "down.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log = nil
	}
	var out io.Writer = io.Discard
	if log != nil {
		out = log
		defer log.Close()
	}
	if err := p.run(ctx, []string{p.exe, "--project", p.project, "service", "remove"}, p.environ, out); err != nil {
		errs = append(errs, fmt.Errorf("service remove: %v", err))
	}
	if p.r == nil {
		if r, err := p.connect(); err == nil {
			p.r = r
		} else {
			errs = append(errs, err)
		}
	}
	if p.r != nil {
		if _, err := bus.DeleteProject(ctx, p.r, p.project); err != nil {
			errs = append(errs, fmt.Errorf("broker keys: %v", err))
		}
		p.r.Close()
		p.r, p.b = nil, nil
	}
	if err := os.Remove(filepath.Join(rolecfg.ProjectsDir(p.getenv), p.project+".toml")); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	for _, d := range []string{p.paths.StateDir(p.project), p.dir} {
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *liveProject) Agents(ctx context.Context) (map[string]liveAgent, error) {
	snaps, err := p.b.Agents(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := p.b.ResumeRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]liveAgent{}
	for _, r := range p.roles {
		a := liveAgent{PID: snaps[r].PID}
		if rec, ok := recs[r]; ok {
			a.Session, a.At = rec.Session, time.UnixMilli(rec.At)
		}
		out[r] = a
	}
	return out, nil
}

func (p *liveProject) Holder(ctx context.Context) (dispatch.ProcID, error) {
	return p.host.Process(ctx)
}

// Crash kills the holder's process, only while pid still is that process.
func (p *liveProject) Crash(_ context.Context, hp dispatch.ProcID) error {
	if st, err := p.procStart(hp.PID); err != nil || st != hp.Start {
		return fmt.Errorf("process %d is no longer the holder that described itself (start %d, now %d, %v): nothing killed", hp.PID, hp.Start, st, err)
	}
	return p.kill(hp.PID)
}

func (p *liveProject) SwapMark(ctx context.Context, role string) (string, error) {
	evs, err := p.b.SwapLog(ctx, role, 1)
	if err != nil || len(evs) == 0 {
		return "", err
	}
	return evs[0].ID, nil
}

// SwapsSince is role's ledger after mark, oldest first.
func (p *liveProject) SwapsSince(ctx context.Context, role, mark string) ([]bus.SwapEvent, error) {
	evs, err := p.b.SwapLog(ctx, role, 50)
	if err != nil {
		return nil, err
	}
	var out []bus.SwapEvent
	for i := len(evs) - 1; i >= 0; i-- {
		if mark == "" || bus.IDLess(mark, evs[i].ID) {
			out = append(out, evs[i])
		}
	}
	return out, nil
}

// Wake sends role a directive from the other role; the dispatcher wakes it.
func (p *liveProject) Wake(ctx context.Context, role string) (string, error) {
	from := p.roles[0]
	if role == from {
		from = p.roles[1]
	}
	return p.b.Cmd(ctx, from, role, bus.CmdDirective, "", liveText)
}

// Drained: role's group read past id and acknowledged it.
func (p *liveProject) Drained(ctx context.Context, role, id string) (bool, error) {
	o, err := p.b.Obligation(ctx, role, 24*time.Hour)
	if err != nil || !o.GroupExists || bus.IDLess(o.LastDelivered, id) {
		return false, err
	}
	pending, err := p.b.PendingFor(ctx, role, id)
	return err == nil && !pending, err
}

func (p *liveProject) HolderHealth(ctx context.Context) (health.Status, string, error) {
	recs, err := p.b.Health(ctx)
	if err != nil {
		return "", "", err
	}
	r, ok := recs["holder"]
	if !ok {
		return "", "not measured yet", nil
	}
	return r.Status, r.Detail, nil
}

// Resumed: the fake's screen says it resumed session; a real client's
// cannot be read, its record (Agents) is the proof.
func (p *liveProject) Resumed(ctx context.Context, role, session string) (bool, error) {
	if p.clients != "fake" {
		return true, nil
	}
	terms, err := p.host.List(ctx, p.project)
	if err != nil {
		return false, err
	}
	for _, t := range terms {
		if t.Name == role && t.Alive {
			sc, err := p.host.Screen(ctx, t.ID, 0)
			if err != nil {
				return false, err
			}
			return strings.Contains(strings.Join(sc.Lines, "\n"), "resumed "+session), nil
		}
	}
	return false, nil
}

var _ liveWorld = (*liveProject)(nil)
