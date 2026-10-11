package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/mission"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// missionRepo: a repository with a loadable change "chg".
func missionRepo(t *testing.T) (string, string) {
	t.Helper()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, "docs/changes/chg"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs/changes/chg/plan.md"), []byte("# plan\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "docs/changes/chg/missions.json"), []byte(`{"change": "chg", "reviewer": "codex", "missions": [
 {"id": "t1", "title": "First", "agent": "kimi", "allowed_paths": ["src/t1/**"], "test": "true", "brief": "plan.md#t1"}]}`), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	return dir, gitIn(t, dir, "rev-parse", "HEAD")
}

func mission1(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	b := dialDispatchMain(t)
	var out, errb bytes.Buffer
	code := runMission(context.Background(), b, "master", args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestMissionCLIDown(t *testing.T) {
	code, _, errOut := mission1(t, "retry", "x/y")
	if code != 1 || !strings.Contains(errOut, "missions down") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestMissionCLIUsage(t *testing.T) {
	b := dialDispatchMain(t)
	for _, args := range [][]string{{}, {"bogus"}, {"load", "no-commit"}, {"retry", "noslash"}, {"abort", "chg"},
		{"abort", "chg", "--reason", "  "}, {"show", "x"}, {"load", "a@b", "--wait", "soon"}} {
		var out, errb bytes.Buffer
		if code := runMission(context.Background(), b, "master", args, &out, &errb); code != 2 {
			t.Errorf("%q: exit %d", args, code)
		}
	}
	var out, errb bytes.Buffer
	if code := runMissions(context.Background(), b, []string{"--bogus"}, ".", &out, &errb); code != 2 {
		t.Errorf("missions --bogus: exit %d", code)
	}
	if code := runMissions(context.Background(), b, []string{"--status"}, ".", &out, &errb); code != 1 || !strings.Contains(out.String(), "missions down") {
		t.Errorf("missions --status: exit %d, %q", code, out.String())
	}
}

func TestMissionCLIRoundTrip(t *testing.T) {
	b := dialDispatchMain(t)
	repo, commit := missionRepo(t)
	cfg := mission.DefaultRunConfig()
	cfg.Repo, cfg.StateHome, cfg.Tick, cfg.Renew, cfg.Lease = repo, t.TempDir(), 100*time.Millisecond, 200*time.Millisecond, 2*time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mission.Run(ctx, b, cfg) }()
	defer func() { cancel(); <-done }()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		if ok, _ := b.ConductorAlive(context.Background()); ok {
			break
		}
	}
	var out, errb bytes.Buffer
	if code := runMission(context.Background(), b, "master", []string{"load", "docs/changes/chg/missions.json@" + commit}, &out, &errb); code != 0 {
		t.Fatalf("load: exit %d, %q %q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "loaded chg rev 1: 1 missions") {
		t.Fatalf("load output %q", out.String())
	}
	out.Reset()
	if code := runMission(context.Background(), b, "master", []string{"status"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "chg rev 1 (active)") || !strings.Contains(out.String(), "t1") {
		t.Fatalf("status: exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := runMission(context.Background(), b, "master", []string{"show", "chg/t1"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"id": "t1"`) {
		t.Fatalf("show: exit %d\n%s", code, out.String())
	}
	out.Reset()
	if code := runMission(context.Background(), b, "master", []string{"retry", "chg/t1"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "not blocked") {
		t.Fatalf("retry of a running mission: exit %d %q", code, errb.String())
	}
	out.Reset()
	if code := runMissions(context.Background(), b, []string{"--status"}, ".", &out, &errb); code != 0 || !strings.Contains(out.String(), "missions up") {
		t.Fatalf("missions --status: %d %q", code, out.String())
	}
}

func TestMissionCLILoadForceNeedsReason(t *testing.T) {
	b := dialDispatchMain(t)
	for _, args := range [][]string{{"load", "a@b", "--force"}, {"load", "a@b", "--force", "  "}, {"skip", "c/t1"}, {"reassign", "c/t1"}, {"reassign", "c/t1", "Bad Name"}} {
		var out, errb bytes.Buffer
		if code := runMission(context.Background(), b, "master", args, &out, &errb); code != 2 {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}

func TestMissionCLISkipReassignQueue(t *testing.T) {
	code, _, errOut := mission1(t, "skip", "chg/t1", "--reason", "done by hand")
	if code != 1 || !strings.Contains(errOut, "missions down") {
		t.Fatalf("skip: exit %d %q", code, errOut)
	}
	code, _, errOut = mission1(t, "reassign", "chg/t1", "grok")
	if code != 1 || !strings.Contains(errOut, "missions down") {
		t.Fatalf("reassign: exit %d %q", code, errOut)
	}
}

func TestMissionCLIRecover(t *testing.T) {
	b := dialDispatchMain(t)
	var out, errb bytes.Buffer
	if code := runMission(context.Background(), b, "master", []string{"load", "--recover", "a@b", "--force", "x"}, &out, &errb); code != 2 {
		t.Fatalf("--recover with --force: exit %d", code)
	}
	code, _, errOut := mission1(t, "load", "--recover", "docs/changes/chg/missions.json@HEAD")
	if code != 1 || !strings.Contains(errOut, "missions down") {
		t.Fatalf("--recover reached usage instead of the queue: exit %d %q", code, errOut)
	}
}

func TestChangeHeaderShowsCompletion(t *testing.T) {
	rec := mission.ChangeRecord{Change: "calc", Rev: 2, State: "active", Branch: "change/calc"}
	if got := changeHeader(rec); got != "calc rev 2 (active) change/calc" {
		t.Fatalf("header = %q", got)
	}
	rec.CompletedRev, rec.CompletedHead = 2, "410e62a0123456789"
	if got := changeHeader(rec); got != "calc rev 2 (active, complete at 410e62a01234) change/calc" {
		t.Fatalf("header = %q", got)
	}
	rec.Rev = 3 // reloaded since: rev 3 is not complete
	if got := changeHeader(rec); got != "calc rev 3 (active) change/calc" {
		t.Fatalf("header = %q", got)
	}
}
