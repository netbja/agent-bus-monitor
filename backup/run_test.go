package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func fakeDump(keys map[string][]bus.DumpedKey) func(context.Context, string) (bus.ProjectDump, error) {
	return func(_ context.Context, p string) (bus.ProjectDump, error) {
		return bus.ProjectDump{Keys: keys[p], Exec: time.Millisecond}, nil
	}
}

func plan(t *testing.T, projects ...string) Plan {
	t.Helper()
	src := t.TempDir()
	cfg := filepath.Join(src, "config.toml")
	os.WriteFile(cfg, []byte("[backup]\nkeep = 3\n"), 0o600)
	return Plan{
		Projects: projects, Host: "h1", Now: t0, Version: "dev", RedisVersion: "8.2.1",
		Dump: fakeDump(map[string][]bus.DumpedKey{
			"demo": {{Key: "demo:board", Type: "hash", Class: bus.KeyRecord, Dump: []byte("\x00payload")}},
		}),
		Files:   []Source{{Member: "config/config.toml", Path: cfg}},
		LeftOut: []LeftOut{{Path: "redis.env", Why: "secret (use --secrets)"}},
	}
}

func TestRunWritesACompleteArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	res, err := Run(context.Background(), dir, plan(t, "demo", "quiet"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "agentbus-backup-h1-20261103T120000Z.tar.zst" || res.Path != filepath.Join(dir, res.Name) {
		t.Fatalf("result %+v", res)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, %v", st, err)
	}
	st, err := os.Stat(res.Path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode %v, %v", st, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir holds %d files (a temporary one left?)", len(entries))
	}
	b, _ := os.ReadFile(res.Path)
	m, err := Verify(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	// Every project in scope is covered, the quiet one included.
	if strings.Join(m.Projects, ",") != "demo,quiet" || len(m.LeftOut) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	_, got := members(t, b)
	var line struct {
		Key, Type, Class string
		Dump             []byte
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(got["redis/demo.jsonl"])), &line); err != nil {
		t.Fatal(err)
	}
	if line.Key != "demo:board" || line.Type != "hash" || line.Class != "record" || string(line.Dump) != "\x00payload" {
		t.Fatalf("redis line %+v", line)
	}
	if got["config/config.toml"] != "[backup]\nkeep = 3\n" {
		t.Fatalf("config member %q", got["config/config.toml"])
	}
	if _, ok := got["redis/quiet.jsonl"]; !ok {
		t.Error("a project with no key has no (empty) redis member")
	}
	if res.Exec["demo"] != time.Millisecond {
		t.Errorf("exec %v", res.Exec)
	}
}

// A failure leaves no archive under any name, the temporary one removed.
func TestRunFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	p := plan(t, "demo")
	p.Dump = func(context.Context, string) (bus.ProjectDump, error) {
		return bus.ProjectDump{}, errors.New("broker gone")
	}
	if _, err := Run(context.Background(), dir, p); err == nil {
		t.Fatal("a failed dump wrote an archive")
	}
	p = plan(t, "demo")
	p.Files = append(p.Files, Source{Member: "config/x", Path: filepath.Join(dir, "missing-but-required")})
	p.Files[len(p.Files)-1].Required = true
	if _, err := Run(context.Background(), dir, p); err == nil {
		t.Fatal("a missing required file wrote an archive")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

// An absent optional source is simply not in the archive.
func TestRunSkipsAnAbsentOptionalFile(t *testing.T) {
	p := plan(t, "demo")
	p.Files = append(p.Files, Source{Member: "config/quota.toml", Path: filepath.Join(t.TempDir(), "none")})
	res, err := Run(context.Background(), t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.Path)
	if _, got := members(t, b); got["config/quota.toml"] != "" {
		t.Fatal("an absent file was archived")
	}
}

// A file growing while it is read is archived at the size it had.
func TestRunArchivesAGrowingFileAtItsSize(t *testing.T) {
	p := plan(t, "demo")
	path := filepath.Join(t.TempDir(), "day.jsonl")
	os.WriteFile(path, []byte("line1\n"), 0o600)
	p.Files = append(p.Files, Source{Member: "state/demo/history/day.jsonl", Path: path})
	p.beforeRead = func(src string) {
		if src == path {
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("line2\n")
			f.Close()
		}
	}
	res, err := Run(context.Background(), t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.Path)
	if _, got := members(t, b); got["state/demo/history/day.jsonl"] != "line1\n" {
		t.Fatalf("got %q", got["state/demo/history/day.jsonl"])
	}
}

func TestRunRefusesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(context.Background(), dir, plan(t, "demo")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), dir, plan(t, "demo")); err == nil {
		t.Fatal("a second archive of the same second replaced the first")
	}
}

func TestArchiveNames(t *testing.T) {
	if got := ArchiveName("my host/é", t0); got != "agentbus-backup-my-host---20261103T120000Z.tar.zst" {
		t.Fatalf("ArchiveName = %q", got)
	}
	for name, want := range map[string]bool{
		"agentbus-backup-h1-20261103T120000Z.tar.zst":      true,
		"agentbus-backup-a-b-20261103T120000Z.tar.zst":     true,
		"agentbus-backup-h1-20261103T120000Z.tar.gz":       false,
		".agentbus-backup-h1-20261103T120000Z.tar.zst.tmp": false,
		"agentbus-backup-h1-20261103T120000Z.tar.zst.tmp":  false,
		"notes.txt": false,
	} {
		if _, _, ok := parseArchiveName(name); ok != want {
			t.Errorf("parseArchiveName(%q) = %v", name, ok)
		}
	}
}

// writeArchive drops a complete archive of projects at time at into dir.
func writeArchive(t *testing.T, dir string, at time.Time, projects ...string) string {
	t.Helper()
	p := plan(t, projects...)
	p.Now = at
	res, err := Run(context.Background(), dir, p)
	if err != nil {
		t.Fatal(err)
	}
	return res.Name
}

func TestListAndCoverage(t *testing.T) {
	dir := t.TempDir()
	a := writeArchive(t, dir, t0, "a", "b")
	b := writeArchive(t, dir, t0.Add(time.Hour), "a")
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, ".agentbus-backup-h1-20261103T150000Z.tar.zst.tmp"), []byte("half"), 0o600)
	list, err := List(dir, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != b || list[1].Name != a {
		t.Fatalf("list %+v, want newest first", list)
	}
	cov := NewestCovering(list)
	if cov["a"].Name != b || cov["b"].Name != a {
		t.Fatalf("covering %+v", cov)
	}
	// Another host's archive in the same dir is not this host's.
	if l, _ := List(dir, "h2"); len(l) != 0 {
		t.Fatalf("h2 sees %+v", l)
	}
}

func TestRetain(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for i := range 4 {
		names = append(names, writeArchive(t, dir, t0.Add(time.Duration(i)*time.Hour), "a"))
	}
	os.WriteFile(filepath.Join(dir, "foreign.tar.zst"), []byte("x"), 0o600)
	removed, err := Retain(dir, "h1", 2)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	if strings.Join(removed, ",") != names[0]+","+names[1] {
		t.Fatalf("removed %q", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "foreign.tar.zst")); err != nil {
		t.Fatal("a foreign file was removed")
	}
	// keep = 0 keeps every one.
	if removed, _ := Retain(dir, "h1", 0); len(removed) != 0 {
		t.Fatalf("keep 0 removed %q", removed)
	}
}

// An old A+B archive then three newer A-only ones, keep 2: the A+B one stays
// (the newest covering B), only the oldest A-only one goes.
func TestRetainByCoverage(t *testing.T) {
	dir := t.TempDir()
	ab := writeArchive(t, dir, t0, "a", "b")
	var aOnly []string
	for i := 1; i <= 3; i++ {
		aOnly = append(aOnly, writeArchive(t, dir, t0.Add(time.Duration(i)*time.Hour), "a"))
	}
	removed, err := Retain(dir, "h1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != aOnly[0] {
		t.Fatalf("removed %q, want only %s", removed, aOnly[0])
	}
	if _, err := os.Stat(filepath.Join(dir, ab)); err != nil {
		t.Fatal("the only archive covering b was removed")
	}
}
