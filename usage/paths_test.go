package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both paths are under the home folder; the test home is a temporary one.
func TestPathsUnderHome(t *testing.T) {
	cases := []struct {
		name string
		xdg  string // XDG_CACHE_HOME; "" = unset
		fn   func() string
		want func(home, xdg string) string
	}{
		{"projects root", "", ProjectsRoot, func(h, _ string) string { return filepath.Join(h, ".claude", "projects") }},
		{"ccstatusline without XDG", "", CCStatuslinePath, func(h, _ string) string {
			return filepath.Join(h, ".cache", "ccstatusline", "usage.json")
		}},
		{"ccstatusline with XDG", "set", CCStatuslinePath, func(_, x string) string {
			return filepath.Join(x, "ccstatusline", "usage.json")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			xdg := ""
			if c.xdg != "" {
				xdg = t.TempDir()
			}
			t.Setenv("XDG_CACHE_HOME", xdg)
			if got, want := c.fn(), c.want(home, xdg); got != want {
				t.Errorf("= %q, want %q", got, want)
			}
		})
	}
}

// With no home at all the paths are empty, never a guess.
func TestPathsWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("this platform finds a home without HOME")
	}
	for name, fn := range map[string]func() string{"ProjectsRoot": ProjectsRoot, "CCStatuslinePath": CCStatuslinePath} {
		if got := fn(); got != "" {
			t.Errorf("%s = %q, want empty", name, got)
		}
	}
}

func TestClipText(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"short", 10, "short"},
		{"  spaced\n\tout  text ", 50, "spaced out text"},
		{"abcdef", 3, "abc"},
		{"éàüöß", 2, "éà"}, // runes, not bytes
		{"exact", 5, "exact"},
	}
	for _, c := range cases {
		if got := clipText(c.in, c.n); got != c.want {
			t.Errorf("clipText(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// Files above the tail window: the tail is read first, and a last turn
// larger than the window falls back to the whole file.
func TestReadTranscriptLargeFiles(t *testing.T) {
	pad := `{"type":"user","message":{"content":"` + strings.Repeat("x", 1000) + `"}}`
	padding := func(bytes int) []string {
		var out []string
		for n := 0; n < bytes; n += len(pad) + 1 {
			out = append(out, pad)
		}
		return out
	}
	cases := []struct {
		name      string
		lines     []string
		wantModel string
		wantCtx   int
	}{
		{
			name:      "last turn inside the tail",
			lines:     append(append([]string{entry("old", 1, 0, 0, false, "2026-11-01T00:00:00Z")}, padding(tailBytes+4096)...), entry("new", 10, 20, 30, false, "2026-11-04T00:00:00Z")),
			wantModel: "new", wantCtx: 60,
		},
		{
			name:      "no turn in the tail: the whole file is scanned",
			lines:     append([]string{entry("early", 5, 5, 5, false, "2026-11-01T00:00:00Z")}, padding(tailBytes+4096)...),
			wantModel: "early", wantCtx: 15,
		},
		{
			name:  "no turn anywhere",
			lines: padding(tailBytes + 4096),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, path := writeTranscript(t, "big-session", c.lines...)
			if info, err := os.Stat(path); err != nil || info.Size() <= tailBytes {
				t.Fatalf("fixture not above the tail window: %v", err)
			}
			got, err := ReadTranscript(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != c.wantModel || got.ContextTokens != c.wantCtx {
				t.Errorf("= %+v, want model %q ctx %d", got, c.wantModel, c.wantCtx)
			}
		})
	}
}

func TestReadTranscriptMissing(t *testing.T) {
	if _, err := ReadTranscript(filepath.Join(t.TempDir(), "absent.jsonl")); err == nil {
		t.Fatal("a missing transcript read")
	}
}
