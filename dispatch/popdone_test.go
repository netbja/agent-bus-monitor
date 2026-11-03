package dispatch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// donePop is a pop every condition of §2 calls done (pop-auto-retire): each
// row of TestPopDone breaks one.
func donePop() PopFacts {
	now := time.UnixMilli(10_000_000)
	return PopFacts{
		Now:         now,
		RetireAfter: 15 * time.Minute,
		Snap: &bus.AgentSnapshot{State: "idle", TS: now.Add(-20 * time.Minute).UnixMilli(),
			Pane: "w1:p3", PID: 42, PIDStart: 4200, Tier: "pop"},
		Pane: Inspection{State: "idle", Ready: true, PIDs: []int{42}, Starts: map[int]uint64{42: 4200}},
	}
}

func TestPopDone(t *testing.T) {
	if done, why := PopDone(donePop()); !done {
		t.Fatalf("the done pop is kept: %s", why)
	}
	const intent = `{"to_profile":"","by":"master","old_pid":42,"old_start":4200,"kind":"retire","nonce":"ab"}`
	for _, c := range []struct {
		name, why string
		edit      func(*PopFacts)
	}{
		{"unknown read", "unknown: board", func(f *PopFacts) { f.Unknown = []string{"board"} }},
		{"no snapshot", "no snapshot", func(f *PopFacts) { f.Snap = nil }},
		{"no tier", "not a pop", func(f *PopFacts) { f.Snap.Tier = "" }},
		{"boot role", "not a pop", func(f *PopFacts) { f.Snap.Tier = "boot" }},
		{"board task", "it owns a board task", func(f *PopFacts) { f.OwnedTasks = []string{"t1 (accepted)"} }},
		{"gate", "a review gate", func(f *PopFacts) { f.Gates = []string{"gate"} }},
		{"mission", "a mission attempt", func(f *PopFacts) { f.Missions = []string{"m"} }},
		{"ask", "an open ask", func(f *PopFacts) { f.OpenAsk = true }},
		{"pending", "messages wait for it", func(f *PopFacts) { f.Obligation.Pending = 1 }},
		{"unread", "messages wait for it", func(f *PopFacts) { f.Obligation.Unread = 2 }},
		// Older than the horizon still waits (foureyes): the forget script
		// reads with no floor, so the judgement does too.
		{"stale", "messages wait for it", func(f *PopFacts) { f.Obligation.Stale = 1 }},
		{"armed", "armed", func(f *PopFacts) { f.Armed = true }},
		{"wake typed", "a wake is being typed", func(f *PopFacts) { f.WakeState = "prompting" }},
		{"swap intent", "a swap or reload holds it", func(f *PopFacts) { f.Intent = `{"kind":"reload"}` }},
		{"another retire", "a swap or reload holds it", func(f *PopFacts) { f.Intent, f.OwnIntent = intent, strings.Replace(intent, "ab", "cd", 1) }},
		{"own intent expected, lapsed", "its retire intent lapsed", func(f *PopFacts) { f.OwnIntent = intent }},
		{"hand-off", "a hand-off waits", func(f *PopFacts) { f.Handoff = true }},
		{"working", "declared working", func(f *PopFacts) { f.Snap.State = "working" }},
		{"turn flip", "a turn since its declaration", func(f *PopFacts) { f.Snap.Auto = "turn" }},
		{"too recent", "idle for 5m", func(f *PopFacts) { f.Snap.TS = f.Now.Add(-5 * time.Minute).UnixMilli() }},
		{"other episode", "another episode", func(f *PopFacts) { f.Episode = 1 }},
		{"inspection error", "its pane could not be read", func(f *PopFacts) { f.Pane.Err = errors.New("x") }},
		{"pane gone", "its pane could not be read", func(f *PopFacts) { f.Pane.Gone = true }},
		{"another process", "does not run its verified process", func(f *PopFacts) { f.Pane.Starts[42] = 1 }},
		{"focused", "the pane is focused", func(f *PopFacts) { f.Pane.Focused = true }},
		{"prompt", "on a prompt", func(f *PopFacts) { f.Pane.State = "blocked" }},
		{"busy", "not ready", func(f *PopFacts) { f.Pane.Busy = true }},
		{"not ready", "not ready", func(f *PopFacts) { f.Pane.Ready = false }},
		{"paused", "the project is paused", func(f *PopFacts) { f.Paused = true }},
		{"held", "its provider is held", func(f *PopFacts) { f.Held = true }},
	} {
		f := donePop()
		c.edit(&f)
		done, why := PopDone(f)
		if done || !strings.Contains(why, c.why) {
			t.Errorf("%s: done=%v why=%q, want kept for %q", c.name, done, why, c.why)
		}
	}
	// Its own intent, byte-equal, passes; the episode it was told passes.
	f := donePop()
	f.Intent, f.OwnIntent, f.Episode = intent, intent, f.Snap.TS
	if done, why := PopDone(f); !done {
		t.Fatalf("own intent and its episode: kept for %s", why)
	}
	if got := PopEpisode(f.Snap); got != f.Snap.TS {
		t.Fatalf("episode = %d, want the declaration's time %d", got, f.Snap.TS)
	}
}

// closedPop is a pop whose verified process is gone and whose pane no
// longer exists (pop-closed-by-hand): closed by hand while it worked.
func closedPop() PopFacts {
	f := donePop()
	f.Snap.State, f.Snap.Auto = "working", "turn"
	f.Closed = true
	f.Pane = Inspection{Gone: true}
	f.Handoff, f.HandoffReport = true, "1793-0" // a hand-off no process will ever /clear
	return f
}

func TestPopDoneJudgesAClosedPop(t *testing.T) {
	if done, why := PopDone(closedPop()); !done {
		t.Fatalf("the closed pop is kept: %s", why)
	}
	for _, c := range []struct {
		name, why string
		edit      func(*PopFacts)
	}{
		{"not a pop", "not a pop", func(f *PopFacts) { f.Snap.Tier = "boot" }},
		{"unknown read", "unknown: board", func(f *PopFacts) { f.Unknown = []string{"board"} }},
		{"board task", "it owns a board task", func(f *PopFacts) { f.OwnedTasks = []string{"t1 (accepted)"} }},
		{"gate", "a review gate", func(f *PopFacts) { f.Gates = []string{"gate"} }},
		{"mission", "a mission attempt", func(f *PopFacts) { f.Missions = []string{"m"} }},
		{"ask", "an open ask", func(f *PopFacts) { f.OpenAsk = true }},
		{"unread", "messages wait for it", func(f *PopFacts) { f.Obligation.Unread = 1 }},
		{"armed", "armed", func(f *PopFacts) { f.Armed = true }},
		{"swap intent", "a swap or reload holds it", func(f *PopFacts) { f.Intent = `{"kind":"reload"}` }},
		{"too recent", "under", func(f *PopFacts) { f.Snap.TS = f.Now.Add(-5 * time.Minute).UnixMilli() }},
		{"other episode", "another episode", func(f *PopFacts) { f.Episode = 1 }},
		{"paused", "the project is paused", func(f *PopFacts) { f.Paused = true }},
		{"held", "its provider is held", func(f *PopFacts) { f.Held = true }},
	} {
		f := closedPop()
		c.edit(&f)
		if done, why := PopDone(f); done || !strings.Contains(why, c.why) {
			t.Errorf("%s: done=%v why=%q, want kept for %q", c.name, done, why, c.why)
		}
	}
	// Not closed, the same facts are judged as today: a working pop is kept.
	f := closedPop()
	f.Closed = false
	if done, _ := PopDone(f); done {
		t.Fatal("a pop not closed, declared working, was judged done")
	}
}

func TestProcGone(t *testing.T) {
	s := &bus.AgentSnapshot{PID: 42, PIDStart: 4200}
	at := func(start uint64, err error) func(int) (uint64, error) {
		return func(int) (uint64, error) { return start, err }
	}
	for _, c := range []struct {
		name string
		snap *bus.AgentSnapshot
		read func(int) (uint64, error)
		want bool
	}{
		{"alive", s, at(4200, nil), false},
		{"no such process", s, at(0, fmt.Errorf("read: %w", ErrNoProcess)), true},
		{"a recycled pid", s, at(9999, nil), true},
		// reviewer-codex: an unreadable or unsupported /proc (macOS,
		// permissions) proves nothing: fail closed.
		{"unreadable", s, at(0, errors.New("permission denied")), false},
		{"no /proc", s, at(0, os.ErrNotExist), false},
		{"no reader", s, nil, false},
		{"no verified pid", &bus.AgentSnapshot{}, at(0, ErrNoProcess), false},
		{"no snapshot", nil, at(0, ErrNoProcess), false},
	} {
		if got := ProcGone(c.snap, c.read); got != c.want {
			t.Errorf("%s: ProcGone = %v, want %v", c.name, got, c.want)
		}
	}
}

// ProcStart says ErrNoProcess only for a pid that /proc, readable, does not
// list: a process that ran and exited.
func TestProcStartNoProcess(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc on this system")
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcStart(cmd.Process.Pid); !errors.Is(err, ErrNoProcess) {
		t.Fatalf("an exited pid: %v, want ErrNoProcess", err)
	}
	if _, err := ProcStart(os.Getpid()); err != nil {
		t.Fatalf("this process: %v", err)
	}
}
