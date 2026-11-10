package filepick

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// homeFixture is a fake home with credential stores at its top, ordinary
// folders, a project with its own dot entries, and links of every kind.
func homeFixture(t *testing.T) (home, outside string) {
	t.Helper()
	base := t.TempDir()
	home, outside = filepath.Join(base, "home"), filepath.Join(base, "outside")
	for rel, body := range map[string]string{
		".ssh/id_ed25519": "k", ".ssh/config": "", ".config/gh/hosts.yml": "", ".netrc": "", ".env": "",
		"Documents/report.md": "", "Documents/notes/a.md": "", "Downloads/log.txt": "", "notes.md": "",
		"p/x/.github/ci.yml": "", "p/x/.git/config": "", "p/x/.env": "", "p/x/main.go": "",
		"private/x": "", "node_modules/m.js": "", "Library/Keychains/login.keychain": "",
	} {
		write(t, home, rel, body)
	}
	write(t, outside, "passwd", "")
	link(t, filepath.Join(home, ".ssh/id_ed25519"), filepath.Join(home, "key"))      // a hidden target
	link(t, filepath.Join(outside, "passwd"), filepath.Join(home, "out"))            // outside home
	link(t, filepath.Join(home, "Documents/report.md"), filepath.Join(home, "a.md")) // fine
	link(t, filepath.Join(home, ".ssh"), filepath.Join(home, "docs"))                // a dir link to a hidden dir
	link(t, outside, filepath.Join(home, "etc"))                                     // a dir link outside
	link(t, filepath.Join(home, "p/x"), filepath.Join(home, "proj"))                 // a dir link inside
	link(t, filepath.Join(home, ".aws"), filepath.Join(home, "a"))                   // a link to a hidden top entry
	write(t, home, ".aws/credentials", "")
	return home, outside
}

func denyPrivate(home string) func(string) bool {
	return func(abs string) bool { return strings.HasPrefix(abs, filepath.Join(home, "private")) }
}

// addendum §2: what is a home query, and which are refused, before anything
// is read.
func TestParseHome(t *testing.T) {
	for _, c := range []struct {
		q, dir, filter, why string
		home                bool
	}{
		{"~", "", "", "", true},
		{"~/", "", "", "", true},
		{"~/Doc", "", "Doc", "", true},
		{"~/Documents/", "Documents", "", "", true},
		{"~/Documents/rep", "Documents", "rep", "", true},
		{"~/Documents//", "", "", "type the path yourself", true},
		{"~//x", "", "", "type the path yourself", true},
		{"~/a//b", "", "", "type the path yourself", true},
		{"~/Documents/../.ssh/", "", "", "type the path yourself", true},
		{"~/../", "", "", "type the path yourself", true},
		{"~/./.aws/", "", "", "type the path yourself", true},
		{"~/a\\b", "", "", "type the path yourself", true},
		{"~bob/x", "", "", "only your own home (~/)", true},
		{"cmd/bu", "", "", "", false},
		{"~notes.md", "", "", "", false}, // a project file whose name starts with ~ (reviewer-codex)
		{"~bob", "", "", "", false},      // no slash yet: still a project query
	} {
		h, why, ok := ParseHome(c.q)
		if ok != c.home || why != c.why || why == "" && ok && (h.Dir != c.dir || h.Filter != c.filter) {
			t.Errorf("%q: %+v %q %v, want dir %q filter %q why %q home %v", c.q, h, why, ok, c.dir, c.filter, c.why, c.home)
		}
	}
}

// addendum §3: one directory at a time; hidden entries at home's top never
// offered; deeper dot entries through the filters; links resolved and judged.
func TestListDir(t *testing.T) {
	home, _ := homeFixture(t)
	old := libraryHidden
	t.Cleanup(func() { libraryHidden = old })
	libraryHidden = false // not on darwin: ~/Library is an ordinary folder (TestListDirLibraryOnDarwin)
	ls := func(dir string) []string {
		t.Helper()
		res, err := ListDir(context.Background(), home, dir, Options{Deny: denyPrivate(home)})
		if err != nil || res.Incomplete != "" {
			t.Fatalf("ListDir(%q): %v %q", dir, err, res.Incomplete)
		}
		return paths(res.Entries)
	}
	if got, want := ls(""), []string{"Documents/", "Downloads/", "Library/", "a.md", "notes.md", "p/"}; !slices.Equal(got, want) {
		t.Fatalf("top of home: %q, want %q", got, want)
	}
	if got, want := ls("Documents"), []string{"Documents/notes/", "Documents/report.md"}; !slices.Equal(got, want) {
		t.Fatalf("one level: %q, want %q", got, want)
	}
	if got, want := ls("p/x"), []string{"p/x/.github/", "p/x/main.go"}; !slices.Equal(got, want) {
		t.Fatalf("deeper dot entries: %q, want %q", got, want)
	}
	if got, want := ls("p/x/.github"), []string{"p/x/.github/ci.yml"}; !slices.Equal(got, want) {
		t.Fatalf("a deeper dot dir: %q", got)
	}
	for _, dir := range []string{".ssh", "docs", "etc", "a", "private", "node_modules", ".config/gh"} {
		if _, err := ListDir(context.Background(), home, dir, Options{Deny: denyPrivate(home)}); err == nil {
			t.Errorf("%q was listed", dir)
		}
	}
	link(t, filepath.Join(home, "Documents"), filepath.Join(home, ".dl")) // a hidden-named link to a fine dir
	if _, err := ListDir(context.Background(), home, ".dl", Options{}); err == nil {
		t.Error("a hidden-named directory link at the top was listed (the typed path is judged too)")
	}
	if _, err := ListDir(context.Background(), home, "notes.md", Options{}); err == nil {
		t.Error("a file was listed as a directory")
	}
	if got := ls("proj"); !slices.Equal(got, []string{"proj/.github/", "proj/main.go"}) {
		t.Fatalf("a dir link inside home lists its target's entries: %q", got)
	}
}

// addendum §3: macOS keeps credentials in ~/Library with no dot.
func TestListDirLibraryOnDarwin(t *testing.T) {
	home, _ := homeFixture(t)
	old := libraryHidden
	t.Cleanup(func() { libraryHidden = old })
	libraryHidden = true
	res, _ := ListDir(context.Background(), home, "", Options{})
	if slices.Contains(paths(res.Entries), "Library/") {
		t.Fatal("~/Library listed on darwin")
	}
	if _, err := ListDir(context.Background(), home, "Library", Options{}); err == nil {
		t.Fatal("~/Library walked on darwin")
	}
	libraryHidden = false
	res, _ = ListDir(context.Background(), home, "", Options{})
	if !slices.Contains(paths(res.Entries), "Library/") {
		t.Fatal("~/Library hidden elsewhere")
	}
}

// addendum §3, §7: the reason an exact query is refused.
func TestHomeRefused(t *testing.T) {
	home, _ := homeFixture(t)
	write(t, home, "dotfiles/netrc", "")
	link(t, filepath.Join(home, "dotfiles/netrc"), filepath.Join(home, ".netrc2"))
	link(t, filepath.Join(home, "node_modules"), filepath.Join(home, "mods"))
	link(t, filepath.Join(home, "Documents"), filepath.Join(home, "vendor"))
	for rel, want := range map[string]string{
		".ssh/config": "hidden in your home: type it yourself", ".netrc": "hidden in your home: type it yourself",
		"p/x/.env": "environment file", "p/x/.git/config": "git directory", "private/x": "denied by roles.toml",
		"key": "hidden in your home: type it yourself", "out": "outside your home", "a.md": "", "p/x/main.go": "",
		"docs/config":       "hidden in your home: type it yourself", // through a directory link to ~/.ssh (foureyes)
		"etc/passwd":        "outside your home",                     // through a directory link out of home
		"proj/main.go":      "",                                      // through a directory link inside home
		".netrc2":           "hidden in your home: type it yourself", // a link whose own name is hidden, its target fine
		"node_modules/m.js": "skipped",                               // a skipped dir, typed
		"mods/m.js":         "skipped",                               // reached through a link into a skipped dir
		"vendor/report.md":  "skipped",                               // typed through a skip-named link to a fine dir
		"../x":              "type the path yourself",
		"a/../.ssh/x":       "type the path yourself",
	} {
		if got := HomeRefused(home, rel, denyPrivate(home)); got != want {
			t.Errorf("HomeRefused(%q) = %q, want %q", rel, got, want)
		}
	}
}

// addendum §2: no home directory fails closed.
func TestListDirNoHome(t *testing.T) {
	for _, home := range []string{"", "relative/home", filepath.Join(t.TempDir(), "missing")} {
		if _, err := ListDir(context.Background(), home, "", Options{}); err != ErrNoHome {
			t.Errorf("home %q: %v, want ErrNoHome", home, err)
		}
	}
}

// addendum §3, §7: one big directory is read in batches; the cap and the
// clock hold inside it.
func TestListDirBounded(t *testing.T) {
	home := t.TempDir()
	for i := range 700 {
		write(t, home, fmt.Sprintf("big/f%03d", i), "")
	}
	res, err := ListDir(context.Background(), home, "big", Options{MaxEntries: 300})
	if err != nil || len(res.Entries) != 300 || !strings.Contains(res.Incomplete, "over 300") {
		t.Fatalf("cap: %v %d %q", err, len(res.Entries), res.Incomplete)
	}
	calls := 0
	t0 := time.Now()
	nowFunc = func() time.Time { calls++; return t0.Add(time.Duration(calls) * time.Millisecond) }
	t.Cleanup(func() { nowFunc = time.Now })
	res, err = ListDir(context.Background(), home, "big", Options{Timeout: 100 * time.Millisecond})
	if err != nil || !strings.Contains(res.Incomplete, "took over") || len(res.Entries) >= 200 {
		t.Fatalf("clock: %v %d %q", err, len(res.Entries), res.Incomplete)
	}
}
