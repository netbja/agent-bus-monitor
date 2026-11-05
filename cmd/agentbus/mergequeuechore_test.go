package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/mergequeue"
)

const mqHead = "f98091be429b220f63dc8b4b2dde3ef7d6d832a5"

// mqChore is a chore on a test project whose ledger approves mqHead, with
// every reader faked (no network, no git).
func mqChore(t *testing.T, origin string) (*mergeQueueChore, *bus.Bus) {
	t.Helper()
	b, _ := dialMain(t)
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
		github: func(context.Context, string, []string) ([]mergequeue.PR, error) {
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
	// gh fails: unknown, the items kept (the bus keeps them), the reason said.
	c.github = func(context.Context, string, []string) ([]mergequeue.PR, error) { return nil, mergequeue.ErrIncomplete }
	if err := c.pass(ctx, "mq"); err != nil {
		t.Fatal(err)
	}
	if rec, _ = b.MergeQueue(ctx); !strings.Contains(rec.Reason, "incomplete") || len(rec.Items) != 1 {
		t.Fatalf("after a cut list: %+v", rec)
	}
}

// GitHub without gh, GitLab and Bitbucket take the git fallback; a local
// forge reads local branches and fetches nothing.
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
		fetched, local := false, false
		ch.fetchBase = func(context.Context, string, []string, string) error { fetched = true; return nil }
		lt := ch.localTips
		ch.localTips = func(ctx context.Context, d string, e []string, base string) ([]mergequeue.Tip, string, error) {
			local = true
			return lt(ctx, d, e, base)
		}
		ch.github = func(context.Context, string, []string) ([]mergequeue.PR, error) {
			t.Fatalf("%s: gh read", name)
			return nil, nil
		}
		if err := ch.pass(ctx, "mq"); err != nil {
			t.Fatal(err)
		}
		rec, _ := b.MergeQueue(ctx)
		if rec.Source != "git" || rec.Reason != "" || len(rec.Items) != 1 || rec.Items[0].Branch != "feat/request-priority" || fetched != c.fetched || local != c.local {
			t.Fatalf("%s: %+v fetched %v local %v", name, rec, fetched, local)
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
	c.github = func(ctx context.Context, _ string, _ []string) ([]mergequeue.PR, error) {
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
