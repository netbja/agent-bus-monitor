package notify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// notify-last.json (spec "The sender interface", "The outcome"): the host's
// record of sends, never a message, a URL or a token. Per target, recent
// (the latest 20 outcomes, for display and health) and hour (the time of
// every send the limit counts, pruned past one hour only, so exempt sends
// never push a counted one out of the count: reviewer-codex); per project,
// the sources it could not read and since when. Every project's dispatch
// service writes it, so every read-modify-write is made under the host lock
// notify.lock beside it, and the file is replaced by a renamed temporary.

const recentMax = 20

// Outcome is one send to one target.
type Outcome struct {
	Project string    `json:"project,omitempty"` // "" = the person's test
	At      time.Time `json:"at"`
	OK      bool      `json:"ok"`
	Err     string    `json:"err,omitempty"` // the short error: target and status, never the URL
	Test    bool      `json:"test,omitempty"`
}

// TargetRecord is one target's sends.
type TargetRecord struct {
	Recent []Outcome   `json:"recent"`
	Hour   []time.Time `json:"hour,omitempty"`
}

// Last is notify-last.json.
type Last struct {
	Targets map[string]*TargetRecord        `json:"targets,omitempty"`
	Sources map[string]map[string]time.Time `json:"sources,omitempty"` // project -> source -> failing since
}

// LastPath is notify-last.json in stateDir (<state> = $XDG_STATE_HOME/agentbus).
func LastPath(stateDir string) string { return filepath.Join(stateDir, "notify-last.json") }

// Add records o for target; counted adds it to the hour the limit reads.
func (l *Last) Add(target string, o Outcome, counted bool) {
	if l.Targets == nil {
		l.Targets = map[string]*TargetRecord{}
	}
	r := l.Targets[target]
	if r == nil {
		r = &TargetRecord{}
		l.Targets[target] = r
	}
	r.Recent = append(r.Recent, o)
	if len(r.Recent) > recentMax {
		r.Recent = append([]Outcome(nil), r.Recent[len(r.Recent)-recentMax:]...)
	}
	if counted {
		r.Hour = append(r.Hour, o.At)
	}
	r.Hour = within(r.Hour, o.At)
}

// Reserve counts a send about to be made in target's hour, under the host
// lock, so two services deciding at once see each other's sends; the send
// itself happens outside the lock.
func (l *Last) Reserve(target string, at time.Time) {
	if l.Targets == nil {
		l.Targets = map[string]*TargetRecord{}
	}
	r := l.Targets[target]
	if r == nil {
		r = &TargetRecord{}
		l.Targets[target] = r
	}
	r.Hour = append(within(r.Hour, at), at)
}

// Unreserve gives back one reservation at at (the send failed or was not
// made): a target that is down does not use up its hour.
func (l *Last) Unreserve(target string, at time.Time) {
	r := l.Targets[target]
	if r == nil {
		return
	}
	for i, t := range r.Hour {
		if t.Equal(at) {
			r.Hour = append(r.Hour[:i], r.Hour[i+1:]...)
			return
		}
	}
}

// within keeps the times less than an hour before now.
func within(ts []time.Time, now time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < time.Hour {
			out = append(out, t)
		}
	}
	return out
}

// SentInHour is how many counted sends target had in the hour before now.
func (l Last) SentInHour(target string, now time.Time) int {
	r := l.Targets[target]
	if r == nil {
		return 0
	}
	n := 0
	for _, t := range r.Hour {
		if now.Sub(t) < time.Hour && !t.After(now) {
			n++
		}
	}
	return n
}

// Latest is target's newest outcome.
func (l Last) Latest(target string) (Outcome, bool) {
	r := l.Targets[target]
	if r == nil || len(r.Recent) == 0 {
		return Outcome{}, false
	}
	return r.Recent[len(r.Recent)-1], true
}

// SetSources records the sources project could not read at now, each with
// the first time it failed in this stretch; nil or empty clears the project.
func (l *Last) SetSources(project string, failing map[string]time.Time, now time.Time) {
	if len(failing) == 0 {
		delete(l.Sources, project)
		return
	}
	if l.Sources == nil {
		l.Sources = map[string]map[string]time.Time{}
	}
	prev := l.Sources[project]
	next := map[string]time.Time{}
	for src, at := range failing {
		if since, ok := prev[src]; ok && !since.After(at) {
			at = since
		}
		next[src] = at
	}
	l.Sources[project] = next
}

// FailingSources is project's failing sources, by name.
func (l Last) FailingSources(project string) []string {
	var out []string
	for s := range l.Sources[project] {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ReadLast reads path; a missing file is an empty record.
func ReadLast(path string) (Last, error) {
	var l Last
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return Last{}, errors.New("notify-last.json does not parse")
	}
	return l, nil
}

// ErrLockBusy: the host lock was not had within lockWait; nothing was read
// nor written, and a pass sends nothing this time.
var ErrLockBusy = errors.New("notify.lock is held: nothing sent this time")

var lockWait = 5 * time.Second

// UpdateLast reads path under the host lock, lets fn change the record, and
// writes it back (fn's error writes nothing).
func UpdateLast(path string, fn func(*Last) error) error {
	unlock, err := LockHost(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unlock()
	l, err := ReadLast(path)
	if err != nil {
		return err
	}
	if err := fn(&l); err != nil {
		return err
	}
	return writeLast(path, l)
}

// LockHost takes <dir>/notify.lock, waiting at most lockWait; the caller
// holds it across the limit's decision and the record's write.
func LockHost(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "notify.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, ErrLockBusy
			}
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func writeLast(path string, l Last) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".notify-last-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
