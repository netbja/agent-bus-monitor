package main

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/service"
)

// backupCheckDeps are the backup check's reads (backup spec, "Sentinel and
// health"): the settings through config.Machine, the folder's archives of
// this host, and <state>/backup-last.json.
type backupCheckDeps struct {
	Getenv func(string) string
	Host   string
	Now    func() time.Time
}

func realBackupCheckDeps() backupCheckDeps {
	host, _ := os.Hostname()
	return backupCheckDeps{Getenv: os.Getenv, Host: host, Now: time.Now}
}

// backupCheck judges project with health.EvaluateBackup; a folder or record
// it cannot read is unknown, never ok.
func backupCheck(project string, d backupCheckDeps) func(context.Context) (health.Outcome, error) {
	return func(context.Context) (health.Outcome, error) {
		r := config.Machine(d.Getenv)
		in := health.BackupInput{Project: project, Now: d.Now(), WarnAfter: r.Duration("backup.warn_after"),
			Daily: r.String("backup.schedule") == "daily", AfterConfigured: r.String("backup.after_command") != ""}
		dir, _, err := config.BackupDir(r, d.Getenv)
		if err != nil {
			in.DirErr = err.Error()
			return health.EvaluateBackup(in), nil
		}
		in.Dir = dir
		list, err := backup.List(dir, d.Host)
		if err != nil {
			in.DirErr = err.Error()
			return health.EvaluateBackup(in), nil
		}
		for _, a := range list {
			if a.Err == nil && slices.Contains(a.Coverage.Projects, project) {
				in.Archives = append(in.Archives, health.BackupArchive{At: a.At, Secrets: a.Coverage.Secrets})
			}
		}
		paths, err := service.PathsFrom(d.Getenv)
		if err != nil {
			in.RecordErr = err.Error()
			return health.EvaluateBackup(in), nil
		}
		last, err := backup.ReadLast(backupLastPath(paths))
		if err != nil {
			in.RecordErr = err.Error()
			return health.EvaluateBackup(in), nil
		}
		if s := last.Scheduled; s != nil {
			in.Scheduled = &health.BackupAttempt{At: s.At, Projects: s.Projects, Error: s.Error, SecretsError: s.SecretsError}
		}
		if a, ok := last.After[project]; ok {
			in.After = &health.BackupAfter{At: a.At, Outcome: a.Outcome}
		}
		return health.EvaluateBackup(in), nil
	}
}
