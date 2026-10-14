package service

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// LogMax and LogKeep bound a service log: the current file plus LogKeep-1
// rotated ones (.1, .2), each at most LogMax bytes.
const (
	LogMax  = 1 << 20
	LogKeep = 3
)

// RotatingFile is an append-only log that rotates by size.
type RotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

// OpenLog opens (or creates, 0600 in a 0700 directory) a rotating log.
func OpenLog(path string, max int64, keep int) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &RotatingFile{path: path, max: max, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *RotatingFile) rotate() error {
	r.f.Close()
	for i := r.keep - 1; i >= 1; i-- {
		from := r.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		_ = os.Rename(from, fmt.Sprintf("%s.%d", r.path, i))
	}
	return r.open()
}

// Write appends p, rotating first when p would cross the limit.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the current file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// TailLog returns the last n lines of a service log, reading the rotated
// file too when the current one is short.
func TailLog(path string, n int) ([]string, error) {
	var lines []string
	found := false
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		found = true
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			lines = append(lines, sc.Text())
			if len(lines) > n {
				lines = lines[1:]
			}
		}
		f.Close()
	}
	if !found {
		return nil, fmt.Errorf("no log at %s", path)
	}
	return lines, nil
}

var _ io.WriteCloser = (*RotatingFile)(nil)

// CrashFile is where launchd puts a service's stderr: only what the process
// printed before its own log opened (a fatal error, a panic).
func (p Paths) CrashFile(project, kind string) string {
	return filepath.Join(p.StateDir(project), kind+".crash.log")
}

// RotateIfLarger moves path to path.1 when it is over max bytes. It is a
// best-effort bound on launchd's stderr file, run at each start: a process
// still writing to the renamed file keeps its descriptor. That file only gets
// lines printed before the service's log opens and a copy of a fatal crash
// (which is also in the log, via debug.SetCrashOutput); a crash ends the
// process, so the restart budget (5 in 10 minutes) bounds its growth too.
// A missing file is not an error.
func RotateIfLarger(path string, max int64) error {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || st.Size() <= max {
		return err
	}
	return os.Rename(path, path+".1")
}

// File is the log's current file, for debug.SetCrashOutput: a fatal crash
// (a panic) is written to the service's own log as well as to stderr.
func (r *RotatingFile) File() *os.File {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f
}
