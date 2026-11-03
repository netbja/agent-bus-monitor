package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Pasting an image into the compose window (usage-cards, the person's
// choice): Ctrl+V reads the clipboard's image with the machine's own tool,
// saves it 0600 under the cache's attachments (0700), and inserts its path in
// the text. Nothing is uploaded: an agent on this machine reads the file.

var (
	errNoClipTool  = errors.New("no clipboard reader: install wl-clipboard (Wayland), xclip (X11) or pngpaste (macOS)")
	errNoClipImage = errors.New("no image in the clipboard")
)

// clipImageMax bounds what a paste reads.
const clipImageMax = 20 << 20

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'} // the PNG signature

// clipTools runs the clipboard readers: the real ones (os/exec) unless a test
// gives its own.
type clipTools struct {
	look func(name string) (string, error)
	out  func(name string, args ...string) ([]byte, error)
}

func realClipTools() clipTools {
	return clipTools{
		look: exec.LookPath,
		out:  func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).Output() },
	}
}

// clipboardImage is the clipboard's PNG image, through the first reader on
// PATH: wl-paste (Wayland), xclip (X11), pngpaste (macOS).
func clipboardImage(t clipTools) ([]byte, error) {
	if t.look == nil || t.out == nil {
		t = realClipTools()
	}
	has := func(name string) bool { _, err := t.look(name); return err == nil }
	var data []byte
	var err error
	switch {
	case has("wl-paste"):
		types, terr := t.out("wl-paste", "--list-types")
		if terr != nil || !strings.Contains(string(types), "image/png") {
			return nil, errNoClipImage
		}
		data, err = t.out("wl-paste", "--no-newline", "--type", "image/png")
	case has("xclip"):
		types, terr := t.out("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		if terr != nil || !strings.Contains(string(types), "image/png") {
			return nil, errNoClipImage
		}
		data, err = t.out("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	case has("pngpaste"):
		data, err = t.out("pngpaste", "-")
	default:
		return nil, errNoClipTool
	}
	if err != nil || !bytes.HasPrefix(data, pngMagic) {
		return nil, errNoClipImage
	}
	if len(data) > clipImageMax {
		return nil, errors.New("the clipboard's image is larger than 20 MiB")
	}
	return data, nil
}

// saveAttachment writes data as a new <time>-<random>.png in dir (0700),
// readable by its owner only (0600), and returns its path.
func saveAttachment(dir string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	var r [4]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(r[:])+".png")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// attachmentsDir is ${XDG_CACHE_HOME:-~/.cache}/agentbus/attachments, "" when
// the cache cannot be found.
func attachmentsDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "agentbus", "attachments")
}

// pastedImageMsg is a paste's outcome: the saved file's path, or why none.
type pastedImageMsg struct {
	path string
	err  error
}

// pasteImageCmd reads and saves the clipboard's image off the update loop.
func pasteImageCmd(tools clipTools, dir string) tea.Cmd {
	return func() tea.Msg {
		if dir == "" {
			return pastedImageMsg{err: errors.New("no cache directory for the image")}
		}
		data, err := clipboardImage(tools)
		if err != nil {
			return pastedImageMsg{err: err}
		}
		path, err := saveAttachment(dir, data)
		return pastedImageMsg{path: path, err: err}
	}
}

// gotPastedImage puts a pasted image's path at the compose window's cursor
// (a space before it unless the text ends with one), or says why there is
// none; a window closed since keeps nothing but the message.
func (m *Model) gotPastedImage(msg pastedImageMsg) {
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = msg.err.Error(), true
		return
	}
	c := m.ui.compose
	if c == nil {
		m.ui.flash, m.ui.flashErr = "image saved: "+msg.path, false
		return
	}
	text := msg.path
	if v := c.area.value(); v != "" && !strings.HasSuffix(v, " ") && !strings.HasSuffix(v, "\n") {
		text = " " + text
	}
	if c.area.insert(text) > 0 {
		m.ui.flash, m.ui.flashErr = "image saved, but the text is full: "+msg.path, true
		return
	}
	m.ui.flash, m.ui.flashErr = "image saved: "+msg.path, false
}
