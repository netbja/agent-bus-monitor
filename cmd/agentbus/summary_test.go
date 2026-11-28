package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// summaryFixture writes a project with one of each: an ask, a blocked task,
// a merge on an approved head, a health warn and fail, a report asking for
// the person, agents in three states.
func summaryFixture(t *testing.T, b *bus.Bus, r *redis.Client) {
	t.Helper()
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { bus.DeleteProject(ctx, r, p) })
	for _, s := range []struct{ a, st string }{{"master", "idle"}, {"coder", "working"}, {"coder2", "working"}, {"foureyes", "idle"}} {
		if _, err := b.Status(ctx, s.a, s.st, "x", bus.AgentIdent{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := b.Ask(ctx, bus.AskDef{From: "coder", Title: "Which way?", Questions: []bus.AskQuestion{{
		ID: "q1", Header: "Way", Question: "Which?", Options: []bus.AskOption{{Label: "a"}, {Label: "b"}}}}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "rl-3e", "master", "coder2", "", "build it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "rl-3e", "coder2", "waits for the holder fix"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	for id, st := range map[string]health.Status{"cmd-delivery": health.Warn, "disk": health.Fail, "holder": health.OK} {
		js, _ := json.Marshal(health.Record{ID: id, Status: st, ObservedAt: now, Since: now, Detail: id + " detail"})
		r.HSet(ctx, bus.HealthKey(p), id, js)
	}
	items, _ := json.Marshal([]bus.MergeItem{{Number: 841, Title: "onboarding-answers", Branch: "b", Head: "h",
		Approvers: []string{"foureyes", "reviewer-codex"}, ApprovedAt: now, CIRed: []string{"test (go 1.26)"}}})
	r.HSet(ctx, bus.MergeQueueKey(p), "attempted_at", now, "items_at", now, "source", "gh", "items", string(items))
	if _, err := b.ReportNeeds(ctx, "master", "note", "pick one", nil, bus.NeedsAnswer); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Report(ctx, "coder", "note", "nothing for the person"); err != nil {
		t.Fatal(err)
	}
}

func summaryRun(t *testing.T, b *bus.Bus, o summaryOpts, width int, args ...string) (int, string, string) {
	t.Helper()
	d := summaryDeps{
		Projects: func(context.Context) ([]string, error) { return []string{b.Project()}, nil },
		Open:     func(string) (*bus.Bus, error) { return b, nil },
		Opts:     o, Now: time.Now, Width: width,
	}
	var out, errw bytes.Buffer
	code := runSummary(context.Background(), b.Project(), args, d, &out, &errw)
	return code, out.String(), errw.String()
}

// Each section against a fixture broker (status-summary §3.3, step 2).
func TestSummarySections(t *testing.T) {
	b, r := dialMain(t)
	summaryFixture(t, b, r)
	code, out, errw := summaryRun(t, b, summaryOpts{CILoop: true}, 200)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errw, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("%d lines, want the title, 6 sections and the sources:\n%s", len(lines), out)
	}
	for i, want := range []string{
		b.Project() + " · ",
		"For you   1 ask (coder) · 1 merge (#841) · 1 report (24 h)",
		"Merge     #841 onboarding-answers · approved by 2 · CI red: test (go 1.26)",
		"Blocked   1 · rl-3e (coder2): waits for the holder fix",
		"Plan      no plan loaded",
		"Team      2 working · 2 idle · 0 failed",
		"Health    1 fail: disk · 1 warn: cmd-delivery",
		"↳ asks, merge-queue, board, plan, agents, health · read ",
	} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], want)
		}
	}
}

// With ci.loop off, CI is "not read", never green; an empty project's
// sections are "none".
func TestSummaryNoneAndCINotRead(t *testing.T) {
	b, r := dialMain(t)
	summaryFixture(t, b, r)
	_, out, _ := summaryRun(t, b, summaryOpts{CILoop: false}, 200)
	if !strings.Contains(out, "approved by 2 · CI not read (ci.loop off)") || strings.Contains(out, "CI red") {
		t.Fatalf("ci.loop off:\n%s", out)
	}
	e, r2 := dialMain(t)
	t.Cleanup(func() { bus.DeleteProject(context.Background(), r2, e.Project()) })
	now := time.Now().UnixMilli()
	r2.HSet(context.Background(), bus.MergeQueueKey(e.Project()), "attempted_at", now, "items_at", now, "source", "gh")
	code, out, _ := summaryRun(t, e, summaryOpts{}, 200)
	for _, want := range []string{"For you   none", "Merge     none", "Blocked   none", "Health    none", "Team      0 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty project lacks %q:\n%s", want, out)
		}
	}
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// A queue the dispatch service has not computed is said, not "?": no read
// failed, the exit stays 0; For you carries attention's unknown row.
func TestSummaryMergeNotComputed(t *testing.T) {
	b, r := dialMain(t)
	t.Cleanup(func() { bus.DeleteProject(context.Background(), r, b.Project()) })
	code, out, _ := summaryRun(t, b, summaryOpts{}, 200)
	if code != 0 || !strings.Contains(out, "Merge     not computed: dispatch down") || !strings.Contains(out, "For you   merge queue unknown: dispatch down") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// A section that cannot be read is "?" with its reason, the others still
// print, and the exit is 1.
func TestSummaryFailedReadIsQuestionMark(t *testing.T) {
	b, r := dialMain(t)
	summaryFixture(t, b, r)
	r.HSet(context.Background(), bus.MergeQueueKey(b.Project()), "items", "{corrupt")
	code, out, _ := summaryRun(t, b, summaryOpts{CILoop: true}, 200)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "Merge     ? merge-queue: merge queue: a corrupt items field") ||
		!strings.Contains(out, "Blocked   1 · rl-3e") {
		t.Fatalf("a failed merge read:\n%s", out)
	}
}

// Lines are clipped to the width with "…": 80 given, 100 when not a terminal.
func TestSummaryClip(t *testing.T) {
	b, r := dialMain(t)
	summaryFixture(t, b, r)
	if _, err := b.Status(context.Background(), "coder", "working", strings.Repeat("x", 10), bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	r.HSet(context.Background(), bus.MergeQueueKey(b.Project()), "items", mustJSON(t, []bus.MergeItem{{Number: 841,
		Title: strings.Repeat("long title ", 20), Branch: "b", Head: "h", Approvers: []string{"a"}, ApprovedAt: 1}}))
	for _, c := range []struct{ width, want int }{{80, 80}, {0, 100}} {
		_, out, _ := summaryRun(t, b, summaryOpts{CILoop: true}, c.width)
		clipped := false
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if n := len([]rune(l)); n > c.want {
				t.Errorf("width %d: a line of %d runes: %q", c.width, n, l)
			}
			if strings.HasPrefix(l, "Merge") {
				clipped = len([]rune(l)) == c.want && strings.HasSuffix(l, "…")
			}
		}
		if !clipped {
			t.Errorf("width %d: the merge line is not clipped to %d with …:\n%s", c.width, c.want, out)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --json is the same sections as typed fields, with "v": 1.
func TestSummaryJSON(t *testing.T) {
	b, r := dialMain(t)
	summaryFixture(t, b, r)
	code, out, _ := summaryRun(t, b, summaryOpts{CILoop: true}, 0, "--json")
	var s Summary
	if err := json.Unmarshal([]byte(out), &s); err != nil || code != 0 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	if s.V != 1 || s.Project != b.Project() || s.ForYou.Counts["asks"] != 1 || s.ForYou.Counts["reports"] != 1 ||
		s.ForYou.Named["merges"] != "#841" || len(s.Merge.Items) != 1 || s.Merge.Items[0].CIRed[0] != "test (go 1.26)" ||
		s.Blocked.Count != 1 || s.Blocked.Tasks[0].Reason != "waits for the holder fix" || s.Team.Counts["working"] != 2 ||
		len(s.Health.Fail) != 1 || s.Plan.Loaded {
		t.Fatalf("json %+v", s)
	}
	var raw map[string]any
	json.Unmarshal([]byte(out), &raw)
	if raw["v"] != float64(1) {
		t.Fatalf("v %v", raw["v"])
	}
}

// --all: one block per project, those with something waiting on the person
// first, then the list's order; --json with it too.
func TestSummaryAll(t *testing.T) {
	quiet, rq := dialMain(t)
	t.Cleanup(func() { bus.DeleteProject(context.Background(), rq, quiet.Project()) })
	if _, err := quiet.Status(context.Background(), "coder", "idle", "x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	rq.HSet(context.Background(), bus.MergeQueueKey(quiet.Project()), "attempted_at", now, "items_at", now, "source", "gh")
	busy, rb := dialMain(t)
	summaryFixture(t, busy, rb)
	buses := map[string]*bus.Bus{quiet.Project(): quiet, busy.Project(): busy}
	d := summaryDeps{
		Projects: func(context.Context) ([]string, error) { return []string{quiet.Project(), busy.Project()}, nil },
		Open:     func(p string) (*bus.Bus, error) { return buses[p], nil },
		Now:      time.Now, Width: 200,
	}
	var out, errw bytes.Buffer
	runSummary(context.Background(), "", []string{"--all"}, d, &out, &errw)
	blocks := strings.Split(strings.TrimRight(out.String(), "\n"), "\n\n")
	if len(blocks) != 2 || !strings.HasPrefix(blocks[0], busy.Project()+" · ") || !strings.HasPrefix(blocks[1], quiet.Project()+" · ") {
		t.Fatalf("--all order:\n%s\n%s", out.String(), errw.String())
	}
	out.Reset()
	runSummary(context.Background(), "", []string{"--all", "--json"}, d, &out, &errw)
	var all struct {
		V        int       `json:"v"`
		Projects []Summary `json:"projects"`
	}
	if err := json.Unmarshal(out.Bytes(), &all); err != nil || all.V != 1 || len(all.Projects) != 2 || all.Projects[0].Project != busy.Project() {
		t.Fatalf("--all --json %v: %+v", err, all)
	}
}

// Usage: an unknown flag, no project without --all.
func TestSummaryUsage(t *testing.T) {
	d := summaryDeps{Now: time.Now, Open: func(string) (*bus.Bus, error) { return nil, errors.New("no") }}
	var out, errw bytes.Buffer
	if code := runSummary(context.Background(), "demo", []string{"--nope"}, d, &out, &errw); code != 2 {
		t.Fatalf("--nope: exit %d", code)
	}
	if code := runSummary(context.Background(), "", nil, d, &out, &errw); code != 2 || !strings.Contains(errw.String(), "--all") {
		t.Fatalf("no project: exit %d %q", code, errw.String())
	}
}

// status with no positional, or one, is a usage error (exit 2) pointing to
// summary, before any broker (a broker on a refused port: never reached).
func TestStatusUsagePointsToSummary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, args := range [][]string{{"-p", "demo", "status"}, {"-p", "demo", "status", "coder"}} {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "REDIS_HOST=127.0.0.1", "REDIS_PORT=1"}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 || strings.TrimSpace(stderr.String()) != statusUsage {
			t.Errorf("%q: %v, stderr %q", args, err, stderr.String())
		}
	}
}

// Reports older than 24 hours are not counted; three blocked tasks show two
// and "+1 more"; the team's anomalies come from their health records and the
// quota holds, failed counted.
func TestSummaryEdges(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	p := b.Project()
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	r.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(p, "report"), ID: strconv.FormatInt(old, 10) + "-0",
		Values: map[string]any{"agent": "master", "kind": "note", "message": "old", "needs": "answer"}})
	summaryFixture(t, b, r)
	for _, task := range []string{"t-b", "t-c"} {
		b.Request(ctx, task, "master", "coder", "", "x", 0)
		b.BlockRequest(ctx, task, "coder", "why "+task)
	}
	if _, err := b.Status(ctx, "coder", "working", "x", bus.AgentIdent{Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	r.HSet(ctx, bus.QuotaKey(p), "hold:anthropic", time.Now().Add(time.Hour).Unix())
	now := time.Now().UnixMilli()
	for id, st := range map[string]health.Status{"agent-launch": health.Warn, "idle-implementer": health.Warn} {
		r.HSet(ctx, bus.HealthKey(p), id, mustJSON(t, health.Record{ID: id, Status: st, ObservedAt: now, Since: now, Detail: id + " says"}))
	}
	_, out, _ := summaryRun(t, b, summaryOpts{CILoop: true}, 400)
	for _, want := range []string{
		"1 report (24 h)",
		"Blocked   3 · rl-3e (coder2): waits for the holder fix · t-b (coder): why t-b · +1 more",
		"1 failed · failed: agent-launch says · stalled: idle-implementer says · quota-paused coder: anthropic at its limit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// The plan line: done/total, the task in progress and the first task
// waiting for the person, the plan's commit on the sources line.
func TestSummaryPlan(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { bus.DeleteProject(ctx, r, b.Project()) })
	t.Setenv("HERDR_ENV", "")
	repo, sha := planRepo(t, `{"plan":"v0.22","goal":"g","tasks":[
 {"id":"build","title":"Build","owner":"coder","board":"build-impl"},
 {"id":"pick","title":"Pick one","owner":"person"}]}`)
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	if code, _, errw := runPlanT(t, b, repo, "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatalf("load: %s", errw)
	}
	b.Request(ctx, "build-impl", "master", "coder", "", "x", 0)
	if err := b.AcceptRequest(ctx, "build-impl", "coder"); err != nil {
		t.Fatal(err)
	}
	_, out, _ := summaryRun(t, b, summaryOpts{}, 200)
	if !strings.Contains(out, "Plan      v0.22 0/2 · in progress: build (coder) · waits for you: pick") ||
		!strings.Contains(out, "plan v0.22@"+sha[:7]) {
		t.Fatalf("plan:\n%s", out)
	}
}

// An agent with no status yet (bound, TS 0) is counted in no state; a kind
// with more than one item names none.
func TestSummaryUncountedAndUnnamed(t *testing.T) {
	b, r := dialMain(t)
	summaryFixture(t, b, r)
	ctx := context.Background()
	r.HSet(ctx, bus.AgentsKey(b.Project()), "fresh", `{"state":"idle","ts":0}`) // a test fixture, not a writer
	if _, _, err := b.Ask(ctx, bus.AskDef{From: "coder2", Title: "Another?", Questions: []bus.AskQuestion{{
		ID: "q1", Header: "Way", Question: "Which?", Options: []bus.AskOption{{Label: "a"}}}}}, ""); err != nil {
		t.Fatal(err)
	}
	_, out, _ := summaryRun(t, b, summaryOpts{CILoop: true}, 0, "--json")
	var s Summary
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}
	if s.Team.Counts["idle"] != 2 || s.ForYou.Counts["asks"] != 2 || s.ForYou.Named["asks"] != "" || s.ForYou.Named["merges"] != "#841" {
		t.Fatalf("team %v, for you %v %v", s.Team.Counts, s.ForYou.Counts, s.ForYou.Named)
	}
}
