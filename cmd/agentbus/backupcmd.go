package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus backup (spec docs/superpowers/specs/2026-11-01-backup-design.md):
// one archive of every project of this host (the broker's and every
// template's), or of the projects named on the command line; never narrowed
// by the ambient AGENT_BUS_PROJECT. Secrets are left out and listed, or with
// --secrets encrypted with age to a recipient (never in clear).

const backupUsage = "usage: agentbus backup [--project <p>]... [--dir <d>] [--secrets [--recipient <age1...>]] [--dry-run] | backup list [--dir <d>]"

type backupDeps struct {
	Getenv   func(string) string
	Now      func() time.Time
	Host     string // this host's name, in the archive's name
	Dial     func() (*redis.Client, error)
	LookPath func(string) (string, error) // finds age
	Environ  func() []string              // the environment backup.after_command starts from, scrubbed
	// Projects is the host's scope (nil: hostProjects, the broker's and the
	// templates'); tests pin theirs on a shared test broker.
	Projects func(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error)
}

func (d backupDeps) hostProjects(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error) {
	if d.Projects != nil {
		return d.Projects(ctx, c, projectsDir)
	}
	return hostProjects(ctx, c, projectsDir)
}

func realBackupDeps(redisHost string) backupDeps {
	host, _ := os.Hostname()
	return backupDeps{Getenv: os.Getenv, Now: time.Now, Host: host,
		Dial: func() (*redis.Client, error) { return bus.Connect(redisHost) }, LookPath: exec.LookPath, Environ: os.Environ}
}

func runBackup(ctx context.Context, args []string, explicitProject string, d backupDeps, out, errw io.Writer) int {
	usage := func(format string, a ...any) int {
		if format != "" {
			fmt.Fprintf(errw, "backup: "+format+"\n", a...)
		}
		fmt.Fprintln(errw, backupUsage)
		return 2
	}
	list := len(args) > 0 && args[0] == "list"
	if list {
		args = args[1:]
	}
	args, dirArg := extractFlag(args, "--dir")
	args, dry := extractBool(args, "--dry-run")
	args, secrets := extractBool(args, "--secrets")
	args, recipient := extractFlag(args, "--recipient")
	var projects []string
	if explicitProject != "" {
		projects = append(projects, explicitProject)
	}
	for {
		var p string
		if args, p = extractFlag(args, "--project"); p == "" {
			break
		}
		projects = append(projects, p)
	}
	if len(args) != 0 {
		return usage("unexpected %q", args[0])
	}
	for _, p := range projects {
		if !bus.ValidName(p) {
			return usage("invalid project %q", p)
		}
	}
	if dirArg != "" && !filepath.IsAbs(dirArg) {
		return usage("--dir: want an absolute folder")
	}
	if recipient != "" && (!secrets || !backup.ValidRecipient(recipient)) {
		return usage("--recipient takes an age public key (age1...) and goes with --secrets")
	}
	r := config.Machine(d.Getenv)
	dir, _, err := config.BackupDir(r, d.Getenv)
	if dirArg != "" {
		dir, err = dirArg, nil
	}
	if err != nil {
		fmt.Fprintf(errw, "backup: %v\n", err)
		return 1
	}
	if list {
		if dry || secrets || len(projects) > 0 {
			return usage("list takes only --dir")
		}
		return backupList(dir, d.Host, out, errw)
	}
	// --secrets is refused before anything is read or written without age or
	// a recipient (spec "Secrets").
	var age string
	if secrets {
		if recipient == "" {
			recipient = r.String("backup.age_recipient")
		}
		if recipient == "" {
			fmt.Fprintln(errw, "backup: --secrets needs an age recipient: --recipient <age1...> or backup.age_recipient in config.toml; nothing written")
			return 1
		}
		if age, err = d.LookPath("age"); err != nil {
			fmt.Fprintln(errw, "backup: --secrets needs age on PATH (https://age-encryption.org); nothing written")
			return 1
		}
	}

	c, err := d.Dial()
	if err != nil {
		fmt.Fprintf(errw, "backup: the broker: %v\n", err)
		return 1
	}
	projectsDir := rolecfg.ProjectsDir(d.Getenv)
	if len(projects) == 0 {
		if projects, err = d.hostProjects(ctx, c, projectsDir); err != nil {
			fmt.Fprintf(errw, "backup: %v\n", err)
			return 1
		}
	}
	projects = sortedUnique(projects)
	if len(projects) == 0 {
		fmt.Fprintln(errw, "backup: no project on this host (none on the broker, no template)")
		return 1
	}
	paths, err := service.PathsFrom(d.Getenv)
	if err != nil {
		fmt.Fprintf(errw, "backup: %v\n", err)
		return 1
	}
	files, left, notes, err := backup.Gather(projects, backup.Locations{
		ConfigDir:    filepath.Dir(config.Path(d.Getenv)),
		ProjectsDir:  projectsDir,
		PasswordFile: bus.PasswordFile(d.Getenv),
		EnvFile:      paths.EnvFile,
		StateDir:     paths.StateDir,
	})
	if err != nil {
		fmt.Fprintf(errw, "backup: %v (no archive written)\n", err)
		return 1
	}
	notes = append(notes, "Redis: each project is read at one instant after its SCAN; a key created after the SCAN is not in the archive, and keys with an expiry are skipped")
	now := d.Now().UTC()
	var secretSrcs []backup.Source
	if secrets {
		secretSrcs = backup.Secrets(projects, backup.Locations{PasswordFile: bus.PasswordFile(d.Getenv), EnvFile: paths.EnvFile})
		left = nil // encrypted, not left out
	}
	after := r.String("backup.after_command")
	if dry {
		if after != "" {
			fmt.Fprintf(out, "  would run backup.after_command: %s (in %s, after the archive is complete)\n", after, dir)
		}
		for _, s := range secretSrcs {
			fmt.Fprintf(out, "  %s: would be encrypted to %s in %s\n", s.Member, recipient, backup.SecretsName)
		}
		return backupDryRun(ctx, c, dir, backup.ArchiveName(d.Host, now), projects, files, left, notes, out, errw)
	}
	job := archiveJob{Projects: projects, Dir: dir, Now: now, Files: files, Left: left, Notes: notes}
	if secrets {
		job.Secrets, job.Recipient, job.Age = secretSrcs, recipient, age
	}
	if _, defaulted, _ := config.BackupDir(r, d.Getenv); defaulted && dirArg == "" && r.String("backup.schedule") == "daily" {
		fmt.Fprintf(errw, "backup: warning: backup.dir is the default (%s) while backup.schedule = daily: the service's environment may resolve another folder; set backup.dir in config.toml\n", dir)
	}
	if _, err := makeArchive(ctx, c, d, r, paths, job, out, errw); err != nil {
		fmt.Fprintf(errw, "backup: %v (no archive written)\n", err)
		return 1
	}
	return 0
}

// archiveJob is one archive to make, by hand or by the schedule.
type archiveJob struct {
	Projects []string
	Dir      string
	Now      time.Time
	Files    []backup.Source
	Left     []backup.LeftOut
	Notes    []string
	// Secrets, when not nil, are sealed with Age to Recipient into secrets.age.
	Secrets        []backup.Source
	Recipient, Age string
	Scheduled      bool
}

// makeArchive is a run of agentbus backup after its checks: the archive,
// then backup.after_command (after the rename, before the retention; its
// failure only warns and never touches the archive), then the retention.
// An error means no archive was written.
func makeArchive(ctx context.Context, c *redis.Client, d backupDeps, r config.Resolver, paths service.Paths, j archiveJob, out, errw io.Writer) (backup.Result, error) {
	b, err := bus.Open(c, j.Projects[0])
	if err != nil {
		return backup.Result{}, err
	}
	info, err := b.RedisInfo(ctx)
	if err != nil {
		return backup.Result{}, fmt.Errorf("the broker: %w", err)
	}
	notes := j.Notes
	var sealed []byte
	if j.Secrets != nil {
		if sealed, err = backup.SealSecrets(ctx, j.Age, j.Recipient, j.Secrets); err != nil {
			return backup.Result{}, fmt.Errorf("secrets: %w", err)
		}
		names := make([]string, len(j.Secrets))
		for i, s := range j.Secrets {
			names[i] = s.Member
		}
		notes = append(notes, fmt.Sprintf("%s: %s, encrypted to %s", backup.SecretsName, strings.Join(names, ", "), j.Recipient))
	}
	res, err := backup.Run(ctx, j.Dir, backup.Plan{
		Projects: j.Projects, Host: d.Host, Now: j.Now, Version: buildinfo.Version, RedisVersion: info.Version,
		Dump:  func(ctx context.Context, p string) (bus.ProjectDump, error) { return bus.DumpProject(ctx, c, p) },
		Files: j.Files, LeftOut: j.Left, Notes: notes, Secrets: sealed,
	})
	if err != nil {
		return backup.Result{}, err
	}
	fmt.Fprintf(out, "backup: %s\n", res.Path)
	for _, p := range j.Projects {
		fmt.Fprintf(out, "  %s: %s, the broker held for %s\n", p, plural(res.Keys[p], "key"), res.Exec[p].Round(time.Microsecond))
	}
	for _, l := range j.Left {
		fmt.Fprintf(out, "  left out: %s (%s)\n", l.Path, l.Why)
	}
	for _, n := range notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
	if j.Secrets != nil {
		fmt.Fprintf(out, "  %s opens only with the private key of %s: keep it where you keep your other keys, not on this disk\n", backup.SecretsName, j.Recipient)
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
		ran := backup.RunAfter(ctx, backup.AfterSpec{Command: after, Dir: j.Dir, Archive: res.Path, Projects: j.Projects,
			Scheduled: j.Scheduled, Env: environ(), Allow: allow, Timeout: r.Duration("backup.after_timeout"), Log: errw})
		if err := backup.RecordAfter(backupLastPath(paths), j.Projects, backup.AfterEntry{At: ran.At, Archive: res.Name, Outcome: ran.Outcome}); err != nil {
			fmt.Fprintf(errw, "backup: recording backup.after_command's outcome: %v\n", err)
		}
		if ran.Outcome == "ok" {
			fmt.Fprintln(out, "  backup.after_command: ok")
		} else {
			fmt.Fprintf(errw, "backup: warning: backup.after_command: %s (the archive is complete and kept)\n", ran.Outcome)
		}
	}
	removed, err := backup.Retain(j.Dir, d.Host, r.Int("backup.keep"))
	for _, n := range removed {
		fmt.Fprintf(out, "  removed %s (backup.keep = %d)\n", n, r.Int("backup.keep"))
	}
	if err != nil {
		fmt.Fprintf(errw, "backup: retention: %v (the archive is complete)\n", err)
	}
	return res, nil
}

// hostProjects is every project of this host: the broker's and every
// template's, sorted, once each.
func hostProjects(ctx context.Context, c *redis.Client, projectsDir string) ([]string, error) {
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
	return sortedUnique(out), nil
}

func backupDryRun(ctx context.Context, c *redis.Client, dir, name string, projects []string, files []backup.Source,
	left []backup.LeftOut, notes []string, out, errw io.Writer) int {
	fmt.Fprintf(out, "would write %s (nothing is written by --dry-run)\n", filepath.Join(dir, name))
	for _, p := range projects {
		keys, err := bus.ProjectKeys(ctx, c, p)
		if err != nil {
			fmt.Fprintf(errw, "backup: %s: %v\n", p, err)
			return 1
		}
		fmt.Fprintf(out, "  %s: %s on the broker (any with an expiry is skipped)\n", p, plural(len(keys), "key"))
	}
	for _, f := range files {
		if st, err := os.Stat(f.Path); err == nil {
			fmt.Fprintf(out, "  %s (%d bytes, from %s)\n", f.Member, st.Size(), f.Path)
		}
	}
	for _, l := range left {
		fmt.Fprintf(out, "  left out: %s (%s)\n", l.Path, l.Why)
	}
	for _, n := range notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
	return 0
}

func backupList(dir, host string, out, errw io.Writer) int {
	list, err := backup.List(dir, host)
	if err != nil {
		fmt.Fprintf(errw, "backup list: %v\n", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintf(out, "no archive of this host in %s\n", dir)
		return 0
	}
	fmt.Fprintf(out, "%s\n", dir)
	for _, a := range list {
		size := "?"
		if st, err := os.Stat(a.Path); err == nil {
			size = fmt.Sprintf("%d bytes", st.Size())
		}
		covers := strings.Join(a.Coverage.Projects, ", ")
		if a.Err != nil {
			covers = "unreadable: " + a.Err.Error()
		}
		fmt.Fprintf(out, "  %s  %s  %s  covers %s\n", a.Name, a.At.Local().Format("2006-01-02 15:04:05"), size, covers)
	}
	return 0
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func sortedUnique(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// backupLastPath is <state>/backup-last.json, the host's backup record:
// $XDG_STATE_HOME/agentbus (else ~/.local/state/agentbus).
func backupLastPath(p service.Paths) string {
	return filepath.Join(p.StateHome, "agentbus", "backup-last.json")
}
