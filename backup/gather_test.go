package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// home lays out a person's files: config dir, two templates, a manifest
// with prompts, a history archive and the secrets.
func home(t *testing.T) (Locations, string) {
	t.Helper()
	root := t.TempDir()
	cfg := filepath.Join(root, "config", "agentbus")
	repo := filepath.Join(root, "repo")
	state := filepath.Join(root, "state", "agentbus")
	write := func(p, c string, mode os.FileMode) {
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(c), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg, "config.toml"), "[backup]\n", 0o600)
	write(filepath.Join(cfg, "quota.toml"), "q\n", 0o600)
	write(filepath.Join(cfg, "projects", "demo.toml"), `working_dir = "/w"
[[tabs]]
name = "busmon"
command = "busmon --project demo"
[[tabs]]
name = "coder"
command = "env 'ROLES_TOML=`+repo+`/roles.toml' /r/scripts/agent-launch coder demo"
`, 0o600)
	write(filepath.Join(repo, "roles.toml"), "schema = 2\n", 0o644)
	write(filepath.Join(repo, "roles", "coder.md"), "# coder\n", 0o644)
	write(filepath.Join(repo, "roles", "notes.txt"), "not a prompt\n", 0o644)
	write(filepath.Join(state, "demo", "history", "2026-11-03.jsonl"), "{}\n", 0o600)
	write(filepath.Join(state, "demo", "history", "cursor.json"), "{}", 0o600)
	write(filepath.Join(state, "demo", "history", ".lock"), "", 0o600)
	write(filepath.Join(state, "demo", "dispatch.log"), "log\n", 0o600)
	write(filepath.Join(state, "demo", "dispatch.restarts"), "1\n", 0o600)
	// Secrets, unreadable: opening one would fail the run.
	write(filepath.Join(cfg, "redis.env"), `REDIS_PASSWORD="pw-SECRET"`+"\n", 0o000)
	write(filepath.Join(cfg, "services", "demo.env"), "REDIS_PASSWORD=pw-SECRET\n", 0o000)
	return Locations{
		ConfigDir:    cfg,
		ProjectsDir:  filepath.Join(cfg, "projects"),
		PasswordFile: filepath.Join(cfg, "redis.env"),
		EnvFile:      func(p string) string { return filepath.Join(cfg, "services", p+".env") },
		StateDir:     func(p string) string { return filepath.Join(state, p) },
	}, repo
}

func TestGather(t *testing.T) {
	loc, repo := home(t)
	files, left, _, err := Gather([]string{"demo", "other"}, loc)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Member+"="+f.Path)
	}
	slices.Sort(got)
	want := []string{
		"config/config.toml=" + filepath.Join(loc.ConfigDir, "config.toml"),
		"config/projects/demo.toml=" + filepath.Join(loc.ProjectsDir, "demo.toml"),
		"config/quota.toml=" + filepath.Join(loc.ConfigDir, "quota.toml"),
		"roles/demo/roles.toml=" + filepath.Join(repo, "roles.toml"),
		"roles/demo/roles/coder.md=" + filepath.Join(repo, "roles", "coder.md"),
		"state/demo/history/2026-11-03.jsonl=" + loc.StateDir("demo") + "/history/2026-11-03.jsonl",
		"state/demo/history/cursor.json=" + loc.StateDir("demo") + "/history/cursor.json",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var leftOut []string
	for _, l := range left {
		leftOut = append(leftOut, l.Path+": "+l.Why)
	}
	if strings.Join(leftOut, "; ") != "redis.env: secret (use --secrets); services/demo.env: secret (use --secrets)" {
		t.Fatalf("left out %q", leftOut)
	}
}

// A default run never opens a secret file (they are 0000 here: an open
// would fail) and the archive's bytes never hold the test password.
func TestRunNeverReadsASecret(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file")
	}
	loc, _ := home(t)
	p := plan(t, "demo")
	var err error
	if p.Files, p.LeftOut, _, err = Gather([]string{"demo"}, loc); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.Path)
	_, got := members(t, b)
	for name, c := range got {
		if strings.Contains(c, "pw-SECRET") {
			t.Fatalf("%s holds the password", name)
		}
	}
	m, err := Verify(bytes.NewReader(b))
	if err != nil || len(m.LeftOut) != 2 {
		t.Fatalf("manifest left out %+v, %v", m.LeftOut, err)
	}
}

// An unreadable history folder fails the gather (reviewer-codex): never an
// archive that silently lacks part of the history.
func TestGatherFailsOnAnUnreadableHistory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 folder")
	}
	loc, _ := home(t)
	sub := filepath.Join(loc.StateDir("demo"), "history", "old")
	os.MkdirAll(sub, 0o700)
	os.WriteFile(filepath.Join(sub, "x.jsonl"), []byte("{}\n"), 0o600)
	os.Chmod(sub, 0o000)
	t.Cleanup(func() { os.Chmod(sub, 0o700) })
	if _, _, _, err := Gather([]string{"demo"}, loc); err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("Gather = %v, want the unreadable history named", err)
	}
	// A project with no history folder at all is fine.
	if _, _, _, err := Gather([]string{"other"}, loc); err != nil {
		t.Fatalf("no history: %v", err)
	}
}

// A template whose tabs name no ROLES_TOML archives no manifest, and the
// manifest's notes say where it is (foureyes).
func TestGatherNotesATemplateWithoutManifest(t *testing.T) {
	loc, _ := home(t)
	os.WriteFile(filepath.Join(loc.ProjectsDir, "bare.toml"), []byte("[[tabs]]\ncommand = \"agent-launch coder bare\"\n"), 0o600)
	_, _, notes, err := Gather([]string{"bare"}, loc)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "bare: its template names no ROLES_TOML") {
		t.Fatalf("notes %q, %v", notes, err)
	}
}
