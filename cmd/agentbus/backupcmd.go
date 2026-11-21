package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
	"github.com/netbja/agent-bus-monitor/internal/textfmt"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus backup (spec docs/superpowers/specs/2026-11-01-backup-design.md):
// one archive of every project of this host (the broker's and every
// template's), or of the projects named on the command line; never narrowed
// by the ambient AGENT_BUS_PROJECT. Secrets are left out and listed, or with
// --secrets encrypted with age to a recipient (never in clear).

const backupUsage = "usage: agentbus backup [--project <p>]... [--dir <d>] [--secrets [--recipient <age1...>]] [--dry-run] | backup list [--dir <d>]"

func runBackup(ctx context.Context, args []string, explicitProject string, d backup.Deps, out, errw io.Writer) int {
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
	} else if why := agentcaller.Refuse(ctx, d.Getenv, nil, "a backup", "agentbus backup"); why != "" {
		// the person's act (broker-hardening step 4: backup connects as
		// admin); defence in depth, as run reset. list reads local files only.
		fmt.Fprintln(errw, "backup: "+why)
		return 1
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
		if projects, err = d.HostProjects(ctx, c, projectsDir); err != nil {
			fmt.Fprintf(errw, "backup: %v\n", err)
			return 1
		}
	}
	slices.Sort(projects)
	projects = slices.Compact(projects)
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
	job := backup.Job{Projects: projects, Dir: dir, Now: now, Files: files, Left: left, Notes: notes}
	if secrets {
		job.Secrets, job.Recipient, job.Age = secretSrcs, recipient, age
	}
	if _, defaulted, _ := config.BackupDir(r, d.Getenv); defaulted && dirArg == "" && r.String("backup.schedule") == "daily" {
		fmt.Fprintf(errw, "backup: warning: backup.dir is the default (%s) while backup.schedule = daily: the service's environment may resolve another folder; set backup.dir in config.toml\n", dir)
	}
	if _, err := backup.MakeArchive(ctx, c, d, r, paths, job, out, errw); err != nil {
		fmt.Fprintf(errw, "backup: %v (no archive written)\n", err)
		return 1
	}
	return 0
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
		fmt.Fprintf(out, "  %s: %s on the broker (any with an expiry is skipped)\n", p, textfmt.Count(len(keys), "key"))
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
