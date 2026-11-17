package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/lifecycle"
	"github.com/netbja/agent-bus-monitor/service"
)

func withBoot(t *testing.T, id string) {
	t.Helper()
	old := runBootID
	runBootID = func() string { return id }
	t.Cleanup(func() { runBootID = old })
}

func runRecordOf(t *testing.T, dir string) lifecycle.Record {
	t.Helper()
	r, err := lifecycle.ReadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The dispatch service: stopped (signal) when a signal ends it, nothing
// when it ends otherwise (running is its reboot judge's: reboot_test.go).
func TestDispatchRunRecord(t *testing.T) {
	withBoot(t, "B")
	dir := t.TempDir()
	r := newDispatchRunRecord(dir, io.Discard)
	if err := lifecycle.Started(dir, "B", t0RR); err != nil {
		t.Fatal(err)
	}
	r.exited(false)
	if got := runRecordOf(t, dir); got.State != lifecycle.Running {
		t.Fatalf("an exit without a signal: %+v", got)
	}
	r.exited(true)
	if got := runRecordOf(t, dir); got.State != lifecycle.Stopped || got.Cause != lifecycle.CauseSignal {
		t.Fatalf("signalled: %+v", got)
	}
	// An unknown boot id writes nothing (§2.3).
	withBoot(t, "")
	other := t.TempDir()
	u := newDispatchRunRecord(other, io.Discard)
	u.exited(true)
	recordUp(other, io.Discard)
	recordStopping(other, lifecycle.CauseDown, io.Discard)
	if left, _ := os.ReadDir(other); len(left) != 0 {
		t.Fatalf("written with no boot id: %v", left)
	}
}

// up writes running (and the counter) before it starts any service.
func TestUpWritesRunningBeforeTheServices(t *testing.T) {
	withBoot(t, "boot-up")
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	dir := u.paths.StateDir("demo")
	starts := 0
	u.m.onCall = func(call string) {
		if r, err := lifecycle.ReadRun(dir); err != nil || r.State != lifecycle.Running || r.Boot != "boot-up" {
			t.Errorf("%s before the run record: %+v %v", call, r, err)
		}
		starts++
	}
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if starts == 0 {
		t.Fatal("no service started: the order was not checked")
	}
	if _, err := lifecycle.ReadCounter(dir); err != nil {
		t.Fatalf("counter: %v", err)
	}
}

// down writes stopped (down) before it stops any service.
func TestDownWritesDownBeforeStopping(t *testing.T) {
	withBoot(t, "boot-down")
	m, d := cmdFixture(t)
	m.Install(service.Spec{Project: "demo", Kind: service.Dispatch})
	m.Start(m.specs["demo/dispatch"].Project, service.Dispatch)
	dir := d.Paths.StateDir("demo")
	stops := 0
	m.onCall = func(call string) {
		if r, err := lifecycle.ReadRun(dir); err != nil || r.Cause != lifecycle.CauseDown {
			t.Errorf("%s before the run record: %+v %v", call, r, err)
		}
		stops++
	}
	var out, errw bytes.Buffer
	if code := runDown([]string{"demo"}, d, &out, &errw); code != 0 {
		t.Fatalf("down = %d: %s", code, errw.String())
	}
	if stops == 0 {
		t.Fatal("nothing stopped: the order was not checked")
	}
}

// suspend writes stopped (suspend) after its pause record and before the
// services stop; a cancelled suspend puts running back.
func TestSuspendWritesTheRunRecord(t *testing.T) {
	withBoot(t, "boot-s")
	w := newSuspendWorld(t)
	w.agent("master", "idle")
	stops := 0
	w.mgr.onCall = func(call string) {
		if r, err := lifecycle.ReadRun(w.state); err != nil || r.Cause != lifecycle.CauseSuspend || r.Boot != "boot-s" {
			t.Errorf("%s before the run record: %+v %v", call, r, err)
		}
		if p := w.pause(); p == nil || !p.Suspended() {
			t.Errorf("%s before the pause record", call)
		}
		stops++
	}
	if code, out, errw := w.run("--reason", "OS updates"); code != 0 {
		t.Fatalf("suspend = %d\n%s\n%s", code, out, errw)
	}
	if stops == 0 {
		t.Fatal("nothing stopped")
	}

	c := newSuspendWorld(t)
	c.agent("coder", "working")
	c.agent("master", "idle")
	if code, out, errw := c.run("--wait", "50ms"); code != 1 {
		t.Fatalf("suspend = %d, want cancelled\n%s\n%s", code, out, errw)
	}
	if r := runRecordOf(t, c.state); r.State != lifecycle.Running || r.Boot != "boot-s" {
		t.Fatalf("after a cancelled suspend: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(c.state, lifecycle.CounterFile)); err != nil {
		t.Fatalf("counter: %v", err)
	}
}

var t0RR = time.Date(2026, 11, 15, 21, 40, 0, 0, time.UTC)
