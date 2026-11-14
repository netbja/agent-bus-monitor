package dispatch

import (
	"errors"
	"testing"
)

// The macOS process table's answer, judged (self-repair step 10): only zero
// records proves a process gone; a failed or malformed answer, several
// records, or a record of another pid is unknown, never gone (reviewer-codex:
// SysctlKinfoProc's EIO also stands for a size mismatch).
func TestKinfoVerdict(t *testing.T) {
	for name, c := range map[string]struct {
		pids []int
		err  error
		gone bool
		ok   bool
	}{
		"one record":       {pids: []int{42}, ok: true},
		"zero records":     {gone: true},
		"failed sysctl":    {err: errors.New("sysctl: EIO")},
		"several records":  {pids: []int{42, 43}},
		"another pid":      {pids: []int{43}},
		"failed, zero too": {err: errors.New("size not a multiple")},
	} {
		err := kinfoVerdict(42, c.pids, c.err)
		switch {
		case c.ok && err != nil, !c.ok && err == nil:
			t.Errorf("%s: %v", name, err)
		case errors.Is(err, ErrNoProcess) != c.gone:
			t.Errorf("%s: gone %v, want %v (%v)", name, errors.Is(err, ErrNoProcess), c.gone, err)
		}
	}
}
