package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A service that fails BudgetStarts times within BudgetWindow gives up and
// exits 0, so neither manager loops on a fault that does not heal. launchd
// has no start limit of its own; systemd's is set to the same numbers.
const (
	BudgetStarts = 5
	BudgetWindow = 10 * time.Minute
)

// Budget is the record of a service's recent failed runs.
type Budget struct{ Path string }

func (b Budget) read(now time.Time) []time.Time {
	raw, _ := os.ReadFile(b.Path)
	var out []time.Time
	for _, f := range strings.Fields(string(raw)) {
		ms, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			continue
		}
		if t := time.UnixMilli(ms); now.Sub(t) < BudgetWindow {
			out = append(out, t)
		}
	}
	return out
}

// Spent reports whether the recent failures used up the budget.
func (b Budget) Spent(now time.Time) bool { return len(b.read(now)) >= BudgetStarts }

// FailureCount is the number of starts still inside the restart window.
func (b Budget) FailureCount(now time.Time) int { return len(b.read(now)) }

// FailureCountChecked is the health reader variant: a missing budget is empty,
// while unreadable or malformed state is not silently reported as healthy.
func (b Budget) FailureCountChecked(now time.Time) (int, error) {
	raw, err := os.ReadFile(b.Path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer clear(raw)
	count := 0
	for _, field := range strings.Fields(string(raw)) {
		ms, err := strconv.ParseInt(field, 10, 64)
		if err != nil || ms <= 0 {
			return 0, os.ErrInvalid
		}
		if age := now.Sub(time.UnixMilli(ms)); age >= 0 && age < BudgetWindow {
			count++
		}
	}
	return count, nil
}

// Failed records one failed run.
func (b Budget) Failed(now time.Time) error {
	ts := append(b.read(now), now)
	var s strings.Builder
	for _, t := range ts {
		s.WriteString(strconv.FormatInt(t.UnixMilli(), 10) + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(b.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(b.Path, []byte(s.String()), 0o600)
}

// Reset forgets the failures: an explicit start is a fresh attempt.
func (b Budget) Reset() error {
	if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Begin counts this start as a failure until End: a process that panics or is
// killed never reaches End, so a crash loop spends the budget too. It returns
// the start's mark for End.
func (b Budget) Begin(now time.Time) time.Time {
	_ = b.Failed(now)
	return now
}

// End withdraws a start that ended cleanly. A zero mark is a no-op.
func (b Budget) End(mark time.Time) error {
	if mark.IsZero() {
		return nil
	}
	raw, err := os.ReadFile(b.Path)
	if err != nil {
		return nil
	}
	want := strconv.FormatInt(mark.UnixMilli(), 10)
	var s strings.Builder
	removed := false
	for _, f := range strings.Fields(string(raw)) {
		if f == want && !removed {
			removed = true
			continue
		}
		s.WriteString(f + "\n")
	}
	return os.WriteFile(b.Path, []byte(s.String()), 0o600)
}
