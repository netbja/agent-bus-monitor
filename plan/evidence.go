package plan

import (
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The check's evidence (onboarding §9.1, spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md): a
// reviewer's verdict quotes the run of a task's check in one line,
//
//	check: <the task's check, verbatim> => exit <n> at <sha>
//
// and a grade's verdict prefixes the line with the item's id (§11.1):
// "dod-3 check: … => exit 0 at <sha>".

// Evidence is one such line: Item is "" on a task's line.
type Evidence struct {
	Item, Check string
	Exit        int
	Head        string
}

// EvidenceLines are the evidence lines of a verdict's text, in order; a
// line of another form is skipped. The check's whitespace runs are
// collapsed to one space, and the last " => exit " splits it from the run,
// so a check may hold "=>".
func EvidenceLines(text string) []Evidence {
	var out []Evidence
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		var e Evidence
		if item, rest, ok := strings.Cut(l, " "); ok && item != "check:" && bus.ValidName(item) {
			e.Item, l = item, rest
		}
		rest, ok := strings.CutPrefix(l, "check: ")
		if !ok {
			continue
		}
		i := strings.LastIndex(rest, " => exit ")
		if i < 0 {
			continue
		}
		code, head, ok := strings.Cut(rest[i+len(" => exit "):], " at ")
		n, err := strconv.Atoi(code)
		if !ok || err != nil || head == "" || strings.ContainsAny(head, " \t") {
			continue
		}
		e.Check, e.Exit, e.Head = collapse(rest[:i]), n, head
		if e.Check == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// collapse is s with its whitespace runs one space, trimmed.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

type evidenceState int

const (
	evidenceNone   evidenceState = iota // no line for the check at the verdict's head
	evidenceFailed                      // the line says exit <n>, n not 0
	evidenceOK                          // exit 0 at the verdict's head
)

// evidenceOf reads v's text (Full when set, else Message) for check at
// v.Head; a verdict without a valid head proves nothing.
func evidenceOf(v bus.Verdict, check string) evidenceState {
	if !bus.ValidHead(v.Head) {
		return evidenceNone
	}
	text := v.Message
	if v.Full != "" {
		text = v.Full
	}
	want, st := collapse(check), evidenceNone
	for _, e := range EvidenceLines(text) {
		if e.Check != want || e.Head != v.Head {
			continue
		}
		if e.Exit == 0 {
			return evidenceOK
		}
		st = evidenceFailed
	}
	return st
}

// CheckEvidence is the head v proves check ran on with exit 0, or false.
func CheckEvidence(v bus.Verdict, check string) (head string, ok bool) {
	if evidenceOf(v, check) != evidenceOK {
		return "", false
	}
	return v.Head, true
}

// ResultHead is the head a task's check must have run on (§9.1): a merge
// task's queued head, else the head recorded merged (fact.Head); a
// merge:false task's board request's commit, when it is a full sha. known
// is false when none is read: a linked PR neither queued nor merged.
func ResultHead(t Task, fact Fact, in Inputs) (head string, known bool) {
	if !t.MergeTask() {
		e, ok := in.Board[t.Board]
		if !ok || e.Request == nil || !bus.ValidHead(e.Request.Commit) {
			return "", false
		}
		return e.Request.Commit, true
	}
	if it := queuedFor(t, in.Merge); it != nil && it.Head != "" {
		return it.Head, true
	}
	if fact.Head != "" {
		return fact.Head, true
	}
	return "", false
}

// sha7 is a head's short form.
func sha7(h string) string { return h[:min(7, len(h))] }

// String is the line as a reader writes it, the head shortened.
func (e Evidence) String() string {
	s := "check: " + e.Check + " => exit " + strconv.Itoa(e.Exit) + " at " + sha7(e.Head)
	if e.Item != "" {
		s = e.Item + " " + s
	}
	return s
}

// KeepEvidence is v with its text reduced to its evidence lines (in Message,
// Full empty), rebuilt from what EvidenceLines read: busmon keeps no verdict
// text (memory), and a check reads the same from what it keeps.
func KeepEvidence(v bus.Verdict) bus.Verdict {
	text := v.Message
	if v.Full != "" {
		text = v.Full
	}
	var lines []string
	for _, e := range EvidenceLines(text) {
		l := "check: " + e.Check + " => exit " + strconv.Itoa(e.Exit) + " at " + e.Head
		if e.Item != "" {
			l = e.Item + " " + l
		}
		lines = append(lines, l)
	}
	v.Message, v.Full = strings.Join(lines, "\n"), ""
	return v
}

// CheckProof is the deciding approval's evidence line for t's check (the
// passing one first), or false when the review is not approved or the line
// is absent: the task page's evidence row (§12.5).
func CheckProof(t Task, fact Fact, in Inputs) (Evidence, bool) {
	vs := subjectsVerdicts(t, fact, in)
	st, i := bus.RollUp(vs)
	if st != "APPROVED" { // an empty check matches no line
		return Evidence{}, false
	}
	d, want := vs[i], collapse(t.Check)
	var found *Evidence
	for _, e := range EvidenceLines(KeepEvidence(d).Message) {
		if e.Check != want {
			continue
		}
		if e.Exit == 0 && e.Head == d.Head {
			return e, true
		}
		if found == nil {
			found = &e
		}
	}
	if found == nil {
		return Evidence{}, false
	}
	return *found, true
}
