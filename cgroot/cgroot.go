// Package cgroot answers, for any agent-bus process, where an agent's
// cgroup is and whether children may be made in it (resource-limits §12.4,
// spec docs/superpowers/specs/2026-11-25-agent-resource-limits-design.md).
// Step 4a: the interface, the detection of the scope and holder layouts and
// of soft mode, the kill method, and the doctor line; nothing here moves a
// process or starts a unit yet (steps 4b to 4d). No dependency.
//
// Only this package's user-mode view (Host) knows the systemd user manager
// (user@<uid>.service); a system mode adds an implementation, not a caller
// change (§12.1 item 7).
package cgroot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Mode is how an agent's memory is capped.
type Mode string

const (
	ModeHolder Mode = "holder" // holder/agents/<project>.<role>.<term> in a delegated holder (step 4d)
	ModeScope  Mode = "scope"  // agentbus-<p>-<key>-<role>-<term>.scope (step 4b)
	ModeSoft   Mode = "soft"   // no usable cgroup: callers take their soft path (RLIMIT_AS, §6)
)

// KillMethod is how a whole cgroup is ended.
type KillMethod string

const (
	KillCgroup KillMethod = "cgroup.kill"
	KillFreeze KillMethod = "freeze-and-kill" // cgroup.freeze, then each pid
	KillNone   KillMethod = "none"
)

// Root is an agent's cgroup (or the host's offer, Host).
type Root interface {
	Mode() Mode
	Delegated() bool // the memory controller is usable in children
	Kill() KillMethod
	// Child makes the child cgroup name with its memory.max, memory.high and
	// memory.swap.max ("" leaves one unset) and returns an fd on its
	// directory (for CLONE_INTO_CGROUP) and its path; a soft root fails.
	Child(name, mem, high, swap string) (fd int, path string, err error)
	// Usage is memory.current, memory.max (0: unlimited) and oom_kill.
	Usage() (current, max, oomKills uint64, err error)
	Path() string   // the cgroup's directory, "" when soft
	Reason() string // why soft, "" otherwise
}

// FS is where the cgroup tree is read: the cgroup v2 mount, this process's
// /proc/self/cgroup and the OS, seams for tests.
type FS struct {
	Mount    string
	GOOS     string
	ProcSelf func() ([]byte, error)
}

// Default reads the live tree.
func Default() FS {
	return FS{Mount: "/sys/fs/cgroup", GOOS: goos, ProcSelf: func() ([]byte, error) { return os.ReadFile("/proc/self/cgroup") }}
}

// Self is this process's agent cgroup (Default().Self()).
func Self() (Root, error) { return Default().Self() }

// Self is the nearest agent root above this process's cgroup: a
// holder/agents/<name> directory or an agentbus-*.scope, the process itself
// in its client leaf or a cmd-/ci- sibling. Anything else is soft, with why.
func (fs FS) Self() (Root, error) {
	rel, why := fs.own()
	if why != "" {
		return soft{why}, nil
	}
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		var mode Mode
		switch {
		case strings.HasPrefix(segs[i], "agentbus-") && strings.HasSuffix(segs[i], ".scope"):
			mode = ModeScope
		case i >= 2 && segs[i-2] == "holder" && segs[i-1] == "agents":
			mode = ModeHolder
		default:
			continue
		}
		return fs.at(mode, filepath.Join(append([]string{fs.Mount}, segs[:i+1]...)...), "memory not delegated"), nil
	}
	return soft{"not in an agent's cgroup"}, nil
}

// Host is what the systemd user manager above this process offers a
// holder: scope mode when its cgroup delegates memory, else soft.
func (fs FS) Host() Root {
	rel, why := fs.own()
	if why != "" {
		return soft{why}
	}
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "user@") && strings.HasSuffix(s, ".service") {
			return fs.at(ModeScope, filepath.Join(append([]string{fs.Mount}, segs[:i+1]...)...), "memory not delegated to the user manager")
		}
	}
	return soft{"no systemd user manager"}
}

// own is this process's cgroup v2 path, or why it has none.
func (fs FS) own() (string, string) {
	if fs.GOOS != "linux" {
		return "", "no cgroups on " + fs.GOOS
	}
	b, err := fs.ProcSelf()
	if err != nil {
		return "", "cannot read /proc/self/cgroup: " + err.Error()
	}
	for _, l := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(l, "0::"); ok {
			return p, ""
		}
	}
	return "", "no cgroup v2 in /proc/self/cgroup"
}

// at is the root at dir in mode when it delegates memory, else soft.
func (fs FS) at(mode Mode, dir, notDelegated string) Root {
	b, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers"))
	if err != nil || !hasWord(string(b), "memory") || !writable(filepath.Join(dir, "cgroup.subtree_control")) {
		return soft{notDelegated + " (" + dir + ")"}
	}
	kill := KillNone
	switch {
	case exists(filepath.Join(dir, "cgroup.kill")):
		kill = KillCgroup
	case exists(filepath.Join(dir, "cgroup.freeze")):
		kill = KillFreeze
	}
	return cgroup{mode: mode, dir: dir, kill: kill}
}

type cgroup struct {
	mode Mode
	dir  string
	kill KillMethod
}

func (c cgroup) Mode() Mode       { return c.mode }
func (c cgroup) Delegated() bool  { return true }
func (c cgroup) Kill() KillMethod { return c.kill }
func (c cgroup) Path() string     { return c.dir }
func (c cgroup) Reason() string   { return "" }

func (c cgroup) Child(name, mem, high, swap string) (int, string, error) {
	if name == "" || name == "client" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
		return -1, "", fmt.Errorf("cgroot: invalid child name %q", name)
	}
	p := filepath.Join(c.dir, name)
	if err := os.Mkdir(p, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return -1, "", fmt.Errorf("cgroot: %w", err)
	}
	for f, v := range map[string]string{"memory.max": mem, "memory.high": high, "memory.swap.max": swap} {
		if v == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(p, f), []byte(v), 0o644); err != nil {
			return -1, "", fmt.Errorf("cgroot: %s: %w", f, err)
		}
	}
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", fmt.Errorf("cgroot: open %s: %w", p, err)
	}
	return fd, p, nil
}

func (c cgroup) Usage() (uint64, uint64, uint64, error) {
	cur, err := readUint(filepath.Join(c.dir, "memory.current"))
	if err != nil {
		return 0, 0, 0, err
	}
	var max uint64
	if b, err := os.ReadFile(filepath.Join(c.dir, "memory.max")); err != nil {
		return 0, 0, 0, err
	} else if s := strings.TrimSpace(string(b)); s != "max" {
		if max, err = strconv.ParseUint(s, 10, 64); err != nil {
			return 0, 0, 0, fmt.Errorf("cgroot: memory.max %q", s)
		}
	}
	ev, err := os.ReadFile(filepath.Join(c.dir, "memory.events"))
	if err != nil {
		return 0, 0, 0, err
	}
	var ooms uint64
	for _, l := range strings.Split(string(ev), "\n") {
		if v, ok := strings.CutPrefix(l, "oom_kill "); ok {
			ooms, _ = strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		}
	}
	return cur, max, ooms, nil
}

// soft is a root with no usable cgroup.
type soft struct{ why string }

func (s soft) Mode() Mode       { return ModeSoft }
func (s soft) Delegated() bool  { return false }
func (s soft) Kill() KillMethod { return KillNone }
func (s soft) Path() string     { return "" }
func (s soft) Reason() string   { return s.why }
func (s soft) Child(string, string, string, string) (int, string, error) {
	return -1, "", errors.New("cgroot: soft mode: " + s.why)
}
func (s soft) Usage() (uint64, uint64, uint64, error) {
	return 0, 0, 0, errors.New("cgroot: soft mode: " + s.why)
}

// Linger is loginctl's Linger for this user ("yes", "no"), "" when it
// cannot say (no loginctl, macOS, an error): user-mode knowledge, kept here.
func Linger(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "loginctl", "show-user", strconv.Itoa(os.Getuid()), "-p", "Linger", "--value").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// DoctorLine is roles doctor's host line (§12.7); linger is loginctl's
// Linger value ("yes", "no", "" unknown).
func DoctorLine(r Root, linger string) string {
	l := "cgroup mode: "
	if r.Mode() == ModeSoft {
		l += "soft: " + r.Reason()
	} else {
		l += string(r.Mode()) + ", memory delegated, kill: " + string(r.Kill())
	}
	switch linger {
	case "yes":
		return l + ", linger on"
	case "no":
		return l + ", linger off: holder stops at logout"
	}
	return l + ", linger unknown"
}

func hasWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}

func writable(p string) bool {
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func readUint(p string) (uint64, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

func closeFD(fd int) { _ = syscall.Close(fd) }
