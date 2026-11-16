// Package lifecycle tells an intentional reboot from an unplanned one
// (reboot-resume spec 2026-11-15, §2-§3.2). A clean shutdown sends the
// services a signal; a crash or a power cut sends none. The dispatch service
// records which happened in a local file, run.json, and the next boot reads
// it. Beside it, unplanned.json counts the automatic restarts. Both are
// local, 0600 and replaced atomically (write a temp file, fsync it, rename,
// fsync the directory): at shutdown the broker stops with the services, and
// a power cut is the very event counted, so neither lives in Redis.
package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The files, in the project's state directory (<statedir>/agentbus/<p>).
const (
	RunFile     = "run.json"
	CounterFile = "unplanned.json"
)

// A run record's states and causes (§2.1).
const (
	Running = "running"
	Stopped = "stopped"

	CauseSignal  = "signal"  // the dispatch service got SIGTERM: a clean shutdown
	CauseDown    = "down"    // agentbus down: the person stopped it
	CauseSuspend = "suspend" // agentbus suspend
)

// ErrMalformed: a file is there but is not what was written.
var ErrMalformed = errors.New("malformed")

// Record is run.json. At is wall-clock and only displayed.
type Record struct {
	Boot  string `json:"boot"`
	State string `json:"state"`
	Cause string `json:"cause"`
	At    string `json:"at"`
}

// Verdict is the judgement of a start (§2.2).
type Verdict string

const (
	None        Verdict = "none"        // nothing automatic, nothing asked
	Intentional Verdict = "intentional" // a clean shutdown
	Unplanned   Verdict = "unplanned"   // no signal: a crash or a power cut
	Suspended   Verdict = "suspended"   // suspended before the shutdown
)

// Judge is the verdict for a start on boot, given the run record read
// (prev, or the error reading it). A missing or unreadable record, or an
// unknown boot id on either side, is None; so is the same boot.
func Judge(prev *Record, err error, boot string) Verdict {
	if err != nil || prev == nil || boot == "" || prev.Boot == "" || prev.Boot == boot {
		return None
	}
	switch {
	case prev.State == Running:
		return Unplanned
	case prev.State == Stopped && prev.Cause == CauseSignal:
		return Intentional
	case prev.State == Stopped && prev.Cause == CauseSuspend:
		return Suspended
	}
	return None // stopped by down, or anything not written by us
}

// ReadRun reads run.json: os.ErrNotExist when missing, ErrMalformed when
// unreadable as a record.
func ReadRun(dir string) (Record, error) {
	b, err := readRegular(filepath.Join(dir, RunFile))
	if err != nil {
		return Record{}, err
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.State == "" {
		return Record{}, fmt.Errorf("%s: %w", RunFile, ErrMalformed)
	}
	return r, nil
}

// Started is the dispatch service's write at start: running, this boot. On
// the first start (no run.json) the counter is created as [] first, so
// run.json present means the counter was created. A counter lost since is
// never recreated here: only the person's up or resume does (§3.2).
func Started(dir, boot string, now time.Time) error {
	return writeRun(dir, Record{Boot: boot, State: Running, At: stamp(now)})
}

// Up is agentbus up's write before it starts the services: the counter
// recreated as [] when missing (never reset when it exists), then running,
// this boot (the person started it: nothing to judge).
func Up(dir, boot string, now time.Time) error {
	if err := ensureCounter(dir); err != nil {
		return err
	}
	return writeRun(dir, Record{Boot: boot, State: Running, At: stamp(now)})
}

// Signalled is the dispatch service's write on SIGTERM: stopped, cause
// signal, only over running of this boot (never over down or suspend).
func Signalled(dir, boot string, now time.Time) error {
	r, err := ReadRun(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if r.State != Running || r.Boot != boot || boot == "" {
		return nil
	}
	return writeRun(dir, Record{Boot: boot, State: Stopped, Cause: CauseSignal, At: stamp(now)})
}

// Stopping is down's and suspend's write before they stop the services.
func Stopping(dir, boot, cause string, now time.Time) error {
	if cause != CauseDown && cause != CauseSuspend {
		return fmt.Errorf("lifecycle: cause %q", cause)
	}
	return writeRun(dir, Record{Boot: boot, State: Stopped, Cause: cause, At: stamp(now)})
}

// Entry is one automatic restart: the boot it restarted, at (Redis TIME, ms).
type Entry struct {
	Boot string `json:"boot"`
	At   int64  `json:"at"`
}

// ReadCounter reads unplanned.json: os.ErrNotExist when missing (never read
// as empty: that would reset the budget), ErrMalformed when unreadable.
func ReadCounter(dir string) ([]Entry, error) {
	b, err := readRegular(filepath.Join(dir, CounterFile))
	if err != nil {
		return nil, err
	}
	var es []Entry
	if json.Unmarshal(b, &es) != nil || es == nil {
		return nil, fmt.Errorf("%s: %w", CounterFile, ErrMalformed)
	}
	for _, e := range es { // an entry the dispatcher did not write would drop out of the window: a reset
		if e.Boot == "" || e.At <= 0 {
			return nil, fmt.Errorf("%s: an entry without its boot or time: %w", CounterFile, ErrMalformed)
		}
	}
	return es, nil
}

// maxFile is the most read of either file (one restart entry is ~30 bytes).
const maxFile = 1 << 20

// readRegular reads path only when it is a regular file (never through a
// symlink, never blocking on a FIFO): os.ErrNotExist when missing,
// ErrMalformed for anything else, never its content.
func readRegular(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	// O_NONBLOCK keeps a FIFO from blocking. Then: a symlink's target is not
	// the file Lstat saw (SameFile), and a FIFO or device is not regular,
	// even one whose writer would feed it what a counter looks like.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if got, err := f.Stat(); err != nil || !got.Mode().IsRegular() || !os.SameFile(fi, got) {
		return nil, fmt.Errorf("%s: not a regular file: %w", filepath.Base(path), ErrMalformed)
	}
	// At most maxFile+1 bytes: a larger file is cut, and a cut JSON value
	// never decodes, so it reads as malformed.
	return io.ReadAll(io.LimitReader(f, maxFile+1))
}

// WriteCounter replaces unplanned.json, fsync'd before it returns.
func WriteCounter(dir string, es []Entry) error {
	if es == nil {
		es = []Entry{}
	}
	b, err := json.Marshal(es)
	if err != nil {
		return err
	}
	return writeAtomic(dir, CounterFile, append(b, '\n'))
}

// ResetCounter empties the counter: the person's resume (§3.4).
func ResetCounter(dir string) error { return WriteCounter(dir, nil) }

// Count adds boot at now (a boot already listed is not added again) and
// keeps only the entries within window of now.
func Count(es []Entry, boot string, now int64, window time.Duration) []Entry {
	out := make([]Entry, 0, len(es)+1)
	seen := false
	for _, e := range es {
		if now-e.At >= window.Milliseconds() {
			continue
		}
		seen = seen || e.Boot == boot
		out = append(out, e)
	}
	if !seen {
		out = append(out, Entry{Boot: boot, At: now})
	}
	return out
}

func ensureCounter(dir string) error {
	if fi, err := os.Lstat(filepath.Join(dir, CounterFile)); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: not a regular file", CounterFile)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return WriteCounter(dir, nil)
}

// writeRun replaces run.json. The write that creates it creates the counter
// first, as [], whoever writes it (§3.2's bootstrap): run.json present means
// the counter was created.
func writeRun(dir string, r Record) error {
	if _, err := os.Lstat(filepath.Join(dir, RunFile)); errors.Is(err, os.ErrNotExist) {
		if err := ensureCounter(dir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeAtomic(dir, RunFile, append(b, '\n'))
}

func stamp(now time.Time) string { return now.UTC().Format(time.RFC3339) }

// writeAtomic writes name in dir: a 0600 temp file beside it, fsync'd,
// renamed into place, then the directory fsync'd.
func writeAtomic(dir, name string, b []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // gone after the rename; left by nothing on an error
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
