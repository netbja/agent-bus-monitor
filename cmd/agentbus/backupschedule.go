package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// The daily backup (backup spec, "Schedule"): the dispatch service's chore
// calls scheduledBackupPass every hour while it holds its lease. With
// backup.schedule = daily it makes one archive of every project of this
// host when any of them has no archive covering it younger than a day, under
// a host-local try-lock, the coverage judged again once the lock is held:
// every project's service runs the chore, one archive is made.

// backupDueAfter is how old the newest archive covering a project may be.
const backupDueAfter = 24 * time.Hour

func serviceBackup(log io.Writer) dispatch.BackupConfig {
	d := realBackupDeps("")
	return dispatch.BackupConfig{Pass: func(ctx context.Context) error { return scheduledBackupPass(ctx, d, log) }}
}

func scheduledBackupPass(ctx context.Context, d backupDeps, log io.Writer) error {
	r := config.Machine(d.Getenv)
	if r.String("backup.schedule") != "daily" {
		return nil
	}
	dir, _, err := config.BackupDir(r, d.Getenv)
	if err != nil {
		return err
	}
	paths, err := service.PathsFrom(d.Getenv)
	if err != nil {
		return err
	}
	c, err := d.Dial()
	if err != nil {
		return fmt.Errorf("the broker: %w", err)
	}
	projectsDir := rolecfg.ProjectsDir(d.Getenv)
	projects, err := d.hostProjects(ctx, c, projectsDir)
	if err != nil || len(projects) == 0 {
		return err
	}
	now := d.Now().UTC()
	if due, err := backupDue(dir, d.Host, projects, now); err != nil || !due {
		return err
	}
	unlock, err := backupLock(filepath.Join(paths.StateHome, "agentbus"))
	if errors.Is(err, errBackupLocked) {
		return nil // another project's chore is making it
	}
	if err != nil {
		return err
	}
	defer unlock()
	if due, err := backupDue(dir, d.Host, projects, now); err != nil || !due {
		return err // made by the chore that held the lock before
	}
	run := backup.ScheduledRun{At: now, Projects: projects}
	record := func() {
		if err := backup.RecordScheduled(backupLastPath(paths), run); err != nil {
			fmt.Fprintf(log, "backup: recording the scheduled run: %v\n", err)
		}
	}
	files, left, notes, err := backup.Gather(projects, backup.Locations{
		ConfigDir: filepath.Dir(config.Path(d.Getenv)), ProjectsDir: projectsDir,
		PasswordFile: bus.PasswordFile(d.Getenv), EnvFile: paths.EnvFile, StateDir: paths.StateDir})
	if err != nil {
		run.Error = err.Error()
		record()
		return err
	}
	job := archiveJob{Projects: projects, Dir: dir, Now: now, Files: files, Left: left, Scheduled: true,
		Notes: append(notes, "made by the daily schedule (backup.schedule = daily)")}
	// Secrets unattended only with a recipient: no private key, no prompt.
	// With one and no age, the archive is still made without them, and the
	// run records the error (a backup without its secrets beats none).
	if recipient := r.String("backup.age_recipient"); recipient != "" {
		if age, err := d.LookPath("age"); err != nil {
			run.SecretsError = "age is not on the service's PATH: the secrets were left out"
		} else {
			job.Secrets, job.Recipient, job.Age = backup.Secrets(projects, backup.Locations{
				PasswordFile: bus.PasswordFile(d.Getenv), EnvFile: paths.EnvFile}), recipient, age
			job.Left = nil
		}
	}
	res, err := makeArchive(ctx, c, d, r, paths, job, log, log)
	if err != nil {
		run.Error = err.Error()
		record()
		return err
	}
	run.Archive, run.Exec = res.Name, map[string]string{}
	for p, e := range res.Exec {
		run.Exec[p] = e.Round(time.Microsecond).String()
	}
	record()
	if run.SecretsError != "" {
		return errors.New(run.SecretsError)
	}
	return nil
}

// backupDue: some project has no archive covering it younger than a day.
func backupDue(dir, host string, projects []string, now time.Time) (bool, error) {
	list, err := backup.List(dir, host)
	if err != nil {
		return false, err
	}
	cov := backup.NewestCovering(list)
	for _, p := range projects {
		if a, ok := cov[p]; !ok || now.Sub(a.At) >= backupDueAfter {
			return true, nil
		}
	}
	return false, nil
}

var errBackupLocked = errors.New("another backup holds the lock")

// backupLock takes <state>/backup.lock without waiting (LOCK_NB), as the
// model refresh's lock: held elsewhere is errBackupLocked.
func backupLock(stateDir string) (func(), error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, "backup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errBackupLocked
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
