package holder

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// Supervised is the holder's service manager where there is none
// (services.manager = none; herdr exit spec 4.4b): each (project, build) is
// a detached supervisor, `agentbus service supervise holder <project> <key>`
// run from that build's binary copy in its own session, which applies the
// units' restart policy (service.Supervisor) and kills the holder's recorded
// process groups after each exit. It implements service.HolderManager, so
// `service gc` (service.GCHolders) works on it unchanged.
//
// What a unit file is to systemd, a record is here:
// <rundir>/agentbus/<project>/holder-<key>.pid, 0600, written by
// InstallHolder (project, build key, raw id, env file, working directory,
// binary) and completed by StartHolder with the supervisor's pid and start
// time. A supervisor is alive only while that pid holds that start time (a
// reused pid is another process). The record lives in a checked run directory
// (ProjectDir) and is never read through a symlink.
type Supervised struct {
	Paths  service.Paths
	Getenv func(string) string // the run directory's environment; nil: os.Getenv
	// Spawn starts argv detached (its own session) in dir, its output
	// appended to log, and returns its pid; nil: spawnDetached.
	Spawn func(argv []string, dir, log string) (int, error)
	// ProcStart is pid's start time; errNoProcess when it is gone (a zombie
	// included). nil: procStartOf (Linux /proc; elsewhere an error, which
	// 6c3 replaces with the platform's own reader).
	ProcStart func(pid int) (uint64, error)
	// StopWait bounds StopHolder's wait for the supervisor to end; 0: 15 s.
	StopWait time.Duration
}

var _ service.HolderManager = Supervised{}

// errNoProcess: the pid is listed no more (or only as a zombie).
var errNoProcess = errors.New("no such process")

// supervisedRecord is one build's record.
type supervisedRecord struct {
	Project, Key, Raw, Env, WorkDir, Bin string
	PID                                  int
	Start                                uint64
}

func (m Supervised) getenv() func(string) string {
	if m.Getenv != nil {
		return m.Getenv
	}
	return os.Getenv
}

func (m Supervised) procStart(pid int) (uint64, error) {
	if m.ProcStart != nil {
		return m.ProcStart(pid)
	}
	return procStartOf(pid)
}

// recordPath is the record of project's build key, its directories checked.
func (m Supervised) recordPath(project, key string) (string, error) {
	if !service.ValidBuildKey(key) {
		return "", fmt.Errorf("holder: invalid build key %q", key)
	}
	dir, err := ProjectDir(m.getenv(), project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "holder-"+key+".pid"), nil
}

// alive: the record's pid still holds its start time. An error means the
// process could not be judged, never "dead".
func (m Supervised) alive(r supervisedRecord) (bool, error) {
	if r.PID <= 1 || r.Start == 0 {
		return false, nil
	}
	start, err := m.procStart(r.PID)
	switch {
	case errors.Is(err, errNoProcess):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("holder: the supervisor %d of project %s, build %s: %w", r.PID, r.Project, r.Key, err)
	}
	return start == r.Start, nil
}

// lock holds an exclusive lock on the build's record while a start, an
// install or a removal reads and rewrites it: two `up` at once start one
// supervisor. The lock file is never removed, not even with the record: its
// inode must stay the one every waiter queues on (an empty 0600 file per
// build in the run directory, gone with it).
func (m Supervised) lock(project, key string) (func(), error) {
	path, err := m.recordPath(project, key)
	if err != nil {
		return nil, err
	}
	return lockRecord(path)
}

// lockRecord is lock's flock on the lock file beside the record at path.
func lockRecord(path string) (func(), error) {
	f, err := os.OpenFile(strings.TrimSuffix(path, ".pid")+".lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("holder: the record's lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("holder: the record's lock: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// InstallHolder writes the build's record (its supervisor, if one runs, is
// kept); it refuses a key already recorded with another raw id (security S7).
func (m Supervised) InstallHolder(s service.HolderSpec) (bool, error) {
	if err := service.CheckHolderSpec(s); err != nil {
		return false, err
	}
	key := service.BuildKey(s.Raw)
	unlock, err := m.lock(s.Project, key)
	if err != nil {
		return false, err
	}
	defer unlock()
	path, err := m.recordPath(s.Project, key)
	if err != nil {
		return false, err
	}
	old, err := readSupervisedRecord(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return false, err
	case old.Raw != s.Raw:
		return false, fmt.Errorf("build key %s of project %s is recorded with raw id %q, not %q: refused", key, s.Project, old.Raw, s.Raw)
	}
	r := supervisedRecord{Project: s.Project, Key: key, Raw: s.Raw, Env: s.EnvFile, WorkDir: s.WorkDir,
		Bin: service.HolderBin(s.Paths, key), PID: old.PID, Start: old.Start}
	if err == nil && r == old {
		return false, nil
	}
	return true, writeSupervisedRecord(path, r)
}

// StartHolder starts the build's supervisor unless it is alive; it never
// stops or restarts another build's.
func (m Supervised) StartHolder(project, key string) error {
	unlock, err := m.lock(project, key)
	if err != nil {
		return err
	}
	defer unlock()
	path, err := m.recordPath(project, key)
	if err != nil {
		return err
	}
	r, err := readSupervisedRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the holder of project %s, build %s is not installed", project, key)
	}
	if err != nil {
		return err
	}
	if ok, err := m.alive(r); err != nil || ok {
		return err
	}
	log := m.Paths.SuperviseLog(project, service.Holder)
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return err
	}
	spawn := m.Spawn
	if spawn == nil {
		spawn = spawnDetached
	}
	pid, err := spawn([]string{r.Bin, "--project", project, "service", "supervise", service.Holder, project, key}, r.WorkDir, log)
	if err != nil {
		return fmt.Errorf("holder: the supervisor of project %s, build %s: %w", project, key, err)
	}
	start, err := m.procStart(pid)
	if err != nil {
		return fmt.Errorf("holder: the supervisor of project %s, build %s (pid %d) did not stay up: %w; see %s", project, key, pid, err, log)
	}
	r.PID, r.Start = pid, start
	return writeSupervisedRecord(path, r)
}

// StopHolder sends the build's supervisor SIGTERM — it stops its holder,
// whose agents end with it — and waits for it. Nothing running: nothing to do.
func (m Supervised) StopHolder(project, key string) error {
	r, ok, err := m.running(project, key)
	if err != nil || !ok {
		return err
	}
	if err := signalPinned(r.PID, r.Start, syscall.SIGTERM, m.procStart); err != nil {
		return fmt.Errorf("holder: stopping the supervisor %d of project %s, build %s: %w", r.PID, project, key, err)
	}
	wait := m.StopWait
	if wait == 0 {
		wait = 15 * time.Second
	}
	for deadline := time.Now().Add(wait); ; time.Sleep(50 * time.Millisecond) {
		ok, err := m.alive(r)
		if err != nil || !ok {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("holder: the supervisor %d of project %s, build %s still runs %v after SIGTERM", r.PID, project, key, wait)
		}
	}
}

// RestartHolder stops the build's supervisor (and its holder, killing every
// agent it holds) and starts a new one: `service restart holder
// --kill-agents` only.
func (m Supervised) RestartHolder(project, key string) error {
	if err := m.StopHolder(project, key); err != nil {
		return err
	}
	return m.StartHolder(project, key)
}

// HolderCommand is what the build's supervisor runs: the build's binary copy
// as the holder, with its env file, recording its terminals' process groups
// (--record-groups) for the kill after an exit — in the recorded working
// directory. The record must be installed.
func (m Supervised) HolderCommand(project, key string) (argv []string, dir, envFile string, err error) {
	path, err := m.recordPath(project, key)
	if err != nil {
		return nil, "", "", err
	}
	r, err := readSupervisedRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", "", fmt.Errorf("the holder of project %s, build %s is not installed", project, key)
	}
	if err != nil {
		return nil, "", "", err
	}
	return []string{r.Bin, "--project", project, service.Holder, "--env", r.Env, "--record-groups"}, r.WorkDir, r.Env, nil
}

// HolderActive: the build's supervisor is alive. No record: inactive.
func (m Supervised) HolderActive(project, key string) (bool, error) {
	_, ok, err := m.running(project, key)
	return ok, err
}

// running reads the build's record and judges its supervisor.
func (m Supervised) running(project, key string) (supervisedRecord, bool, error) {
	path, err := m.recordPath(project, key)
	if err != nil {
		return supervisedRecord{}, false, err
	}
	r, err := readSupervisedRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	ok, err := m.alive(r)
	return r, ok, err
}

// RemoveHolder deletes the record of a build whose supervisor is gone; it
// never stops one. A missing record is not an error.
func (m Supervised) RemoveHolder(project, key string) error {
	unlock, err := m.lock(project, key)
	if err != nil {
		return err
	}
	defer unlock()
	r, ok, err := m.running(project, key)
	switch {
	case err != nil:
		return err
	case ok:
		return fmt.Errorf("the holder of project %s, build %s runs (supervisor %d): stop it first", project, key, r.PID)
	}
	path, _ := m.recordPath(project, key)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// The lock file stays (reviewer-codex): a start queued on its inode
	// must share it with every later install and start. Unlinked, a new
	// file at the same path would let two of them each find no supervisor.
	return nil
}

// HolderUnits lists the records whose recorded project is project ("" every
// project's), matched on the recorded values, never on a name prefix. A
// record that cannot be read, or whose values disagree with its place, is
// left out.
func (m Supervised) HolderUnits(project string) ([]service.HolderUnit, error) {
	return m.holderUnits(project, false)
}

// HolderUnitsStrict is HolderUnits that fails on a record it cannot read, or
// whose values disagree with its place (service.StrictHolderLister).
func (m Supervised) HolderUnitsStrict(project string) ([]service.HolderUnit, error) {
	return m.holderUnits(project, true)
}

func (m Supervised) holderUnits(project string, strict bool) ([]service.HolderUnit, error) {
	var projects []string
	if project != "" {
		projects = []string{project}
	} else {
		root, err := RunAgentbusDir(m.getenv())
		if err != nil {
			return nil, err
		}
		ents, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			if e.IsDir() && bus.ValidName(e.Name()) {
				projects = append(projects, e.Name())
			}
		}
	}
	var out []service.HolderUnit
	for _, p := range projects {
		dir, err := ProjectDir(m.getenv(), p)
		if err != nil {
			return nil, err
		}
		names, err := filepath.Glob(filepath.Join(dir, "holder-*.pid"))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			r, err := readSupervisedRecord(name)
			if err != nil || r.Project != p || filepath.Base(name) != "holder-"+r.Key+".pid" || !service.ValidBuildKey(r.Key) {
				if strict && err != nil {
					return nil, fmt.Errorf("holder record %s cannot be read: %w", name, err)
				}
				if strict {
					return nil, fmt.Errorf("holder record %s disagrees with its place (project %s, build %s)", name, r.Project, r.Key)
				}
				continue
			}
			out = append(out, service.HolderUnit{Project: r.Project, Key: r.Key, Raw: r.Raw})
		}
	}
	slices.SortFunc(out, func(a, b service.HolderUnit) int {
		return strings.Compare(a.Project+"\x00"+a.Key, b.Project+"\x00"+b.Key)
	})
	return out, nil
}

// spawnDetached starts argv in its own session (setsid: no terminal, no
// group of the caller's), stdin /dev/null, stdout and stderr appended to log
// (0600), with a unit's environment (unitEnv). It is reaped in the
// background while the caller lives; once the caller exits, init adopts it.
func spawnDetached(argv []string, dir, log string) (int, error) {
	out, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer null.Close()
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir, c.Stdin, c.Stdout, c.Stderr = dir, null, out, out
	c.Env = unitEnv(os.Environ())
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return 0, err
	}
	go func() { _ = c.Wait() }()
	return c.Process.Pid, nil
}

// unitEnvKeys are the variables a detached supervisor keeps from the shell
// that ran up: what a user manager gives a unit (the user, the locale, the
// XDG directories every path is read from). The rest — an agent's name, a
// pane, a test's broker — is that shell's, never a service's; the service
// reads its own from its env file, as under systemd.
var unitEnvKeys = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"}

// unitEnv is environ cut to unitEnvKeys.
func unitEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		if k, _, ok := strings.Cut(kv, "="); ok && slices.Contains(unitEnvKeys, k) {
			out = append(out, kv)
		}
	}
	return out
}

// Each record field, in the file's order.
var supervisedFields = []string{"project", "build", "build_raw", "env", "workdir", "bin", "pid", "start"}

// writeSupervisedRecord replaces path atomically with r (0600). A value
// holding a line break is refused: it would forge a field.
func writeSupervisedRecord(path string, r supervisedRecord) error {
	vals := []string{r.Project, r.Key, r.Raw, r.Env, r.WorkDir, r.Bin, strconv.Itoa(r.PID), strconv.FormatUint(r.Start, 10)}
	var b strings.Builder
	for i, v := range vals {
		if strings.ContainsAny(v, "\n\r") {
			return fmt.Errorf("holder: the record's %s holds a line break: refused", supervisedFields[i])
		}
		b.WriteString(supervisedFields[i] + " " + v + "\n")
	}
	return writePrivate(path, b.String())
}

// readSupervisedRecord reads path as a private regular file of ours, never
// through a symlink; os.ErrNotExist when there is none.
func readSupervisedRecord(path string) (supervisedRecord, error) {
	f, err := openPrivate(path, "the supervisor record")
	if err != nil {
		return supervisedRecord{}, err
	}
	defer f.Close()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), " "); ok {
			vals[k] = v
		}
	}
	if err := sc.Err(); err != nil {
		return supervisedRecord{}, err
	}
	for _, k := range supervisedFields[:6] {
		if vals[k] == "" {
			return supervisedRecord{}, fmt.Errorf("holder: the supervisor record %s has no %s", path, k)
		}
	}
	pid, _ := strconv.Atoi(vals["pid"])
	start, _ := strconv.ParseUint(vals["start"], 10, 64)
	return supervisedRecord{Project: vals["project"], Key: vals["build"], Raw: vals["build_raw"], Env: vals["env"],
		WorkDir: vals["workdir"], Bin: vals["bin"], PID: pid, Start: start}, nil
}

// openPrivate opens path for reading, refusing a symlink (O_NOFOLLOW) and
// anything but a regular file of ours that no one else may read or write;
// os.ErrNotExist when there is none. what names the file in the error.
func openPrivate(path, what string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("holder: %s %s: %w", what, path, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || !ok || int(sys.Uid) != os.Getuid() || st.Mode().Perm()&0o077 != 0 {
		f.Close()
		return nil, fmt.Errorf("holder: %s %s is not a private file of yours: left alone", what, path)
	}
	return f, nil
}
