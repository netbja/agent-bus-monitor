package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/service"
)

// escalateEnv gives the dispatcher's config a private home, as the service
// unit's environment does.
func escalateEnv(t *testing.T) service.Paths {
	t.Helper()
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "XDG_CONFIG_HOME": root + "/cfg", "XDG_STATE_HOME": root + "/state",
		"XDG_DATA_HOME": root + "/data", "XDG_RUNTIME_DIR": root + "/run"} {
		t.Setenv(k, v)
	}
	return service.Paths{Home: root, ConfigHome: root + "/cfg", StateHome: root + "/state", DataHome: root + "/data"}
}

// captureSupervised replaces the supervisor: the config a path would run
// with, kept instead of run.
func captureSupervised(t *testing.T) *dispatch.Config {
	t.Helper()
	var got dispatch.Config
	old := supervise
	supervise = func(_ context.Context, _ *bus.Bus, _ dispatch.Runtime, cfg dispatch.Config, _ io.Writer) int {
		got = cfg
		return 0
	}
	t.Cleanup(func() { supervise = old })
	return &got
}

// stallNoticeNames runs cfg's team-stall notice on a fresh stall (master
// registered and idle, lead not registered): the person is told at once
// that target cannot act only when the notice's target is "lead".
func stallNoticeNames(t *testing.T, w *cmdtest.StallWorld, cfg dispatch.Config) []string {
	t.Helper()
	rec := w.PutStall()
	if cfg.Health.StallNotify == nil {
		t.Fatal("no team-stall notice")
	}
	if err := cfg.Health.StallNotify(context.Background(), "stall-holder", rec); err != nil {
		t.Fatal(err)
	}
	return w.PersonNotices()
}

// The dispatch service's escalation target (service run --escalate-to, or
// AGENT_BUS_ESCALATE_TO) reaches the stall checks, not only the loop.
func TestServiceDispatchEscalatesToItsTarget(t *testing.T) {
	w := cmdtest.NewStallWorld(t)
	paths := escalateEnv(t)
	got := captureSupervised(t)
	d := serviceRunDepsFor(w.B.Project(), "", "h1", paths)
	if code := d.RunDispatch(context.Background(), w.B, "lead", nil, io.Discard); code != 0 {
		t.Fatalf("RunDispatch = %d", code)
	}
	if got.EscalateTo != "lead" {
		t.Fatalf("loop target %q", got.EscalateTo)
	}
	notes := stallNoticeNames(t, w, *got)
	if len(notes) != 1 || !strings.Contains(notes[0], "lead cannot act (lead is not registered)") {
		t.Fatalf("stall notice %q, want it to name lead", notes)
	}
}

// Without a target the service escalates to master, everywhere.
func TestServiceDispatchEscalatesToMasterByDefault(t *testing.T) {
	w := cmdtest.NewStallWorld(t)
	paths := escalateEnv(t)
	got := captureSupervised(t)
	d := serviceRunDepsFor(w.B.Project(), "", "h1", paths)
	if code := d.RunDispatch(context.Background(), w.B, "", nil, io.Discard); code != 0 {
		t.Fatalf("RunDispatch = %d", code)
	}
	if got.EscalateTo != "master" {
		t.Fatalf("loop target %q", got.EscalateTo)
	}
	if notes := stallNoticeNames(t, w, *got); len(notes) != 0 { // master can act: no notice yet
		t.Fatalf("stall notice %q", notes)
	}
}

// The foreground dispatcher's --escalate-to reaches the stall checks too.
func TestForegroundDispatchEscalatesToItsTarget(t *testing.T) {
	w := cmdtest.NewStallWorld(t)
	escalateEnv(t)
	got := captureSupervised(t)
	if code := runDispatch(context.Background(), w.B, []string{"--escalate-to", "lead"}, noRuntime{t}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("runDispatch = %d", code)
	}
	if got.EscalateTo != "lead" {
		t.Fatalf("loop target %q", got.EscalateTo)
	}
	notes := stallNoticeNames(t, w, *got)
	if len(notes) != 1 || !strings.Contains(notes[0], "lead cannot act (lead is not registered)") {
		t.Fatalf("stall notice %q, want it to name lead", notes)
	}
}

// The reboot judge in the service's config (cfg.Reboot, the one the service
// runs) tells the service's target that the team restarts: this boot is
// already judged a restart, so the judge gives its directive again.
func TestServiceRebootEscalatesToItsTarget(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"lead", "lead"}, {"", "master"}} {
		w := cmdtest.NewStallWorld(t)
		paths := escalateEnv(t)
		withBoot(t, "B7")
		got := captureSupervised(t)
		d := serviceRunDepsFor(w.B.Project(), "", "h1", paths)
		if code := d.RunDispatch(context.Background(), w.B, tc.in, nil, io.Discard); code != 0 {
			t.Fatalf("RunDispatch = %d", code)
		}
		rec := bus.RebootRecord{Boot: "B7", Verdict: bus.RebootRestarting, N: 1, Max: 3, Window: 86400000}
		if err := w.B.FencedPutReboot(context.Background(), "stall-holder", rec); err != nil {
			t.Fatal(err)
		}
		if got.Reboot == nil {
			t.Fatal("the service judges no reboot")
		}
		if err := got.Reboot(context.Background(), "stall-holder"); err != nil {
			t.Fatal(err)
		}
		if told := w.DispatchDirectives(tc.want); len(told) != 1 || !strings.Contains(told[0], "restarted after an unplanned stop") {
			t.Errorf("%q: directives to %s %q", tc.in, tc.want, told)
		}
	}
}
