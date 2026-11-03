package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fakeBroker holds each project's keys as name -> payload.
type fakeBroker struct {
	version  string
	keys     map[string]map[string]string
	restored map[string][]string
	failOn   string // a project whose Restore fails
}

func (f *fakeBroker) Version(context.Context) (string, error) { return f.version, nil }
func (f *fakeBroker) Keys(_ context.Context, p string) ([]string, error) {
	var out []string
	for k := range f.keys[p] {
		out = append(out, k)
	}
	slices.Sort(out)
	return out, nil
}
func (f *fakeBroker) Restore(_ context.Context, p string, keys []bus.DumpedKey, del []string) (int, error) {
	if p == f.failOn {
		return 0, errors.New("payload refused")
	}
	if f.keys[p] == nil {
		f.keys[p] = map[string]string{}
	}
	for _, k := range del {
		delete(f.keys[p], k)
	}
	for _, k := range keys {
		if _, ok := f.keys[p][k.Key]; ok {
			return 0, fmt.Errorf("key exists: %s", k.Key)
		}
	}
	for _, k := range keys {
		f.keys[p][k.Key] = string(k.Dump)
		f.restored[p] = append(f.restored[p], k.Key)
	}
	return len(keys), nil
}

// restoreWorld: an archive of demo (a record key, a runtime key, an unknown
// key; a template naming a manifest; prompts; a history file) and quiet (no
// key), restored into empty temporary directories.
type restoreWorld struct {
	t       *testing.T
	archive string
	loc     Locations
	broker  *fakeBroker
	live    map[string]string
	locked  map[string]bool
	confirm bool
	repo    string
}

func newRestoreWorld(t *testing.T) *restoreWorld {
	t.Helper()
	src := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	put := func(name, c string) string {
		p := filepath.Join(src, name)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(c), 0o640)
		return p
	}
	p := plan(t, "demo", "quiet")
	p.Dump = fakeDump(map[string][]bus.DumpedKey{"demo": {
		{Key: "demo:board", Type: "hash", Class: bus.KeyRecord, Dump: []byte("B")},
		{Key: "demo:agents", Type: "hash", Class: bus.KeyRuntime, Dump: []byte("A")},
		{Key: "demo:foreign", Type: "string", Class: bus.KeyUnknown, Dump: []byte("F")},
	}})
	p.Files = []Source{
		{Member: "config/config.toml", Path: put("config.toml", "[backup]\n")},
		{Member: "config/projects/demo.toml", Path: put("demo.toml", "[[tabs]]\ncommand = \"env ROLES_TOML="+repo+"/roles.toml agent-launch coder demo\"\n")},
		{Member: "roles/demo/roles.toml", Path: put("roles.toml", "schema = 2\n")},
		{Member: "roles/demo/roles/coder.md", Path: put("coder.md", "# coder\n")},
		{Member: "state/demo/history/2026-11-03.jsonl", Path: put("day.jsonl", "{}\n")},
	}
	res, err := Run(context.Background(), t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w := &restoreWorld{t: t, archive: res.Path, repo: repo, confirm: true,
		broker: &fakeBroker{version: "8.2.1", keys: map[string]map[string]string{}, restored: map[string][]string{}},
		live:   map[string]string{}, locked: map[string]bool{},
		loc: Locations{
			ConfigDir:   filepath.Join(root, "config"),
			ProjectsDir: filepath.Join(root, "config", "projects"),
			StateDir:    func(p string) string { return filepath.Join(root, "state", p) },
			EnvFile:     func(p string) string { return filepath.Join(root, "config", "services", p+".env") },
		}}
	return w
}

func (w *restoreWorld) run(o RestoreOptions) (Report, string, error) {
	var out bytes.Buffer
	rep, err := Restore(context.Background(), w.archive, o, RestoreDeps{
		Broker: w.broker,
		Live:   func(_ context.Context, p string) (string, error) { return w.live[p], nil },
		Lock: func(p string) (func(), error) {
			if w.locked[p] {
				return nil, errors.New("held")
			}
			return func() {}, nil
		},
		Confirm: func(string, int) bool { return w.confirm },
		Loc:     w.loc,
	}, &out)
	return rep, out.String(), err
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return string(b)
}

func TestRestoreEverything(t *testing.T) {
	w := newRestoreWorld(t)
	rep, out, err := w.run(RestoreOptions{})
	if err != nil || rep.Refused != 0 {
		t.Fatalf("restore: %+v %v\n%s", rep, err, out)
	}
	// Records only by default: the runtime and unknown keys stay out.
	if got := w.broker.restored["demo"]; strings.Join(got, ",") != "demo:board" {
		t.Fatalf("restored %q", got)
	}
	if read(t, filepath.Join(w.loc.ConfigDir, "config.toml")) != "[backup]\n" ||
		read(t, filepath.Join(w.loc.ProjectsDir, "demo.toml")) == "" ||
		read(t, filepath.Join(w.repo, "roles.toml")) != "schema = 2\n" ||
		read(t, filepath.Join(w.repo, "roles", "coder.md")) != "# coder\n" ||
		read(t, filepath.Join(w.loc.StateDir("demo"), "history", "2026-11-03.jsonl")) != "{}\n" {
		t.Fatal("a file was not restored")
	}
	if st, _ := os.Stat(filepath.Join(w.loc.ConfigDir, "config.toml")); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want the manifest's 0640", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(w.loc.StateDir("demo"), "history")); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	for _, want := range []string{"redis demo:agents: skip (runtime)", "redis demo:foreign: skip (unknown)", "redis demo:board: write",
		"agentbus up demo", "health"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// A second restore: keys present (refused without --force), files identical.
	rep, out, _ = w.run(RestoreOptions{})
	if rep.Refused == 0 || !strings.Contains(out, "refused (1 key present") || !strings.Contains(out, "skip (identical)") {
		t.Fatalf("second restore: %+v\n%s", rep, out)
	}
}

func TestRestoreWithRuntime(t *testing.T) {
	w := newRestoreWorld(t)
	if _, out, err := w.run(RestoreOptions{WithRuntime: true, RedisOnly: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := w.broker.restored["demo"]
	slices.Sort(got)
	if strings.Join(got, ",") != "demo:agents,demo:board,demo:foreign" {
		t.Fatalf("restored %q", got)
	}
	if _, err := os.Stat(filepath.Join(w.loc.ConfigDir, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("--redis-only wrote a file")
	}
}

// Nothing is written for a refused project: live, locked, keys present
// without --force, not confirmed; an older broker refuses everything.
func TestRestoreRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		arrange func(w *restoreWorld)
		o       RestoreOptions
		want    string
	}{
		"live":                {func(w *restoreWorld) { w.live["demo"] = "the dispatch lease is held" }, RestoreOptions{}, "live"},
		"live with --force":   {func(w *restoreWorld) { w.live["demo"] = "agent coder's pid 42 is alive" }, RestoreOptions{Force: true}, "live"},
		"locked":              {func(w *restoreWorld) { w.locked["demo"] = true }, RestoreOptions{}, "held"},
		"keys present":        {func(w *restoreWorld) { w.broker.keys["demo"] = map[string]string{"demo:x": "1"} }, RestoreOptions{}, "--force"},
		"force not confirmed": {func(w *restoreWorld) { w.broker.keys["demo"] = map[string]string{"demo:x": "1"}; w.confirm = false }, RestoreOptions{Force: true}, "not confirmed"},
		"restore fails":       {func(w *restoreWorld) { w.broker.failOn = "demo" }, RestoreOptions{}, "payload refused"},
	} {
		w := newRestoreWorld(t)
		c.arrange(w)
		rep, out, _ := w.run(c.o)
		if rep.Refused == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s: %+v\n%s", name, rep, out)
		}
		if len(w.broker.restored["demo"]) != 0 {
			t.Errorf("%s: keys restored", name)
		}
		// demo's files are not written; quiet's are, the host's too.
		if _, err := os.Stat(filepath.Join(w.loc.ProjectsDir, "demo.toml")); !os.IsNotExist(err) {
			t.Errorf("%s: demo's template written", name)
		}
		if _, err := os.Stat(filepath.Join(w.loc.StateDir("demo"), "history")); !os.IsNotExist(err) {
			t.Errorf("%s: demo's history written", name)
		}
	}
	for _, v := range []string{"8.2.0", "8.1.9", "7.4.9", "8.2"} {
		w := newRestoreWorld(t)
		w.broker.version = v
		if _, out, err := w.run(RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "older") {
			t.Errorf("broker %s: %v\n%s", v, err, out)
		}
		if len(w.broker.restored) != 0 {
			t.Errorf("broker %s: restored", v)
		}
	}
	w := newRestoreWorld(t)
	w.broker.version = "8.2.10"
	if _, out, err := w.run(RestoreOptions{}); err != nil {
		t.Errorf("a newer patch refused: %v\n%s", err, out)
	}
	if _, _, err := w.run(RestoreOptions{Projects: []string{"absent"}}); err == nil {
		t.Error("a project the archive does not cover was accepted")
	}
}

func TestRestoreForce(t *testing.T) {
	w := newRestoreWorld(t)
	w.broker.keys["demo"] = map[string]string{"demo:board": "old", "demo:extra": "x"}
	if rep, out, err := w.run(RestoreOptions{Force: true, RedisOnly: true}); err != nil || rep.Refused != 0 {
		t.Fatalf("%+v %v\n%s", rep, err, out)
	}
	if got := w.broker.keys["demo"]; len(got) != 1 || got["demo:board"] != "B" {
		t.Fatalf("keys %v", got)
	}
}

func TestRestoreFiles(t *testing.T) {
	w := newRestoreWorld(t)
	cfg := filepath.Join(w.loc.ConfigDir, "config.toml")
	os.MkdirAll(w.loc.ConfigDir, 0o700)
	os.WriteFile(cfg, []byte("mine\n"), 0o600)
	// A differing file is kept without --force.
	_, out, _ := w.run(RestoreOptions{FilesOnly: true})
	if read(t, cfg) != "mine\n" || !strings.Contains(out, "keep (differs") {
		t.Fatalf("kept? %q\n%s", read(t, cfg), out)
	}
	// --force replaces it through a rename and keeps a symlink a link.
	real := filepath.Join(t.TempDir(), "real.toml")
	os.WriteFile(real, []byte("linked\n"), 0o600)
	os.Remove(cfg)
	os.Symlink(real, cfg)
	if _, out, err := w.run(RestoreOptions{FilesOnly: true, Force: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if st, _ := os.Lstat(cfg); st.Mode()&os.ModeSymlink == 0 || read(t, real) != "[backup]\n" {
		t.Fatalf("symlink kept=%v, target %q", st.Mode()&os.ModeSymlink != 0, read(t, real))
	}
	// --project names a project: the host's settings are not touched.
	w2 := newRestoreWorld(t)
	if _, out, _ := w2.run(RestoreOptions{Projects: []string{"demo"}}); !strings.Contains(out, "config/config.toml: skip (host setting") {
		t.Fatalf("scoped restore:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(w2.loc.ConfigDir, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("a scoped restore wrote the host's config.toml")
	}
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	w := newRestoreWorld(t)
	rep, out, err := w.run(RestoreOptions{DryRun: true})
	if err != nil || rep.Refused != 0 {
		t.Fatalf("%+v %v\n%s", rep, err, out)
	}
	if len(w.broker.restored) != 0 {
		t.Fatal("a dry run restored keys")
	}
	if entries, _ := os.ReadDir(filepath.Dir(w.loc.ConfigDir)); len(entries) != 0 {
		t.Fatalf("a dry run wrote %v", entries)
	}
	for _, want := range []string{"redis demo:board: write", "config/config.toml", ": write"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
}

// An archive whose member names escape the restore's folders is refused by
// Verify before anything is written.
func TestVerifyRefusesUnsafeNames(t *testing.T) {
	for _, name := range []string{"../x", "state/demo/history/../../../x", "/abs"} {
		b := retarWith(t, name)
		if _, err := Verify(bytes.NewReader(b)); err == nil {
			t.Errorf("%q verified", name)
		}
	}
}

// The roles.toml destination comes from the archive's template: only an
// absolute, clean .../roles.toml is written to.
func TestDestinationRefusesAnUnsafeManifestPath(t *testing.T) {
	loc := Locations{StateDir: func(p string) string { return "/s/" + p }}
	for _, m := range []string{"/home/u/.bashrc", "rel/roles.toml", "/a/../b/roles.toml"} {
		tpl := map[string][]byte{"demo": []byte("[[tabs]]\ncommand = \"env ROLES_TOML=" + m + " agent-launch coder demo\"\n")}
		if dest, _, _, why := destination("roles/demo/roles.toml", loc, tpl); why == "" {
			t.Errorf("%s: restored to %s", m, dest)
		}
		if dest, _, _, why := destination("roles/demo/roles/coder.md", loc, tpl); why == "" {
			t.Errorf("%s: a prompt restored to %s", m, dest)
		}
	}
}
