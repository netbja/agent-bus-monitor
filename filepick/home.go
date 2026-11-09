package filepick

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The @~/ listing (addendum docs/superpowers/specs/2026-11-09-compose-at-
// file-home-addendum.md): the person's home, one directory at a time, never
// as a tree; hidden entries at its top (every credential store) never
// offered; everything else through the project's filters, judged on resolved
// paths. A home path is inserted absolute (§4), which the caller does.

// ErrNoHome: $HOME is empty, relative or missing; nothing is offered.
var ErrNoHome = errors.New("no home directory")

// libraryHidden: on macOS, ~/Library holds the Keychains, cookies and the
// applications' tokens under names with no dot, so it is treated as hidden
// at the top of home (foureyes). A variable for its test.
var libraryHidden = runtime.GOOS == "darwin"

// errNotOffered is a directory the listing will not read, with its reason.
type errNotOffered struct{ why string }

func (e errNotOffered) Error() string { return "not offered: " + e.why }

// HomeQuery is a query under ~: the directory to list (slash form, relative
// to home, "" for home itself) and the name filter after its last slash.
type HomeQuery struct {
	Dir, Filter string
}

// ParseHome reads a query: isHome when it starts with ~; why when it is
// refused before anything is read (§2): another user's home, a backslash, a
// . or .. segment, or an empty one other than the one trailing slash that
// marks a directory.
func ParseHome(q string) (h HomeQuery, why string, isHome bool) {
	if !strings.HasPrefix(q, "~") {
		return HomeQuery{}, "", false
	}
	if q == "~" {
		return HomeQuery{}, "", true
	}
	if !strings.HasPrefix(q, "~/") {
		return HomeQuery{}, "only your own home (~/)", true
	}
	rest := q[2:]
	const refuse = "type the path yourself"
	if strings.ContainsRune(rest, '\\') {
		return HomeQuery{}, refuse, true
	}
	segs := strings.Split(rest, "/")
	for i, s := range segs {
		if s == "" && i == len(segs)-1 { // the directory marker, or ~/ itself
			continue
		}
		if s == "" || s == "." || s == ".." {
			return HomeQuery{}, refuse, true
		}
	}
	if strings.HasSuffix(rest, "/") || rest == "" {
		return HomeQuery{Dir: strings.TrimSuffix(rest, "/")}, "", true
	}
	dir := path.Dir(rest)
	if dir == "." {
		dir = ""
	}
	return HomeQuery{Dir: dir, Filter: path.Base(rest)}, "", true
}

// hiddenTop: rel's first segment is hidden at home's top (§3 rule 1).
func hiddenTop(rel string) bool {
	first, _, _ := strings.Cut(rel, "/")
	return strings.HasPrefix(first, ".") || libraryHidden && first == "Library"
}

// homeRefused is why rel under home is never offered: rule 1 first, then the
// project's rules (unusual name, secretpath, the shared denies).
func (w *walker) homeRefused(rel string) string {
	if hiddenTop(rel) {
		return "hidden in your home: type it yourself"
	}
	return w.refused(rel, w.abs(rel))
}

// homeWalker is a walker rooted at home.
func homeWalker(ctx context.Context, home string, o Options) (*walker, error) {
	if !filepath.IsAbs(home) {
		return nil, ErrNoHome
	}
	home = filepath.Clean(home)
	if st, err := os.Stat(home); err != nil || !st.IsDir() {
		return nil, ErrNoHome
	}
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, ErrNoHome
	}
	w := &walker{ctx: ctx, root: home, real: real, deny: o.Deny, max: or(o.MaxEntries, 50000), timeout: o.Timeout}
	if w.timeout <= 0 {
		w.timeout = 2 * time.Second
	}
	w.deadline = nowFunc().Add(w.timeout)
	return w, nil
}

// inHome is p (resolved) relative to the resolved home, slash form; false
// when it lies outside.
func (w *walker) inHome(p string) (string, bool) {
	rel, err := filepath.Rel(w.real, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

// passes: every component of rel (resolved, relative to home) passes the
// rules and the skip list (§2: a link on the way is judged where it lands).
func (w *walker) passes(rel string) string {
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		at := path.Join(parts[:i+1]...)
		if skipDirs[parts[i]] {
			return "skipped"
		}
		if why := w.homeRefused(at); why != "" {
			return why
		}
	}
	return ""
}

// ListDir lists the one directory dir (slash form, relative to home, "" for
// home) with the filters of §3. The directory is resolved and must lie in
// the resolved home, every component of the typed and of the resolved path
// passing the rules; an entry's path keeps the typed form. Read in batches,
// with the cap and the clock at every entry.
func ListDir(ctx context.Context, home, dir string, o Options) (Result, error) {
	w, err := homeWalker(ctx, home, o)
	if err != nil {
		return Result{}, err
	}
	dir = strings.Trim(filepath.ToSlash(dir), "/")
	if dir != "" {
		if _, why, _ := ParseHome("~/" + dir + "/"); why != "" {
			return Result{}, errNotOffered{why}
		}
	}
	if why := w.passes(dir); why != "" {
		return Result{}, errNotOffered{why}
	}
	resolved, err := filepath.EvalSymlinks(w.abs(dir))
	if err != nil {
		return Result{}, errNotOffered{"cannot be read"}
	}
	rrel, ok := w.inHome(resolved)
	if !ok {
		return Result{}, errNotOffered{"outside your home"}
	}
	if why := w.passes(rrel); why != "" {
		return Result{}, errNotOffered{why}
	}
	f, err := os.Open(resolved)
	if err != nil {
		return Result{}, errNotOffered{"cannot be read"}
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.IsDir() {
		return Result{}, errNotOffered{"not a directory"}
	}
	for {
		ents, rerr := f.ReadDir(readBatch)
		for _, e := range ents {
			if w.stopped() {
				return w.res, nil
			}
			if err := w.homeEntry(dir, rrel, e); err != nil {
				return w.res, nil // the cap: Incomplete says so
			}
		}
		if rerr != nil {
			return w.res, nil
		}
	}
}

// homeEntry offers one entry of the listed directory: dir is its typed path,
// rrel the resolved one, both relative to home.
func (w *walker) homeEntry(dir, rrel string, e fs.DirEntry) error {
	name := e.Name()
	child, real := path.Join(dir, name), path.Join(rrel, name)
	switch t := e.Type(); {
	case t&fs.ModeSymlink != 0: // a file link only, its target judged too
		if w.homeLinkRefused(child) == "" {
			return w.add(Entry{Path: child})
		}
	case t.IsDir():
		if !skipDirs[name] && w.homeRefused(real) == "" && w.homeRefused(child) == "" {
			return w.add(Entry{Path: child, Dir: true})
		}
	case t.IsRegular():
		if w.homeRefused(real) == "" && w.homeRefused(child) == "" {
			return w.add(Entry{Path: child})
		}
	}
	return nil
}

// homeLinkRefused is why the link rel under home is not offered: its
// resolved target must be a regular file inside home, and both the link's
// path and the target's pass the rules.
func (w *walker) homeLinkRefused(rel string) string {
	target, err := filepath.EvalSymlinks(w.abs(rel))
	if err != nil {
		return "not a file"
	}
	st, err := os.Stat(target)
	if err != nil || !st.Mode().IsRegular() {
		return "not a file"
	}
	trel, ok := w.inHome(target)
	if !ok {
		return "outside your home"
	}
	if why := w.homeRefused(rel); why != "" {
		return why
	}
	return w.passes(trel)
}

// HomeRefused is why rel under home would never be offered, for the
// popup's footer when a query names it exactly; "" when it may be.
func HomeRefused(home, rel string, deny func(string) bool) string {
	w, err := homeWalker(context.Background(), home, Options{Deny: deny})
	if err != nil {
		return ErrNoHome.Error()
	}
	rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
	if st, err := os.Lstat(w.abs(rel)); err == nil && st.Mode()&fs.ModeSymlink != 0 {
		return w.homeLinkRefused(rel)
	}
	return w.homeRefused(rel)
}
