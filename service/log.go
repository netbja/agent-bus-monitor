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
