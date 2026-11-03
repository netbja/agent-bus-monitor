package health

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The backup check (backup spec, "Sentinel and health"): per project, three
// independent facts, each a warning for the person (agentbus backup):
// no archive covering it younger than backup.warn_after; the latest
// scheduled attempt that covered it failed (only while backup.schedule =
// daily); the latest run of backup.after_command for an archive covering it
// failed. Never a fail: it wakes nobody, sentinel only relays it.

// BackupArchive is one complete archive covering the project.
type BackupArchive struct {
	At      time.Time
	Secrets bool // it holds secrets.age
}

// BackupAttempt is the latest scheduled run (backup-last.json, scheduled).
type BackupAttempt struct {
	At           time.Time
	Projects     []string
	Error        string // no archive was written
	SecretsError string // an archive without its secrets (a recipient, no age)
}

// BackupAfter is the latest run of after_command for the project.
type BackupAfter struct {
	At      time.Time
	Outcome string
}

// BackupInput is everything the check reads for one project.
type BackupInput struct {
	Project         string
	Dir             string          // the folder read, always named in the detail
	DirErr          string          // the folder could not be read: unknown
	RecordErr       string          // backup-last.json could not be read: unknown
	Archives        []BackupArchive // covering the project, any order
	Now             time.Time
	WarnAfter       time.Duration // 0 = the age reminder off
	Daily           bool          // backup.schedule = daily: the attempt is read
	Scheduled       *BackupAttempt
	AfterConfigured bool // backup.after_command is set: its runs are read
	After           *BackupAfter
}

// EvaluateBackup judges one project; unknown when the folder or the record
// cannot be read, ok when nothing warns.
func EvaluateBackup(in BackupInput) Outcome {
	folder := "folder " + in.Dir
	switch {
	case in.DirErr != "":
		return Outcome{Status: Unknown, Detail: folder + ": cannot be read (" + in.DirErr + ")"}
	case in.RecordErr != "":
		return Outcome{Status: Unknown, Detail: folder + "; the backup record cannot be read (" + in.RecordErr + ")"}
	}
	var newest *BackupArchive
	for i := range in.Archives {
		if newest == nil || in.Archives[i].At.After(newest.At) {
			newest = &in.Archives[i]
		}
	}
	var warns []string
	if in.WarnAfter > 0 {
		switch {
		case newest == nil:
			warns = append(warns, "no archive covers "+in.Project)
		case in.Now.Sub(newest.At) >= in.WarnAfter:
			warns = append(warns, fmt.Sprintf("the newest archive covering %s is %s old", in.Project, age(in.Now.Sub(newest.At))))
		}
	}
	if a := in.Scheduled; in.Daily && a != nil && slices.Contains(a.Projects, in.Project) && (a.Error != "" || a.SecretsError != "") {
		// Cleared by a complete archive covering the project made after the
		// failed attempt, by hand or scheduled; for the age case, one that
		// holds the secrets.
		cleared := false
		for _, ar := range in.Archives {
			if ar.At.After(a.At) && (a.Error != "" || ar.Secrets) {
				cleared = true
			}
		}
		if !cleared {
			why := a.Error
			if why == "" {
				why = a.SecretsError
			}
			warns = append(warns, fmt.Sprintf("the scheduled backup of %s failed: %s", a.At.Local().Format("2006-01-02 15:04"), why))
		}
	}
	if in.AfterConfigured && in.After != nil && in.After.Outcome != "ok" {
		warns = append(warns, fmt.Sprintf("backup.after_command failed at %s: %s", in.After.At.Local().Format("2006-01-02 15:04"), in.After.Outcome))
	}
	detail := folder
	if newest != nil {
		detail += fmt.Sprintf("; newest archive covering %s: %s old", in.Project, age(in.Now.Sub(newest.At)))
	}
	if len(warns) > 0 {
		return Outcome{Status: Warn, Detail: folder + "; " + strings.Join(warns, "; ")}
	}
	return Outcome{Status: OK, Detail: detail}
}

// age is a duration as days and hours, or hours and minutes under a day.
func age(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
	return d.Round(time.Minute).String()
}
