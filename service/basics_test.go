package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamesRoundTrip(t *testing.T) {
	for _, p := range []string{"demo", "my-app", "a-b-c"} {
		for _, k := range Kinds {
			if gp, gk, ok := parseUnitName(UnitName(p, k)); !ok || gp != p || gk != k {
				t.Fatalf("unit %s -> %q %q %v", UnitName(p, k), gp, gk, ok)
			}
			if gp, gk, ok := parseLabel(Label(p, k)); !ok || gp != p || gk != k {
				t.Fatalf("label %s -> %q %q %v", Label(p, k), gp, gk, ok)
			}
		}
	}
	for _, bad := range []string{"agentbus-demo.service", "other-demo-dispatch.service", "agentbus-demo-web.service", "agentbus--dispatch.service"} {
		if _, _, ok := parseUnitName(bad); ok {
			t.Fatalf("%s parsed", bad)
		}
	}
}

func TestPathsHonourXDGAndIgnoreRelative(t *testing.T) {
	env := map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "/c", "XDG_STATE_HOME": "rel"}
	p, err := PathsFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if p.EnvFile("demo") != "/c/agentbus/services/demo.env" || p.LogFile("demo", Dispatch) != "/h/.local/state/agentbus/demo/dispatch.log" {
		t.Fatalf("paths = %+v / %s / %s", p, p.EnvFile("demo"), p.LogFile("demo", Dispatch))
	}
}

func TestCollectEnvKeepsConnectionDropsPaneIdentity(t *testing.T) {
	env := CollectEnv([]string{"PATH=/bin", "REDIS_PASSWORD=s3cret", "HERDR_SESSION=s", "HERDR_PANE_ID=w1:p1",
		"HERDR_TAB_ID=t", "AGENT_BUS_PROJECT=x", "AGENT_BUS_AGENT=me", "AGENT_BUS_HUMAN=bernard", "ANTHROPIC_API_KEY=k", "HOME=/h"})
	for _, k := range []string{"PATH", "REDIS_PASSWORD", "HERDR_SESSION", "AGENT_BUS_HUMAN"} {
		if _, ok := env[k]; !ok {
			t.Fatalf("%s dropped: %v", k, env)
		}
	}
	for _, k := range []string{"HERDR_PANE_ID", "HERDR_TAB_ID", "AGENT_BUS_PROJECT", "AGENT_BUS_AGENT", "ANTHROPIC_API_KEY", "HOME"} {
		if _, ok := env[k]; ok {
			t.Fatalf("%s kept: %v", k, env)
		}
	}
}

func TestEnvFileIsPrivateAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "demo.env")
	env := map[string]string{"REDIS_PASSWORD": `p"a$s\w%rd`, "PATH": "/a b:/c"}
	if changed, err := WriteEnvFile(path, env); err != nil || !changed {
		t.Fatalf("write = %v, %v", changed, err)
	}
	if changed, _ := WriteEnvFile(path, env); changed {
		t.Fatal("the same content counted as a change")
	}
	st, _ := os.Stat(path)
	dst, _ := os.Stat(filepath.Dir(path))
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
		t.Fatalf("modes = %v %v", st.Mode().Perm(), dst.Mode().Perm())
	}
	got, err := LoadEnvFile(path)
	if err != nil || got["REDIS_PASSWORD"] != env["REDIS_PASSWORD"] || got["PATH"] != env["PATH"] {
		t.Fatalf("load = %v, %v", got, err)
	}
	os.Chmod(path, 0o644)
	if _, err := LoadEnvFile(path); !errors.Is(err, ErrEnvPermissions) {
		t.Fatalf("a world-readable file was accepted: %v", err)
	}
}

func TestRotatingLogIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s", "dispatch.log")
	r, err := OpenLog(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		r.Write([]byte(strings.Repeat("x", 19) + "\n"))
	}
	r.Write([]byte("last line\n"))
	r.Close()
	matches, _ := filepath.Glob(path + "*")
	if len(matches) != 3 {
		t.Fatalf("files = %v, want the log and 2 rotated", matches)
	}
	for _, m := range matches {
		if st, _ := os.Stat(m); st.Size() > 100 {
			t.Fatalf("%s is %d bytes", m, st.Size())
		}
	}
	lines, err := TailLog(path, 3)
	if err != nil || len(lines) != 3 || lines[2] != "last line" {
		t.Fatalf("tail = %q, %v", lines, err)
	}
	if _, err := TailLog(filepath.Join(t.TempDir(), "none.log"), 3); err == nil {
		t.Fatal("a missing log tailed")
	}
}

func TestRotateIfLargerBoundsTheCrashLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missions.crash.log")
	os.WriteFile(path, []byte(strings.Repeat("x", 50)), 0o600)
	if err := RotateIfLarger(path, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("a small file was rotated")
	}
	os.WriteFile(path, []byte(strings.Repeat("x", 150)), 0o600)
	if err := RotateIfLarger(path, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a large file was kept in place")
	}
	if st, err := os.Stat(path + ".1"); err != nil || st.Size() != 150 {
		t.Fatalf("rotated = %v %v", st, err)
	}
	if err := RotateIfLarger(filepath.Join(t.TempDir(), "none"), 100); err != nil {
		t.Fatalf("a missing file: %v", err)
	}
}

// A start counts until it ends cleanly: a process that panics or is killed
// never calls End, so a crash loop spends the budget like any failure.
func TestBudgetCountsAStartUntilItEndsCleanly(t *testing.T) {
	b := Budget{Path: filepath.Join(t.TempDir(), "d", "dispatch.restarts")}
	now := time.Now()
	for i := 0; i < BudgetStarts-1; i++ {
		b.End(b.Begin(now.Add(time.Duration(i) * time.Millisecond)))
	}
	if b.Spent(now.Add(time.Second)) {
		t.Fatal("clean runs spent the budget")
	}
	for i := 0; i < BudgetStarts; i++ {
		b.Begin(now.Add(time.Duration(10+i) * time.Millisecond)) // crashed: no End
	}
	if !b.Spent(now.Add(time.Second)) {
		t.Fatal("five crashed starts did not spend the budget")
	}
}

func TestBudgetGivesUpAfterFiveFailuresInTheWindow(t *testing.T) {
	b := Budget{Path: filepath.Join(t.TempDir(), "d", "dispatch.restarts")}
	now := time.Now()
	for i := 0; i < BudgetStarts-1; i++ {
		b.Failed(now.Add(time.Duration(i) * time.Second))
	}
	if b.Spent(now.Add(10 * time.Second)) {
		t.Fatal("spent after 4 failures")
	}
	b.Failed(now.Add(5 * time.Second))
	if !b.Spent(now.Add(10 * time.Second)) {
		t.Fatal("not spent after 5 failures")
	}
	if b.Spent(now.Add(BudgetWindow + time.Minute)) {
		t.Fatal("old failures still count")
	}
	b.Reset()
	if b.Spent(now.Add(10 * time.Second)) {
		t.Fatal("reset kept the failures")
	}
}
