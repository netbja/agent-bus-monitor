// Package filepick lists a project's files for busmon's @ in the compose
// window (spec docs/superpowers/specs/2026-11-08-compose-at-file-design.md):
// a walk in Go that runs no program, honours the .gitignore files read as
// text, never follows a directory link, offers a file link only when its
// target is a regular file inside the root that passes every filter, lists a
// nested repository or worktree as one entry, and never offers what looks
// like a secret or what roles.toml denies every agent. It reads names only,
// never a file's content (apart from .gitignore and info/exclude).
package filepick

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/netbja/agent-bus-monitor/internal/secretpath"
)

// Entry is one path offered: relative to the root, slash-separated.
type Entry struct {
	Path string
	Dir  bool
	Repo bool // a directory holding a .git entry (a worktree, a nested repository): one entry, walked on demand
}

// Options bound and filter a walk. Zero values take the spec's bounds.
type Options struct {
	Root       string            // the project's checkout, absolute
	Sub        string            // "" or a Repo entry's path: walk inside it only, paths keep the prefix (§2.2)
	Deny       func(string) bool // roles.toml's shared Read denies, on absolute paths; nil: none
	MaxDepth   int               // 12
	MaxEntries int               // 50 000
	Timeout    time.Duration     // 2 s
}

// Result is what a walk found; Incomplete says why it stopped early ("" when
// it did not), and Entries holds what it had read by then.
type Result struct {
	Entries    []Entry
	Incomplete string
}

// ErrNoCheckout: the root is not an absolute path to a directory on this
// machine (busmon against a remote broker, a missing working_dir).
var ErrNoCheckout = errors.New("no checkout on this machine")

// skipDirs are never walked: too big to be useful.
var skipDirs = map[string]bool{"node_modules": true, ".venv": true, "vendor": true}

const maxIgnoreFile = 1 << 20 // a .gitignore larger than this is not read

var errStop = errors.New("stop")

type walker struct {
	ctx      context.Context
	root     string // as given, cleaned
	real     string // the root with its links resolved: where a link's target must lie
	deny     func(string) bool
	maxDepth int
	max      int
	timeout  time.Duration
	deadline time.Time
	res      Result
}

// List walks the root (or Sub inside it) and returns what may be offered.
func List(ctx context.Context, o Options) (Result, error) {
	if !filepath.IsAbs(o.Root) {
		return Result{}, ErrNoCheckout
	}
	root := filepath.Clean(o.Root)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return Result{}, ErrNoCheckout
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, ErrNoCheckout
	}
	w := &walker{ctx: ctx, root: root, real: real, deny: o.Deny,
		maxDepth: or(o.MaxDepth, 12), max: or(o.MaxEntries, 50000), timeout: o.Timeout}
	if w.timeout <= 0 {
		w.timeout = 2 * time.Second
	}
	w.deadline = time.Now().Add(w.timeout)
	var rules ignoreSet
	start := ""
	if o.Sub != "" {
		sub := filepath.ToSlash(filepath.Clean(o.Sub))
		if filepath.IsAbs(o.Sub) || sub == "." || sub == ".." || strings.HasPrefix(sub, "../") || !w.isRepo(sub) {
			return Result{}, fmt.Errorf("%s is not a repository inside the project", o.Sub)
		}
		start = sub
	} else if b, ok := readSmall(filepath.Join(root, ".git", "info", "exclude")); ok {
		rules = rules.with("", b)
	}
	if err := w.dir(start, 0, rules); err != nil && !errors.Is(err, errStop) {
		return w.res, err
	}
	return w.res, nil
}

func or(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// stopped says whether the walk must end now, and records why.
func (w *walker) stopped() bool {
	if w.ctx.Err() != nil || !time.Now().Before(w.deadline) {
		w.res.Incomplete = "took over " + w.timeout.String()
		return true
	}
	return false
}

func (w *walker) add(e Entry) error {
	if len(w.res.Entries) >= w.max {
		w.res.Incomplete = fmt.Sprintf("over %d entries", w.max)
		return errStop
	}
	w.res.Entries = append(w.res.Entries, e)
	return nil
}

func (w *walker) abs(rel string) string { return filepath.Join(w.root, filepath.FromSlash(rel)) }

// isRepo: rel holds a .git entry, a file (a worktree) or a directory.
func (w *walker) isRepo(rel string) bool {
	st, err := os.Lstat(w.abs(rel))
	if err != nil || !st.IsDir() {
		return false
	}
	_, err = os.Lstat(filepath.Join(w.abs(rel), ".git"))
	return err == nil
}

// dir lists the directory rel, whose entries sit at depth+1.
func (w *walker) dir(rel string, depth int, rules ignoreSet) error {
	if w.stopped() {
		return errStop
	}
	if b, ok := readSmall(filepath.Join(w.abs(rel), ".gitignore")); ok {
		rules = rules.with(rel, b)
	}
	ents, err := os.ReadDir(w.abs(rel))
	if err != nil {
		return nil // an unreadable directory is left out, the walk goes on
	}
	for i, e := range ents {
		if i%256 == 255 && w.stopped() {
			return errStop
		}
		name := e.Name()
		child := name
		if rel != "" {
			child = rel + "/" + name
		}
		switch t := e.Type(); {
		case name == ".git":
			continue
		case t&fs.ModeSymlink != 0:
			if w.linkOK(child, rules) {
				if err := w.add(Entry{Path: child}); err != nil {
					return err
				}
			}
		case t.IsDir():
			if skipDirs[name] || rules.ignored(child, true) || w.refused(child, w.abs(child)) != "" {
				continue
			}
			if w.isRepo(child) { // a worktree or a nested repository: one entry (§2.2)
				if err := w.add(Entry{Path: child, Dir: true, Repo: true}); err != nil {
					return err
				}
				continue
			}
			if err := w.add(Entry{Path: child, Dir: true}); err != nil {
				return err
			}
			if depth+1 >= w.maxDepth {
				w.res.Incomplete = fmt.Sprintf("deeper than %d levels", w.maxDepth)
				continue
			}
			if err := w.dir(child, depth+1, rules); err != nil {
				return err
			}
		case t.IsRegular():
			if rules.ignored(child, false) || w.refused(child, w.abs(child)) != "" {
				continue
			}
			if err := w.add(Entry{Path: child}); err != nil {
				return err
			}
		}
	}
	return nil
}

// linkOK: a link is offered only when its target, resolved, is a regular file
// inside the root and both the link and the target pass every filter; a
// directory link is never listed nor walked (no loop, no escape).
func (w *walker) linkOK(rel string, rules ignoreSet) bool {
	return !rules.ignored(rel, false) && w.linkRefused(rel) == ""
}

func (w *walker) linkRefused(rel string) string {
	target, err := filepath.EvalSymlinks(w.abs(rel))
	if err != nil {
		return "not a file"
	}
	st, err := os.Stat(target)
	if err != nil || !st.Mode().IsRegular() {
		return "not a file"
	}
	in, err := filepath.Rel(w.real, target)
	if err != nil || in == ".." || strings.HasPrefix(in, ".."+string(filepath.Separator)) {
		return "outside the project"
	}
	if why := w.refused(rel, w.abs(rel)); why != "" {
		return why
	}
	return w.refused(filepath.ToSlash(in), filepath.Join(w.root, in))
}

// refused is why a path (relative and absolute) is never offered: a secret
// kind, a deny of roles.toml, an unusual name; "" when it may be.
func (w *walker) refused(rel, abs string) string {
	if strings.ContainsRune(rel, '`') || strings.IndexFunc(rel, unicode.IsControl) >= 0 {
		return "unusual name"
	}
	if kind, ok := secretpath.Is(rel); ok {
		return kind
	}
	if w.deny != nil && w.deny(abs) {
		return "denied by roles.toml"
	}
	return ""
}

// Refused is why the file rel would never be offered, for the popup's footer
// when a query names it exactly (§3): its secret kind, "denied by
// roles.toml", "unusual name", or for a link "outside the project" or "not a
// file"; "" when it may be offered.
func Refused(root, rel string, deny func(string) bool) string {
	root = filepath.Clean(root)
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		real = root
	}
	w := &walker{root: root, real: real, deny: deny}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if st, err := os.Lstat(w.abs(rel)); err == nil && st.Mode()&fs.ModeSymlink != 0 {
		return w.linkRefused(rel)
	}
	return w.refused(rel, w.abs(rel))
}

// SubFor is the Repo entry a query walks into (§2.2): the query starts with
// that entry's path and a slash.
func SubFor(entries []Entry, query string) (string, bool) {
	for _, e := range entries {
		if e.Repo && strings.HasPrefix(query, e.Path+"/") {
			return e.Path, true
		}
	}
	return "", false
}

// readSmall reads a regular file (never through a link) of at most
// maxIgnoreFile bytes.
func readSmall(abs string) (string, bool) {
	st, err := os.Lstat(abs)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxIgnoreFile {
		return "", false
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	return string(b), true
}
