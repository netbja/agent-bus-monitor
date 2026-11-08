package main

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func composeClip(t *testing.T, clip *fakeClip) Model {
	t.Helper()
	m := newShell(shellConfig{project: "p", self: "charlie"})
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m.cfg.attachDir = filepath.Join(t.TempDir(), "attachments")
	m.cfg.clip = clip.tool()
	m = keys(t, m, 'I')
	if m.ui.compose == nil {
		t.Fatal("no compose window")
	}
	return m
}

var ctrlV = tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}

// The person: speech-to-text tools (Murmure, Handy) paste with Ctrl+V; with
// text and no image in the clipboard, Ctrl+V inserts the text, as a paste.
func TestComposeCtrlVPastesText(t *testing.T) {
	for name, clip := range map[string]*fakeClip{
		"wayland": {env: map[string]string{"WAYLAND_DISPLAY": "w"}, tools: map[string]bool{"wl-paste": true}, out: map[string][]byte{
			"wl-paste --list-types": []byte("text/plain;charset=utf-8\nUTF8_STRING\n"), "wl-paste --no-newline --type text": []byte("bonjour master")}},
		"x11": {env: map[string]string{"DISPLAY": ":0"}, tools: map[string]bool{"xclip": true}, out: map[string][]byte{
			"xclip -selection clipboard -t TARGETS -o": []byte("TARGETS\nUTF8_STRING\n"), "xclip -selection clipboard -o": []byte("bonjour master")}},
		"macos": {goos: "darwin", tools: map[string]bool{"pngpaste": true, "pbpaste": true}, out: map[string][]byte{
			"pngpaste -": []byte("not a png"), "pbpaste": []byte("bonjour master")}},
	} {
		m := composeClip(t, clip)
		m.ui.compose.onTo = false // the text has the keyboard, as when the person dictates into it
		m.ui.compose.area.set("dit ")
		m = stepCmd(t, m, ctrlV)
		if got := m.ui.compose.area.value(); got != "dit bonjour master" || m.ui.flashErr {
			t.Errorf("%s: text %q flash %q", name, got, m.ui.flash)
		}
	}
	// Neither an image nor a text: it says so, inserts nothing.
	m := composeClip(t, &fakeClip{goos: "darwin", tools: map[string]bool{"pngpaste": true}, out: map[string][]byte{"pngpaste -": []byte("x")}})
	m = stepCmd(t, m, ctrlV)
	if m.ui.compose.area.value() != "" || !strings.Contains(m.ui.flash, "no image in the clipboard") {
		t.Errorf("empty: %q %q", m.ui.compose.area.value(), m.ui.flash)
	}
}

// The person: the same image pasted twice is not inserted twice: the window
// says it is already there; deleted from the text, a new paste puts the same
// file back, never a second copy on disk.
func TestComposeSameImageTwice(t *testing.T) {
	clip := &fakeClip{goos: "darwin", tools: map[string]bool{"pngpaste": true}, out: map[string][]byte{"pngpaste -": pngBytes}}
	m := composeClip(t, clip)
	m = stepCmd(t, m, ctrlV)
	first := m.ui.compose.area.value()
	m = stepCmd(t, m, ctrlV)
	if m.ui.compose.area.value() != first || !strings.Contains(m.ui.flash, "already in the message") {
		t.Fatalf("twice: %q flash %q", m.ui.compose.area.value(), m.ui.flash)
	}
	m.ui.compose.area.set("")
	m = stepCmd(t, m, ctrlV)
	if m.ui.compose.area.value() != strings.TrimSpace(first) {
		t.Fatalf("after it was removed: %q, want %q", m.ui.compose.area.value(), first)
	}
	files, _ := filepath.Glob(filepath.Join(m.cfg.attachDir, "*.png"))
	if len(files) != 1 {
		t.Fatalf("%d files saved, want 1: %v", len(files), files)
	}
	clip.out["pngpaste -"] = append(append([]byte{}, pngBytes...), '!') // another image: a new file
	m = stepCmd(t, m, ctrlV)
	if files, _ = filepath.Glob(filepath.Join(m.cfg.attachDir, "*.png")); len(files) != 2 {
		t.Fatalf("another image: %d files", len(files))
	}
}

// The person: the footer writes Ctrl, never ^; a click on "Ctrl+S send" sends.
func TestComposeFooterCtrlAndClick(t *testing.T) {
	m := composeClip(t, &fakeClip{})
	m.snap.agents = map[string]*agentState{"master": {state: "idle", registered: true}}
	m.ui.compose.to.set("@master")
	m.ui.compose.area.set("hello")
	plain := m.grid().plain()
	if !strings.Contains(plain, "Ctrl+S send") || strings.Contains(plain, "^S") {
		t.Fatalf("footer:\n%s", plain)
	}
	f, _ := m.composeRects()
	row := strings.Split(plain, "\n")[f.Y+f.H-2]
	x := len([]rune(row[:strings.Index(row, "Ctrl+S send")]))
	fw := &fakeWriter{}
	m.cfg.w = fw
	tab := len([]rune(row[:strings.Index(row, "tab recipient")]))
	m.ui.compose.onTo = false
	if cmd := m.composeClick(tab+1, f.Y+f.H-2); cmd != nil || len(fw.sent) != 0 || !m.ui.compose.onTo {
		t.Fatalf("a click on tab recipient: sent %v, onTo %v", fw.sent, m.ui.compose.onTo)
	}
	cmd := m.composeClick(x+2, f.Y+f.H-2)
	if cmd == nil {
		t.Fatalf("a click on Ctrl+S send did nothing (flash %q)", m.ui.flash)
	}
	cmd()
	if len(fw.sent) == 0 {
		t.Fatalf("a click on Ctrl+S send sent nothing (flash %q)", m.ui.flash)
	}
	if m.composeClick(x+2, f.Y+1) != nil {
		t.Fatal("a click elsewhere sent")
	}
}

// A text pasted with Ctrl+V goes to the field that has the keyboard, as a
// terminal paste does: the recipient row takes its first line only.
func TestComposeCtrlVTextIntoTheRecipient(t *testing.T) {
	m := composeClip(t, &fakeClip{goos: "darwin", tools: map[string]bool{"pbpaste": true}, out: map[string][]byte{"pbpaste": []byte("@master\nrest")}})
	m.ui.compose.onTo = true
	m = stepCmd(t, m, ctrlV)
	if m.ui.compose.to.value() != "@master" || m.ui.compose.area.value() != "" {
		t.Fatalf("to %q text %q", m.ui.compose.to.value(), m.ui.compose.area.value())
	}
}
