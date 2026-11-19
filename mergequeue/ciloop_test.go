package mergequeue

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// ci-loop §3-§5 and §7's tests, on the disposable broker: RunCI decides,
// the fenced scripts write.

type ciWorld struct {
	t      *testing.T
	r      *redis.Client
	b      *bus.Bus
	vs     []bus.Verdict
	base   map[string]Base
	baseEr map[string]error
	reads  map[string]int
	cap    int
}

func newCIWorld(t *testing.T) *ciWorld {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	name := "ci" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, name) })
	if ok, err := b.AcquireDispatch(context.Background(), "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	return &ciWorld{t: t, r: r, b: b, base: map[string]Base{}, baseEr: map[string]error{}, reads: map[string]int{}, cap: 3}
}

func (w *ciWorld) cfg() CIConfig {
	return CIConfig{Project: w.b.Project(), Master: "master", Cap: w.cap,
		ReadBase: func(_ context.Context, branch string) (Base, error) {
			w.reads[branch]++
			if err := w.baseEr[branch]; err != nil {
				return Base{}, err
			}
			return w.base[branch], nil
		}}
}

func (w *ciWorld) pass(prs ...PR) string {
	w.t.Helper()
	reason, err := RunCI(context.Background(), w.b, "tok", w.cfg(), prs, w.vs)
	if err != nil {
		w.t.Fatal(err)
	}
	return reason
}

type sent struct{ target, text, wake string }

func (w *ciWorld) cmds() []sent {
	w.t.Helper()
	xs, err := w.r.XRange(context.Background(), bus.StreamKey(w.b.Project(), "cmd"), "-", "+").Result()
	if err != nil {
		w.t.Fatal(err)
	}
	var out []sent
	for _, x := range xs {
		out = append(out, sent{target: x.Values["target"].(string), text: x.Values["command"].(string), wake: str(x.Values["wake"])})
	}
	return out
}

func str(v interface{}) string { s, _ := v.(string); return s }

func (w *ciWorld) notices() int {
	n, _ := w.r.XLen(context.Background(), bus.StreamKey(w.b.Project(), "notify")).Result()
	return int(n)
}

func (w *ciWorld) count(pr int) int {
	rec, err := w.b.CIRecord(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	return rec[pr].Count
}

func (w *ciWorld) state(agent, state string) {
	w.t.Helper()
	if _, err := w.b.Status(context.Background(), agent, state, "x", bus.AgentIdent{}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *ciWorld) authored(head, author string) {
	w.vs = append(w.vs, bus.Verdict{Subject: "s", Author: author, Reviewer: "foureyes", Decision: "approve", Head: head},
		bus.Verdict{Subject: "s", Author: author, Reviewer: "reviewer-codex", Decision: "approve", Head: head})
}

func failed(name string) Check {
	return Check{Name: name, URL: "https://ci/" + name, Actions: true, Failed: true}
}
func passed(name string) Check  { return Check{Name: name} }
func running(name string) Check { return Check{Name: name, Running: true} }

func redPR(n int, head string, checks ...Check) PR {
	return PR{Number: n, Head: head, Branch: "fix/b" + strconv.Itoa(n), Base: "dev", Checks: checks}
}

func baseOf(checks ...Check) Base { return Base{Head: hC, Rollup: Classify(checks)} }

// A hand-off goes to the head's author, woken, with master's copy; the same
// episode sends once; a new head sends again and counts; a green pass does
// not reset the count.
func TestCILoopHandoffOnceThenNewHeadCounts(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.authored(hB, "coder")
	w.pass(redPR(1, hA, failed("build"), passed("lint")))
	c := w.cmds()
	if len(c) != 2 || c[0].target != "coder" || c[0].wake != "" || c[1].target != "master" || c[1].wake != "no" {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(c[0].text, "hand-off 1 of 3: `build` (https://ci/build)") || !strings.Contains(c[0].text, "report to master") {
		t.Fatalf("text %q", c[0].text)
	}
	w.pass(redPR(1, hA, failed("build"), passed("lint")))
	if len(w.cmds()) != 2 {
		t.Fatal("the same episode sent again")
	}
	w.pass(redPR(1, hB, passed("build"))) // green: nothing, the count stays
	w.pass(redPR(1, hB, failed("build")))
	if len(w.cmds()) != 4 || w.count(1) != 2 {
		t.Fatalf("new head: %d cmds, count %d", len(w.cmds()), w.count(1))
	}
}

// A pending check sends nothing, even beside a failed one; once all are
// complete, one hand-off lists every failure, and the count moves once.
func TestCILoopWaitsForTheHeadsCI(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.pass(redPR(1, hA, failed("build"), running("test")))
	if len(w.cmds()) != 0 {
		t.Fatal("a running check was handed off")
	}
	w.pass(redPR(1, hA, failed("build"), failed("test")))
	if c := w.cmds(); len(c) != 2 || !strings.Contains(c[0].text, "`build` (https://ci/build), `test` (https://ci/test)") || w.count(1) != 1 {
		t.Fatalf("%+v count %d", c, w.count(1))
	}
}

// The owner: one author over two reviewers; a self-addressed verdict is no
// author; none or two authors is master (no copy then).
func TestCIOwnerRule(t *testing.T) {
	vs := []bus.Verdict{
		{Author: "coder", Reviewer: "foureyes", Head: hA}, {Author: "coder", Reviewer: "reviewer-codex", Head: hA},
		{Author: "foureyes", Reviewer: "foureyes", Head: hB}, {Author: "coder2", Reviewer: "foureyes", Head: hB},
		{Author: "coder", Reviewer: "foureyes", Head: hC}, {Author: "docs", Reviewer: "foureyes", Head: hC},
	}
	for head, want := range map[string]string{hA: "coder", hB: "coder2", hC: "master", "f00d": "master"} {
		if got := CIOwner(vs, head, "master"); got != want {
			t.Errorf("%s: %s, want %s", head, got, want)
		}
	}
	w := newCIWorld(t)
	w.pass(redPR(5, hA, failed("build")))
	if c := w.cmds(); len(c) != 1 || c[0].target != "master" || c[0].wake != "" {
		t.Fatalf("an unknown author: %+v", c)
	}
}

// A blocked or waiting owner holds a hand-off, then is sent; it never holds
// the stop.
func TestCILoopHeldOwner(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.state("coder", "blocked")
	w.pass(redPR(1, hA, failed("build")))
	if len(w.cmds()) != 0 || w.count(1) != 0 {
		t.Fatal("a blocked owner was handed off")
	}
	w.state("coder", "working")
	w.pass(redPR(1, hA, failed("build")))
	if len(w.cmds()) != 2 {
		t.Fatalf("after the block: %+v", w.cmds())
	}
	// At the cap, a waiting owner does not hold the stop.
	w.r.HSet(context.Background(), bus.CIKey(w.b.Project()), "pr:2", CIDigest("x")+"|3|0|om")
	w.authored(hB, "coder")
	w.state("coder", "waiting")
	w.pass(redPR(1, hA, failed("build")), redPR(2, hB, failed("build")))
	if w.notices() != 1 {
		t.Fatalf("the stop waited for a held owner: %d notices", w.notices())
	}
}

// The known limitation (§4): owner part done, master's copy refused, the
// owner then waiting: nothing while held; the copy once the owner leaves
// held, the count unchanged, if the episode is still red; if the episode
// changes while held, the old copy is not sent and the new action is decided.
func TestCILoopKnownLimitation(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	pr := redPR(1, hA, failed("build"))
	dig := CIDigest("1", hA, "build")
	w.r.HSet(context.Background(), bus.CIKey(w.b.Project()), "pr:1", dig+"|1|0|o")
	w.state("coder", "waiting")
	w.pass(pr)
	if len(w.cmds()) != 0 {
		t.Fatal("wrote while the owner is held")
	}
	w.state("coder", "idle")
	w.pass(pr)
	if c := w.cmds(); len(c) != 1 || c[0].target != "master" || w.count(1) != 1 {
		t.Fatalf("%+v count %d", c, w.count(1))
	}

	w2 := newCIWorld(t)
	w2.authored(hA, "coder")
	w2.authored(hB, "coder")
	w2.r.HSet(context.Background(), bus.CIKey(w2.b.Project()), "pr:1", dig+"|1|0|o")
	w2.state("coder", "waiting")
	w2.pass(redPR(1, hB, failed("build"))) // a new head while held: a new hand-off, held too
	w2.state("coder", "idle")
	w2.pass(redPR(1, hB, failed("build")))
	for _, c := range w2.cmds() {
		if !strings.Contains(c.text, "hand-off 2 of 3") {
			t.Fatalf("the old episode's copy was sent: %+v", c)
		}
	}
	if len(w2.cmds()) != 2 || w2.count(1) != 2 {
		t.Fatalf("%+v count %d", w2.cmds(), w2.count(1))
	}
}

// The cap stops the loop once: master's directive (wake=no), the person's
// notice naming ci reset; then nothing.
func TestCILoopStopAtTheCap(t *testing.T) {
	w := newCIWorld(t)
	w.cap = 1
	w.authored(hA, "coder")
	w.authored(hB, "coder")
	w.pass(redPR(1, hA, failed("build")))
	w.pass(redPR(1, hB, failed("build")))
	c := w.cmds()
	if len(c) != 3 || c[2].target != "master" || c[2].wake != "no" || !strings.Contains(c[2].text, "CI loop stopped") || w.notices() != 1 {
		t.Fatalf("%+v notices %d", c, w.notices())
	}
	xs, _ := w.r.XRange(context.Background(), bus.StreamKey(w.b.Project(), "notify"), "-", "+").Result()
	if m := str(xs[0].Values["message"]); !strings.Contains(m, "agentbus ci reset 1") || str(xs[0].Values["for"]) != bus.ForPerson {
		t.Fatalf("notice %v", xs[0].Values)
	}
	w.pass(redPR(1, hC, failed("test")))
	if len(w.cmds()) != 3 || w.notices() != 1 {
		t.Fatal("a stopped PR sent again")
	}
}

// A closed or merged PR's field is deleted; a draft is skipped.
func TestCILoopClosedPRForgottenDraftSkipped(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.pass(redPR(1, hA, failed("build")))
	d := redPR(2, hB, failed("build"))
	d.Draft = true
	w.pass(d)
	if rec, _ := w.b.CIRecord(context.Background()); len(rec) != 0 || len(w.cmds()) != 2 {
		t.Fatalf("record %+v cmds %d", rec, len(w.cmds()))
	}
}

// A: the log hint names gh run view --log-failed only for an Actions check
// run; a status (a Jenkins job) gives the URL alone.
func TestCILoopLogHint(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.authored(hB, "coder2")
	j := Check{Name: "jenkins/pr", URL: "https://jenkins.internal/job/1", Failed: true}
	w.pass(redPR(1, hA, failed("build")), redPR(2, hB, j))
	c := w.cmds()
	if !strings.Contains(c[0].text, "gh run view --log-failed") {
		t.Fatalf("actions: %q", c[0].text)
	}
	if strings.Contains(c[2].text, "gh run view") || !strings.Contains(c[2].text, "Read the log from the link.") || !strings.Contains(c[2].text, "https://jenkins.internal/job/1") {
		t.Fatalf("jenkins: %q", c[2].text)
	}
}

// No log field, no command output: an entry holds the fixed fields only.
func TestCILoopEntriesHoldNoLog(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.pass(redPR(1, hA, failed("build")))
	xs, _ := w.r.XRange(context.Background(), bus.StreamKey(w.b.Project(), "cmd"), "-", "+").Result()
	allowed := map[string]bool{"from": true, "target": true, "type": true, "ref": true, "command": true, "text_complete": true, "episode": true, "wake": true}
	for _, x := range xs {
		for k := range x.Values {
			if !allowed[k] {
				t.Fatalf("field %q in an entry", k)
			}
		}
	}
}

// B, the person's seven, and step 1b's order.
func TestCILoopBaseRedHoldsAndTellsMasterOnce(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.authored(hB, "coder2")
	w.base["dev"] = baseOf(failed("test-linux"), passed("build"))
	prs := []PR{redPR(1, hA, failed("test-linux")), redPR(2, hB, failed("test-linux"), failed("build"))}
	w.pass(prs...)
	c := w.cmds()
	if len(c) != 1 || c[0].target != "master" || c[0].wake != "" || !strings.Contains(c[0].text, "`dev` is red at `"+hC[:7]+"`: `test-linux` (https://ci/test-linux). 2 PR(s)") {
		t.Fatalf("base red: %+v", c)
	}
	if w.count(1) != 0 || w.count(2) != 0 || w.reads["dev"] != 1 {
		t.Fatalf("counts %d %d reads %d", w.count(1), w.count(2), w.reads["dev"])
	}
	// The same base episode on the next pass: nothing more to master.
	w.pass(prs...)
	if len(w.cmds()) != 1 {
		t.Fatalf("the same base episode told master again: %+v", w.cmds())
	}
	// The base green again, a PR still red: a normal hand-off, count +1.
	w.base["dev"] = baseOf(passed("test-linux"))
	w.pass(prs[0])
	if len(w.cmds()) != 3 || w.count(1) != 1 {
		t.Fatalf("base green: %+v count %d", w.cmds(), w.count(1))
	}
}

func TestCILoopBaseRedOnAnotherCheckHandsOff(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.base["dev"] = baseOf(failed("test-linux"), passed("build"))
	w.pass(redPR(1, hA, failed("build")))
	if c := w.cmds(); len(c) != 2 || c[0].target != "coder" || w.count(1) != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestCILoopBaseRunningWaits(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.base["dev"] = baseOf(running("test-linux"), passed("build"))
	w.pass(redPR(1, hA, failed("test-linux")))
	if len(w.cmds()) != 0 || w.count(1) != 0 {
		t.Fatalf("%+v", w.cmds())
	}
}

func TestCILoopBaseReadFailsSendsNothing(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.baseEr["dev"] = errors.New("gh timed out")
	reason := w.pass(redPR(1, hA, failed("test-linux")), redPR(2, hA, failed("x")))
	if len(w.cmds()) != 0 || w.count(1) != 0 || w.reads["dev"] != 1 {
		t.Fatalf("%+v reads %d", w.cmds(), w.reads["dev"])
	}
	if !strings.Contains(reason, "ci: base dev read failed: gh timed out") {
		t.Fatalf("reason %q", reason)
	}
}

func TestCILoopBaseWithNoRollupHandsOff(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	w.base["dev"] = baseOf()
	w.pass(redPR(1, hA, failed("test-linux")))
	if len(w.cmds()) != 2 || w.count(1) != 1 {
		t.Fatalf("%+v", w.cmds())
	}
}

// Step 1b before the stop: a PR at the cap whose failure the red base shares
// is held, not stopped; a stopped PR stays stopped whatever the base.
func TestCILoopBaseHoldsBeforeTheStop(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	ctx := context.Background()
	w.r.HSet(ctx, bus.CIKey(w.b.Project()), "pr:1", CIDigest("old")+"|3|0|om", "pr:2", CIDigest("old2")+"|3|1|mn")
	w.base["dev"] = baseOf(failed("test-linux"))
	w.pass(redPR(1, hA, failed("test-linux")), redPR(2, hA, failed("build")))
	if w.notices() != 0 {
		t.Fatal("a PR the red base holds was stopped, or a stopped PR wrote")
	}
	if rec, _ := w.b.CIRecord(ctx); rec[1].Stopped || !rec[2].Stopped || rec[1].Count != 3 {
		t.Fatalf("%+v", rec)
	}
	if w.reads["dev"] != 1 {
		t.Fatalf("reads %d", w.reads["dev"])
	}
}

// No red PR, no base call.
func TestCILoopNoRedNoBaseRead(t *testing.T) {
	w := newCIWorld(t)
	w.pass(redPR(1, hA, passed("build")), redPR(2, hB, running("x"), failed("y")))
	if len(w.reads) != 0 {
		t.Fatalf("reads %v", w.reads)
	}
}

// A lost lease ends the pass with ErrNotHolder.
func TestCILoopLostLease(t *testing.T) {
	w := newCIWorld(t)
	w.authored(hA, "coder")
	if _, err := RunCI(context.Background(), w.b, "stale", w.cfg(), []PR{redPR(1, hA, failed("b"))}, w.vs); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("%v", err)
	}
}
