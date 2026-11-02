package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tomlFile(t *testing.T, text string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "agentbus")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.toml")
	if text != "" {
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The smallest edit (settings page): the key's own line replaced, or the key
// added under its table, the table appended when missing; every other line,
// comment and order kept byte for byte.
func TestSetSmallestEdit(t *testing.T) {
	const before = "# my settings\n[display]\ntheme = \"light\"  # was dark\nlimit = 40\n\n# attention\n[waiting]\nbell = false\n"
	for name, c := range map[string]struct {
		key  string
		v    any
		want string
	}{
		"a line replaced, its comment kept": {"display.theme", "dark", "# my settings\n[display]\ntheme = \"dark\"  # was dark\nlimit = 40\n\n# attention\n[waiting]\nbell = false\n"},
		"added under its table": {"waiting.snooze", 10 * time.Minute,
			"# my settings\n[display]\ntheme = \"light\"  # was dark\nlimit = 40\n\n# attention\n[waiting]\nbell = false\nsnooze = \"10m\"\n"},
		"added before the next table": {"display.limit", 0, "# my settings\n[display]\ntheme = \"light\"  # was dark\nlimit = 0\n\n# attention\n[waiting]\nbell = false\n"},
		"a table appended": {"dispatch.quota_warn", 80.5,
			before + "\n[dispatch]\nquota_warn = 80.5\n"},
		"a nested table appended": {"health.runaway.screen_repeats", 6,
			before + "\n[health.runaway]\nscreen_repeats = 6\n"},
	} {
		p := tomlFile(t, before)
		if err := Set(p, c.key, c.v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := read(t, p); got != c.want {
			t.Errorf("%s:\n%q\nwant\n%q", name, got, c.want)
		}
		if f := Load(p, "config.toml"); len(f.Warnings) != 0 || !f.Has(c.key) {
			t.Errorf("%s: reloaded %+v", name, f.Warnings)
		}
	}
	// "added under its table" keeps the table's blank line before the next
	// table: the key goes after the table's last key.
	p := tomlFile(t, "[display]\ntheme = \"dark\"\n\n[waiting]\nbell = true\n")
	if err := Set(p, "display.limit", 10); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "[display]\ntheme = \"dark\"\nlimit = 10\n\n[waiting]\nbell = true\n" {
		t.Fatalf("under its table: %q", got)
	}
}

// A new file: the directory 0700, the file 0600, no temporary left behind.
func TestSetCreatesTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x", "agentbus", "config.toml")
	if err := Set(p, "identity.as", "charlie"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "[identity]\nas = \"charlie\"\n" {
		t.Fatalf("new file %q", got)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("left behind: %v", ents)
	}
}

// Never a value its own rule refuses, never a file that does not parse, never
// an edit that would change another key: nothing is written, the reason given.
func TestSetRefuses(t *testing.T) {
	const good = "[display]\ntheme = \"light\"\n"
	p := tomlFile(t, good)
	var r *RefusedError
	if err := Set(p, "display.theme", "purple"); !errors.As(err, &r) || r.Why != "want auto, dark or light" {
		t.Fatalf("refused value: %v", err)
	}
	if err := Set(p, "dispatch.quota_warn", 150.0); !errors.As(err, &r) || !strings.Contains(r.Why, "up to 100") {
		t.Fatalf("refused percent: %v", err)
	}
	if err := Set(p, "no.such_key", 1); err == nil {
		t.Fatal("an unknown key written")
	}
	if read(t, p) != good {
		t.Fatal("a refusal wrote")
	}

	broken := "[display]\ntheme = \"light\"\nlimit = = 3\n"
	p = tomlFile(t, broken)
	var np *NotParsingError
	if err := Set(p, "display.limit", 3); !errors.As(err, &np) || np.Line != 3 {
		t.Fatalf("unparseable: %v", err)
	}
	if read(t, p) != broken {
		t.Fatal("an unparseable file was written")
	}

	// A table header inside a multi-line string fools the line scan: the edit
	// would land in the string, change it and set nothing; read back, refused.
	trap := "[notes]\ntext = \"\"\"\n[display]\n\"\"\"\n"
	p = tomlFile(t, trap)
	if err := Set(p, "display.theme", "dark"); err == nil || read(t, p) != trap {
		t.Fatalf("string trap: %v %q", err, read(t, p))
	}

	// A hand-written inline table the line edit cannot reach: verified, refused.
	inline := "waiting = { bell = true }\n"
	p = tomlFile(t, inline)
	if err := Set(p, "waiting.snooze", 5*time.Minute); err == nil || read(t, p) != inline {
		t.Fatalf("inline table: %v %q", err, read(t, p))
	}
}

// Unset removes the key's line, every other byte kept; an absent key is no
// edit at all.
func TestUnset(t *testing.T) {
	const before = "# mine\n[display]\ntheme = \"light\"\nlimit = 40\n[waiting]\nbell = false\n"
	p := tomlFile(t, before)
	if err := Unset(p, "display.theme"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "# mine\n[display]\nlimit = 40\n[waiting]\nbell = false\n" {
		t.Fatalf("unset: %q", got)
	}
	if err := Unset(p, "waiting.snooze"); err != nil || read(t, p) != "# mine\n[display]\nlimit = 40\n[waiting]\nbell = false\n" {
		t.Fatalf("unset absent: %v", err)
	}
	if err := Unset(filepath.Join(t.TempDir(), "none.toml"), "display.theme"); err != nil {
		t.Fatalf("unset with no file: %v", err)
	}
}

// A trailing comment on the edited line stays after the new value, and a '#'
// inside the old string value is not taken for one (foureyes, part 1).
func TestSetKeepsTheTrailingComment(t *testing.T) {
	for name, c := range map[string]struct {
		before, key string
		v           any
		want        string
	}{
		"after a number":                     {"[display]\nlimit = 40 # lines\n", "display.limit", 10, "[display]\nlimit = 10 # lines\n"},
		"a hash in the old string":           {"[identity]\nas = \"a#b\"\t# me\n", "identity.as", "carol", "[identity]\nas = \"carol\"\t# me\n"},
		"an escaped quote in the old string": {"[identity]\nas = \"a\\\"#b\" # me\n", "identity.as", "carol", "[identity]\nas = \"carol\" # me\n"},
		"a literal string":                   {"[identity]\nas = 'a#b' # me\n", "identity.as", "carol", "[identity]\nas = \"carol\" # me\n"},
		"no comment":                         {"[identity]\nas = \"a#b\"\n", "identity.as", "carol", "[identity]\nas = \"carol\"\n"},
	} {
		p := tomlFile(t, c.before)
		if err := Set(p, c.key, c.v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := read(t, p); got != c.want {
			t.Errorf("%s:\n%q\nwant\n%q", name, got, c.want)
		}
	}
}

// A config.toml that is a symlink (dotfiles) stays one: the target is
// edited, the link kept; Unset alike. A dangling link is refused, never
// replaced by a plain file.
func TestSetFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("[display]\nlimit = 40\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	isLink := func() {
		t.Helper()
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("config.toml is no longer a symlink: %v %v", fi, err)
		}
	}
	if err := Set(link, "display.limit", 10); err != nil {
		t.Fatal(err)
	}
	isLink()
	if got := read(t, target); got != "[display]\nlimit = 10\n" {
		t.Fatalf("target: %q", got)
	}
	if err := Unset(link, "display.limit"); err != nil {
		t.Fatal(err)
	}
	isLink()
	if got := read(t, target); got != "[display]\n" {
		t.Fatalf("target after unset: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*", ".config.toml.*")); len(left) != 0 {
		t.Fatalf("temporary left: %v", left)
	}

	dangling := filepath.Join(dir, "other", "config.toml")
	if err := os.MkdirAll(filepath.Dir(dangling), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere", "config.toml"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := Set(dangling, "display.limit", 10); err == nil {
		t.Fatal("a dangling symlink was written through")
	}
	if fi, err := os.Lstat(dangling); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the dangling symlink was replaced")
	}
}
