package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Pasting an image into the compose window (usage-cards, the person's
// choice): Ctrl+V reads the clipboard's image with the machine's own tool,
// saves it 0600 under the cache's attachments (0700), and inserts its path in
// the text. Nothing is uploaded: an agent on this machine reads the file.

var (
	errNoClipTool  = errors.New("no clipboard reader for this session: wl-clipboard (Wayland), xclip (X11) or pngpaste (macOS)")
	errNoClipImage = errors.New("no image in the clipboard")
)

// clipImageMax bounds what a paste reads.
const clipImageMax = 20 << 20

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'} // the PNG signature

// clipTools runs the clipboard readers: the real ones (os/exec) unless a test
// gives its own.
type clipTools struct {
	env  func(string) string // the session: WAYLAND_DISPLAY, DISPLAY
	goos string
	look func(name string) (string, error)
	out  func(name string, args ...string) ([]byte, error)
}

// clipTimeout bounds one clipboard tool: one that hangs (a clipboard owner
// that never answers) is killed (foureyes).
const clipTimeout = 5 * time.Second

func realClipTools() clipTools {
	return clipTools{
		env:  os.Getenv,
		goos: runtime.GOOS,
		look: exec.LookPath,
		out: func(name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), clipTimeout)
			defer cancel()
			return boundedOutput(exec.CommandContext(ctx, name, args...), clipImageMax)
		},
	}
}

// boundedOutput is cmd's stdout read through max+1 bytes: past max, the
// command is killed and refused, so a reader never buffers more than it may
// keep (reviewer-codex).
func boundedOutput(cmd *exec.Cmd, max int) ([]byte, error) {
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(io.LimitReader(out, int64(max)+1))
	if len(data) > max {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("the clipboard's image is larger than 20 MiB")
	}
	if werr := cmd.Wait(); werr != nil {
		return nil, werr
	}
	return data, rerr
}

// clipboardImage is the clipboard's PNG image, through the session's own
// reader (foureyes: by the session, never by what is installed, since an X11
// session with wl-clipboard installed has no Wayland clipboard): wl-paste
// under Wayland, xclip under X11, pngpaste on macOS.
func clipboardImage(t clipTools) ([]byte, error) {
	if t.look == nil || t.out == nil || t.env == nil {
		t = realClipTools()
	}
	has := func(name string) bool { _, err := t.look(name); return err == nil }
	var data []byte
	var err error
	switch {
	case t.env("WAYLAND_DISPLAY") != "" && has("wl-paste"):
		types, terr := t.out("wl-paste", "--list-types")
		if terr != nil || !strings.Contains(string(types), "image/png") {
			return nil, errNoClipImage
		}
		data, err = t.out("wl-paste", "--no-newline", "--type", "image/png")
	case t.env("DISPLAY") != "" && has("xclip"):
		types, terr := t.out("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		if terr != nil || !strings.Contains(string(types), "image/png") {
			return nil, errNoClipImage
		}
		data, err = t.out("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	case t.goos == "darwin" && has("pngpaste"):
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

// pastedImageMsg is a paste's outcome: the saved file's path (the image's
// hash, dup when that image was saved before), or the clipboard's text when
// it holds no image, or why none.
type pastedImageMsg struct {
	path, hash string
	dup        bool
	text       string
	err        error
}

// clipTextMax bounds a pasted text: the compose window's limit cuts it
// further.
const clipTextMax = 1 << 20

// clipboardText is the clipboard's text through the session's own reader
// (the person: speech-to-text tools such as Murmure or Handy paste by
// sending Ctrl+V with text in the clipboard).
func clipboardText(t clipTools) (string, error) {
	if t.look == nil || t.out == nil || t.env == nil {
		t = realClipTools()
	}
	has := func(name string) bool { _, err := t.look(name); return err == nil }
	var data []byte
	var err error
	switch {
	case t.env("WAYLAND_DISPLAY") != "" && has("wl-paste"):
		types, terr := t.out("wl-paste", "--list-types")
		if terr != nil || !strings.Contains(string(types), "text/plain") && !strings.Contains(string(types), "UTF8_STRING") {
			return "", errNoClipImage
		}
		data, err = t.out("wl-paste", "--no-newline", "--type", "text")
	case t.env("DISPLAY") != "" && has("xclip"):
		types, terr := t.out("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
		if terr != nil || !strings.Contains(string(types), "UTF8_STRING") && !strings.Contains(string(types), "text/plain") && !strings.Contains(string(types), "STRING") {
			return "", errNoClipImage
		}
		data, err = t.out("xclip", "-selection", "clipboard", "-o")
	case t.goos == "darwin" && has("pbpaste"):
		data, err = t.out("pbpaste")
	default:
		return "", errNoClipTool
	}
	if err != nil || len(bytes.TrimSpace(data)) == 0 || !utf8.Valid(data) {
		return "", errNoClipImage
	}
	if len(data) > clipTextMax {
		return "", errors.New("the clipboard's text is larger than 1 MiB")
	}
	return string(data), nil
}

// pasteImageCmd reads the clipboard off the update loop: its image, saved
// (an image saved before, still on disk, is never saved twice: known is
// hash -> path), else its text.
func pasteImageCmd(tools clipTools, dir string, known map[string]string) tea.Cmd {
	return func() tea.Msg {
		data, err := clipboardImage(tools)
		if err != nil {
			if text, terr := clipboardText(tools); terr == nil {
				return pastedImageMsg{text: text}
			}
			return pastedImageMsg{err: err}
		}
		if dir == "" {
			return pastedImageMsg{err: errors.New("no cache directory for the image")}
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if p, ok := known[hash]; ok {
			if _, err := os.Stat(p); err == nil {
				return pastedImageMsg{path: p, hash: hash, dup: true}
			}
		}
		pruneAttachments(dir, time.Now())
		path, err := saveAttachment(dir, data)
		return pastedImageMsg{path: path, hash: hash, err: err}
	}
}

// gotPastedImage puts a pasted image's path at the compose window's cursor,
// or says why there is none; a window closed since keeps nothing but the
// message.
func (m *Model) gotPastedImage(msg pastedImageMsg) {
	if msg.err != nil {
		m.ui.flash, m.ui.flashErr = msg.err.Error(), true
		return
	}
	c := m.ui.compose
	if msg.text != "" { // no image: the clipboard's text, as a terminal paste
		if c != nil {
			m.ui.flash, m.ui.flashErr = "", false
			m.pasteCompose(msg.text)
		}
		return
	}
	if msg.hash != "" {
		if m.ui.pastedImages == nil {
			m.ui.pastedImages = map[string]string{}
		}
		m.ui.pastedImages[msg.hash] = msg.path
	}
	if c == nil {
		m.ui.flash, m.ui.flashErr = "image saved: "+msg.path, false
		return
	}
	if msg.dup && strings.Contains(c.area.value(), msg.path) { // the person: never the same link twice
		m.ui.flash, m.ui.flashErr = "this image is already in the message: "+msg.path, true
		return
	}
	dropped := 0
	c.area.edit("", func() { dropped = insertPath(&c.area, msg.path) }) // one step, as a paste
	if dropped > 0 {
		m.ui.flash, m.ui.flashErr = "image saved, but the text is full: "+msg.path, true
		return
	}
	m.ui.flash, m.ui.flashErr = "image saved: "+msg.path, false
}

// insertPath puts path at a's cursor (a selection replaced), with a space
// before it unless it starts the text or follows a space or a line break, and
// one after it unless it ends the text or meets one (reviewer-codex: the
// cursor's neighbours, never the end of the draft). dropped as insert's.
func insertPath(a *textArea, path string) (dropped int) {
	a.cutSelection()
	gap := func(i int) bool { return i < 0 || i >= len(a.text) || a.text[i] == ' ' || a.text[i] == '\n' }
	if !gap(a.cur - 1) {
		path = " " + path
	}
	if !gap(a.cur) {
		path += " "
	}
	return a.insert(path)
}

// attachmentKeep is how long a pasted image is kept (foureyes: the
// attachments are pruned at each paste).
const attachmentKeep = 30 * 24 * time.Hour

// attachmentName is the name saveAttachment gives: only these files are ever
// pruned.
var attachmentName = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{8}\.png$`)

// pruneAttachments removes from dir the attachments older than
// attachmentKeep: only regular files with saveAttachment's name (never a link,
// never another file), best effort.
func pruneAttachments(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !attachmentName.MatchString(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > attachmentKeep {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
