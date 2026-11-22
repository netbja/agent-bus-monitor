package personmem

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Spec 2026-11-21-person-memory-design r2, §4.4, step 4 part b: e in
// busmon's Memory page opens a topic file in $EDITOR, the person's own act.
// On save the check runs; a clean memory commits the edited file as one
// commit, a failed check leaves the file as saved and commits nothing.

func TestCommitEditCleanCommitsTheFile(t *testing.T) {
	s := store(t)
	twoDecisions(t, s)
	ctx := context.Background()
	before := commits(t, s)
	b, err := os.ReadFile(s.path("notifications"))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), "importance: minor", "importance: critical", 1)
	if edited == string(b) {
		t.Fatalf("nothing to edit in\n%s", b)
	}
	if err := os.WriteFile(s.path("notifications"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, committed, err := s.CommitEdit(ctx, "notifications")
	if err != nil || len(fs) != 0 || !committed {
		t.Fatalf("findings %v committed %v err %v", fs, committed, err)
	}
	if n := commits(t, s); n != before+1 {
		t.Fatalf("commits %d, want %d", n, before+1)
	}
	if log := gitLog(t, s); !strings.Contains(log, "edit notifications") || !strings.Contains(log, "+  importance: critical") {
		t.Fatalf("the edit is not the last commit:\n%s", log)
	}
	// Saved without a change: nothing to commit, and no error.
	if fs, committed, err := s.CommitEdit(ctx, "notifications"); err != nil || len(fs) != 0 || committed {
		t.Fatalf("unchanged: findings %v committed %v err %v", fs, committed, err)
	}
	if n := commits(t, s); n != before+1 {
		t.Fatalf("unchanged: commits %d, want %d", n, before+1)
	}
}

func TestCommitEditFailedCheckCommitsNothing(t *testing.T) {
	s := store(t)
	_, _, pref := twoDecisions(t, s)
	ctx := context.Background()
	before := commits(t, s)
	b, err := os.ReadFile(s.path("notifications"))
	if err != nil {
		t.Fatal(err)
	}
	// A hand edit that points at nothing: the check names it.
	edited := strings.Replace(string(b), "  quote: ", "  supersedes: 20200101-1-0\n  quote: ", 1)
	if err := os.WriteFile(s.path("notifications"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, committed, err := s.CommitEdit(ctx, "notifications")
	if err != nil || committed || len(fs) == 0 || !strings.Contains(fs[0].String(), "notifications.md") {
		t.Fatalf("findings %v committed %v err %v", fs, committed, err)
	}
	if n := commits(t, s); n != before {
		t.Fatalf("commits %d, want %d (nothing committed)", n, before)
	}
	if after, _ := os.ReadFile(s.path("notifications")); string(after) != edited {
		t.Fatalf("the file is not left as saved:\n%s", after)
	}
	_ = pref
}

func TestCommitEditRefusesABadTopicName(t *testing.T) {
	s := store(t)
	if _, _, err := s.CommitEdit(context.Background(), "../x"); err == nil {
		t.Fatal("a topic name outside the store was accepted")
	}
}
