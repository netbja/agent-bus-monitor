package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

const testPlan = `{"plan":"v0.22","goal":"one batch","tasks":[
 {"id":"roles","title":"Specialist roles","owner":"coder","board":"roles-impl"},
 {"id":"release","title":"Tag and install","owner":"person","after":["roles"]}]}`

// planRepo commits docs/plans/v0.22.json in a fresh repository; it returns
// the repository and the commit.
func planRepo(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	os.MkdirAll(filepath.Join(dir, "docs/plans"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs/plans/v0.22.json"), []byte(body), 0o644)
	git("add", ".")
	git("commit", "-qm", "plan")
	return dir, git("rev-parse", "HEAD")
}

func runPlanT(t *testing.T, b *bus.Bus, repo string, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runPlan(context.Background(), b, repo, args, &out, &errw)
	return code, out.String(), errw.String()
}

func TestPlanCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project())) })
	t.Setenv("HERDR_ENV", "")
	repo, sha := planRepo(t, testPlan)

	if code, _, errw := runPlanT(t, b, repo, "show"); code == 0 || !strings.Contains(errw, "no plan") {
		t.Fatalf("show with no plan: %d %s", code, errw)
	}
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@"+sha); code == 0 || !strings.Contains(errw, "no such project") {
		t.Fatalf("load for an unknown project: %d %s", code, errw)
	}
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/missing.json@"+sha); code == 0 {
		t.Fatalf("a missing file was loaded: %s", errw)
	}
	if code, out, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@HEAD"); code != 0 || !strings.Contains(out, sha[:7]) {
		t.Fatalf("load: %d %s %s", code, out, errw)
	}
	if code, _, errw := runPlanT(t, b, repo, "link", "roles", "--pr", "102"); code != 0 {
		t.Fatalf("link: %s", errw)
	}
	if code, _, _ := runPlanT(t, b, repo, "link", "nope", "--pr", "1"); code == 0 {
		t.Fatal("a fact for an unknown task was accepted")
	}
	if code, _, errw := runPlanT(t, b, repo, "note", "roles", "rebased", "on", "main"); code != 0 {
		t.Fatalf("note: %s", errw)
	}

	b.BoardClaim(ctx, "roles-impl", "coder", "feat/roles")
	b.AppendVerdict(ctx, bus.Verdict{Subject: "roles-impl", Author: "coder", Reviewer: "foureyes", Decision: "approve"})
	code, out, errw := runPlanT(t, b, repo, "show")
	if code != 0 {
		t.Fatalf("show: %d %s", code, errw)
	}
	for _, want := range []string{"v0.22", "one batch", sha[:7], "waiting for you", "roles", "#102", "approved", "rebased on main", "release", "waiting on roles"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}

	if code, _, errw := runPlanT(t, b, repo, "done", "roles"); code != 0 {
		t.Fatalf("done: %s", errw)
	}
	code, out, _ = runPlanT(t, b, repo, "show", "--json")
	var js struct {
		Plan, Commit string
		Done, Total  int
		Tasks        []struct{ ID, State, Label string }
	}
	if code != 0 || json.Unmarshal([]byte(out), &js) != nil {
		t.Fatalf("show --json: %d %s", code, out)
	}
	if js.Plan != "v0.22" || js.Commit != sha || js.Done != 1 || js.Total != 2 || js.Tasks[0].State != "done" ||
		!strings.HasPrefix(js.Tasks[0].Label, "merged (recorded") || js.Tasks[1].State != "waiting" {
		t.Fatalf("json: %+v", js)
	}

	if code, _, _ := runPlanT(t, b, repo, "clear"); code != 0 {
		t.Fatal("clear")
	}
	if _, ok, _ := b.Plan(ctx); ok {
		t.Fatal("still loaded after clear")
	}
}

func TestPlanShowFailsOnAFailedRead(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.BoardKey(b.Project())) })
	t.Setenv("HERDR_ENV", "")
	repo, sha := planRepo(t, testPlan)
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatal(errw)
	}
	r.Set(ctx, bus.BoardKey(b.Project()), "not a hash", 0) // WRONGTYPE on every board read
	code, out, errw := runPlanT(t, b, repo, "show")
	if code == 0 || !strings.Contains(errw, "board") || !strings.Contains(out, "?") {
		t.Fatalf("a failed board read: %d\n%s\n%s", code, out, errw)
	}
}
