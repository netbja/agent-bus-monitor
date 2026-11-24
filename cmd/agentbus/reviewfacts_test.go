package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/bustest"
)

// board-drift §3.2 (step 2): request review records the head it was given
// (--commit, else the branch's tip) and the base tip it read: origin/<base>
// after a fetch, or the local <base> in local mode. A head already in that
// base is refused; a failed fetch records no base and refuses nothing.

type reviewRepo struct {
	t   *testing.T
	dir string
}

func (r reviewRepo) git(args ...string) string {
	r.t.Helper()
	c := exec.Command("git", args...)
	c.Dir = r.dir
	c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r reviewRepo) commit(name string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(name), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", name)
	r.git("commit", "-q", "-m", name)
	return r.git("rev-parse", "HEAD")
}

// newReviewRepo: dev with one commit, coder/t1 one commit beyond it,
// coder/empty at dev.
func newReviewRepo(t *testing.T) (r reviewRepo, base, tip string) {
	t.Helper()
	r = reviewRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "dev")
	base = r.commit("a")
	r.git("branch", "coder/empty")
	r.git("checkout", "-q", "-b", "coder/t1")
	tip = r.commit("b")
	return r, base, tip
}

func TestReviewFactsLocal(t *testing.T) {
	r, base, tip := newReviewRepo(t)
	g := reviewGitIn(r.dir)
	head, rb, err := reviewFacts(g, "auto", "dev", "coder/t1", "")
	if err != nil || head != tip || rb != base {
		t.Fatalf("a branch beyond the base: %q %q %v", head, rb, err)
	}
	// A branch with no commit beyond the base, or a --commit in it: refused.
	if _, _, err := reviewFacts(g, "auto", "dev", "coder/empty", ""); err == nil || !strings.Contains(err.Error(), "nothing to review: "+base+" is already in dev") {
		t.Fatalf("an empty branch: %v", err)
	}
	if _, _, err := reviewFacts(g, "local", "dev", "coder/t1", base); err == nil {
		t.Fatal("a --commit in the base accepted")
	}
	if _, _, err := reviewFacts(g, "auto", "dev", "coder/t1", "abcdef1"); err == nil || !strings.Contains(err.Error(), "not a commit in this checkout") {
		t.Fatalf("an unknown --commit: %v", err)
	}
	// --commit wins over the branch's tip.
	if head, _, err := reviewFacts(g, "auto", "dev", "coder/empty", tip); err != nil || head != tip {
		t.Fatalf("--commit: %q %v", head, err)
	}
	// No branch that resolves, no commit: nothing recorded, nothing refused.
	if head, rb, err := reviewFacts(g, "auto", "dev", "coder/gone", ""); err != nil || head != "" || rb != base {
		t.Fatalf("no head: %q %q %v", head, rb, err)
	}
	// A base that does not resolve: no base, nothing refused.
	if head, rb, err := reviewFacts(g, "auto", "main", "coder/empty", ""); err != nil || rb != "" || head != base {
		t.Fatalf("no base: %q %q %v", head, rb, err)
	}
}

// With a remote, the base tip is origin's, read after a fetch: a local base
// behind origin does not count; a fetch that fails records no base.
func TestReviewFactsRemote(t *testing.T) {
	r, _, tip := newReviewRepo(t)
	origin := reviewRepo{t: t, dir: t.TempDir()}
	origin.git("init", "-q", "--bare")
	r.git("remote", "add", "origin", origin.dir)
	r.git("push", "-q", "origin", "coder/t1:dev") // origin's dev already holds the branch's tip
	g := reviewGitIn(r.dir)
	if _, _, err := reviewFacts(g, "none", "dev", "coder/t1", ""); err == nil || !strings.Contains(err.Error(), "already in dev") {
		t.Fatalf("a head in origin's base (the local base behind it): %v", err)
	}
	if tipOrigin := r.git("rev-parse", "origin/dev"); tipOrigin != tip {
		t.Fatalf("not fetched: %s", tipOrigin)
	}
	r.git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "nowhere"))
	if head, rb, err := reviewFacts(g, "none", "dev", "coder/t1", ""); err != nil || rb != "" || head != tip {
		t.Fatalf("a failed fetch: %q %q %v", head, rb, err)
	}
}

// The command: request review in the agent's checkout records the head and
// the base tip on the request (and --more); one with nothing beyond the base
// is refused, the request left as it was.
func TestRequestReviewRecordsFacts(t *testing.T) {
	b, _ := bustest.Open(t)
	ctx := context.Background()
	r, base, tip := newReviewRepo(t)
	t.Chdir(r.dir)
	for name, v := range map[string]string{"delivery.forge": "local", "delivery.base": "dev"} { // no origin; its base is dev
		k, _ := config.Lookup(name)
		t.Setenv(k.Env, v)
	}
	for _, task := range []string{"t1", "t2"} {
		if _, err := b.Request(ctx, task, "master", "coder", "", "build", time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := b.AcceptRequest(ctx, task, "coder"); err != nil {
			t.Fatal(err)
		}
	}
	if err := runRequest(ctx, b, "coder", []string{"review", "t1", "--branch", "coder/t1", "--more", "step", "1"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	m, _ := b.Board(ctx)
	if q := m["t1"].Request; m["t1"].State != "review" || q.Commit != tip || q.ReviewBase != base || !q.ReviewMore || q.ReviewNote != "step 1" {
		t.Fatalf("recorded %+v %+v", m["t1"], q)
	}
	err := runRequest(ctx, b, "coder", []string{"review", "t2", "--branch", "coder/empty"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "nothing to review") {
		t.Fatalf("an empty branch: %v", err)
	}
	if m, _ = b.Board(ctx); m["t2"].State != "accepted" {
		t.Fatalf("refused but written: %+v", m["t2"])
	}
}
