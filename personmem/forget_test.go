package personmem

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Spec 2026-11-21-person-memory-design r2, step 3's store side: forget
// (§4.2: an observation or a topic, one commit), purge (the person's answer
// 2: the repository rebuilt as one commit of the current files), and
// foureyes' nits on step 2 (a lock wait bounded by ctx, .lock ignored).

// twoDecisions: a decision on merge-policy superseded by a second, and a
// preference on notifications.
func twoDecisions(t *testing.T, s *Store) (first, second, pref Observation) {
	t.Helper()
	rd := reader()
	rd["demo:7-0"] = Entry{Kind: "cmd", Type: "directive", Body: "main first after all", Human: true, Typed: true}
	ctx := context.Background()
	var err error
	if first, err = s.Remember(ctx, rd, decision("merge-policy", dirID, "yes, dev first")); err != nil {
		t.Fatal(err)
	}
	r := decision("merge-policy", "7-0", "main first")
	r.Supersedes = first.ID
	if second, err = s.Remember(ctx, rd, r); err != nil {
		t.Fatal(err)
	}
	if pref, err = s.Remember(ctx, nil, Request{Topic: "notifications", Type: "preference", Importance: "minor",
		Busmon: true, At: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC), Quote: "no sound at night", By: "charlie"}); err != nil {
		t.Fatal(err)
	}
	return first, second, pref
}

func gitLog(t *testing.T, s *Store) string {
	t.Helper()
	out, err := exec.Command("git", "-C", s.Dir, "log", "-p", "--all").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v %s", err, out)
	}
	return string(out)
}

// forget <id>: the observation leaves its topic in one commit; an
// observation that superseded it no longer points at it, so check stays
// clean.
func TestForgetAnObservation(t *testing.T) {
	s := store(t)
	first, second, _ := twoDecisions(t, s)
	before := commits(t, s)
	what, err := s.Forget(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(what, first.ID) || !strings.Contains(what, "merge-policy") {
		t.Fatalf("said %q", what)
	}
	tp, err := s.Load("merge-policy")
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Obs) != 1 || tp.Obs[0].ID != second.ID || tp.Obs[0].Supersedes != "" {
		t.Fatalf("topic after forget %+v", tp.Obs)
	}
	if commits(t, s) != before+1 {
		t.Fatalf("%d commits, want %d", commits(t, s), before+1)
	}
	if f := s.Check(); len(f) != 0 {
		t.Fatalf("check after forget: %v", f)
	}
	b, _ := os.ReadFile(s.path("merge-policy"))
	if strings.Contains(string(b), "dev first") {
		t.Fatalf("the forgotten quote is still in the file:\n%s", b)
	}
}

// forget <topic>: the file goes, in one commit.
func TestForgetATopic(t *testing.T) {
	s := store(t)
	twoDecisions(t, s)
	before := commits(t, s)
	if _, err := s.Forget(context.Background(), "notifications"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.path("notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file left: %v", err)
	}
	if ts, _ := s.Topics(); len(ts) != 1 || ts[0] != "merge-policy" {
		t.Fatalf("topics %v", ts)
	}
	if commits(t, s) != before+1 {
		t.Fatalf("%d commits", commits(t, s))
	}
}

// forget of nothing known: an error naming it, nothing written.
func TestForgetUnknown(t *testing.T) {
	s := store(t)
	twoDecisions(t, s)
	before := commits(t, s)
	for _, what := range []string{"nope", "20261121-9-0", "Bad Topic"} {
		if _, err := s.Forget(context.Background(), what); err == nil || !strings.Contains(err.Error(), "nothing") {
			t.Errorf("%q: %v", what, err)
		}
	}
	if commits(t, s) != before {
		t.Fatalf("a refused forget committed")
	}
}

// purge: the history is one commit of the current files; a forgotten text
// leaves the disk's git history; it says how many commits went.
func TestPurgeRebuildsOneCommit(t *testing.T) {
	s := store(t)
	first, _, _ := twoDecisions(t, s)
	if _, err := s.Forget(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gitLog(t, s), "dev first") {
		t.Fatal("the forgotten quote should still be in the history before purge")
	}
	n, err := s.Commits(context.Background())
	if err != nil || n != 4 {
		t.Fatalf("commits %d %v", n, err)
	}
	files := map[string]string{}
	for _, tp := range []string{"merge-policy", "notifications"} {
		b, _ := os.ReadFile(s.path(tp))
		files[tp] = string(b)
	}
	gone, err := s.Purge(context.Background())
	if err != nil || gone != 4 {
		t.Fatalf("purge: %d %v", gone, err)
	}
	if commits(t, s) != 1 || strings.Contains(gitLog(t, s), "dev first") {
		t.Fatalf("after purge: %d commits\n%s", commits(t, s), gitLog(t, s))
	}
	for tp, want := range files {
		if b, _ := os.ReadFile(s.path(tp)); string(b) != want {
			t.Fatalf("%s changed by purge", tp)
		}
	}
	out, _ := exec.Command("git", "-C", s.Dir, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("after purge, untracked or changed: %s", out)
	}
	// An empty store purges to nothing.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	empty, _ := Open(os.Getenv)
	if gone, err := empty.Purge(context.Background()); err != nil || gone != 0 {
		t.Fatalf("empty purge: %d %v", gone, err)
	}
}

// foureyes (step 2): the lock is ignored by git, so the repository stays
// clean beside it.
func TestLockFileIgnored(t *testing.T) {
	s := store(t)
	twoDecisions(t, s)
	if _, err := os.Stat(filepath.Join(s.Dir, ".lock")); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", s.Dir, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("untracked beside the topics: %s", out)
	}
}

// foureyes (step 2): a writer waiting for the lock gives up when its ctx
// ends, writing nothing.
func TestLockWaitBoundedByContext(t *testing.T) {
	s := store(t)
	twoDecisions(t, s)
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	before := commits(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := s.Forget(ctx, "notifications"); done <- err }()
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("forget under a held lock still waits after its ctx ended")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forget under a held lock: %v after %v", err, time.Since(start))
	}
	if commits(t, s) != before {
		t.Fatal("wrote while another held the lock")
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if _, err := s.Forget(context.Background(), "notifications"); err != nil {
		t.Fatalf("after the lock is freed: %v", err)
	}
}
