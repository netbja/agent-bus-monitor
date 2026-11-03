package config

import (
	"strings"
	"testing"
	"time"
)

// The backup settings (backup spec, "Settings"): backup.dir an absolute
// folder ("" = the default under $XDG_DATA_HOME), backup.keep a count, 0
// keeping every archive; both in group Backup, read by agentbus backup and
// the dispatch service through Machine.
func TestBackupKeys(t *testing.T) {
	dir, ok := Lookup("backup.dir")
	if !ok || dir.Kind != String || dir.Default != "" || dir.Env != "AGENT_BUS_BACKUP_DIR" || dir.Group() != GroupBackup {
		t.Fatalf("backup.dir row %+v, group %q", dir, dir.Group())
	}
	for v, why := range map[any]string{
		"":                       "",
		"/data/backups/agentbus": "",
		"relative/dir":           "want an absolute folder",
		"~/backups":              "want an absolute folder",
		7:                        "want an absolute folder",
	} {
		if _, got := dir.file(v); got != why {
			t.Errorf("backup.dir file %#v: %q, want %q", v, got, why)
		}
	}
	if _, res := dir.env("/x"); res != envUse {
		t.Error("an absolute env dir is not used")
	}
	if _, res := dir.env("rel"); res != envSkip {
		t.Error("a relative env dir is not skipped")
	}
	keep, ok := Lookup("backup.keep")
	if !ok || keep.Kind != Int || keep.Default != 7 || keep.Env != "AGENT_BUS_BACKUP_KEEP" || keep.Group() != GroupBackup {
		t.Fatalf("backup.keep row %+v", keep)
	}
	for v, why := range map[any]string{int64(0): "", int64(3): "", int64(-1): "want a whole number, 0 keeps every archive"} {
		if _, got := keep.file(v); got != why {
			t.Errorf("backup.keep file %#v: %q, want %q", v, got, why)
		}
	}
	p := writeFile(t, "config.toml", "[backup]\ndir = \"/data/backups/agentbus\"\nkeep = 3\n")
	r := Resolver{Getenv: env(nil), Files: []*File{Load(p, "config.toml")}}
	if r.String("backup.dir") != "/data/backups/agentbus" || r.Int("backup.keep") != 3 {
		t.Fatalf("resolved %q %d", r.String("backup.dir"), r.Int("backup.keep"))
	}
}

// BackupDir is backup.dir when set, else $XDG_DATA_HOME/agentbus/backups,
// else ~/.local/share/agentbus/backups; defaulted says it came from the
// default (agentbus backup warns about that while daily is on, step 6).
func TestBackupDir(t *testing.T) {
	for _, c := range []struct {
		env       map[string]string
		file      string
		want      string
		defaulted bool
	}{
		{map[string]string{"HOME": "/h"}, "", "/h/.local/share/agentbus/backups", true},
		{map[string]string{"HOME": "/h", "XDG_DATA_HOME": "/d"}, "", "/d/agentbus/backups", true},
		{map[string]string{"HOME": "/h", "XDG_DATA_HOME": "rel"}, "", "/h/.local/share/agentbus/backups", true},
		{map[string]string{"HOME": "/h"}, "[backup]\ndir = \"/b\"\n", "/b", false},
		{map[string]string{"HOME": "/h", "AGENT_BUS_BACKUP_DIR": "/e"}, "[backup]\ndir = \"/b\"\n", "/e", false},
	} {
		r := Resolver{Getenv: env(c.env)}
		if c.file != "" {
			r.Files = []*File{Load(writeFile(t, "config.toml", c.file), "config.toml")}
		}
		got, defaulted, err := BackupDir(r, env(c.env))
		if err != nil || got != c.want || defaulted != c.defaulted {
			t.Errorf("%v %q: %q %v %v, want %q %v", c.env, c.file, got, defaulted, err, c.want, c.defaulted)
		}
	}
	if _, _, err := BackupDir(Resolver{Getenv: env(nil)}, env(nil)); err == nil {
		t.Error("no HOME and no backup.dir: a folder was invented")
	}
}

func TestBackupAgeRecipient(t *testing.T) {
	k, ok := Lookup("backup.age_recipient")
	if !ok || k.Kind != String || k.Default != "" || k.Group() != GroupBackup {
		t.Fatalf("row %+v", k)
	}
	good := "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq"
	for v, why := range map[any]string{"": "", good: "", "AGE-SECRET-KEY-1ABC": "want an age public key (age1...)", "age1short": "want an age public key (age1...)"} {
		if _, got := k.file(v); got != why {
			t.Errorf("file %#v: %q, want %q", v, got, why)
		}
	}
	if _, res := k.env("AGE-SECRET-KEY-1ABC"); res != envSkip {
		t.Error("a private key in the env was used")
	}
}

// backup.after_command, after_env and after_timeout (spec "After the
// archive"): the person's own, from config.toml alone: no env variable,
// never a project's services env file (MachineOnly), never written by
// agentbus (Set and Unset refuse them).
func TestBackupAfterKeys(t *testing.T) {
	for _, name := range []string{"backup.after_command", "backup.after_env", "backup.after_timeout"} {
		k, ok := Lookup(name)
		if !ok || k.Env != "" || !k.MachineOnly || !k.PersonOnly || k.Group() != GroupBackup {
			t.Fatalf("%s row %+v", name, k)
		}
		p := writeFile(t, "config.toml", "[backup]\n")
		if err := Set(p, name, "x"); err == nil || !strings.Contains(err.Error(), "edit config.toml yourself") {
			t.Errorf("Set %s: %v", name, err)
		}
		if err := Unset(p, name); err == nil || !strings.Contains(err.Error(), "edit config.toml yourself") {
			t.Errorf("Unset %s: %v", name, err)
		}
	}
	p := writeFile(t, "config.toml", "[backup]\nafter_command = \"restic backup /data/backups/agentbus\"\n"+
		"after_env = [\"RESTIC_PASSWORD_FILE\", \"RESTIC_REPOSITORY\"]\nafter_timeout = \"10m\"\n")
	r := Resolver{Getenv: env(map[string]string{"AGENT_BUS_BACKUP_AFTER_COMMAND": "rm -rf /"}), Files: []*File{Load(p, "config.toml")}}
	if r.String("backup.after_command") != "restic backup /data/backups/agentbus" ||
		r.String("backup.after_env") != "RESTIC_PASSWORD_FILE,RESTIC_REPOSITORY" || r.Duration("backup.after_timeout") != 10*time.Minute {
		t.Fatalf("resolved %q %q %v", r.String("backup.after_command"), r.String("backup.after_env"), r.Duration("backup.after_timeout"))
	}
	k, _ := Lookup("backup.after_env")
	for _, c := range []struct {
		v   any
		why string
	}{
		{[]any{"A_B", "x1"}, ""},
		{[]any{"A B"}, "want a list of variable names"},
		{[]any{1}, "want a list of variable names"},
		{"A", "want a list of variable names"},
	} {
		if _, got := k.file(c.v); got != c.why {
			t.Errorf("after_env %#v: %q, want %q", c.v, got, c.why)
		}
	}
	if d, _ := Lookup("backup.after_timeout"); d.Default != 30*time.Minute {
		t.Errorf("after_timeout default %v", d.Default)
	}
}

func TestBackupSchedule(t *testing.T) {
	k, ok := Lookup("backup.schedule")
	if !ok || k.Kind != String || k.Default != "off" || k.Group() != GroupBackup {
		t.Fatalf("row %+v", k)
	}
	for v, why := range map[any]string{"off": "", "daily": "", "hourly": "want off or daily", true: "want off or daily"} {
		if _, got := k.file(v); got != why {
			t.Errorf("file %#v: %q, want %q", v, got, why)
		}
	}
	if _, res := k.env("weekly"); res != envSkip {
		t.Error("an unknown env schedule was used")
	}
}
