package rolecfg

import (
	"os"
	"strings"
	"testing"
)

// merge-conflicts spec §3 step 2 (the person's yes, 2026-11-21): master's skill
// carries four scheduling rules and the command that lists an in-flight
// branch's files; no stacking is said with its recovery, in master's skill and
// in the peer skill the implementer reads.
func TestConflictSchedulingRules(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(repoPath(t, path))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(b)), " ")
	}
	const onto = "git rebase --onto origin/<base> <parent's old head>"
	master := read("skills/agent-bus-master/SKILL.md")
	// Each rule's operative sentence, not only its title: deleting or changing
	// what a rule asks fails here (reviewer-codex).
	for _, want := range []string{
		"Before you send a task, list the files of each branch in flight",
		"git diff --name-only origin/<base>...origin/<branch>",
		"Step N+1 of a spec is never in flight beside step N: send it once N is merged.",
		"while one is in flight, send nothing that touches its files.",
		"A pending one waits until the in-flight PRs on those files merge.",
		"the second to merge rebases, and regenerates the goldens with `-update`",
		"One PR per step, each from `origin/<base>`.",
		"rebase it with `" + onto + "`: that replays only the child's own commits.",
	} {
		if !strings.Contains(master, want) {
			t.Errorf("the master skill lacks %q", want)
		}
	}
	peer := read("skills/agent-bus/SKILL.md")
	for _, want := range []string{
		"Never branch a step from another unmerged branch.",
		"rebase your branch with `" + onto + "`",
		"that replays only your own commits.",
	} {
		if !strings.Contains(peer, want) {
			t.Errorf("the peer skill lacks %q", want)
		}
	}
}
