package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// P1-16, the person's answer (2026-11-16: "the project must work without
// necessarily github, bitbucket, gitlab or local"): up on a folder that is not
// a Git repository starts the project without the conductor, the missions
// service needing Git, and says once how to enable it. No refusal, and no
// missions service left waiting for git init: one running from an earlier up
// is stopped. busmon's picker runs this same plain up.
func TestUpStartsWithoutMissionsOnAFolderWithoutGit(t *testing.T) {
	work, _, h, _, deps := upFixture(t)
	u := withServices(t, &deps)
	u.m.specs["demo/"+service.Missions] = service.Spec{Project: "demo", Kind: service.Missions} // an earlier up's unit,
	u.m.active["demo/"+service.Missions] = true                                                   // waiting for git init
	var asked []string
	deps.GitRepo = func(dir string) bool { asked = append(asked, dir); return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up on a folder without Git = %d: %s", code, errw.String())
	}
	if len(asked) != 1 || asked[0] != work {
		t.Fatalf("git asked of %q, want the project folder", asked)
	}
	if len(u.m.calls) == 0 || slices.Contains(u.m.calls, "start demo "+service.Missions) || slices.Contains(u.m.calls, "install demo "+service.Missions) {
		t.Fatalf("missions installed or started: %q", u.m.calls)
	}
	if !slices.Contains(u.m.calls, "start demo "+service.Dispatch) {
		t.Fatalf("dispatch not started: %q", u.m.calls)
	}
	if !slices.Contains(u.m.calls, "stop demo "+service.Missions) || u.m.active["demo/"+service.Missions] {
		t.Fatalf("the waiting missions service was left running: %q", u.m.calls)
	}
	if len(u.marks) != 1 || strings.Join(u.marks[0].Kinds, ",") != service.Dispatch {
		t.Fatalf("marks %+v: missions must not be expected", u.marks)
	}
	if len(h.started()) == 0 {
		t.Fatal("no agent started")
	}
	msg := out.String()
	if n := strings.Count(msg, "git init"); n != 1 {
		t.Fatalf("the way to missions said %d times: %q", n, msg)
	}
	for _, want := range []string{"no Git repository in " + work, "git init " + work, "agentbus up demo"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("out lacks %q: %q", want, msg)
		}
	}
	if strings.Contains(errw.String(), "nothing started") {
		t.Fatalf("refused: %q", errw.String())
	}
}

// Without missions there is nothing to ask of git: up goes on.
func TestUpWithoutMissionsNeedsNoGit(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	withServices(t, &deps)
	deps.GitRepo = func(string) bool { t.Error("git asked without missions"); return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--no-missions"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up --no-missions = %d: %s", code, errw.String())
	}
}

// A Git repository starts as before: dispatch and missions, no word of git.
func TestUpStartsMissionsInAGitRepository(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.GitRepo = func(string) bool { return true }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || len(u.marks) != 1 ||
		strings.Join(u.marks[0].Kinds, ",") != service.Dispatch+","+service.Missions {
		t.Fatalf("up in a repository = %d (marks %+v): %s", code, u.marks, errw.String())
	}
	if !slices.Contains(u.m.calls, "start demo "+service.Missions) || strings.Contains(out.String(), "git init") {
		t.Fatalf("calls %q, out %q", u.m.calls, out.String())
	}
}
