package main

import (
	"errors"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"os/exec"
	"runtime"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// A pid the real dispatch.ProcStart finds gone is dead to relaunch.SnapLife, so a tab
// whose old client still shows in herdr reads busy (waited on), never alive
// (the reload of foureyes, 2026-11-09 20:24: "still reads alive 500ms after
// the old process exited").
func TestSnapLifeReadsAGonePidDead(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("dispatch.ProcStart reads /proc")
	}
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	gone := c.Process.Pid // run and reaped: /proc lists it no more
	if _, err := dispatch.ProcStart(gone); !errors.Is(err, dispatch.ErrNoProcess) {
		t.Skipf("pid %d was reused already (%v)", gone, err)
	}
	if got := relaunch.SnapLife(bus.AgentSnapshot{PID: gone, PIDStart: 1}, true, dispatch.ProcStart); got != relaunch.LifeDead {
		t.Fatalf("a gone pid reads %s, want dead", got)
	}
	// anything else proves nothing: unknown, as before
	odd := func(int) (uint64, error) { return 0, errors.New("permission denied") }
	if got := relaunch.SnapLife(bus.AgentSnapshot{PID: gone, PIDStart: 1}, true, odd); got != relaunch.LifeUnknown {
		t.Fatalf("an unreadable pid reads %s, want unknown", got)
	}
}
