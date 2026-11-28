package mergequeue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Stashes on a disposable repository: every stash's message, newest first;
// none is an empty list, not an error; outside a repository it fails.
func TestStashes(t *testing.T) {
	w := newGitWorld(t)
	ctx := context.Background()
	if got, err := Stashes(ctx, w.work, w.env); err != nil || len(got) != 0 {
		t.Fatalf("no stash: %v %v", got, err)
	}
	git(t, w.env, w.work, "switch", "-q", "-c", "coder/x")
	for i, msg := range []string{"coder wip", "second"} {
		os.WriteFile(filepath.Join(w.work, "f"), []byte{byte('2' + i), '\n'}, 0o644)
		git(t, w.env, w.work, "stash", "push", "-q", "-m", msg)
	}
	got, err := Stashes(ctx, w.work, w.env)
	if err != nil || !slices.Equal(got, []string{"On coder/x: second", "On coder/x: coder wip"}) {
		t.Fatalf("stashes: %q %v", got, err)
	}
	if _, err := Stashes(ctx, t.TempDir(), w.env); err == nil {
		t.Fatal("outside a repository: no error")
	}
}

// HeldBranches: a claim's branch is its owner's, a request's branch its
// target's; a done entry holds nothing.
func TestHeldBranches(t *testing.T) {
	got := HeldBranches(map[string]bus.BoardEntry{
		"a": {Owner: "coder", State: "working", Branch: "coder/a"},
		"b": {Owner: "master", State: "review", Request: &bus.RequestInfo{Target: "coder2", Branch: "coder2/b"}},
		"c": {Owner: "coder", State: "done", Branch: "coder/c"},
	})
	slices.SortFunc(got, func(x, y Held) int { return cmpStr(x.Branch, y.Branch) })
	if !slices.Equal(got, []Held{{"coder", "coder/a"}, {"coder2", "coder2/b"}}) {
		t.Fatalf("%+v", got)
	}
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// JudgeLosable's rules: an unpushed held branch, a pushed one, one with no
// local ref; a stash names a role as a whole name only; every judged role
// has an entry; a branch held by a role not judged is skipped; no remote
// (Pushed nil) leaves nothing unpushed; a Pushed error fails the whole.
func TestJudgeLosable(t *testing.T) {
	pushed := map[string]bool{"coder/pushed": true}
	in := LosableIn{
		Roles: []string{"coder", "coder2", "docs"},
		Held:  []Held{{"coder", "coder/new"}, {"coder", "coder/pushed"}, {"coder", "coder/gone"}, {"coder", "coder/new"}, {"ghost", "ghost/x"}},
		Local: map[string]string{"coder/new": "n", "coder/pushed": "p", "ghost/x": "g"},
		Stashes: []string{"On coder2/y: wip", "WIP on dev: abc docs: notes", "On coderx: no", "On mycoder/z: no",
			"On coder/new: one"},
		Pushed: func(branch, _ string) (bool, error) { return pushed[branch], nil },
	}
	got, err := JudgeLosable(in)
	if err != nil {
		t.Fatal(err)
	}
	if c := got["coder"]; !slices.Equal(c.Branches, []string{"coder/new"}) || !slices.Equal(c.Stashes, []string{"On coder/new: one"}) {
		t.Fatalf("coder: %+v", c)
	}
	if c := got["coder2"]; len(c.Branches) != 0 || !slices.Equal(c.Stashes, []string{"On coder2/y: wip"}) {
		t.Fatalf("coder2: %+v", c)
	}
	if d, ok := got["docs"]; !ok || len(d.Branches) != 0 || !slices.Equal(d.Stashes, []string{"WIP on dev: abc docs: notes"}) {
		t.Fatalf("docs: %+v %v", d, ok)
	}
	if _, ok := got["ghost"]; ok || len(got) != 3 {
		t.Fatalf("roles: %+v", got)
	}

	in.Pushed = nil
	if got, _ := JudgeLosable(in); len(got["coder"].Branches) != 0 {
		t.Fatalf("no remote: %+v", got["coder"])
	}
	in.Pushed = func(string, string) (bool, error) { return false, errors.New("git failed") }
	if got, err := JudgeLosable(in); err == nil || got != nil {
		t.Fatalf("a failed judgement: %+v %v", got, err)
	}
}

// A stash names a role only as a token of its own: a branch whose name
// starts with the role and goes on in a branch character is another
// branch's (reviewer-codex).
func TestNamesRole(t *testing.T) {
	for msg, want := range map[string]bool{
		"On coder/x: wip": true, "WIP on dev: 1234567 coder: notes": true, "On dev: by coder.": true,
		"On dev: (coder) wip": true, "coder": true, "On dev: coder, then docs": true,
		"On coder2/x: wip": false, "On coder-extra/x: wip": false, "On coder.v2/x: wip": false,
		"On my.coder/x: wip": false, "On feat/coder: wip": false, "On coder_x/y: wip": false, "On dev: coder.v2 done": false,
	} {
		if got := namesRole("coder").MatchString(msg); got != want {
			t.Errorf("%q: %v, want %v", msg, got, want)
		}
	}
}

// At most stashListMax messages are kept for one role.
func TestJudgeLosableStashCap(t *testing.T) {
	var msgs []string
	for range stashListMax + 2 {
		msgs = append(msgs, "On coder/x: wip")
	}
	got, _ := JudgeLosable(LosableIn{Roles: []string{"coder"}, Stashes: msgs})
	if len(got["coder"].Stashes) != stashListMax {
		t.Fatalf("%d", len(got["coder"].Stashes))
	}
}
