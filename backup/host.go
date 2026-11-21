package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/brokerrt"
	"github.com/netbja/agent-bus-monitor/internal/textfmt"
	"github.com/netbja/agent-bus-monitor/service"
)

// Moved from cmd/agentbus (dispatchsvc P5): an archive run after its
// checks, the host's projects and the host's backup record, shared by
// agentbus backup and the backup schedule.

type Deps struct {
	Getenv   func(string) string
	Now      func() time.Time
	Host     string // this host's name, in the archive's name
	Dial     func() (*redis.Client, error)
	LookPath func(string) (string, error) // finds age
	Environ  func() []string              // the environment backup.after_command starts from, scrubbed
	// Projects is the host's scope (nil: HostProjects, the broker's and the
	// templates'); tests pin theirs on a shared test broker.
	Projects func(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error)
	// Snapshot takes the broker's RDB for an archive in dir (broker-hardening
	// step 5; RealDeps: RealSnapshot). nil: none taken, nothing said.
	Snapshot func(c *redis.Client, dir string) func(ctx context.Context) (path, why string)
}

func (d Deps) HostProjects(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error) {
	if d.Projects != nil {
		return d.Projects(ctx, c, projectsDir)
	}
	return HostProjects(ctx, c, projectsDir)
}

func RealDeps(redisHost string) Deps {
	host, _ := os.Hostname()
	return Deps{Getenv: os.Getenv, Now: time.Now, Host: host,
		Dial: func() (*redis.Client, error) { return bus.Connect(redisHost) }, LookPath: exec.LookPath, Environ: os.Environ,
		Snapshot: RealSnapshot(os.Getenv, exec.LookPath)}
}

// RealSnapshot snapshots the broker through its container runtime
// (broker.runtime) when that runs it here, else from its own data directory.
func RealSnapshot(getenv func(string) string, lookPath func(string) (string, error)) func(c *redis.Client, dir string) func(ctx context.Context) (string, string) {
	return func(c *redis.Client, dir string) func(ctx context.Context) (string, string) {
		return func(ctx context.Context) (string, string) {
			rt := brokerrt.Of(getenv).Bin()
			if _, err := lookPath(rt); err != nil {
				rt = ""
			}
			output := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return exec.CommandContext(ctx, name, args...).Output()
			}
			return NewSnapshotter(c, rt, output, dir).Take(ctx)
		}
	}
}

// Job is one archive to make, by hand or by the schedule.
type Job struct {
	Projects []string
	Dir      string
	Now      time.Time
	Files    []Source
	Left     []LeftOut
	Notes    []string
	// Secrets, when not nil, are sealed with Age to Recipient into secrets.age.
	Secrets        []Source
	Recipient, Age string
	Scheduled      bool
}

// MakeArchive is a run of agentbus backup after its checks: the archive,
// then backup.after_command (after the rename, before the retention; its
// failure only warns and never touches the archive), then the retention.
// An error means no archive was written.
func MakeArchive(ctx context.Context, c *redis.Client, d Deps, r config.Resolver, paths service.Paths, j Job, out, errw io.Writer) (Result, error) {
	b, err := bus.Open(c, j.Projects[0])
	if err != nil {
		return Result{}, err
	}
	info, err := b.RedisInfo(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("the broker: %w", err)
	}
	notes := j.Notes
	var sealed []byte
	if j.Secrets != nil {
		if sealed, err = SealSecrets(ctx, j.Age, j.Recipient, j.Secrets); err != nil {
			return Result{}, fmt.Errorf("secrets: %w", err)
		}
		names := make([]string, len(j.Secrets))
		for i, s := range j.Secrets {
			names[i] = s.Member
		}
		notes = append(notes, fmt.Sprintf("%s: %s, encrypted to %s", SecretsName, strings.Join(names, ", "), j.Recipient))
	}
	var snapshot func(ctx context.Context) (string, string)
	if d.Snapshot != nil {
		snapshot = d.Snapshot(c, j.Dir)
	}
	res, err := Run(ctx, j.Dir, Plan{
		Projects: j.Projects, Host: d.Host, Now: j.Now, Version: buildinfo.Version, RedisVersion: info.Version, Server: info.Product,
		Dump:  func(ctx context.Context, p string) (bus.ProjectDump, error) { return bus.DumpProject(ctx, c, p) },
		Files: j.Files, LeftOut: j.Left, Notes: notes, Secrets: sealed, Snapshot: snapshot,
	})
	if err != nil {
		return Result{}, err
	}
	fmt.Fprintf(out, "backup: %s\n", res.Path)
	for _, p := range j.Projects {
		fmt.Fprintf(out, "  %s: %s, the broker held for %s\n", p, textfmt.Count(res.Keys[p], "key"), res.Exec[p].Round(time.Microsecond))
	}
	switch {
	case res.Snapshot > 0:
		fmt.Fprintf(out, "  %s: the broker's snapshot, %s (restored by hand: see the operator notes)\n", SnapshotMember, textfmt.Count(int(res.Snapshot), "byte"))
	case res.SnapshotWhy != "":
		fmt.Fprintf(out, "  left out: %s (%s); the archive is logical only\n", SnapshotMember, res.SnapshotWhy)
	}
	for _, l := range j.Left {
		fmt.Fprintf(out, "  left out: %s (%s)\n", l.Path, l.Why)
	}
	for _, n := range notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
	if j.Secrets != nil {
		fmt.Fprintf(out, "  %s opens only with the private key of %s: keep it where you keep your other keys, not on this disk\n", SecretsName, j.Recipient)
	}
	if after := r.String("backup.after_command"); after != "" {
		var allow []string
		if a := r.String("backup.after_env"); a != "" {
			allow = strings.Split(a, ",")
		}
		environ := os.Environ
		if d.Environ != nil {
			environ = d.Environ
		}
		ran := RunAfter(ctx, AfterSpec{Command: after, Dir: j.Dir, Archive: res.Path, Projects: j.Projects,
			Scheduled: j.Scheduled, Env: environ(), Allow: allow, Timeout: r.Duration("backup.after_timeout"), Log: errw})
		if err := RecordAfter(LastPath(paths), j.Projects, AfterEntry{At: ran.At, Archive: res.Name, Outcome: ran.Outcome}); err != nil {
			fmt.Fprintf(errw, "backup: recording backup.after_command's outcome: %v\n", err)
		}
		if ran.Outcome == "ok" {
			fmt.Fprintln(out, "  backup.after_command: ok")
		} else {
			fmt.Fprintf(errw, "backup: warning: backup.after_command: %s (the archive is complete and kept)\n", ran.Outcome)
		}
	}
	removed, err := Retain(j.Dir, d.Host, r.Int("backup.keep"))
	for _, n := range removed {
		fmt.Fprintf(out, "  removed %s (backup.keep = %d)\n", n, r.Int("backup.keep"))
	}
	if err != nil {
		fmt.Fprintf(errw, "backup: retention: %v (the archive is complete)\n", err)
	}
	return res, nil
}

// HostProjects is every project of this host: the broker's and every
// template's, sorted, once each.
func HostProjects(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error) {
	ps, err := bus.Projects(ctx, c)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range ps {
		out = append(out, p.Project)
	}
	tpls, _ := filepath.Glob(filepath.Join(projectsDir, "*.toml"))
	for _, t := range tpls {
		if name := strings.TrimSuffix(filepath.Base(t), ".toml"); bus.ValidName(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// LastPath is <state>/backup-last.json, the host's backup record:
// $XDG_STATE_HOME/agentbus (else ~/.local/state/agentbus).
func LastPath(p service.Paths) string {
	return filepath.Join(p.StateHome, "agentbus", "backup-last.json")
}
