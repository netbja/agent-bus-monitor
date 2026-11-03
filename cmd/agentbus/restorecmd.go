package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
)

// agentbus restore (spec docs/superpowers/specs/2026-11-01-backup-design.md,
// "Restore"): the person's act, never scheduled. Every member is verified
// first; a live project is never restored, with or without --force.

const restoreUsage = "usage: agentbus restore <archive> [--project <p>]... [--redis-only | --files-only] [--with-runtime] [--identity <age key file>] [--dry-run] [--force [--yes]]"

type restoreDeps struct {
	Getenv    func(string) string
	Dial      func() (*redis.Client, error)
	Herdr     func(args ...string) ([]byte, error)
	Services  func() (service.Manager, string) // the manager, or why there is none
	ProcStart func(pid int) (uint64, error)
	Lock      func(project string) (func(), error)
	Stdin     io.Reader
	TTY       bool                         // stdin is a terminal: --force may ask
	LookPath  func(string) (string, error) // finds age, for --identity
}

func realRestoreDeps(redisHost string) restoreDeps {
	return restoreDeps{
		Getenv: os.Getenv,
		Dial:   func() (*redis.Client, error) { return bus.Connect(redisHost) },
		Herdr: func(args ...string) ([]byte, error) {
			return exec.Command(herdrBin(os.Getenv), args...).Output()
		},
		Services: func() (service.Manager, string) {
			return service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
		},
		ProcStart: dispatch.ProcStart,
		Lock: func(project string) (func(), error) {
			return upLock(upCacheDir(os.Getenv))(project, false)
		},
		Stdin:    os.Stdin,
		TTY:      setup.IsTerminal(os.Stdin),
		LookPath: exec.LookPath,
	}
}

func runRestore(ctx context.Context, args []string, explicitProject string, d restoreDeps, out, errw io.Writer) int {
	usage := func(format string, a ...any) int {
		if format != "" {
			fmt.Fprintf(errw, "restore: "+format+"\n", a...)
		}
		fmt.Fprintln(errw, restoreUsage)
		return 2
	}
	var o backup.RestoreOptions
	args, o.RedisOnly = extractBool(args, "--redis-only")
	args, o.FilesOnly = extractBool(args, "--files-only")
	args, o.WithRuntime = extractBool(args, "--with-runtime")
	args, o.DryRun = extractBool(args, "--dry-run")
	args, o.Force = extractBool(args, "--force")
	args, yes := extractBool(args, "--yes")
	args, o.Identity = extractFlag(args, "--identity")
	if explicitProject != "" {
		o.Projects = append(o.Projects, explicitProject)
	}
	for {
		var p string
		if args, p = extractFlag(args, "--project"); p == "" {
			break
		}
		o.Projects = append(o.Projects, p)
	}
	switch {
	case len(args) != 1 || strings.HasPrefix(args[0], "-"):
		return usage("")
	case o.RedisOnly && o.FilesOnly:
		return usage("--redis-only and --files-only exclude each other")
	case yes && !o.Force:
		return usage("--yes answers --force's question: it needs --force")
	}
	for _, p := range o.Projects {
		if !bus.ValidName(p) {
			return usage("invalid project %q", p)
		}
	}
	o.Projects = sortedUnique(o.Projects)
	var age string
	if o.Identity != "" {
		var err error
		if age, err = d.LookPath("age"); err != nil {
			fmt.Fprintln(errw, "restore: --identity needs age on PATH (https://age-encryption.org); nothing written")
			return 1
		}
	}
	c, err := d.Dial()
	if err != nil {
		fmt.Fprintf(errw, "restore: the broker: %v\n", err)
		return 1
	}
	paths, err := service.PathsFrom(d.Getenv)
	if err != nil {
		fmt.Fprintf(errw, "restore: %v\n", err)
		return 1
	}
	// The services' state must be known (reviewer-codex): no manager found
	// refuses, unless AGENTBUS_SERVICE_MANAGER=none says this host runs none.
	mgr, noMgr := d.Services()
	if mgr == nil && d.Getenv("AGENTBUS_SERVICE_MANAGER") != "none" {
		noMgr = "no service manager to ask (" + noMgr + "): stop the project's services first (agentbus down <p>), " +
			"or set AGENTBUS_SERVICE_MANAGER=none if this host runs none"
	} else {
		noMgr = ""
	}
	in := bufio.NewReader(d.Stdin)
	rep, err := backup.Restore(ctx, args[0], o, backup.RestoreDeps{
		Broker: restoreBroker{c},
		Live: func(ctx context.Context, p string) (string, error) {
			if noMgr != "" {
				return "", errors.New(noMgr)
			}
			return projectLive(ctx, c, p, d, mgr)
		},
		Lock: d.Lock,
		Confirm: func(p string, keys int) bool {
			if yes {
				return true
			}
			if !d.TTY {
				fmt.Fprintf(errw, "restore: --force on %s needs a terminal to ask, or --yes\n", p)
				return false
			}
			fmt.Fprintf(errw, "restore: replace %s of %s%s? [y/N] ", plural(keys, "key"), p, projectFacts(ctx, c, p))
			line, _ := in.ReadString('\n')
			return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
		},
		Loc: backup.Locations{
			ConfigDir:    filepath.Dir(config.Path(d.Getenv)),
			ProjectsDir:  rolecfg.ProjectsDir(d.Getenv),
			PasswordFile: bus.PasswordFile(d.Getenv),
			EnvFile:      paths.EnvFile,
			StateDir:     paths.StateDir,
		},
		Age: age,
	}, out)
	if err != nil {
		fmt.Fprintf(errw, "restore: %v\n", err)
		return 1
	}
	if rep.Refused > 0 {
		return 1
	}
	return 0
}

// restoreBroker is backup.Broker on the broker.
type restoreBroker struct{ c *redis.Client }

func (r restoreBroker) Version(ctx context.Context) (string, error) {
	b, err := bus.Open(r.c, "restore") // RedisInfo reads no project key
	if err != nil {
		return "", err
	}
	info, err := b.RedisInfo(ctx)
	return info.Version, err
}

func (r restoreBroker) Keys(ctx context.Context, p string) ([]string, error) {
	return bus.ProjectKeys(ctx, r.c, p)
}

func (r restoreBroker) Restore(ctx context.Context, p string, keys []bus.DumpedKey, del []string) (int, error) {
	return bus.RestoreProject(ctx, r.c, p, keys, del)
}

// projectLive says why p is live, whatever its keys (spec: an empty keyspace
// is no proof of a dead project): its dispatch or conductor lease held, a
// service active, its herdr workspace open, or a registered agent's verified
// process alive. A read that fails is an error: the restore refuses.
func projectLive(ctx context.Context, c *redis.Client, p string, d restoreDeps, mgr service.Manager) (string, error) {
	for _, k := range []string{bus.DispatchKey(p), bus.ConductorKey(p)} {
		n, err := c.Exists(ctx, k).Result()
		if err != nil {
			return "", err
		}
		if n > 0 {
			return k + " (a lease) is held", nil
		}
	}
	if mgr != nil {
		for _, kind := range service.Kinds {
			st, err := mgr.State(p, kind)
			if err != nil {
				return "", fmt.Errorf("its %s service: %w", kind, err)
			}
			if st.Active {
				return "its " + kind + " service is active (agentbus down " + p + ")", nil
			}
		}
	}
	raw, err := d.Herdr("workspace", "list")
	if err != nil {
		return "", fmt.Errorf("herdr workspace list: %w (start herdr, or close nothing: the check cannot be skipped)", err)
	}
	var ws struct {
		Result struct {
			Workspaces []struct {
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &ws); err != nil {
		return "", fmt.Errorf("herdr workspace list: %w", err)
	}
	for _, w := range ws.Result.Workspaces {
		if w.Label == p {
			return "its herdr workspace is open", nil
		}
	}
	b, err := bus.Open(c, p)
	if err != nil {
		return "", err
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return "", err
	}
	for name, s := range snaps {
		if s.PID == 0 {
			continue
		}
		start, err := d.ProcStart(s.PID)
		switch {
		case err == nil && start == s.PIDStart:
			return fmt.Sprintf("agent %s's verified pid %d is alive", name, s.PID), nil
		case err != nil && !pidListedNoMore(err):
			return "", fmt.Errorf("agent %s's pid %d: %w", name, s.PID, err)
		}
	}
	return "", nil
}

// projectFacts is what --force replaces, as busmon --delete says it: agents
// and last activity.
func projectFacts(ctx context.Context, c *redis.Client, p string) string {
	ps, err := bus.Projects(ctx, c)
	if err != nil {
		return ""
	}
	for _, s := range ps {
		if s.Project == p {
			last := "never"
			if s.LastTS > 0 {
				last = time.UnixMilli(s.LastTS).Local().Format("2006-01-02 15:04")
			}
			return fmt.Sprintf(" (%s, last activity %s)", plural(s.Agents, "agent"), last)
		}
	}
	return ""
}

// pidListedNoMore: a pid's start could not be read because /proc, readable
// here, lists no such pid. Any other error (no /proc, a failed read) proves
// nothing: the project is refused.
func pidListedNoMore(err error) bool {
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, serr := os.Stat("/proc/self/stat")
	return serr == nil
}
