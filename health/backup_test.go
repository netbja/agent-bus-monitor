package health

import (
	"strings"
	"testing"
	"time"
)

var bkNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func bkIn() BackupInput {
	return BackupInput{Project: "demo", Dir: "/data/backups/agentbus", Now: bkNow, WarnAfter: 7 * 24 * time.Hour}
}

func archiveAt(ago time.Duration, secrets bool) BackupArchive {
	return BackupArchive{At: bkNow.Add(-ago), Secrets: secrets}
}

// The backup check (backup spec, "Sentinel and health"): per project, three
// independent facts; the detail always names the folder read.
func TestEvaluateBackup(t *testing.T) {
	failed := &BackupAttempt{At: bkNow.Add(-2 * time.Hour), Projects: []string{"demo"}, Error: "disk full"}
	noAge := &BackupAttempt{At: bkNow.Add(-2 * time.Hour), Projects: []string{"demo"}, SecretsError: "age is not on the service's PATH"}
	for _, c := range []struct {
		name string
		edit func(*BackupInput)
		want Status
		has  string
	}{
		{"none", func(in *BackupInput) {}, Warn, "no archive covers demo"},
		{"old", func(in *BackupInput) { in.Archives = []BackupArchive{archiveAt(8*24*time.Hour, false)} }, Warn, "8d"},
		{"fresh", func(in *BackupInput) { in.Archives = []BackupArchive{archiveAt(time.Hour, false)} }, OK, ""},
		{"unreadable folder", func(in *BackupInput) { in.DirErr = "permission denied" }, Unknown, "permission denied"},
		{"unreadable record", func(in *BackupInput) { in.RecordErr = "bad json" }, Unknown, "bad json"},
		{"warn_after 0, none", func(in *BackupInput) { in.WarnAfter = 0 }, OK, ""},
		// A fresh earlier success plus a failed scheduled attempt: warn.
		{"failed after a success", func(in *BackupInput) {
			in.Daily, in.Scheduled, in.Archives = true, failed, []BackupArchive{archiveAt(5*time.Hour, false)}
		}, Warn, "disk full"},
		// A newer success, by hand covering the project: ok.
		{"newer archive", func(in *BackupInput) {
			in.Daily, in.Scheduled, in.Archives = true, failed, []BackupArchive{archiveAt(time.Hour, false)}
		}, OK, ""},
		// The age case: warn until an archive with secrets.age.
		{"no age, newer without secrets", func(in *BackupInput) {
			in.Daily, in.Scheduled, in.Archives = true, noAge, []BackupArchive{archiveAt(time.Hour, false)}
		}, Warn, "age"},
		{"no age, newer with secrets", func(in *BackupInput) {
			in.Daily, in.Scheduled, in.Archives = true, noAge, []BackupArchive{archiveAt(time.Hour, true)}
		}, OK, ""},
		// reviewer-codex: an attempt that left the secrets out and also
		// failed clears only with a newer archive holding them.
		{"both errors, newer without secrets", func(in *BackupInput) {
			both := *noAge
			both.Error = "disk full"
			in.Daily, in.Scheduled, in.Archives = true, &both, []BackupArchive{archiveAt(time.Hour, false)}
		}, Warn, "disk full"},
		{"both errors, newer with secrets", func(in *BackupInput) {
			both := *noAge
			both.Error = "disk full"
			in.Daily, in.Scheduled, in.Archives = true, &both, []BackupArchive{archiveAt(time.Hour, true)}
		}, OK, ""},
		// warn_after = 0 never silences a failure.
		{"warn_after 0, failed", func(in *BackupInput) {
			in.Daily, in.Scheduled, in.WarnAfter, in.Archives = true, failed, 0, []BackupArchive{archiveAt(5*time.Hour, false)}
		}, Warn, "disk full"},
		// schedule off: an old failed record is not read.
		{"off, old failure", func(in *BackupInput) {
			in.Scheduled, in.Archives = failed, []BackupArchive{archiveAt(5*time.Hour, false)}
		}, OK, ""},
		// A failed attempt whose scope did not include the project.
		{"failure elsewhere", func(in *BackupInput) {
			other := *failed
			other.Projects = []string{"other"}
			in.Daily, in.Scheduled, in.Archives = true, &other, []BackupArchive{archiveAt(5*time.Hour, false)}
		}, OK, ""},
		// The command: a failure warns whatever the age; emptied, it is not read.
		{"after failed", func(in *BackupInput) {
			in.AfterConfigured, in.After, in.Archives = true, &BackupAfter{At: bkNow.Add(-time.Hour), Outcome: "exit 3"}, []BackupArchive{archiveAt(time.Hour, false)}
		}, Warn, "exit 3"},
		{"after ok", func(in *BackupInput) {
			in.AfterConfigured, in.After, in.Archives = true, &BackupAfter{At: bkNow.Add(-time.Hour), Outcome: "ok"}, []BackupArchive{archiveAt(time.Hour, false)}
		}, OK, ""},
		{"after emptied", func(in *BackupInput) {
			in.After, in.Archives = &BackupAfter{At: bkNow.Add(-time.Hour), Outcome: "timeout"}, []BackupArchive{archiveAt(time.Hour, false)}
		}, OK, ""},
		{"after failed, warn_after 0", func(in *BackupInput) {
			in.WarnAfter, in.AfterConfigured, in.After, in.Archives = 0, true, &BackupAfter{At: bkNow.Add(-time.Hour), Outcome: "timeout"}, []BackupArchive{archiveAt(time.Hour, false)}
		}, Warn, "timeout"},
	} {
		in := bkIn()
		c.edit(&in)
		got := EvaluateBackup(in)
		if got.Status != c.want || !strings.Contains(got.Detail, c.has) || !strings.Contains(got.Detail, "/data/backups/agentbus") {
			t.Errorf("%s: %v %q, want %v with %q and the folder", c.name, got.Status, got.Detail, c.want, c.has)
		}
	}
	if d, ok := ByID("backup"); !ok || d.Place(Warn) != PlaceForYou || d.Command != "agentbus backup" || d.Place(Fail) != PlaceForYou {
		t.Fatalf("definition %+v", d)
	}
}
