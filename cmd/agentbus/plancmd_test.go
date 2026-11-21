package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

const testPlan = `{"plan":"v0.22","goal":"one batch","tasks":[
 {"id":"roles","title":"Specialist roles","owner":"coder","board":"roles-impl"},
 {"id":"release","title":"Tag and install","owner":"person","after":["roles"],"priority":"low"}]}`

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
	code := runPlan(context.Background(), b, repo, func(string) string { return "" }, args, &out, &errw) // the person
	return code, out.String(), errw.String()
}

// ACL answer b (the person's yes): the plan's writes are master's. An agent's
// environment is refused before any write; master and the person may; show
// stays open to all (defence in depth, as run reset: the ACL users enforce it).
func TestPlanWritesAreMasters(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() {
		r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.PlanIgnoreKey(b.Project()))
	})
	t.Setenv("HERDR_ENV", "")
	repo, sha := planRepo(t, testPlan)
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{}) // load wants the project on the broker
	as := func(agent string, args ...string) (int, string) {
		var out, errw bytes.Buffer
		code := runPlan(ctx, b, repo, func(k string) string {
			if k == "AGENT_BUS_AGENT" {
				return agent
			}
			return ""
		}, args, &out, &errw)
		return code, errw.String()
	}
	for _, args := range [][]string{{"load", "docs/plans/v0.22.json@" + sha}, {"link", "roles", "--pr", "4"}, {"done", "roles"},
		{"note", "roles", "x"}, {"ignore", "12"}, {"clear"}} {
		if code, errw := as("coder", args...); code == 0 || !strings.Contains(errw, "master's") {
			t.Errorf("coder %v: %d %q", args, code, errw)
		}
	}
	if _, ok, _ := b.Plan(ctx); ok {
		t.Fatal("an agent's refused load wrote the plan")
	}
	if code, errw := as("master", "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatalf("master load: %d %s", code, errw)
	}
	if code, errw := as("coder", "show"); code != 0 {
		t.Fatalf("coder show: %d %s", code, errw)
	}
	if code, errw := as("", "note", "roles", "the person's"); code != 0 {
		t.Fatalf("the person's note: %d %s", code, errw)
	}
}

func TestPlanCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() {
		r.Del(ctx, bus.PlanKey(b.Project()), bus.PlanFactsKey(b.Project()), bus.PlanIgnoreKey(b.Project()))
	})
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
	// plan ignore <pr> (plan-drift): a merge no task links, said on purpose.
	if code, out, errw := runPlanT(t, b, repo, "ignore", "#458"); code != 0 || !strings.Contains(out, "#458 ignored") {
		t.Fatalf("ignore: %d %s %s", code, out, errw)
	}
	if got, _ := b.PlanIgnored(ctx); !got[458] {
		t.Fatalf("ignored %v", got)
	}
	for _, bad := range [][]string{{"ignore"}, {"ignore", "x"}, {"ignore", "0"}, {"ignore", "1", "2"}} {
		if code, _, _ := runPlanT(t, b, repo, bad...); code == 0 {
			t.Errorf("%v accepted", bad)
		}
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

	// forge-delivery: --ref replaces the PR link, and show names it short.
	ref := "coder/roles@" + strings.Repeat("e5", 20)
	if code, _, errw := runPlanT(t, b, repo, "link", "roles", "--ref", ref); code != 0 {
		t.Fatalf("link --ref: %s", errw)
	}
	if code, out, _ := runPlanT(t, b, repo, "show"); code != 0 || !strings.Contains(out, "ref coder/roles@e5e5e5e") || strings.Contains(out, "#102") {
		t.Fatalf("show after --ref:\n%s", out)
	}
	if code, _, _ := runPlanT(t, b, repo, "link", "roles", "--ref", "coder/roles@abc"); code == 0 {
		t.Fatal("a short sha was accepted as a ref")
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

// Another deliverer (docs) does not write the plan's facts: it asks master,
// with the command to run (ACL answer b).
func TestPlanLinkByMaster(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PlanFactsKey(b.Project()), bus.StreamKey(b.Project(), "cmd")) })
	pr, ref := planLinkByMaster(b, "docs")
	if err := pr(ctx, "roles", 42); err != nil {
		t.Fatal(err)
	}
	if err := ref(ctx, "release", "docs/x@abc123"); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.Exists(ctx, bus.PlanFactsKey(b.Project())).Result(); n != 0 {
		t.Fatal("the plan's facts were written")
	}
	msgs, err := r.XRange(ctx, bus.StreamKey(b.Project(), "cmd"), "-", "+").Result()
	if err != nil || len(msgs) != 2 {
		t.Fatalf("%v %v", msgs, err)
	}
	for i, want := range []string{"agentbus plan link roles --pr 42", "agentbus plan link release --ref docs/x@abc123"} {
		if msgs[i].Values["target"] != "master" || msgs[i].Values["from"] != "docs" || !strings.Contains(msgs[i].Values["command"].(string), want) {
			t.Errorf("%d: %v, want %q", i, msgs[i].Values, want)
		}
	}
}

// deliver's deps: master links itself; another deliverer asks master.
func TestDeliverLinksThroughMaster(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PlanFactsKey(b.Project()), bus.StreamKey(b.Project(), "cmd")) })
	if err := realDeliverDeps(b, "docs", t.TempDir()).LinkPR(ctx, "roles", 7); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.XLen(ctx, bus.StreamKey(b.Project(), "cmd")).Result(); n != 1 {
		t.Fatalf("docs' link: %d directives", n)
	}
	if n, _ := r.Exists(ctx, bus.PlanFactsKey(b.Project())).Result(); n != 0 {
		t.Fatal("docs wrote the plan's facts")
	}
}

// The guard's production wiring (foureyes): the built binary, in an agent's
// environment, refuses a plan write before writing anything.
func TestPlanWriteRefusedInMain(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.PlanFactsKey(b.Project())) })
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	dir := t.TempDir()
	cmd := exec.Command(bin, "-p", b.Project(), "plan", "note", "roles", "x")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "AGENT_BUS_AGENT=coder",
		"REDIS_URL=" + os.Getenv("AGENTBUS_TEST_REDIS_URL")}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(stderr.String(), "the plan's writes are master's") {
		t.Fatalf("coder's plan note: %v, stderr %q", err, stderr.String())
	}
	if n, _ := r.Exists(ctx, bus.PlanFactsKey(b.Project())).Result(); n != 0 {
		t.Fatal("the refused note was written")
	}
}
