package main

import (
	"bytes"
	"strings"
	"testing"
)

// P1-16 (demo field test): up started the missions service on a folder that
// is not a Git repository, and it sat waiting 'no Git repository in
// /data/projects/test_agbm: run git init there' with nothing on the
// person's screen. up now refuses it up front, says how to go on, and
// starts nothing; it never runs git init in the person's folder.
func TestUpRefusesMissionsOnAFolderWithoutGit(t *testing.T) {
	work, _, h, _, deps := upFixture(t)
	u := withServices(t, &deps)
	var asked []string
	deps.GitRepo = func(dir string) bool { asked = append(asked, dir); return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code == 0 {
		t.Fatalf("up on a folder without Git = 0: %s", out.String())
	}
	msg := errw.String()
	for _, want := range []string{"no Git repository in " + work, "git init " + work, "agentbus up demo --no-missions", "nothing started"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("stderr lacks %q: %q", want, msg)
		}
	}
	if len(asked) != 1 || asked[0] != work {
		t.Fatalf("git asked of %q, want the project folder", asked)
	}
	if len(u.marks) != 0 || len(h.started()) != 0 {
		t.Fatalf("something started: marks %+v, terminals %v", u.marks, h.started())
	}
}

// Without missions there is nothing to wait for git: up goes on.
func TestUpWithoutMissionsNeedsNoGit(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	withServices(t, &deps)
	deps.GitRepo = func(string) bool { t.Error("git asked without missions"); return false }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--no-missions"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up --no-missions = %d: %s", code, errw.String())
	}
}

// A Git repository starts as before.
func TestUpStartsMissionsInAGitRepository(t *testing.T) {
	work, _, _, _, deps := upFixture(t)
	u := withServices(t, &deps)
	deps.GitRepo = func(string) bool { return true }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 || len(u.marks) != 1 {
		t.Fatalf("up in a repository = %d (marks %+v): %s", code, u.marks, errw.String())
	}
}
