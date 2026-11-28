// Package codeintel is the code-intel relay's ground (code-intel spec): which
// of the project's worktrees a path or a branch belongs to, failing closed
// (§4), and the revision each answer read (§3).
package codeintel

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree is one working tree of the project's repository.
type Worktree struct {
	Path   string // as listed; List resolves it (filepath.EvalSymlinks)
	Branch string // "" when detached
	Head   string
}

// ParseWorktrees reads git worktree list --porcelain -z: each attribute
// ends in NUL and an entry in an empty one, so a path may hold a newline
// (foureyes). A bare entry has no working tree, and a prunable one is gone:
// neither is a worktree here.
func ParseWorktrees(porcelain string) []Worktree {
	var out []Worktree
	for _, block := range strings.Split(porcelain, "\x00\x00") {
		var w Worktree
		skip := false
		for _, l := range strings.Split(block, "\x00") {
			k, v, _ := strings.Cut(l, " ")
			switch k {
			case "worktree":
				w.Path = v
			case "HEAD":
				w.Head = v
			case "branch":
				w.Branch = strings.TrimPrefix(v, "refs/heads/")
			case "bare", "prunable":
				skip = true
			}
		}
		if w.Path != "" && !skip {
			out = append(out, w)
		}
	}
	return out
}

// Trees are the project's worktrees as one read listed them.
type Trees struct {
	Project string
	List    []Worktree
}

// List reads repo's worktrees (git -C repo worktree list --porcelain -z, with
// exactly env), each path resolved; a listed path that does not resolve
// (a removed directory not yet pruned) is left out.
func List(ctx context.Context, project, repo string, env []string) (Trees, error) {
	out, err := git(ctx, repo, env, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return Trees{}, err
	}
	return resolved(project, ParseWorktrees(out)), nil
}

// resolved is ws with each root resolved, as the spec asks of every listed
// root (git lists real paths today; a root it ever lists through a symlink
// still routes). A root that does not resolve is left out.
func resolved(project string, ws []Worktree) Trees {
	t := Trees{Project: project}
	for _, w := range ws {
		p, err := filepath.EvalSymlinks(w.Path)
		if err != nil {
			continue
		}
		w.Path = p
		t.List = append(t.List, w)
	}
	return t
}

// Resolve is the worktree a path lies in: the path resolved, then the
// deepest listed root that contains it. Worktrees nest (a project's
// .worktrees/ inside its main checkout), so a first match would route a
// coder's file to the main checkout's backend. A path that does not
// resolve, or lies in no worktree, is refused, and so is a relative one:
// the relay's directory is its launch's, not the agent's current one, so
// no base would route it right (reviewer-codex).
func (t Trees) Resolve(path string) (Worktree, error) {
	if !filepath.IsAbs(path) {
		return Worktree{}, fmt.Errorf("not an absolute path: %s (code-intel takes a file's full path)", path)
	}
	p, err := filepath.EvalSymlinks(path)
	if err == nil {
		if w, ok := t.owner(p); ok {
			return w, nil
		}
	}
	return Worktree{}, fmt.Errorf("not a worktree of %s: %s", t.Project, path)
}

// owner is the deepest root containing p, compared element by element.
func (t Trees) owner(p string) (Worktree, bool) {
	var best Worktree
	depth := -1
	for _, w := range t.List {
		if !within(p, w.Path) {
			continue
		}
		if d := strings.Count(filepath.Clean(w.Path), string(filepath.Separator)); d > depth {
			best, depth = w, d
		}
	}
	return best, depth >= 0
}

// within tells whether p is dir or below it. Like memory's within, by
// filepath.Rel, never a string prefix: /a/b does not contain /a/bc.
func within(p, dir string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// ForBranch is the one worktree that has branch checked out; none or
// several is refused, never a fallback to another tree.
func (t Trees) ForBranch(branch string) (Worktree, error) {
	var found []Worktree
	for _, w := range t.List {
		if branch != "" && w.Branch == branch {
			found = append(found, w)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Worktree{}, fmt.Errorf("branch %s is checked out in no worktree of %s", branch, t.Project)
	}
	paths := make([]string, len(found))
	for i, w := range found {
		paths[i] = w.Path
	}
	return Worktree{}, fmt.Errorf("branch %s is checked out in several worktrees of %s: %s", branch, t.Project, strings.Join(paths, ", "))
}

// Revision is what an answer read (§3): a branch moves, a head does not.
type Revision struct {
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Dirty    bool   `json:"dirty"` // uncommitted changes, untracked files included
}

// Stamp reads w's revision now: its branch ("" when detached), its HEAD,
// and whether it is dirty. The branch is read again, never taken from the
// list: a worktree can switch branch in between.
func Stamp(ctx context.Context, w Worktree, env []string) (Revision, error) {
	out, err := git(ctx, w.Path, env, "rev-parse", "HEAD", "--abbrev-ref", "HEAD")
	if err != nil {
		return Revision{}, err
	}
	head, branch, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if branch == "HEAD" {
		branch = ""
	}
	st, err := git(ctx, w.Path, env, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return Revision{}, err
	}
	return Revision{w.Path, branch, head, strings.TrimSpace(st) != ""}, nil
}

// git runs git -C dir with exactly env. It never takes an optional lock:
// status would otherwise refresh the index under index.lock in the
// agent's own worktree, and the agent's git add or commit would fail on it
// (foureyes). core.fsmonitor is off, so no worktree's config runs a command
// from here.
func git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	c.Env = env
	out, err := c.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}
