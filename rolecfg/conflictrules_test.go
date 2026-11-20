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
	for _, want := range []string{
		"Sibling steps run in series",
		"A move or a rewrite runs alone",
		"Two settings PRs may run in parallel",
		"No stacking, and the recovery when it happened anyway",
		"git diff --name-only origin/<base>...origin/<branch>",
		onto,
	} {
		if !strings.Contains(master, want) {
			t.Errorf("the master skill lacks %q", want)
		}
	}
	if peer := read("skills/agent-bus/SKILL.md"); !strings.Contains(peer, onto) {
		t.Errorf("the peer skill lacks %q", onto)
	}
}
