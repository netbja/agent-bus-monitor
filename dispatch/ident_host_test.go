package dispatch

import (
	"errors"
	"os/exec"
	"testing"
)

// The contract self-repair's R1 relies on, on every host with a process
// identity (Linux /proc, macOS sysctl; self-repair step 10): a process that
// was reaped reads ErrNoProcess, the one fact "gone" is built on
// (relaunch.PidListedNoMore); a live one reads a start that does not change.
func TestProcStartGoneIsErrNoProcess(t *testing.T) {
	c := exec.Command("/bin/sh", "-c", "exit 0")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pid := c.Process.Pid
	if err := c.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcStart(pid); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("a reaped pid reads %v, want ErrNoProcess", err)
	}
	if _, err := ProcStart(999999999); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("a pid that never was reads %v, want ErrNoProcess", err)
	}
}
