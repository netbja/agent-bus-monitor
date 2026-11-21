package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// treeWorld: the person's clone with release trees v0.1.0 and v0.2.0 under
// <src>, a plain directory v0.0.9 (no worktree) and a scratch directory
// that is no tag; the deps reading them, holders read as none.
type treeWorld struct {
	w        world
	src      string
	d        Deps
	out, err bytes.Buffer
	gc       []string
}

func newTreeWorld(t *testing.T) *treeWorld {
	t.Helper()
	tw := &treeWorld{w: newWorld(t)}
	home := t.TempDir()
	env := map[string]string{"HOME": home, "XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_STATE_HOME": filepath.Join(home, "state")}
	tw.d = Deps{Getenv: func(k string) string { return env[k] }, Out: &tw.out, Err: &tw.err,
		HolderKeys: func(context.Context) (map[string]bool, error) { return map[string]bool{}, nil },
		HolderGC:   func(_ context.Context, current string) error { tw.gc = append(tw.gc, current); return nil }}
	tw.src = tw.d.srcDir()
	_ = os.MkdirAll(tw.src, 0o755)
	for _, tag := range []string{"v0.1.0", "v0.2.0"} {
		sh(t, tw.w.clone, "worktree", "add", "-q", "--detach", filepath.Join(tw.src, tag), tag)
	}
	_ = os.MkdirAll(filepath.Join(tw.src, "v0.0.9"), 0o755)
	_ = os.WriteFile(filepath.Join(tw.src, "v0.0.9", "keep"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(tw.src, "scratch"), 0o755)
	return tw
}

func whys(trees []Tree) map[string]string {
	out := map[string]string{}
	for _, t := range trees {
		out[t.Tag] = t.Why
	}
	return out
}

// quiet-update-auto §5: only vX.Y.Z worktrees of their clone are judged;
// the installed one is kept, a former one nothing needs is not.
func TestTreesJudged(t *testing.T) {
	tw := newTreeWorld(t)
	got := whys(Trees(context.Background(), tw.d, "v0.2.0"))
	if len(got) != 2 || got["v0.2.0"] != "installed" || got["v0.1.0"] != "" {
		t.Fatalf("%v", got)
	}
}

// Each rule that keeps a former tree, and the fail-closed reads.
func TestTreesKept(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name      string
		set       func(tw *treeWorld)
		installed string
		want      string
	}{
		{"a kept snapshot names it", func(tw *treeWorld) {
			if err := WriteSnapshot(filepath.Join(tw.d.updates(), "20261121T000000Z-v0.1.0-v0.2.0"),
				Manifest{BinDir: binDir(t), From: "v0.1.0", To: "v0.2.0", FromTree: filepath.Join(tw.src, "v0.1.0")}); err != nil {
				t.Fatal(err)
			}
		}, "v0.2.0", "snapshot 20261121T000000Z-v0.1.0-v0.2.0 needs it for a rollback"},
		// reviewer-codex: a snapshot of another format is still restorable
		// by its own binary, so it still pins its trees.
		{"an older format's snapshot names it", func(tw *treeWorld) {
			dir := filepath.Join(tw.d.updates(), "20251121T000000Z-v0.0.1-v0.1.0")
			_ = os.MkdirAll(dir, 0o700)
			_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"format":0,"from_tree":"`+filepath.Join(tw.src, "v0.1.0")+`"}`), 0o600)
		}, "v0.2.0", "snapshot 20251121T000000Z-v0.0.1-v0.1.0 needs it for a rollback"},
		{"a manifest that cannot be decoded", func(tw *treeWorld) {
			dir := filepath.Join(tw.d.updates(), "20261121T000000Z-v0.1.0-v0.2.0")
			_ = os.MkdirAll(dir, 0o700)
			_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{not json`), 0o600)
		}, "v0.2.0", "the snapshots cannot all be read"},
		{"a manifest that cannot be read", func(tw *treeWorld) {
			dir := filepath.Join(tw.d.updates(), "20261121T000000Z-v0.1.0-v0.2.0")
			_ = os.MkdirAll(filepath.Join(dir, "manifest.json"), 0o700) // a directory: ReadFile fails
		}, "v0.2.0", "the snapshots cannot all be read"},
		{"the snapshots directory cannot be read", func(tw *treeWorld) {
			_ = os.MkdirAll(filepath.Dir(tw.d.updates()), 0o700)
			_ = os.WriteFile(tw.d.updates(), []byte("a file, not a directory"), 0o600)
		}, "v0.2.0", "the snapshots cannot all be read"},
		{"a holder build of it is on disk", func(tw *treeWorld) {
			key := service.BuildKey("v0.1.0+" + tw.w.v1[:12])
			tw.d.HolderKeys = func(context.Context) (map[string]bool, error) { return map[string]bool{key: true}, nil }
		}, "v0.2.0", "a holder of its build (" + service.BuildKey("v0.1.0+"+"") + ""},
		{"dirty", func(tw *treeWorld) {
			_ = os.WriteFile(filepath.Join(tw.src, "v0.1.0", "f"), []byte("edited by the person"), 0o644)
		}, "v0.2.0", "it has changes of yours"},
		{"the holders cannot be read", func(tw *treeWorld) {
			tw.d.HolderKeys = func(context.Context) (map[string]bool, error) { return nil, errors.New("no manager") }
		}, "v0.2.0", "the holder builds cannot be read (no manager)"},
		{"no holder reader", func(tw *treeWorld) { tw.d.HolderKeys = nil }, "v0.2.0", "the holder builds cannot be read (not read)"},
		{"the installed version unknown", func(*treeWorld) {}, "", "the installed version cannot be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tw := newTreeWorld(t)
			c.set(tw)
			got := whys(Trees(ctx, tw.d, c.installed))
			if c.name == "a holder build of it is on disk" {
				if !strings.HasPrefix(got["v0.1.0"], "a holder of its build (") {
					t.Fatalf("%v", got)
				}
				return
			}
			if !strings.HasPrefix(got["v0.1.0"], c.want) {
				t.Fatalf("v0.1.0: %q, want %q…", got["v0.1.0"], c.want)
			}
		})
	}
}

// An incomplete snapshot (no manifest) is never restored: it pins nothing.
func TestTreesIncompleteSnapshotNamesNothing(t *testing.T) {
	tw := newTreeWorld(t)
	_ = os.MkdirAll(filepath.Join(tw.d.updates(), "20261121T000000Z-v0.1.0-v0.2.0", "bin"), 0o700)
	if got := whys(Trees(context.Background(), tw.d, "v0.2.0")); got["v0.1.0"] != "" {
		t.Fatalf("%v", got)
	}
}

// PruneTrees: the holder collection first, with the new build as current;
// then git worktree remove of what nothing needs. The installed tree, the
// plain directory and the non-tag one stay; the clone's branches, HEAD and
// status do not move.
func TestPruneTrees(t *testing.T) {
	tw := newTreeWorld(t)
	before := cloneState(t, tw.w.clone)
	PruneTrees(context.Background(), tw.d, "v0.2.0")
	if _, err := os.Stat(filepath.Join(tw.src, "v0.1.0")); err == nil {
		t.Fatalf("v0.1.0 kept: %s %s", tw.out.String(), tw.err.String())
	}
	if list := sh(t, tw.w.clone, "worktree", "list", "--porcelain"); strings.Contains(list, "v0.1.0") || !strings.Contains(list, "v0.2.0") {
		t.Fatalf("worktree list:\n%s", list)
	}
	for _, kept := range []string{"v0.2.0", "v0.0.9/keep", "scratch"} {
		if _, err := os.Stat(filepath.Join(tw.src, kept)); err != nil {
			t.Errorf("%s removed", kept)
		}
	}
	if !strings.Contains(tw.out.String(), "removed the old tree "+filepath.Join(tw.src, "v0.1.0")) {
		t.Fatalf("out %q", tw.out.String())
	}
	if cloneState(t, tw.w.clone) != before {
		t.Fatal("the clone moved")
	}
	head := sh(t, filepath.Join(tw.src, "v0.2.0"), "rev-parse", "HEAD")
	if len(tw.gc) != 1 || tw.gc[0] != service.BuildKey("v0.2.0+"+head[:12]) {
		t.Fatalf("holder collection with %v", tw.gc)
	}
}

// A dirty former tree is kept and named; git itself would refuse it
// (worktree remove without --force).
func TestPruneTreesKeepsADirtyOne(t *testing.T) {
	tw := newTreeWorld(t)
	_ = os.WriteFile(filepath.Join(tw.src, "v0.1.0", "f"), []byte("edited by the person"), 0o644)
	PruneTrees(context.Background(), tw.d, "v0.2.0")
	if b, _ := os.ReadFile(filepath.Join(tw.src, "v0.1.0", "f")); string(b) != "edited by the person" {
		t.Fatal("the person's change is gone")
	}
	if !strings.Contains(tw.out.String(), "kept the old tree "+filepath.Join(tw.src, "v0.1.0")+": it has changes of yours") {
		t.Fatalf("out %q", tw.out.String())
	}
}

// A tree whose clone is gone is not a worktree any clone lists: untouched.
func TestPruneTreesCloneGone(t *testing.T) {
	tw := newTreeWorld(t)
	_ = os.RemoveAll(tw.w.clone)
	PruneTrees(context.Background(), tw.d, "v0.2.0")
	if _, err := os.Stat(filepath.Join(tw.src, "v0.1.0", "f")); err != nil {
		t.Fatal("a tree of a gone clone was removed")
	}
}

// A successful update prunes (after the snapshots): a former release tree
// nothing needs is removed, the new one stays; --list shows each tree with
// why it is kept.
func TestUpdatePrunesOldTrees(t *testing.T) {
	f := newFlow(t)
	d := f.deps(false)
	d.HolderKeys = func(context.Context) (map[string]bool, error) { return map[string]bool{}, nil }
	src := d.srcDir()
	_ = os.MkdirAll(src, 0o755)
	sh(t, f.w.clone, "worktree", "add", "-q", "--detach", filepath.Join(src, "v0.1.0"), "v0.1.0")
	if code := Run(context.Background(), d, Options{Project: "a", Yes: true}); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, f.out.String(), f.errw.String())
	}
	if _, err := os.Stat(filepath.Join(src, "v0.1.0")); err == nil {
		t.Fatalf("the old tree is kept:\n%s\n%s", f.out.String(), f.errw.String())
	}
	if _, err := os.Stat(filepath.Join(src, "v0.2.0")); err != nil {
		t.Fatal("the installed tree was removed")
	}
	f.out.Reset()
	f.r.version = "v0.2.0"
	List(d)
	if !strings.Contains(f.out.String(), "tree "+filepath.Join(src, "v0.2.0")+"  installed") {
		t.Fatalf("list: %q", f.out.String())
	}
}
