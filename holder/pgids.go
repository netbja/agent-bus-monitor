package holder

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/netbja/agent-bus-monitor/service"
)

// Without a service manager there is no cgroup: when a holder dies, its
// supervisor kills the process groups the holder recorded for its terminals
// (spec 4.4b, the equivalent of KillMode=control-group). The record is
// <rundir>/agentbus/<project>/holder-<key>.pgids, one "<leader pid> <its
// start time>" line per live terminal, rewritten at every start and end.

// PgidsPath is the record of project's build key, its directories checked
// (ProjectDir).
func PgidsPath(getenv func(string) string, project, key string) (string, error) {
	if !service.ValidBuildKey(key) {
		return "", fmt.Errorf("holder: invalid build key %q", key)
	}
	dir, err := ProjectDir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "holder-"+key+".pgids"), nil
}

// pgMu orders the record's rewrites: the last one written is the latest.
var pgMu sync.Mutex

// recordGroups rewrites cfg.Pgids with the live terminals' leaders (none:
// no record). A failure is not fatal: the record only serves a crash.
func (s *Server) recordGroups() {
	if s.cfg.Pgids == "" {
		return
	}
	pgMu.Lock()
	defer pgMu.Unlock()
	s.mu.Lock()
	var lines []string
	for _, t := range s.terms {
		t.mu.Lock()
		if t.alive && t.start != 0 {
			lines = append(lines, strconv.Itoa(t.pid)+" "+strconv.FormatUint(t.start, 10))
		}
		t.mu.Unlock()
	}
	s.mu.Unlock()
	sort.Strings(lines)
	_ = writePrivate(s.cfg.Pgids, strings.Join(lines, "\n")+"\n")
}

// writePrivate replaces path atomically with a 0600 file.
func writePrivate(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pgids-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// KillRecordedGroups hands each recorded group (its leader's pid and start
// time) to killGroup — KillGroup in production, which decides process by
// process — and returns the processes killed. The record is read from a
// checked run directory, never through a symlink, and removed after, unless
// a kill failed (it is then kept and the error returned); a line it cannot
// read is skipped. No record: nothing, no error. Call it as soon as
// the holder's exit is seen: what started after that is not the holder's.
func KillRecordedGroups(getenv func(string) string, project, key string, killGroup func(pgid int, leaderStart uint64) (int, error)) (int, error) {
	path, err := PgidsPath(getenv, project, key)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("holder: the group record %s: %w", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || !ok || int(sys.Uid) != os.Getuid() || st.Mode().Perm()&0o077 != 0 {
		return 0, fmt.Errorf("holder: the group record %s is not a private file of yours: left alone", path)
	}
	n := 0
	var errs []error
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		start, err2 := strconv.ParseUint(fields[1], 10, 64)
		if err1 != nil || err2 != nil || pid <= 1 || start == 0 {
			continue
		}
		k, err := killGroup(pid, start)
		n += k
		if err != nil {
			errs = append(errs, err)
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		// The record is the only trace of what is left to kill: kept, so the
		// failure is seen and the kill can be done again (reviewer-codex).
		return n, fmt.Errorf("%w (the record %s is kept)", errors.Join(errs...), path)
	}
	_ = os.Remove(path)
	return n, nil
}

// KillGroup kills what is left of a dead holder's terminal group: the
// processes of group pgid that started between the leader (leaderStart) and
// now (killGroupMembers).
func KillGroup(pgid int, leaderStart uint64) (int, error) {
	return killGroupMembers(pgid, leaderStart, nowTicks())
}
