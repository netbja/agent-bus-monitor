// Package holder is the agents' terminal holder (herdr exit spec
// docs/superpowers/specs/2026-11-10-herdr-exit-design.md, 4.4 and 4.2): one
// process per project owning the PTYs, reached over a unix socket. This
// file and its neighbours are its foundations (step 5a): the run directory,
// the wire and its closed frames, and the peer checks; nothing uses the
// holder yet.
package holder

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// maxSocketPathFor is the longest unix socket path goos takes: its
// sun_path less the NUL (Linux 108, macOS 104). Another OS gets the
// smallest of the two. The field failure (P0-2): macOS's limit applied on
// Linux refused a 105-byte path, so the holder never listened.
func maxSocketPathFor(goos string) int {
	if goos == "linux" {
		return 107
	}
	return 103
}

type runRoot struct {
	path     string
	fallback bool // $TMPDIR/agentbus-<uid> or /tmp/agentbus-<uid>: a level of ours, checked too
}

// runDir is <rundir> (spec 4.4): $XDG_RUNTIME_DIR when set, else a per-uid
// directory under $TMPDIR, else under /tmp.
func runDir(getenv func(string) string) runRoot {
	if x := getenv("XDG_RUNTIME_DIR"); x != "" {
		return runRoot{path: x}
	}
	tmp := getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	return runRoot{path: filepath.Join(tmp, "agentbus-"+strconv.Itoa(os.Getuid())), fallback: true}
}

// ProjectDir is <rundir>/agentbus/<project> under the run-directory rules
// (security S6), the one function the holder, the supervisor and every
// client use: each level (the fallback root, agentbus, the project) is
// created 0700 when missing, then read with Lstat, never Stat: a symlink is
// refused, and the owner must be this uid and the mode exactly 0700, under
// $XDG_RUNTIME_DIR as in the fallback (macOS ignores a socket's own mode:
// the directories are the control).
func ProjectDir(getenv func(string) string, project string) (string, error) {
	if !bus.ValidName(project) {
		return "", fmt.Errorf("holder: invalid project %q", project)
	}
	dir, err := RunAgentbusDir(getenv)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, project)
	if err := ensureLevel(p); err != nil {
		return "", err
	}
	return p, nil
}

// RunAgentbusDir is <rundir>/agentbus, each level checked as ProjectDir
// checks it: the parent of every project's directory (a gc lists the
// projects there without creating one).
func RunAgentbusDir(getenv func(string) string) (string, error) {
	root := runDir(getenv)
	var levels []string
	if root.fallback {
		levels = append(levels, root.path)
	}
	levels = append(levels, filepath.Join(root.path, "agentbus"))
	for _, l := range levels {
		if err := ensureLevel(l); err != nil {
			return "", err
		}
	}
	return levels[len(levels)-1], nil
}

func ensureLevel(path string) error {
	// Mkdir never follows a symlink (an existing one is EEXIST); what is
	// there then, made by us, before us or by a racer, is read once.
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("holder run directory: %w", err)
	}
	st, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("holder run directory: %w", err)
	}
	return checkLevel(path, st, os.Getuid())
}

// checkLevel is one level's check: a real directory (no symlink), owned by
// uid, mode exactly 0700.
func checkLevel(path string, st fs.FileInfo, uid int) error {
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("holder run directory %s is a symlink: refused", path)
	case !st.IsDir():
		return fmt.Errorf("holder run directory %s is not a directory", path)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("holder run directory %s: its owner cannot be read", path)
	}
	if int(sys.Uid) != uid {
		return fmt.Errorf("holder run directory %s is owned by uid %d, not you: another user created it; remove it or set TMPDIR", path, sys.Uid)
	}
	if m := st.Mode().Perm(); m != 0o700 {
		return fmt.Errorf("holder run directory %s has mode %04o, not 0700", path, m)
	}
	return nil
}

// SocketPath is the holder socket of project's build key:
// <rundir>/agentbus/<project>/holder-<key>.sock, its directories checked
// (ProjectDir). A path longer than this OS's unix socket takes is refused.
func SocketPath(getenv func(string) string, project, key string) (string, error) {
	if !service.ValidBuildKey(key) {
		return "", fmt.Errorf("holder: invalid build key %q", key)
	}
	dir, err := ProjectDir(getenv, project)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, socketName(key))
	if err := socketFits(p); err != nil {
		return "", err
	}
	return p, nil
}

// SocketPathFits says whether project's socket for key fits this OS's unix
// socket path, creating nothing: agentbus up asks it before it starts
// anything, so a holder that could never listen is refused up front.
func SocketPathFits(getenv func(string) string, project, key string) error {
	if !bus.ValidName(project) {
		return fmt.Errorf("holder: invalid project %q", project)
	}
	if !service.ValidBuildKey(key) {
		return fmt.Errorf("holder: invalid build key %q", key)
	}
	return socketFits(filepath.Join(runDir(getenv).path, "agentbus", project, socketName(key)))
}

func socketFits(p string) error {
	if limit := maxSocketPathFor(runtime.GOOS); len(p) > limit {
		return fmt.Errorf("holder socket path %s is %d bytes; a unix socket path takes %d here (%s): set a shorter XDG_RUNTIME_DIR or TMPDIR",
			p, len(p), limit+1, runtime.GOOS)
	}
	return nil
}

// The socket's name is bounded whatever the build key (P0-2 B): h-<id>.sock,
// id the first 16 hex digits of the key's sha256, with h-<id>.key beside it,
// written by its holder, naming the key in full. Before this, the name was
// holder-<key>.sock: a holder of an older build still listens there after an
// upgrade, so every reader takes both (ProjectSockets, SocketFor).

func socketID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func socketName(key string) string { return "h-" + socketID(key) + ".sock" }

func legacySocketName(key string) string { return "holder-" + key + ".sock" }

// KeyFilePath is the key file beside key's socket, its directories checked.
func KeyFilePath(getenv func(string) string, project, key string) (string, error) {
	sock, err := SocketPath(getenv, project, key)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(sock, ".sock") + ".key", nil
}

// SocketFor is key's socket to dial: the new name when a socket is there,
// else the legacy name when one is, else the new name (where a holder of
// this build listens).
func SocketFor(getenv func(string) string, project, key string) (string, error) {
	p, err := SocketPath(getenv, project, key)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(p); err == nil {
		return p, nil
	}
	legacy := filepath.Join(filepath.Dir(p), legacySocketName(key))
	if _, err := os.Lstat(legacy); err == nil {
		return legacy, nil
	}
	return p, nil
}

// ProjectSockets is every holder socket in a project's run directory, by
// build key: holder-<key>.sock, and h-<id>.sock whose h-<id>.key (a regular
// file) names a valid key of that id. A missing directory has none.
func ProjectSockets(dir string) (map[string]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range ents {
		name := e.Name()
		if key, ok := strings.CutPrefix(name, "holder-"); ok {
			if key, ok = strings.CutSuffix(key, ".sock"); ok && service.ValidBuildKey(key) {
				if _, taken := out[key]; !taken { // the new name wins
					out[key] = filepath.Join(dir, name)
				}
			}
			continue
		}
		id, ok := strings.CutPrefix(name, "h-")
		if id, ok = strings.CutSuffix(id, ".sock"); !ok {
			continue
		}
		key, ok := readKeyFile(filepath.Join(dir, "h-"+id+".key"))
		if !ok || socketID(key) != id {
			continue
		}
		out[key] = filepath.Join(dir, name)
	}
	return out, nil
}

// readKeyFile reads a key file: a regular file (Lstat: no symlink), small,
// holding one valid build key.
func readKeyFile(path string) (string, bool) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 256 {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	key := strings.TrimSpace(string(b))
	return key, service.ValidBuildKey(key)
}

// WriteKeyFile writes key's key file, 0600, replacing one there (a temp file
// renamed into place).
func WriteKeyFile(path, key string) error {
	if !service.ValidBuildKey(key) {
		return fmt.Errorf("holder: invalid build key %q", key)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".key.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(key + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
