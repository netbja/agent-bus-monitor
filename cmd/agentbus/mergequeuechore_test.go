package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

const mqHead = "f98091be429b220f63dc8b4b2dde3ef7d6d832a5"

// mqChore is a chore on a test project whose ledger approves mqHead, with
// every reader faked (no network, no git).
func mqChore(t *testing.T, origin string) (*mergeQueueChore, *bus.Bus) {
	t.Helper()
	b, _ := bustest.Open(t)
	ctx := context.Background()
	if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: "request-priority", Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: mqHead, Message: "suites pass"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "mq", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch %v %v", ok, err)
	}
	c := &mergeQueueChore{b: b, dir: t.TempDir(), getenv: func(string) string { return "" }, environ: func() []string { return []string{"PATH=/nowhere"} },
		repo:   func(context.Context, string, []string) error { return nil },
		origin: func(context.Context, string, []string) (string, bool, error) { return origin, origin != "", nil },
		hasGH:  func([]string) bool { return true },
		github: func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
			return []mergequeue.PR{{Number: 309, Title: "request priority", Branch: "feat/request-priority", Head: mqHead}}, nil
		},
		remoteTips: func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
			return []mergequeue.Tip{{Branch: "feat/request-priority", Head: mqHead}}, "1111111111111111111111111111111111111111", nil
		},
		localTips: func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
			return []mergequeue.Tip{{Branch: "feat/request-priority", Head: mqHead}}, "1111111111111111111111111111111111111111", nil
		},
		fetchBase: func(context.Context, string, []string, string) error { return nil },
		inBase: func(context.Context, string, []string, string, []string) (map[string]bool, error) {
			return map[string]bool{mqHead: false}, nil
		},
		delivered: func(_ context.Context, _ string, _ []string, _ string, heads []string) (map[string]bool, error) {
			return map[string]bool{}, nil
		},
		done: func(context.Context, []bus.Verdict) (map[string]bool, error) { return map[string]bool{}, nil },
	}
	return c, b
}

func TestMergeQueueChoreGitHub(t *testing.T) {
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	ctx := context.Background()
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	rec, _ := b.MergeQueue(ctx)
	if rec.Source != "gh" || rec.Reason != "" || len(rec.Items) != 1 || rec.Items[0].Number != 309 || rec.Items[0].Subjects[0] != "request-priority" {
		t.Fatalf("%+v", rec)
	}
	// The same approved PR conflicting with its base: no item, recorded for a
	// rebase with its own base (foureyes: the chore passes it on).
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Branch: "feat/request-priority", Head: mqHead, Base: "release", Conflicting: true}}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); len(rec.Items) != 0 || len(rec.Conflicting) != 1 || rec.Conflicting[0] != (bus.MergeConflict{Number: 309, Base: "release"}) {
		t.Fatalf("conflicting: %+v", rec)
	}
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return []mergequeue.PR{{Number: 309, Title: "request priority", Branch: "feat/request-priority", Head: mqHead}}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	// gh fails: unknown, the items kept (the bus keeps them), the reason said.
	c.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
		return nil, mergequeue.ErrIncomplete
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); !strings.Contains(rec.Reason, "incomplete") || len(rec.Items) != 1 {
		t.Fatalf("after a cut list: %+v", rec)
	}
}

// GitHub without gh, GitLab and Bitbucket take the git fallback; a local
// forge reads only local branches and fetches nothing (the local branches
// are read in every mode since master-idle, to name an undelivered head's
// branch: what tells local apart is that origin is never read).
func TestMergeQueueChoreFallbacks(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		origin  string
		noGH    bool
		fetched bool
		local   bool
	}{
		"github without gh": {"https://github.com/o/r.git", true, true, false},
		"gitlab":            {"git@gitlab.com:o/r.git", false, true, false},
		"local":             {"/srv/git/r.git", false, false, true},
	} {
		ch, b := mqChore(t, c.origin)
		if c.noGH {
			ch.hasGH = func([]string) bool { return false }
		}
		fetched, remote := false, false
		ch.fetchBase = func(context.Context, string, []string, string) error { fetched = true; return nil }
		rt := ch.remoteTips
		ch.remoteTips = func(ctx context.Context, d string, e []string, base string) ([]mergequeue.Tip, string, error) {
			remote = true
			return rt(ctx, d, e, base)
		}
		ch.github = func(context.Context, string, []string, bool) ([]mergequeue.PR, error) {
			t.Fatalf("%s: gh read", name)
			return nil, nil
		}
		// No PR reader, no CI loop: a PR's CI record is never forgotten as closed.
		if _, err := b.FencedCIStep(ctx, "mq", bus.CIStep{PR: 7, Digest: strings.Repeat("a", 40), Cap: 3, Owner: "coder", Master: "master",
			Handoff: [2]string{"h", "h"}, MasterCopy: [2]string{"m", "m"}, StopMaster: "s", StopNotice: "n"}); err != nil {
			t.Fatal(err)
		}
		if err := ch.pass(ctx, "mq"); err != nil {
			t.Fatal(err)
		}
		rec, _ := b.MergeQueue(ctx)
		if rec.Source != "git" || rec.Reason != "" || len(rec.Items) != 1 || rec.Items[0].Branch != "feat/request-priority" || fetched != c.fetched || remote == c.local {
			t.Fatalf("%s: %+v fetched %v origin read %v", name, rec, fetched, remote)
		}
		if ci, _ := b.CIRecord(ctx); ci[7].Count != 1 {
			t.Fatalf("%s: the git fallback ran the CI loop: %+v", name, ci)
		}
	}
}

// What cannot be read is the reason, never an empty queue.
func TestMergeQueueChoreUnknowns(t *testing.T) {
	ctx := context.Background()
	for name, edit := range map[string]func(*mergeQueueChore){
		"no repository": func(c *mergeQueueChore) {
			c.repo = func(context.Context, string, []string) error { return errors.New("x") }
		},
		"no origin while the setting names github": func(c *mergeQueueChore) {
			c.origin = func(context.Context, string, []string) (string, bool, error) { return "", false, nil }
			c.getenv = func(k string) string {
				if k == "AGENT_BUS_DELIVERY_FORGE" {
					return "github"
				}
				return ""
			}
		},
		"ls-remote": func(c *mergeQueueChore) {
			c.hasGH = func([]string) bool { return false }
			c.remoteTips = func(context.Context, string, []string, string) ([]mergequeue.Tip, string, error) {
				return nil, "", errors.New("x")
			}
		},
		"fetch": func(c *mergeQueueChore) {
			c.hasGH = func([]string) bool { return false }
			c.fetchBase = func(context.Context, string, []string, string) error { return errors.New("x") }
		},
		"in base": func(c *mergeQueueChore) {
			c.hasGH = func([]string) bool { return false }
			c.inBase = func(context.Context, string, []string, string, []string) (map[string]bool, error) {
				return nil, errors.New("x")
			}
		},
	} {
		c, b := mqChore(t, "git@github.com:o/r.git")
		edit(c)
		if err := c.pass(ctx, "mq"); err != nil {
			t.Fatal(err)
		}
		if rec, _ := b.MergeQueue(ctx); rec.Reason == "" || rec.ItemsAt != 0 {
			t.Fatalf("%s: %+v", name, rec)
		}
	}
}

func TestMergeQueueChoreFenced(t *testing.T) {
	c, _ := mqChore(t, "git@github.com:o/r.git")
	if err := c.pass(context.Background(), "not-holder"); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("without the lease: %v", err)
	}
}

// The commands' environment: no askpass, no prompt, English.
func TestMergeQueueEnv(t *testing.T) {
	env := mergeQueueEnv([]string{"PATH=/bin", "GIT_ASKPASS=/usr/bin/ksshaskpass", "SSH_ASKPASS=x", "LANG=fr_FR.UTF-8"})
	joined := strings.Join(env, "\n")
	for _, want := range []string{"GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "SSH_ASKPASS_REQUIRE=never", "PATH=/bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env lacks %s:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "GIT_ASKPASS=") || strings.Contains(joined, "SSH_ASKPASS=x") {
		t.Fatalf("an askpass survived:\n%s", joined)
	}
}

// No origin with the forge on auto is deliver's local mode: local branches,
// nothing fetched.
func TestMergeQueueChoreNoOriginIsLocal(t *testing.T) {
	c, b := mqChore(t, "")
	fetched := false
	c.fetchBase = func(context.Context, string, []string, string) error { fetched = true; return nil }
	if err := c.pass(context.Background(), "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(context.Background()); rec.Reason != "" || rec.Source != "git" || len(rec.Items) != 1 || fetched {
		t.Fatalf("%+v fetched %v", rec, fetched)
	}
}

// Reads that exhaust the pass's budget still leave time for the fenced write:
// the failure is recorded, never the old success left looking current.
func TestMergeQueueChoreBudgetSpentStillRecords(t *testing.T) {
	c, b := mqChore(t, "git@github.com:o/r.git")
	ctx := context.Background()
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	c.budget = 50 * time.Millisecond
	c.github = func(ctx context.Context, _ string, _ []string, _ bool) ([]mergequeue.PR, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatalf("the write after a spent budget: %v", err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason == "" || len(rec.Items) != 1 {
		t.Fatalf("the spent pass is not recorded: %+v", rec)
	}
}

// master-idle §3.1: the undelivered heads, in the forge and none modes, the
// gh path included: an approved head with no tip on origin and that the git
// test does not find in the base, its local branch named; dropped, delivered
// or pushed it is out; local mode lists nothing.
func TestMergeQueueChoreUndelivered(t *testing.T) {
	ctx := context.Background()
	const waiting = "fa2d796f00000000000000000000000000000000"
	setup := func(origin string) (*mergeQueueChore, *bus.Bus, *bool, *[]string) {
		c, b := mqChore(t, origin)
		if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: "herdr-6c2-2", Author: "coder2", Reviewer: "foureyes", Decision: "approve", Head: waiting}); err != nil {
			t.Fatal(err)
		}
		lt := c.localTips
		c.localTips = func(ctx context.Context, d string, e []string, base string) ([]mergequeue.Tip, string, error) {
			tips, tip, err := lt(ctx, d, e, base)
			return append(tips, mergequeue.Tip{Branch: "feat/herdr-6c2-2", Head: waiting}), tip, err
		}
		fetched, judged := false, []string{}
		c.fetchBase = func(context.Context, string, []string, string) error { fetched = true; return nil }
		c.delivered = func(_ context.Context, _ string, _ []string, _ string, heads []string) (map[string]bool, error) {
			// The run's judgement (run-budget §2) takes every approved head,
			// mqHead included; undelivered's never does (it is on origin).
			if !slices.Contains(heads, mqHead) {
				judged = append(judged, heads...)
			}
			return map[string]bool{}, nil
		}
		return c, b, &fetched, &judged
	}
	for name, origin := range map[string]string{"gh": "git@github.com:o/r.git", "none": "git@git.example.org:o/r.git"} {
		c, b, fetched, judged := setup(origin)
		if err := c.pass(ctx, "mq"); err != nil {
			t.Fatal(err)
		}
		rec, _ := b.MergeQueue(ctx)
		if rec.Reason != "" || len(rec.Undelivered) != 1 || rec.Undelivered[0].Head != waiting || rec.Undelivered[0].Branch != "feat/herdr-6c2-2" ||
			rec.Undelivered[0].Subject != "herdr-6c2-2" || !*fetched || len(*judged) != 1 || (*judged)[0] != waiting {
			t.Fatalf("%s: %+v fetched %v judged %v", name, rec, *fetched, *judged)
		}
		// Delivered by the git test: out.
		c.delivered = func(context.Context, string, []string, string, []string) (map[string]bool, error) {
			return map[string]bool{waiting: true}, nil
		}
		if err := c.pass(ctx, "mq"); err != nil {
			t.Fatal(err)
		}
		if rec, _ = b.MergeQueue(ctx); len(rec.Undelivered) != 0 {
			t.Fatalf("%s: delivered still listed %+v", name, rec.Undelivered)
		}
	}
	// Dropped: out, and never judged.
	c, b, _, judged := setup("git@github.com:o/r.git")
	if err := b.DropHead(ctx, waiting, "redundant", "master"); err != nil {
		t.Fatal(err)
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); len(rec.Undelivered) != 0 || len(*judged) != 0 {
		t.Fatalf("dropped: %+v judged %v", rec.Undelivered, *judged)
	}
	// Its plan task done: out.
	c, b, _, _ = setup("git@github.com:o/r.git")
	c.done = func(context.Context, []bus.Verdict) (map[string]bool, error) {
		return map[string]bool{"herdr-6c2-2": true}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); len(rec.Undelivered) != 0 {
		t.Fatalf("done: %+v", rec.Undelivered)
	}
	// The git test fails: the pass is unknown, never an empty list.
	c, b, _, _ = setup("git@github.com:o/r.git")
	c.delivered = func(context.Context, string, []string, string, []string) (map[string]bool, error) {
		return nil, errors.New("git show: cannot judge")
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); !strings.Contains(rec.Reason, "cannot judge") || rec.ItemsAt != 0 {
		t.Fatalf("a failed git test: %+v", rec)
	}
	// Local mode: nothing is master's to push.
	c, b, _, judged = setup("/srv/git/r.git")
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason != "" || len(rec.Undelivered) != 0 || len(*judged) != 0 {
		t.Fatalf("local: %+v judged %v", rec, *judged)
	}
}

// run-budget §2: each pass hands the run the approved heads (within 72 h)
// the git test finds in the base: the first starts the run with them as its
// baseline, a later one counts each new head once, in every mode; a pass
// that cannot judge them counts nothing and starts no run.
func TestMergeQueueChoreRun(t *testing.T) {
	for _, origin := range []string{"git@github.com:netbja/agent-bus-monitor.git", ""} {
		c, b := mqChore(t, origin)
		ctx := context.Background()
		var judged [][]string
		in := map[string]bool{}
		fail := errors.New("git broke")
		var err error
		c.delivered = func(_ context.Context, _ string, _ []string, baseTip string, heads []string) (map[string]bool, error) {
			// Origin's base, or the local one in local mode: never none
			// (foureyes, M20).
			if baseTip != "1111111111111111111111111111111111111111" {
				t.Errorf("%q: judged against base %q", origin, baseTip)
			}
			judged = append(judged, heads)
			return in, err
		}
		err = fail
		if perr := c.pass(ctx, "mq"); perr != nil {
			t.Fatal(perr)
		}
		if rec, _ := b.Run(ctx); rec != nil {
			t.Fatalf("%q: a run started without its baseline: %+v", origin, rec)
		}
		err, in[mqHead] = nil, true
		if perr := c.pass(ctx, "mq"); perr != nil {
			t.Fatal(perr)
		}
		rec, _ := b.Run(ctx)
		if rec == nil || rec.Deliveries != 0 || !rec.Counted[mqHead] {
			t.Fatalf("%q: started %+v, want mqHead as the baseline", origin, rec)
		}
		head2 := strings.Repeat("e", 40)
		if _, verr := b.AppendVerdict(ctx, bus.Verdict{Subject: "run-budget-1", Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: head2, Message: "ok"}); verr != nil {
			t.Fatal(verr)
		}
		in[head2] = true
		// An approved head the git test does not find in the base: never a
		// delivery (foureyes, M11b).
		head3 := strings.Repeat("f", 40)
		if _, verr := b.AppendVerdict(ctx, bus.Verdict{Subject: "run-budget-2", Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: head3, Message: "ok"}); verr != nil {
			t.Fatal(verr)
		}
		for i := 0; i < 2; i++ {
			if perr := c.pass(ctx, "mq"); perr != nil {
				t.Fatal(perr)
			}
		}
		if rec, _ = b.Run(ctx); rec.Deliveries != 1 {
			t.Fatalf("%q: %d deliveries, want 1", origin, rec.Deliveries)
		}
		if rec.Counted[head3] {
			t.Fatalf("%q: a head not in the base counted", origin)
		}
		if last := judged[len(judged)-1]; len(last) != 3 {
			t.Fatalf("%q: judged %v, want the three approved heads", origin, last)
		}
		err = fail
		if perr := c.pass(ctx, "mq"); perr != nil {
			t.Fatal(perr)
		}
		if rec, _ = b.Run(ctx); rec == nil || rec.Deliveries != 1 {
			t.Fatalf("%q: a failed judgement changed the run: %+v", origin, rec)
		}
	}
}

// run-budget §2 (reviewer-codex): the 72 h window is judged on the broker's
// clock, as the approvals' times are: the broker 73 h on, a fresh approve is
// out of the window whatever the host's clock says; the broker's time
// unreadable, the run counts nothing and undelivered is unknown.
func TestMergeQueueChoreWindowOnBrokerTime(t *testing.T) {
	c, b := mqChore(t, "git@git.example.org:o/r.git")
	ctx := context.Background()
	var judged [][]string
	c.delivered = func(_ context.Context, _ string, _ []string, _ string, heads []string) (map[string]bool, error) {
		judged = append(judged, heads)
		return map[string]bool{}, nil
	}
	const waiting = "fa2d796f00000000000000000000000000000000"
	if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: "herdr-6c2-2", Author: "coder2", Reviewer: "foureyes", Decision: "approve", Head: waiting}); err != nil {
		t.Fatal(err)
	}
	c.serverNow = func(context.Context) (time.Time, error) { return time.Now().Add(73 * time.Hour), nil }
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	rec, _ := b.MergeQueue(ctx)
	if len(judged) != 0 || len(rec.Undelivered) != 0 || rec.Reason != "" {
		t.Fatalf("the broker 73h on: judged %v, undelivered %+v (%q)", judged, rec.Undelivered, rec.Reason)
	}
	if run, _ := b.Run(ctx); run == nil || len(run.Counted) != 0 {
		t.Fatalf("the broker 73h on: run %+v, want started with nothing in the window", run)
	}
	c.serverNow = func(context.Context) (time.Time, error) { return time.Time{}, errors.New("broker down") }
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); rec.Reason != "broker time unavailable" {
		t.Fatalf("no broker time: %+v", rec)
	}
	c.serverNow = nil // the broker's own clock: the approve is fresh
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); len(rec.Undelivered) != 1 || len(judged) == 0 {
		t.Fatalf("the broker's clock: undelivered %+v judged %v", rec.Undelivered, judged)
	}
}

// run-budget §2 (foureyes, M14): the pass counts a gap as paused only past
// three passes (15 min): 10 min without a pass is run time, 20 min paused.
func TestMergeQueueChoreRunGap(t *testing.T) {
	c, b := mqChore(t, "git@git.example.org:o/r.git")
	ctx := context.Background()
	rdb, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	back := func(d time.Duration) {
		rec, err := b.Run(ctx)
		if err != nil || rec == nil {
			t.Fatalf("Run %v %v", rec, err)
		}
		if err := rdb.HSet(ctx, bus.RunKey(b.Project()), "seen_at", rec.SeenAt-d.Milliseconds()).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	back(10 * time.Minute)
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.Run(ctx); rec.PausedMs != 0 {
		t.Fatalf("10m without a pass: paused %dms, want run time", rec.PausedMs)
	}
	back(20 * time.Minute)
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.Run(ctx); rec.PausedMs < (20 * time.Minute).Milliseconds() {
		t.Fatalf("20m without a pass: paused %dms, want the gap", rec.PausedMs)
	}
}

// merge-popup-setting: the queue's base is the project's, as deliver's
// (dev-branch-flow §4.1): the template's [delivery] base, else the machine's
// delivery.base. The chore read only the machine's, so it said main while
// every PR went to dev.
func TestMergeQueueChoreUsesTheProjectBase(t *testing.T) {
	c, b := mqChore(t, "git@github.com:netbja/agent-bus-monitor.git")
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, b.Project()+".toml"), []byte("[delivery]\nbase = \"dev\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.getenv = func(k string) string {
		if k == "AGENTBUS_PROJECTS_DIR" {
			return dir
		}
		return ""
	}
	var asked []string
	c.remoteTips = func(_ context.Context, _ string, _ []string, base string) ([]mergequeue.Tip, string, error) {
		asked = append(asked, base)
		return []mergequeue.Tip{{Branch: "feat/request-priority", Head: mqHead}}, "1111111111111111111111111111111111111111", nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Base != "dev" || len(asked) == 0 || asked[0] != "dev" {
		t.Fatalf("base %q, tips asked for %v", rec.Base, asked)
	}
}

// merge-popup-setting: the dispatch service's chore reads the delivery's
// mark, so a delivered PR is passed for at once.
func TestServiceMergeQueueReadsTheDueMark(t *testing.T) {
	b, _ := bustest.Open(t)
	cfg := serviceMergeQueue(b, func(string) string { return "" }, nil)
	if cfg.Pass == nil {
		t.Skip("no chore in this directory")
	}
	if cfg.Due == nil {
		t.Fatal("the service's merge queue never reads the delivery's mark")
	}
}

// ci-loop (spec 2026-11-19-ci-loop-design.md §2-§5): on the gh path the pass
// asks for the rollup only while ci.loop is on (foureyes on ci-loop-lib),
// runs the loop with ci.max_handoffs after the read, and names the loop's
// failure in the record's ci field, never as the queue's reason; off, it
// neither reads the rollup nor writes; merge-queue --live never writes.
func TestMergeQueueChoreCILoop(t *testing.T) {
	c, b := mqChore(t, "git@github.com:o/r.git")
	ctx := context.Background()
	env := map[string]string{}
	c.getenv = func(k string) string { return env[k] }
	asked := []bool{}
	red := func(n int, head string) mergequeue.PR {
		return mergequeue.PR{Number: n, Branch: "feat/x", Head: head, Base: "dev",
			Checks: []mergequeue.Check{{Name: "build", Failed: true, At: time.Unix(1, 0)}}}
	}
	c.github = func(_ context.Context, _ string, _ []string, checks bool) ([]mergequeue.PR, error) {
		asked = append(asked, checks)
		return []mergequeue.PR{red(309, mqHead)}, nil
	}
	bases := []string{}
	c.readBase = func(ctx context.Context, _ string, _ []string, branch string) (mergequeue.Base, error) {
		bases = append(bases, branch)
		// One call's budget, not the pass's (the reads get 18 s of 20).
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > mergeQueueCall {
			t.Errorf("a base read without its own %s budget: %v %v", mergeQueueCall, time.Until(d), ok)
		}
		return mergequeue.Base{Head: strings.Repeat("b", 40)}, nil
	}
	// Live: the rollup is read (the setting is on) but nothing is written.
	if rec := c.derive(ctx); rec.Source != "gh" || rec.Reason != "" {
		t.Fatalf("live derive: %+v", rec)
	}
	if rec, _ := b.CIRecord(ctx); len(rec) != 0 || len(bases) != 0 {
		t.Fatalf("live wrote %+v, read bases %v", rec, bases)
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	ci, _ := b.CIRecord(ctx)
	if e := ci[309]; e.Count != 1 || !strings.Contains(e.Done, "o") || len(bases) != 1 || bases[0] != "dev" {
		t.Fatalf("the hand-off: %+v bases %v", ci, bases)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason != "" || rec.CI != "" {
		t.Fatalf("a clean loop names nothing: %+v", rec)
	}
	// ci.max_handoffs 0: a new red head goes straight to the stop.
	env["AGENT_BUS_CI_MAX_HANDOFFS"] = "0"
	c.github = func(_ context.Context, _ string, _ []string, checks bool) ([]mergequeue.PR, error) {
		asked = append(asked, checks)
		return []mergequeue.PR{red(309, strings.Repeat("c", 40))}, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if ci, _ = b.CIRecord(ctx); !ci[309].Stopped {
		t.Fatalf("cap 0 does not stop: %+v", ci)
	}
	// A base that cannot be read: the loop's reason in ci, the queue's own
	// outcome untouched.
	delete(env, "AGENT_BUS_CI_MAX_HANDOFFS")
	c.github = func(_ context.Context, _ string, _ []string, checks bool) ([]mergequeue.PR, error) {
		asked = append(asked, checks)
		return []mergequeue.PR{red(309, mqHead), red(310, strings.Repeat("d", 40))}, nil
	}
	c.readBase = func(context.Context, string, []string, string) (mergequeue.Base, error) {
		return mergequeue.Base{}, errors.New("gh: 502")
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := b.MergeQueue(ctx); rec.Reason != "" || !strings.Contains(rec.CI, "ci: base dev read failed: gh: 502") {
		t.Fatalf("the base's failure: %+v", rec)
	}
	// ci.loop off: no rollup asked for, no base read, nothing written, the
	// closed PRs' fields kept as they were.
	env["AGENT_BUS_CI_LOOP"] = "off"
	before, _ := b.CIRecord(ctx)
	c.readBase = func(context.Context, string, []string, string) (mergequeue.Base, error) {
		t.Fatal("a base read with the loop off")
		return mergequeue.Base{}, nil
	}
	c.github = func(_ context.Context, _ string, _ []string, checks bool) ([]mergequeue.PR, error) {
		asked = append(asked, checks)
		return nil, nil
	}
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	after, _ := b.CIRecord(ctx)
	if len(after) != len(before) || asked[len(asked)-1] {
		t.Fatalf("the loop off: record %+v -> %+v, asked %v", before, after, asked)
	}
	if !slices.Equal(asked[:len(asked)-1], []bool{true, true, true, true}) {
		t.Fatalf("the rollup asked for while on: %v", asked)
	}
}
