// Package holder is the agents' terminal holder (herdr exit spec
// docs/superpowers/specs/2026-11-10-herdr-exit-design.md, 4.4 and 4.2): one
// process per project owning the PTYs, reached over a unix socket. This
// file and its neighbours are its foundations (step 5a): the run directory,
// the wire and its closed frames, and the peer checks; nothing uses the
// holder yet.
package holder

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// maxSocketPath is the longest socket path every platform takes: macOS's
// sun_path holds 104 bytes, its NUL included.
const maxSocketPath = 103

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
	root := runDir(getenv)
	var levels []string
	if root.fallback {
		levels = append(levels, root.path)
	}
	levels = append(levels, filepath.Join(root.path, "agentbus"), filepath.Join(root.path, "agentbus", project))
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
// (ProjectDir). A path longer than a unix socket takes is refused.
func SocketPath(getenv func(string) string, project, key string) (string, error) {
	if !service.ValidBuildKey(key) {
		return "", fmt.Errorf("holder: invalid build key %q", key)
	}
	dir, err := ProjectDir(getenv, project)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "holder-"+key+".sock")
	if len(p) > maxSocketPath {
		return "", fmt.Errorf("holder socket path %s is %d bytes; a unix socket path must fit in 104 bytes (macOS): set a shorter XDG_RUNTIME_DIR or TMPDIR", p, len(p))
	}
	return p, nil
}
