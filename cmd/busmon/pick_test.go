package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/projlist"
)

func pickRows(names ...string) []projlist.Row {
	var rows []projlist.Row
	for _, n := range names {
		rows = append(rows, projlist.Row{Name: n, WorkingDir: "/src/" + n, Path: "/cfg/" + n + ".toml"})
	}
	return rows
}

func TestAgentbusBeside(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	beside := filepath.Join(filepath.Dir(exe), "agentbus")
	got := agentbusBeside()
	if _, err := os.Stat(beside); err == nil {
		if got != beside {
			t.Fatalf("got %q, want %q", got, beside)
		}
	} else if got != "agentbus" {
		t.Fatalf("got %q, want the PATH fallback", got)
	}
}

func TestUpRunnerReportsStderrTailAndTimeout(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "agentbus")
	script := "#!/bin/sh\necho \"args: $*\"\nif [ \"$2\" = slow ]; then sleep 2; exit 0; fi\necho first line >&2\necho \"agentbus up: bootstrap failed: $PICK_TEST_VAR\" >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICK_TEST_VAR", "env passed")
	err := upRunner(fake, 5*time.Second)("demo")
	if err == nil || err.Error() != "bootstrap failed: env passed" {
		t.Fatalf("err %v: want the stderr tail with the environment passed through", err)
	}
	if strings.Contains(err.Error(), "first line") || strings.Contains(err.Error(), "args:") {
		t.Fatalf("err %v: only the stderr tail belongs on one status line", err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	start := time.Now()
	err = upRunner(fake, 100*time.Millisecond)("slow")
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err %v: want a timeout that says up was left running", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the wait is not bounded")
	}
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "AGENT_BUS_PROJECT", "REDIS_URL", "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "AGENT_BUS_HUMAN", "AGENTBUS_PROJECTS_DIR":
			continue
		}
		env = append(env, kv)
	}
	return env
}

func TestPickMainHelper(t *testing.T) {
	if os.Getenv("BUSMON_PICK_MAIN_HELPER") != "1" {
		t.Skip("helper for TestPickRunsAboveTheProjectExit")
	}
	runPicker = func(host, theme string) int { fmt.Println("PICKER RAN"); return 0 }
	os.Args = append([]string{"busmon"}, strings.Fields(os.Getenv("BUSMON_PICK_ARGS"))...)
	main()
}

func TestUpRunnerHelper(t *testing.T) {
	fake := os.Getenv("BUSMON_UP_HELPER")
	if fake == "" {
		t.Skip("helper for TestUpRunnerOutlivesTheWait")
	}
	fmt.Println(upRunner(fake, 50*time.Millisecond)("demo"))
	os.Exit(0)
}

// A finished up leaves no log behind.
func TestUpRunnerRemovesItsLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	fake := filepath.Join(t.TempDir(), "agentbus")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := upRunner(fake, 5*time.Second)("demo"); err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	if logs, _ := filepath.Glob(filepath.Join(dir, "agentbus-up-*.log")); len(logs) != 0 {
		t.Fatalf("logs left: %v", logs)
	}
}

// TestPickRunsAboveTheProjectExit runs main in a child process (the only way
// to see an os.Exit) with no project and an unreachable broker.
func TestPickRunsAboveTheProjectExit(t *testing.T) {
	if os.Getenv("BUSMON_PICK_MAIN_HELPER") == "1" {
		return
	}
	for _, args := range [][]string{{"--pick"}, {"--pick", "--reset", "--yes"}, {"--reset", "--yes", "--pick"}, {"--pick", "--delete", "demo", "--yes"}} {
		dir := t.TempDir()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPickMainHelper$")
		cmd.Env = append(cleanEnv(), "BUSMON_PICK_MAIN_HELPER=1", "BUSMON_PICK_ARGS="+strings.Join(args, " "),
			"HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir,
			"REDIS_URL=unix://"+filepath.Join(dir, "no-broker.sock"), "AGENT_BUS_NO_UPDATE_CHECK=1")
		out, err := cmd.CombinedOutput()
		s := string(out)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, s)
		}
		if !strings.Contains(s, "PICKER RAN") {
			t.Fatalf("%v: the picker did not run:\n%s", args, s)
		}
		for _, bad := range []string{"project required", "Purged", "Deleted", "Redis connection failed"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%v: %q in output:\n%s", args, bad, s)
			}
		}
	}
}

// A picker that stops waiting (timeout, esc, exit) leaves up to finish: no
// pipe for it to die on, no tie to the picker's session, the log kept and
// named.
func TestUpRunnerOutlivesTheWait(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	marker := filepath.Join(dir, "finished")
	fake := filepath.Join(dir, "agentbus")
	script := "#!/bin/sh\nsleep 0.3\necho still provisioning\necho late error >&2\ntouch " + marker + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The runner runs in a child that exits as soon as its wait runs out, as
	// busmon does: whatever the runner held (pipes, session) goes with it.
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpRunnerHelper$")
	cmd.Env = append(os.Environ(), "BUSMON_UP_HELPER="+fake)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "left to finish") {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	logs, _ := filepath.Glob(filepath.Join(tmp, "agentbus-up-*.log"))
	if len(logs) != 1 || !strings.Contains(string(out), logs[0]) {
		t.Fatalf("the log %v is not named in %q", logs, out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("up did not finish after the picker stopped waiting")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if b, _ := os.ReadFile(logs[0]); !strings.Contains(string(b), "late error") {
		t.Fatalf("its stderr did not reach the log: %q", b)
	}
}

func TestPickProductionTimeoutIsTwoSeconds(t *testing.T) {
	if pickTimeout != 2*time.Second {
		t.Fatalf("pickTimeout = %v", pickTimeout)
	}
	if m := newPickShell(context.Background(), "/cfg", nil, nil, nil, nil); m.timeout != pickTimeout {
		t.Fatalf("default model timeout %v", m.timeout)
	}
}

// The header reads the broker down when every row is unknown, or when the
// connection failed; one answered row means it answered.
func TestPickBrokerDownIsEveryRowUnknown(t *testing.T) {
	m := newPickShell(context.Background(), "/cfg", pickRows("a", "b"), nil, nil, nil)
	if m.snapshot().down {
		t.Fatal("down before any check")
	}
	m = pickStep(t, m, pickAnnotatedMsg{rows: pickRows("a", "b")}) // every row Unknown
	if !m.snapshot().down {
		t.Fatal("every row unknown should read broker unreachable")
	}
	rows := pickRows("a", "b")
	rows[1].Broker = projlist.Absent
	if m = pickStep(t, m, pickAnnotatedMsg{rows: rows}); m.snapshot().down {
		t.Fatal("one answered row means the broker answered")
	}
	m = newPickShell(context.Background(), "/cfg", nil, nil, nil, nil)
	m = pickStep(t, m, pickAnnotatedMsg{connErr: errors.New("dial tcp: refused")})
	if !m.snapshot().down || len(m.snapshot().vis) != 0 {
		t.Fatal("a failed connection with no rows is still a down broker, and invents no row")
	}
}

func TestPickLookup(t *testing.T) {
	for _, c := range []struct {
		k    Key
		want Action
	}{
		{Key{Code: keyUp}, ActUp}, {Key{Code: keyDown}, ActDown}, {Key{Code: keyEnter}, ActOpen},
		{Key{Code: keyEscape}, ActClose}, {Key{Code: keyRune, Rune: 'q'}, ActPickType}, {Key{Code: keyRune, Rune: 'j'}, ActPickType},
		{Key{Code: keyBackspace}, ActPickType}, {Key{Code: keyCtrlC}, ActQuit},
	} {
		if got := lookup(ModePick, PanelAgents, TierWide, c.k); got != c.want {
			t.Errorf("%+v: %v, want %v", c.k, got, c.want)
		}
	}
	if f := footerHints(ModePick, PanelAgents, TierWide, 120).text(); !strings.Contains(f, "enter open") || !strings.Contains(f, "esc close") {
		t.Fatalf("footer %q", f)
	}
}

// The picker reads the broker through the keys function and nothing else:
// the listing probes each row once, an open is one fresh check, and run is
// the only other effect.
func TestPickListingAndOpeningOnlyRead(t *testing.T) {
	var probes atomic.Int32
	keys := func(_ context.Context, p string) ([]string, error) {
		probes.Add(1)
		return []string{p + ":status"}, nil
	}
	m := newPickShell(context.Background(), "/cfg", pickRows("a", "b", "c"), nil, keys, func(string) error { return nil })
	msg := m.Init()().(tea.BatchMsg)[0]()
	if probes.Load() != 3 {
		t.Fatalf("probes %d", probes.Load())
	}
	m = pickStep(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = pickStep(t, m, msg)
	m, cmd := pickPress(t, m, kEnter)
	pickRun(t, m, cmd)
	if probes.Load() != 4 {
		t.Fatalf("open is not one fresh check: %d probes", probes.Load())
	}
}
