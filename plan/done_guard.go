package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// plan done's guard (onboarding §9.3, spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md): for an
// agent task with a check, master never types the merged head; plan done
// works it out and records done only at a head the deciding approval
// checked.

// Gated reports whether t's done needs its check's proof: an agent's task
// with a check, not a mission (whose conductor runs its test, §7.3).
func Gated(t Task) bool { return t.Check != "" && t.Owner != Person && t.Mission == "" }

// MergedSource is where a gated task's merged head is read: a linked PR
// (read through the forge), or a head already known (a ref's sha, a
// merge:false request's commit).
type MergedSource struct {
	PR   int
	Head string
}

// DoneSource is t's MergedSource, or why none can be read.
func DoneSource(t Task, fact Fact, in Inputs) (MergedSource, error) {
	switch {
	case fact.PR > 0:
		return MergedSource{PR: fact.PR}, nil
	case fact.Ref != "":
		i := strings.LastIndex(fact.Ref, "@")
		if i < 0 || !bus.ValidHead(fact.Ref[i+1:]) {
			return MergedSource{}, fmt.Errorf("%s: its ref %q names no full sha", t.ID, fact.Ref)
		}
		return MergedSource{Head: fact.Ref[i+1:]}, nil
	case !t.MergeTask():
		if head, known := ResultHead(t, fact, in); known {
			return MergedSource{Head: head}, nil
		}
		return MergedSource{}, fmt.Errorf("%s: delivered without a full --commit: the checked head is unknown", t.ID)
	}
	return MergedSource{}, fmt.Errorf("%s: no PR or ref linked (agentbus plan link): the merged head is unknown", t.ID)
}

// CheckedDone is nil when the deciding approval of t's subjects quotes its
// check with exit 0 at exactly head, else what to do about it.
func CheckedDone(t Task, fact Fact, in Inputs, head string) error {
	vs := subjectsVerdicts(t, fact, in)
	approved := "none"
	if st, i := bus.RollUp(vs); st == "APPROVED" {
		switch evidenceOf(vs[i], t.Check) {
		case evidenceOK:
			if vs[i].Head == head {
				return nil
			}
			approved = sha7(vs[i].Head)
		case evidenceFailed:
			return fmt.Errorf("%s: check failed at %s; ask a reviewer to run the check at %s", t.ID, sha7(vs[i].Head), sha7(head))
		}
	}
	return fmt.Errorf("%s: the merged head %s was not approved with the check (approved: %s); ask a reviewer to run the check at %s",
		t.ID, sha7(head), approved, sha7(head))
}

// ErrNoReader: the forge has no reader of a merged PR's head here.
var ErrNoReader = errors.New("no reader of a merged pull request's head for this forge")

// PersonCheckQID is the question id that binds a person's check (§9.4):
// "check-" and 16 hex digits of SHA-256(plan ‖ 0x00 ‖ task id ‖ 0x00 ‖
// check). Another check, task or plan gives another id, so an old passes
// never answers a new check.
func PersonCheckQID(f File, t Task) string {
	h := sha256.Sum256([]byte(f.Plan + "\x00" + t.ID + "\x00" + t.Check))
	return "check-" + hex.EncodeToString(h[:8])
}

// CheckMissions holds §7.3: a mission task with a check has that mission's
// missions.json test as its check, character for character (the conductor
// runs the test, so it enforces the check). missions reads a change's tests
// by mission id at the plan's commit; a file that cannot be read refuses.
// plan load calls it (plan draft too, step b); Validate reads no file.
func CheckMissions(f File, missions func(change string) (map[string]string, error)) error {
	read := map[string]map[string]string{}
	for _, t := range f.Tasks {
		if t.Mission == "" || t.Check == "" {
			continue
		}
		change, id, _ := strings.Cut(t.Mission, "/")
		tests, ok := read[change]
		if !ok {
			var err error
			if tests, err = missions(change); err != nil {
				return fmt.Errorf("plan: task %s: cannot read %s's missions.json: %w", t.ID, change, err)
			}
			read[change] = tests
		}
		test, ok := tests[id]
		switch {
		case !ok:
			return fmt.Errorf("plan: task %s: mission %s is not in %s's missions.json", t.ID, t.Mission, change)
		case test != t.Check:
			return fmt.Errorf("plan: task %s: its check differs from mission %s's test %q", t.ID, t.Mission, test)
		}
	}
	return nil
}
