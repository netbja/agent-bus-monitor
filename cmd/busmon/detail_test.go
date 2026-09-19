package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The four text states must never read alike: "complete", "cut when it was
// published", "only the preview survives" and "gone from the bus" each call for
// a different next move, and the whole point of the detail view is telling them
// apart.
func TestClassifyKeepsTheFourStatesDistinct(t *testing.T) {
	complete := classify("report", "hi", "hi there", "yes")
	truncated := classify("report", "hi…", "", "no")
	previewOnly := classify("report", "short note", "", "")
	gone := missingFidelity()

	labels := map[string]string{
		"complete":    complete.Label,
		"truncated":   truncated.Label,
		"previewOnly": previewOnly.Label,
		"gone":        gone.Label,
	}
	seen := map[string]string{}
	for name, l := range labels {
		if l == "" {
			t.Errorf("%s has an empty label", name)
		}
		if other, dup := seen[l]; dup {
			t.Errorf("%s and %s share the label %q", name, other, l)
		}
		seen[l] = name
	}
	if !strings.Contains(truncated.Label, "truncated") {
		t.Errorf("a known-truncated entry must say so: %q", truncated.Label)
	}
	if !strings.Contains(gone.Label, "not retained") || !strings.Contains(gone.Label, "cause unknown") {
		// An empty read cannot tell trimming from deletion, so the label must
		// report the observation and stop short of naming a cause.
		t.Errorf("an unreadable entry must say what is known and no more: %q", gone.Label)
	}
}

// A marked-complete entry is the only one allowed to claim completeness with no
// caveat; an unmarked one must carry the "unverified" note even when it looks
// whole (Coordination v1: an absent marker means unknown, not complete).
func TestClassifyCaveatsOnlyUnmarkedEntries(t *testing.T) {
	if c := classify("report", "hi", "hi there", "yes").Caveat; c != "" {
		t.Errorf("marked-complete entry should carry no caveat, got %q", c)
	}
	if c := classify("report", "hi…", "", "no").Caveat; c != "" {
		t.Errorf("marked-truncated entry should carry no caveat, got %q", c)
	}
	for name, f := range map[string]fidelity{
		"legacy full":        classify("report", "p", "a longer full text", ""),
		"legacy preview":     classify("report", "short note", "", ""),
		"legacy cmd":         classify("cmd", "do the thing", "", ""),
		"legacy cut preview": classify("report", "a long one…", "", ""),
	} {
		if f.Caveat == "" {
			t.Errorf("%s: an unmarked entry must say its completeness is unverified", name)
		}
	}
}

// A trailing "…" is not evidence. The sanitiser writes one when it truncates,
// but an author may equally have ended a sentence with it, so an unmarked entry
// stays unknown — reading the ellipsis as proof would turn a guess into a claim
// the operator would then act on.
func TestClassifyNeverInfersTruncationFromAnEllipsis(t *testing.T) {
	f := classify("report", "the beginning of a very long report…", "", "")
	if strings.Contains(f.Label, "truncated") {
		t.Errorf("an unmarked entry must not be declared truncated: %q", f.Label)
	}
	if f.Caveat == "" {
		t.Errorf("an unmarked entry must say its completeness is unverified: %+v", f)
	}
	// And the marked case still states it plainly.
	if marked := classify("report", "cut…", "", "no"); !strings.Contains(marked.Label, "truncated at publish") {
		t.Errorf("a marked-truncated entry must say so: %q", marked.Label)
	}
}

// A subject ref ("pr-42") is not a stream id, so there is no root entry that
// could be missing. Claiming one would invent a hole in a whole exchange.
func TestThreadSectionDoesNotInventAMissingRootForASubjectRef(t *testing.T) {
	e := bus.Event{ID: "900-0", Kind: "cmd", Type: bus.CmdChallenge, Ref: "pr-42",
		From: "foureyes", Target: "coder", Message: "explain the diff"}
	th := threadState{ID: "pr-42", Loaded: true, Entries: []bus.Event{e}}
	if got := threadSection(e, th, time.Unix(0, 0)); strings.Contains(got, "not retained") {
		t.Errorf("a subject ref has no root entry to lose:\n%s", got)
	}
	// A real stream id that is absent is still reported.
	reply := bus.Event{ID: "900-0", Kind: "cmd", Type: bus.CmdReply, Ref: "1758141663000-0",
		From: "coder", Target: "hermes", Message: "done"}
	th = threadState{ID: "1758141663000-0", Loaded: true, Entries: []bus.Event{reply}}
	if got := threadSection(reply, th, time.Unix(0, 0)); !strings.Contains(got, "not retained") {
		t.Errorf("an absent stream-id root must still be reported:\n%s", got)
	}
}

func TestClassifyRetainedFullTextReportsItsSize(t *testing.T) {
	f := classify("report", "preview", strings.Repeat("é", 1240), "")
	if !strings.Contains(f.Label, "1240 chars") {
		t.Errorf("label should count runes, not bytes: %q", f.Label)
	}
}

func TestMessageBodyPrefersRetainedFullText(t *testing.T) {
	e := bus.Event{Kind: "report", Message: "one line preview", Full: "line one\nline two"}
	if got := messageBody(e); got != "line one\nline two" {
		t.Errorf("messageBody = %q, want the retained full text", got)
	}
	if got := messageBody(bus.Event{Kind: "notify", Message: "hello"}); got != "hello" {
		t.Errorf("messageBody = %q, want the stored message when there is no full text", got)
	}
}

func TestDetailTextCarriesAuthorRecipientDateAndID(t *testing.T) {
	at := time.Date(2026, 9, 17, 22, 41, 3, 0, time.UTC)
	now := at.Add(12 * time.Minute)
	e := bus.Event{
		ID: "1758141663000-0", Kind: "cmd", Type: bus.CmdDirective,
		From: "hermes", Target: "coder", Ref: "1758141000000-0",
		Message: "rebase onto main and re-run the suite",
	}
	got := detailText(e, at, now, threadState{ID: e.Ref, Loaded: true, Entries: []bus.Event{e}})

	for _, want := range []string{
		"hermes",              // author
		"coder",               // recipient
		"2026-09-17 22:41:03", // absolute date
		"12m ago",             // age
		"1758141663000-0",     // entry id
		"1758141000000-0",     // thread id
		"reply in thread",     // position in the thread
		"rebase onto main",    // the body
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail is missing %q:\n%s", want, got)
		}
	}
}

// Newlines are the whole reason this view exists: the flat feed collapses them.
func TestDetailTextKeepsNewlinesAndUnicode(t *testing.T) {
	e := bus.Event{
		ID: "1-0", Kind: "report", Agent: "coder", RKind: bus.ReportNote,
		Message: "résumé: 3 étapes — fait",
		Full:    "résumé:\n  1. relire le contrat ✅\n  2. écrire les tests\n\t3. 日本語もOK",
	}
	got := detailText(e, time.Unix(0, 0), time.Unix(60, 0), threadState{})
	if !strings.Contains(got, "1. relire le contrat ✅\n") {
		t.Errorf("newlines must survive into the detail view:\n%s", got)
	}
	if !strings.Contains(got, "日本語もOK") {
		t.Errorf("unicode must survive into the detail view:\n%s", got)
	}
}

// `y` must put back exactly what the agent wrote — no framing, no tags, no
// re-wrapping — because the operator pastes it somewhere that matters.
func TestDetailPlainIsTheStoredTextVerbatim(t *testing.T) {
	full := "line one\n\nline three\twith a tab"
	e := bus.Event{Kind: "report", Message: "line one line three with a tab", Full: full}
	if got := detailPlain(e); got != full {
		t.Errorf("detailPlain = %q, want the stored text verbatim", got)
	}
}

// status/report/notify carry no correlation id. Showing one would be an
// invention; showing an empty field would look like a lost thread.
func TestDetailThreadSaysNoneForStreamsWithoutThreads(t *testing.T) {
	for _, kind := range []string{"status", "report", "notify"} {
		e := bus.Event{ID: "1-0", Kind: kind, Agent: "coder", Message: "x"}
		if id := threadID(e); id != "" {
			t.Errorf("%s: threadID = %q, want none", kind, id)
		}
		got := detailText(e, time.Unix(0, 0), time.Unix(0, 0), threadState{})
		if !strings.Contains(got, "no thread id") {
			t.Errorf("%s: detail should say the stream carries no thread id:\n%s", kind, got)
		}
		if strings.Contains(got, "── thread ──") {
			t.Errorf("%s: detail should not render a thread section:\n%s", kind, got)
		}
	}
}

func TestThreadIDIsRefElseOwnID(t *testing.T) {
	root := bus.Event{ID: "10-0", Kind: "cmd", Type: bus.CmdChallenge}
	if got := threadID(root); got != "10-0" {
		t.Errorf("a cmd with no ref is its own thread root, got %q", got)
	}
	reply := bus.Event{ID: "11-0", Kind: "cmd", Type: bus.CmdReply, Ref: "10-0"}
	if got := threadID(reply); got != "10-0" {
		t.Errorf("a cmd with a ref belongs to that thread, got %q", got)
	}
}

// A thread whose root has been trimmed out of the capped stream must say so.
// Quietly listing the survivors would read as the whole exchange.
func TestThreadSectionFlagsAMissingRoot(t *testing.T) {
	now := time.Unix(1000, 0)
	reply := bus.Event{ID: "900-0", Kind: "cmd", Type: bus.CmdReply, Ref: "100-0", From: "coder", Target: "hermes", Message: "done"}
	th := threadState{ID: "100-0", Loaded: true, Entries: []bus.Event{reply}}
	got := threadSection(reply, th, now)
	if !strings.Contains(got, "100-0") || !strings.Contains(got, "not retained") {
		t.Errorf("a missing thread root must be called out:\n%s", got)
	}
}

// Before the read comes back, the thread is unknown — not empty. "No replies"
// on a request that has one would send the operator down the wrong path.
func TestThreadSectionDistinguishesLoadingFromEmpty(t *testing.T) {
	e := bus.Event{ID: "5-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder"}
	loading := threadSection(e, threadState{ID: "5-0"}, time.Unix(0, 0))
	if !strings.Contains(loading, "reading") {
		t.Errorf("an unloaded thread must read as loading:\n%s", loading)
	}
	empty := threadSection(e, threadState{ID: "5-0", Loaded: true}, time.Unix(0, 0))
	if !strings.Contains(empty, "no entry of this thread is in the retained cmd stream") {
		t.Errorf("an empty loaded thread must say the entries are gone:\n%s", empty)
	}
}

func TestThreadPlainCopiesTheWholeExchange(t *testing.T) {
	root := bus.Event{ID: "100-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "review PR 42"}
	reply := bus.Event{ID: "200-0", Kind: "cmd", Type: bus.CmdReply, Ref: "100-0", From: "coder", Target: "hermes", Message: "found two issues"}
	got := threadPlain(root, threadState{ID: "100-0", Loaded: true, Entries: []bus.Event{root, reply}})
	for _, want := range []string{"review PR 42", "found two issues", "thread 100-0"} {
		if !strings.Contains(got, want) {
			t.Errorf("thread copy missing %q:\n%s", want, got)
		}
	}
}

// A thread that never loaded still has to copy something useful: the entry the
// operator is actually looking at.
func TestThreadPlainFallsBackToTheEntry(t *testing.T) {
	e := bus.Event{ID: "1-0", Kind: "cmd", Type: bus.CmdDirective, From: "a", Target: "b", Message: "the ask"}
	if got := threadPlain(e, threadState{ID: "1-0"}); got != "the ask" {
		t.Errorf("threadPlain fallback = %q, want the entry body", got)
	}
}
