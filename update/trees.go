package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/netbja/agent-bus-monitor/mission"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
)

// Pruning old release trees (quiet-update-auto spec §5): every update adds
// <src>/<tag>, a worktree of the person's clone, and nothing removed one.
// After a successful update, a tree nothing needs is removed with git
// worktree remove, never --force. A tree is kept while it is the installed
// one, a kept snapshot names it, a holder build of it is on disk, or it is
// dirty; anything under <src> that is not a vX.Y.Z worktree of its clone is
// never touched.

// Tree is one release tree and why it is kept ("" = nothing needs it).
type Tree struct {
	Path, Tag, Why string
	common         string // its clone's git dir
}

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// srcDir is ${XDG_DATA_HOME:-~/.local/share}/agentbus/src.
func (d Deps) srcDir() string { return filepath.Dir(d.treeFor("v0.0.0")) }

// Trees is every release tree under <src>, each with why it is kept.
// installed is the installed binaries' tag ("" when it cannot be read: every
// tree is kept). A tree that is not a worktree of its clone is left out:
// it is never touched.
func Trees(ctx context.Context, d Deps, installed string) []Tree {
	entries, err := os.ReadDir(d.srcDir())
	if err != nil {
		return nil
	}
	named, snapErr := snapshotTrees(d.updates())
	var holders map[string]bool
	holdersErr := fmt.Errorf("not read")
	if d.HolderKeys != nil {
		holders, holdersErr = d.HolderKeys(ctx)
	}
	var out []Tree
	for _, e := range entries {
		if !e.IsDir() || !releaseTag.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(d.srcDir(), e.Name())
		common, ok := worktreeOf(ctx, path)
		if !ok {
			continue // not a worktree its clone lists: never touched
		}
		t := Tree{Path: path, Tag: e.Name(), common: common}
		clone := mission.Git{Repo: filepath.Dir(common)}
		switch {
		case installed == "":
			t.Why = "the installed version cannot be read"
		case e.Name() == installed:
			t.Why = "installed"
		case snapErr != nil:
			t.Why = "the snapshots cannot all be read (" + snapErr.Error() + ")"
		case named[path] != "":
			t.Why = "snapshot " + named[path] + " needs it for a rollback"
		case holdersErr != nil:
			t.Why = "the holder builds cannot be read (" + holdersErr.Error() + ")"
		default:
			head, err := clone.Head(ctx, path)
			if err != nil {
				t.Why = "its HEAD cannot be read"
				break
			}
			if key := service.BuildKey(e.Name() + "+" + head[:min(12, len(head))]); holders[key] {
				t.Why = "a holder of its build (" + key + ") is on disk"
				break
			}
			if dirty, err := clone.Dirty(ctx, path); err != nil || dirty {
				t.Why = "it has changes of yours (or cannot be read)"
			}
		}
		out = append(out, t)
	}
	return out
}

// snapshotTrees are the trees each kept snapshot names (from_tree,
// new_tree), read from its manifest whatever its format (reviewer-codex): a
// rollback runs the snapshot's own binary, so an older format is still
// restorable and still needs its trees. An updates directory or a manifest
// that cannot be read or decoded is an error, and the caller keeps every
// tree. An incomplete snapshot (no manifest) is never restored: it names
// nothing.
func snapshotTrees(updates string) (map[string]string, error) {
	named := map[string]string{}
	ents, err := os.ReadDir(updates)
	if errors.Is(err, fs.ErrNotExist) {
		return named, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(updates, e.Name(), "manifest.json"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var refs struct {
			FromTree string `json:"from_tree"`
			NewTree  string `json:"new_tree"`
		}
		if err == nil {
			err = json.Unmarshal(b, &refs)
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", e.Name(), err)
		}
		for _, t := range []string{refs.FromTree, refs.NewTree} {
			if t != "" {
				named[filepath.Clean(t)] = e.Name()
			}
		}
	}
	return named, nil
}

// worktreeOf is path's clone's git dir, when that clone lists path as a
// worktree; ok false otherwise (no git, a clone gone, a plain directory).
func worktreeOf(ctx context.Context, path string) (string, bool) {
	common, err := git(ctx, "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return "", false
	}
	list, err := git(ctx, "--git-dir", common, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false
	}
	want, _ := filepath.EvalSymlinks(path)
	for _, l := range strings.Split(list, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			if got, _ := filepath.EvalSymlinks(p); got == want {
				return common, true
			}
		}
	}
	return "", false
}

// PruneTrees is §5 after a successful update: the holder collection first
// (HolderGC, so an old holder inactive for weeks does not pin its tree),
// then each tree nothing needs removed. It says what it removed and why a
// tree of a former release is kept when that is the person's (dirty).
func PruneTrees(ctx context.Context, d Deps, installed string) {
	if d.HolderGC != nil { // the new build is the current one: never collected
		tree := d.treeFor(installed)
		if head, err := (mission.Git{Repo: tree}).Head(ctx, tree); err == nil {
			if err := d.HolderGC(ctx, service.BuildKey(installed+"+"+head[:min(12, len(head))])); err != nil {
				fmt.Fprintln(d.Err, "agentbus update: the old holders could not be collected: "+err.Error())
			}
		}
	}
	for _, t := range Trees(ctx, d, installed) {
		switch {
		case t.Why == "":
			if _, err := git(ctx, "--git-dir", t.common, "worktree", "remove", t.Path); err != nil {
				fmt.Fprintf(d.Err, "agentbus update: the old tree %s was kept: %v\n", t.Path, err)
				continue
			}
			_, _ = git(ctx, "--git-dir", t.common, "worktree", "prune")
			d.say("removed the old tree %s (nothing needs it)", t.Path)
		case strings.HasPrefix(t.Why, "it has changes"):
			d.say("kept the old tree %s: %s", t.Path, t.Why)
		}
	}
}

// installedTag is the tag the installed agentbus reports, "" when it
// cannot be read.
func installedTag(ctx context.Context, d Deps) string {
	out, err := d.R.Run(ctx, setup.Cmd{Name: filepath.Join(d.BinDir, "agentbus"), Args: []string{"version"}})
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "agentbus version "); ok && releaseTag.MatchString(v) {
			return v
		}
	}
	return ""
}
