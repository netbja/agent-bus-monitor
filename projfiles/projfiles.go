// Package projfiles is what a project's delete may also remove from disk
// (projects-admin §5.5, the person's Q3, Q6 and Q7): with delete_files, the
// project's template and its worktrees; with delete_backups, the archives of
// that project alone, except the one made just before the delete. roles.toml,
// machine-wide, is never touched, and neither is the checkout itself.
//
// Worktrees are read as text, never by running git: git's config can make
// git run a program, and that text is writable by any agent. So a directory
// is removed only on git's own two-way link, judged on resolved paths
// strictly below the working directory (foureyes, reviewer-codex).
package projfiles

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/backup"
)

// Options says what to look at, and which of the two options are on.
type Options struct {
	Project     string
	Template    string // the project's template path (<projects dir>/<p>.toml), "" none
	WorkingDir  string // the template's working_dir, "" none
	BackupDir   string // config.BackupDir
	Host        string // this machine, as archives name it
	Files       bool   // projects.delete_files: the template and the worktrees
	Backups     bool   // projects.delete_backups: the archives of the project alone
	KeepArchive string // the archive the panel's b made just now: never removed (Q7)
}

// Worktree is one worktree that passes git's two-way link.
type Worktree struct {
	Dir    string // D, removed
	Entry  string // W/.git/worktrees/<name>, removed after D
	Rel    string // D relative to W, for the panel
	Branch string // from the entry's HEAD, "" detached or unreadable
}

// Plan is what a delete would remove, and what it keeps and why.
type Plan struct {
	Template     string
	Worktrees    []Worktree
	NotWorktrees []string // "<entry>: not a worktree: kept (why)"
	Archives     []string // of the project alone
	Shared       int      // archives that also hold other projects: kept
	Unreadable   []string // archives whose coverage cannot be read: kept
	Kept         string   // the archive made now, resolved: kept
	Errs         []error  // what could not be read
}

// Empty is a plan that removes nothing.
func (p Plan) Empty() bool {
	return p.Template == "" && len(p.Worktrees) == 0 && len(p.Archives) == 0
}

// Done is what Apply removed, and what failed.
type Done struct {
	Template  string
	Worktrees []string
	Archives  []string
	Errs      []error
}

// PlanFor reads what a delete with o would remove. It removes nothing.
func PlanFor(o Options) (Plan, error) {
	var p Plan
	if o.Files {
		p.Template = template(o)
		p.Worktrees, p.NotWorktrees, p.Errs = worktrees(o.WorkingDir)
	}
	if o.Backups && o.BackupDir != "" {
		if err := archives(o, &p); err != nil {
			return p, err
		}
	}
	return p, nil
}

// Apply plans again, at the moment of removing, and removes what passes:
// the panel's plan was read earlier, and the tree may have changed since.
func Apply(o Options) Done {
	p, err := PlanFor(o)
	d := Done{Errs: p.Errs}
	if err != nil {
		d.Errs = append(d.Errs, err)
	}
	if p.Template != "" {
		if err := os.Remove(p.Template); err != nil { // a link: the link, never its target
			d.Errs = append(d.Errs, err)
		} else {
			d.Template = p.Template
		}
	}
	for _, wt := range p.Worktrees {
		// RemoveAll removes a link and never follows it.
		if err := os.RemoveAll(wt.Dir); err != nil {
			d.Errs = append(d.Errs, err)
			continue
		}
		if err := os.RemoveAll(wt.Entry); err != nil {
			d.Errs = append(d.Errs, err)
		}
		d.Worktrees = append(d.Worktrees, wt.Rel)
	}
	for _, a := range p.Archives {
		if err := os.Remove(a); err != nil {
			d.Errs = append(d.Errs, err)
			continue
		}
		d.Archives = append(d.Archives, a)
	}
	return d
}

// template is o.Template when it is the project's own (<project>.toml), a
// file or a link; "" otherwise.
func template(o Options) string {
	if o.Template == "" || filepath.Base(o.Template) != o.Project+".toml" {
		return ""
	}
	st, err := os.Lstat(o.Template)
	if err != nil || !(st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		return ""
	}
	return o.Template
}

// worktrees reads W/.git/worktrees/* and keeps an entry only when all three
// hold (§5.5): its gitdir names D/.git; D, resolved, lies strictly below the
// resolved W, outside W/.git, with no link on any component; D/.git is a
// regular file whose gitdir resolves to exactly that entry.
func worktrees(w string) ([]Worktree, []string, []error) {
	if w == "" {
		return nil, nil, nil
	}
	rw, err := filepath.EvalSymlinks(w)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, []error{err}
	}
	gitDir := filepath.Join(rw, ".git")
	if st, err := os.Lstat(gitDir); err != nil || !st.IsDir() {
		return nil, nil, nil // no repository, or itself a worktree: nothing listed
	}
	entries, err := os.ReadDir(filepath.Join(gitDir, "worktrees"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, []error{err}
	}
	var out []Worktree
	var kept []string
	for _, e := range entries {
		entry := filepath.Join(gitDir, "worktrees", e.Name())
		wt, why := check(rw, gitDir, entry)
		if why != "" {
			kept = append(kept, e.Name()+": not a worktree: kept ("+why+")")
			continue
		}
		out = append(out, wt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, kept, nil
}

func check(rw, gitDir, entry string) (Worktree, string) {
	if st, err := os.Lstat(entry); err != nil || !st.IsDir() {
		return Worktree{}, "not a directory"
	}
	gd, err := firstLine(filepath.Join(entry, "gitdir"))
	if err != nil {
		return Worktree{}, "no gitdir"
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(entry, gd)
	}
	gd = filepath.Clean(gd)
	if filepath.Base(gd) != ".git" {
		return Worktree{}, "gitdir does not name a .git file"
	}
	d := filepath.Dir(gd)
	rd, err := filepath.EvalSymlinks(d)
	if err != nil {
		return Worktree{}, "its directory cannot be resolved"
	}
	if rd != d {
		return Worktree{}, "a link on the way"
	}
	rel, err := filepath.Rel(rw, rd)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return Worktree{}, "not below the working directory"
	}
	if r, err := filepath.Rel(gitDir, rd); err == nil && (r == "." || !strings.HasPrefix(r, "..")) {
		return Worktree{}, "inside .git"
	}
	st, err := os.Lstat(gd)
	if err != nil || !st.Mode().IsRegular() {
		return Worktree{}, "its .git is not a file"
	}
	back, err := firstLine(gd)
	if err != nil {
		return Worktree{}, "its .git cannot be read"
	}
	back, ok := strings.CutPrefix(back, "gitdir:")
	if !ok {
		return Worktree{}, "its .git names no gitdir"
	}
	back = strings.TrimSpace(back)
	if !filepath.IsAbs(back) {
		back = filepath.Join(d, back)
	}
	rb, err := filepath.EvalSymlinks(back)
	if err != nil || rb != entry {
		return Worktree{}, "its .git points elsewhere"
	}
	branch := ""
	if head, err := firstLine(filepath.Join(entry, "HEAD")); err == nil {
		branch, _ = strings.CutPrefix(head, "ref: refs/heads/")
		if strings.HasPrefix(branch, "ref:") || len(branch) == 40 {
			branch = ""
		}
	}
	return Worktree{Dir: d, Entry: entry, Rel: filepath.ToSlash(rel), Branch: branch}, ""
}

// firstLine is a file's first line, read with a bounded reader.
func firstLine(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s, err := bufio.NewReader(io.LimitReader(f, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// archives sorts this host's archives in o.BackupDir: the project's alone go,
// unless it is the one made now; shared and unreadable ones stay.
func archives(o Options, p *Plan) error {
	list, err := backup.List(o.BackupDir, o.Host)
	if err != nil {
		return fmt.Errorf("the backup folder: %w", err)
	}
	var keep os.FileInfo
	if o.KeepArchive != "" {
		if r, err := filepath.EvalSymlinks(o.KeepArchive); err == nil {
			if st, err := os.Stat(r); err == nil {
				keep = st
			}
		}
	}
	for _, a := range list {
		if keep != nil {
			if st, err := os.Stat(a.Path); err == nil && os.SameFile(st, keep) {
				p.Kept = a.Path
				continue
			}
		}
		if a.Err != nil {
			p.Unreadable = append(p.Unreadable, a.Path)
			continue
		}
		mine := false
		for _, q := range a.Coverage.Projects {
			mine = mine || q == o.Project
		}
		switch {
		case !mine:
		case len(a.Coverage.Projects) == 1:
			p.Archives = append(p.Archives, a.Path)
		default:
			p.Shared++
		}
	}
	return nil
}

// Lines are the panel's (and busmon --delete's) words for p.
func (p Plan) Lines(project string) []string {
	var out []string
	if p.Template != "" {
		out = append(out, "template "+p.Template)
	}
	if len(p.Worktrees) > 0 {
		var ws []string
		for _, w := range p.Worktrees {
			s := w.Rel
			if w.Branch != "" {
				s += " (" + w.Branch + ")"
			}
			ws = append(ws, s)
		}
		out = append(out, "worktrees: "+strings.Join(ws, ", ")+": uncommitted changes in them are lost")
	}
	if len(p.NotWorktrees) > 0 {
		out = append(out, fmt.Sprintf("%d %s: not a worktree, kept", len(p.NotWorktrees), plural(len(p.NotWorktrees), "entry", "entries")))
	}
	if b := p.backupLine(project); b != "" {
		out = append(out, b)
	}
	if p.Kept != "" {
		out = append(out, "kept: "+p.Kept+" (made now)")
	}
	return out
}

func (p Plan) backupLine(project string) string {
	var parts []string
	if n := len(p.Archives); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s of %s alone", n, plural(n, "archive", "archives"), project))
	}
	if p.Shared > 0 {
		parts = append(parts, fmt.Sprintf("%d %s also hold other projects: kept", p.Shared, plural(p.Shared, "archive", "archives")))
	}
	if n := len(p.Unreadable); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unreadable: kept", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return "backups: " + strings.Join(parts, "; ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
