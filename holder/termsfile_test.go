package holder

import (
	"os"
	"path/filepath"
	"testing"
)

// A pane id never names two terminals (holder hardening, found by
// TestHolderRestartEndToEnd): a holder that restarts after a crash, or
// after a reboot, numbers on from its TermsFile instead of from t1 again,
// so self-repair's proof ("a new process in a new pane") holds for the
// relaunched agent. Without the file the numbering starts at t1 as before.
func TestHolderNumbersOnAcrossProcesses(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "holder.terms")
	s1, sock1 := newTestHolderWith(t, func(c *Config) { c.TermsFile = file })
	cl := dial(t, sock1)
	if a := startRole(t, cl, "echoer"); a.ID != "t1" {
		t.Fatalf("first: %s", a.ID)
	}
	if a := startRole(t, cl, "echoer"); a.ID != "t2" {
		t.Fatalf("second: %s", a.ID)
	}
	s1.Shutdown() // the process gone, its record kept
	_, sock2 := newTestHolderWith(t, func(c *Config) { c.TermsFile = file })
	if a := startRole(t, dial(t, sock2), "echoer"); a.ID != "t3" {
		t.Fatalf("after a restart: %s, want t3", a.ID)
	}
	// An unreadable record numbers from t1, as a holder without one.
	if err := os.WriteFile(file, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, sock3 := newTestHolderWith(t, func(c *Config) { c.TermsFile = file })
	if a := startRole(t, dial(t, sock3), "echoer"); a.ID != "t1" {
		t.Fatalf("after a garbled record: %s", a.ID)
	}
}
