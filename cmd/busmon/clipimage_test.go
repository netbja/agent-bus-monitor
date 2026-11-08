package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

var pngBytes = append(append([]byte{}, pngMagic...), []byte("rest of a picture")...)

// fakeClip is a machine's clipboard tools: the ones on PATH, each command's
// answer, and what ran.
type fakeClip struct {
	env   map[string]string // the session: WAYLAND_DISPLAY, DISPLAY
	goos  string
	tools map[string]bool
	out   map[string][]byte
	err   map[string]error
	ran   []string
	// endless is a command whose output never ends, read through the bound
	// the caller asks for; read counts the bytes taken from it.
	endless string
	read    int
}

// countingReader is an endless output that counts what is read of it.
type countingReader struct{ n *int }

func (r countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	*r.n += len(p)
	return len(p), nil
}

func (f *fakeClip) tool() clipTools {
	goos := f.goos
	if goos == "" {
		goos = "linux"
	}
	return clipTools{
		env:  func(k string) string { return f.env[k] },
		goos: goos,
		look: func(name string) (string, error) {
			if f.tools[name] {
				return "/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		out: func(max int, name string, args ...string) ([]byte, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			f.ran = append(f.ran, key)
			if key == f.endless {
				return boundedRead(countingReader{&f.read}, max)
			}
			return f.out[key], f.err[key]
		},
	}
}

func TestClipboardImageReadsWhicheverToolExists(t *testing.T) {
	cases := map[string]struct {
		clip *fakeClip
		want error
	}{
		"wayland": {&fakeClip{env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, tools: map[string]bool{"wl-paste": true, "xclip": true}, out: map[string][]byte{
			"wl-paste --list-types": []byte("text/plain\nimage/png\n"), "wl-paste --no-newline --type image/png": pngBytes}}, nil},
		// an X11 session with wl-clipboard installed too (Fedora) reads with xclip (foureyes)
		"x11": {&fakeClip{env: map[string]string{"DISPLAY": ":0"}, tools: map[string]bool{"wl-paste": true, "xclip": true}, out: map[string][]byte{
			"xclip -selection clipboard -t TARGETS -o": []byte("TARGETS\nimage/png\n"), "xclip -selection clipboard -t image/png -o": pngBytes}}, nil},
		"macos":      {&fakeClip{goos: "darwin", tools: map[string]bool{"pngpaste": true}, out: map[string][]byte{"pngpaste -": pngBytes}}, nil},
		"no tool":    {&fakeClip{env: map[string]string{"DISPLAY": ":0"}}, errNoClipTool},
		"no session": {&fakeClip{tools: map[string]bool{"wl-paste": true, "xclip": true}}, errNoClipTool},
		"text only": {&fakeClip{env: map[string]string{"WAYLAND_DISPLAY": "w"}, tools: map[string]bool{"wl-paste": true}, out: map[string][]byte{
			"wl-paste --list-types": []byte("text/plain\n")}}, errNoClipImage},
		"not a png":          {&fakeClip{goos: "darwin", tools: map[string]bool{"pngpaste": true}, out: map[string][]byte{"pngpaste -": []byte("GIF89a")}}, errNoClipImage},
		"pngpaste with none": {&fakeClip{goos: "darwin", tools: map[string]bool{"pngpaste": true}, err: map[string]error{"pngpaste -": errors.New("exit 1")}}, errNoClipImage},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := clipboardImage(c.clip.tool())
			if !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v (ran %v)", err, c.want, c.clip.ran)
			}
			if c.want == nil && string(data) != string(pngBytes) {
				t.Fatalf("data %q", data)
			}
		})
	}
}

// The image is saved 0600 in a 0700 directory, never anywhere else, and
// nothing leaves the machine.
func TestSaveAttachment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attachments")
	path, err := saveAttachment(dir, pngBytes)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || filepath.Ext(path) != ".png" {
		t.Fatalf("path %s", path)
	}
	if st, _ := os.Stat(path); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", st)
	}
	if st, _ := os.Stat(dir); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st)
	}
	if b, _ := os.ReadFile(path); string(b) != string(pngBytes) {
		t.Fatal("content")
	}
	if other, _ := saveAttachment(dir, pngBytes); other == path {
		t.Fatal("two pastes share a file")
	}
}

// Ctrl+V in the compose window pastes the clipboard's image: saved, its path
// inserted at the cursor; without one, it says why and inserts nothing.
func TestComposePastesAnImage(t *testing.T) {
	m := newShell(shellConfig{project: "p", self: "charlie"})
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	dir := filepath.Join(t.TempDir(), "attachments")
	m.cfg.attachDir = dir
	clip := &fakeClip{goos: "darwin", tools: map[string]bool{"pngpaste": true}, out: map[string][]byte{"pngpaste -": pngBytes}}
	m.cfg.clip = clip.tool()
	m = keys(t, m, 'I')
	if m.ui.compose == nil {
		t.Fatal("no compose window")
	}
	m.ui.compose.area.set("see") // the text so far; the cursor at its end
	m = stepCmd(t, m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	text := m.ui.compose.area.value()
	if !strings.HasPrefix(text, "see "+dir+string(filepath.Separator)) || !strings.HasSuffix(text, ".png") {
		t.Fatalf("text %q", text)
	}
	if !strings.Contains(m.ui.flash, "image saved") {
		t.Errorf("flash %q", m.ui.flash)
	}

	clip.out["pngpaste -"] = []byte("plain text")
	before := m.ui.compose.area.value()
	m = stepCmd(t, m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if m.ui.compose.area.value() != before || !m.ui.flashErr || !strings.Contains(m.ui.flash, "no image in the clipboard") {
		t.Errorf("text %q flash %q", m.ui.compose.area.value(), m.ui.flash)
	}
}

// stepCmd is one Update, then its command run and its message fed back once.
func stepCmd(t testing.TB, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd == nil {
		return m
	}
	if out := cmd(); out != nil {
		m = step(t, m, out)
	}
	return m
}

// The path's separators come from the text around the cursor, before and
// after it, never from the end of the draft (reviewer-codex).
func TestPastedPathSeparators(t *testing.T) {
	for _, c := range []struct {
		text string
		cur  int
		want string
	}{
		{"hello world", 6, "hello P world"},
		{"hello world", 5, "hello P world"},
		{"see", 3, "see P"},
		{"", 0, "P"},
		{"a\nb", 2, "a\nP b"},
	} {
		a := textArea{limit: 1000}
		a.set(c.text)
		a.cur = c.cur
		insertPath(&a, "P")
		if got := a.value(); got != c.want {
			t.Errorf("%q at %d: %q, want %q", c.text, c.cur, got, c.want)
		}
	}
}

// A clipboard reader's output is read through its bound: past it, the
// reader is stopped and refused, never buffered whole (reviewer-codex).
func TestBoundedOutputStopsAtItsBound(t *testing.T) {
	if _, err := exec.LookPath("yes"); err != nil {
		t.Skip("no yes on PATH")
	}
	done := make(chan error, 1)
	go func() {
		_, err := boundedOutput(exec.Command("yes"), 4096)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an endless output was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not stop at its bound")
	}
	if out, err := boundedOutput(exec.Command("printf", "ok"), 4096); err != nil || string(out) != "ok" {
		t.Fatalf("%q %v", out, err)
	}
}

// A clipboard tool that hangs is killed at its timeout (foureyes).
func TestBoundedOutputHasATimeout(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := boundedOutput(exec.CommandContext(ctx, "sleep", "10"), 4096); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

// Each paste prunes our own attachments older than 30 days: never another
// file, never a link, never a newer one (foureyes).
func TestPruneAttachments(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-31 * 24 * time.Hour)
	mk := func(name string, at time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	gone := mk("20260901-101010-0123abcd.png", old)
	fresh := mk("20261102-101010-0123abce.png", time.Now())
	theirs := mk("notes.png", old)
	link := filepath.Join(dir, "20260901-101011-0123abcf.png")
	if err := os.Symlink(theirs, link); err != nil {
		t.Fatal(err)
	}
	pruneAttachments(dir, time.Now())
	for p, want := range map[string]bool{gone: false, fresh: true, theirs: true, link: true} {
		_, err := os.Lstat(p)
		if (err == nil) != want {
			t.Errorf("%s: exists %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}

// reviewer-codex: a text past clipTextMax is refused after reading about
// clipTextMax, never the image's 20 MiB, and the paste says why.
func TestClipboardTextRefusesALongTextWithoutReadingItAll(t *testing.T) {
	f := &fakeClip{env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, tools: map[string]bool{"wl-paste": true},
		out:     map[string][]byte{"wl-paste --list-types": []byte("text/plain\n")},
		endless: "wl-paste --no-newline --type text"}
	if _, err := clipboardText(f.tool()); !errors.Is(err, errClipTooLarge) {
		t.Fatalf("a long text: %v, want too large", err)
	}
	if f.read > 2*clipTextMax {
		t.Fatalf("read %d bytes of the text, want at most about %d", f.read, clipTextMax+1)
	}
	f.read = 0
	msg := pasteImageCmd(f.tool(), t.TempDir(), nil)().(pastedImageMsg)
	if msg.text != "" || !errors.Is(msg.err, errClipTooLarge) || !strings.Contains(msg.err.Error(), "1 MiB") {
		t.Fatalf("the paste of a long text: %+v", msg)
	}
	if f.read > 2*clipTextMax {
		t.Fatalf("the paste read %d bytes of the text", f.read)
	}
}
