package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)

// fidelity states how much of a message's original text is actually in hand.
// The operator has to be able to tell four situations apart, because each one
// calls for a different next move: the text is all here; it was cut short when
// it was published (asking the author to resend is the only way back); only the
// one-line preview was ever stored; or the entry has aged out of the stream.
// Label is the claim, Tone colours it, Caveat is what weakens it.
type fidelity struct {
	Label  string
	Tone   string
	Caveat string
}

// unmarked is the caveat carried by every entry published before the
// Coordination v1 completeness marker existed. Per that contract an entry with
// no marker is explicitly *unknown*, even when nothing suggests it was cut —
// so busmon says "unverified" rather than quietly promising completeness.
const unmarked = "completeness not marked on this entry — published before the marker existed"

// classify maps an entry's stored text to what can honestly be said about it.
// textComplete is the Coordination v1 marker: "yes" complete, "no" truncated at
// publish, "" unmarked/legacy. preview is the bounded one-line field every entry
// carries; full is the retained multi-line text (reports only, and only when it
// says more than the preview).
func classify(kind, preview, full, textComplete string) fidelity {
	switch textComplete {
	case "yes":
		if full != "" {
			return fidelity{Label: fmt.Sprintf("complete — full text retained (%d chars)", runeLen(full)), Tone: "green"}
		}
		return fidelity{Label: "complete — this is the whole message as published", Tone: "green"}
	case "no":
		return fidelity{Label: "truncated at publish — the rest was never stored", Tone: "yellow"}
	}
	// No marker: the entry is from before Coordination v1. A trailing "…" is
	// NOT evidence of a cut — SanitizeReportMessage writes one when it
	// truncates, but an author may also simply have ended a sentence with it,
	// so reading it as proof would turn a guess into a claim. Unmarked entries
	// report what is stored and admit the rest is unknown.
	switch {
	case full != "":
		return fidelity{Label: fmt.Sprintf("retained text, %d chars", runeLen(full)), Tone: "yellow", Caveat: unmarked}
	case kind == "report":
		return fidelity{Label: "one-line preview, no further text retained", Tone: "yellow", Caveat: unmarked}
	default:
		return fidelity{Label: "the single text field this stream stores", Tone: "yellow", Caveat: unmarked}
	}
}

// missingFidelity is the fourth state: an id somebody still points at reads back
// empty. That is all it means — an empty range cannot tell a trimmed entry from
// a deleted one or from an id that never existed, so the label stops at "not
// retained" and does not name a cause.
func missingFidelity() fidelity {
	return fidelity{Label: "not retained — this id reads back empty (cause unknown)", Tone: "red"}
}

// eventFidelity classifies one feed event against the Coordination v1
// completeness marker: "yes" complete, "no" truncated at publish, and empty for
// every entry published before the marker existed — which classify reports as
// unverified rather than guessing from the text.
func eventFidelity(e bus.Event) fidelity {
	return classify(e.Kind, e.Message, e.Full, e.TextComplete)
}

// messageBody is the most complete text busmon holds for an entry: the retained
// full text when there is one, else the stored preview. Newlines are kept — the
// flat feed collapses them, this view is where they come back.
func messageBody(e bus.Event) string {
	if e.Full != "" {
		return e.Full
	}
	return e.Message
}

func runeLen(s string) int { return len([]rune(s)) }

// threadState is what busmon knows about a cmd entry's thread at render time.
// Loaded false means the read has not come back yet — rendered as "loading",
// never as "no replies", which would read as an answered request.
type threadState struct {
	ID      string
	Entries []bus.Event
	Loaded  bool
	Err     error
}

// detailKind names the stream and, where the stream has a sub-type, that too.
func detailKind(e bus.Event) string {
	switch e.Kind {
	case "report":
		if e.RKind != "" {
			return "report · " + e.RKind
		}
		return "report"
	case "cmd":
		if e.Type != "" {
			return "cmd · " + e.Type
		}
		return "cmd"
	case "":
		return "(unknown)"
	}
	return e.Kind
}

// detailFrom is the author: status/report carry an agent, notify/cmd a sender.
func detailFrom(e bus.Event) string {
	switch {
	case e.Agent != "":
		return e.Agent
	case e.From != "":
		return e.From
	}
	return "(unknown)"
}

// detailTo is the addressee. Only cmd entries have one; the other three streams
// are broadcasts, and saying so is more useful than an empty field.
func detailTo(e bus.Event) string {
	if e.Target != "" {
		return e.Target
	}
	return "(broadcast — every reader of this project)"
}

// threadID is the identity of an entry's thread: its ref when it carries one,
// else its own id (it is then the root). Only cmd entries thread; the other
// streams carry no correlation id and must not be given a made-up one.
func threadID(e bus.Event) string {
	if e.Kind != "cmd" {
		return ""
	}
	if e.Ref != "" {
		return e.Ref
	}
	return e.ID
}

// detailThread renders the thread row: the id, whether this entry is the root
// or a reply, and how much of the thread survives in the stream.
func detailThread(e bus.Event, th threadState) string {
	id := threadID(e)
	if id == "" {
		return tag("gray", "— (this stream carries no thread id)")
	}
	// A ref is whatever the sender chose to correlate on — `agentbus challenge`
	// refs are subjects like "pr-42", not stream ids — so "root" is only a
	// meaningful word when the thread id could name an entry at all.
	pos := "in thread"
	switch {
	case id == e.ID:
		pos = "thread root"
	case looksLikeStreamID(id):
		pos = "reply in thread"
	}
	out := id + "  " + tag("gray", "("+pos+")")
	switch {
	case th.Err != nil:
		out += "  " + tag("red", "[thread unreadable: "+th.Err.Error()+"]")
	case !th.Loaded:
		out += "  " + tag("gray", "[reading…]")
	default:
		word := "entries"
		if len(th.Entries) == 1 {
			word = "entry"
		}
		out += "  " + tag("gray", fmt.Sprintf("[%d retained %s]", len(th.Entries), word))
	}
	return out
}

// detailText renders the whole overlay: a metadata block, the full body, and —
// for a cmd — the thread transcript. Everything it claims is read from the
// entry; nothing is inferred from silence.
func detailText(e bus.Event, at, now time.Time, th threadState) string {
	var sb strings.Builder
	row := func(k, v string) { fmt.Fprintf(&sb, "%s  %s\n", tag("gray", pad(k, 6)), v) }

	row("kind", tview.Escape(detailKind(e)))
	row("from", tview.Escape(detailFrom(e)))
	row("to", tview.Escape(detailTo(e)))
	row("when", fmt.Sprintf("%s  %s", at.Format("2006-01-02 15:04:05"),
		tag("gray", "("+humanAge(now.Sub(at))+" ago)")))
	row("id", tview.Escape(e.ID))
	row("thread", detailThread(e, th))

	f := eventFidelity(e)
	row("text", tag(f.Tone, f.Label))
	if f.Caveat != "" {
		row("", tag("gray", f.Caveat))
	}
	if e.Kind == "status" && e.State != "" {
		row("state", tag(stateColor(e.State), e.State)+"  "+tag("gray", "(as declared by the agent)"))
	}

	sb.WriteString("\n")
	if body := messageBody(e); body != "" {
		sb.WriteString(tview.Escape(body))
	} else {
		sb.WriteString(tag("gray", "(no text)"))
	}
	sb.WriteString("\n")

	if threadID(e) != "" {
		sb.WriteString("\n" + threadSection(e, th, now))
	}
	return sb.String()
}

// threadSection lists the thread's retained entries oldest→newest. A root that
// is no longer in the stream is called out rather than hidden: "the rest of this
// exchange is gone" is the fact the operator needs when reconstructing a
// request, and an absent root is not an answered one.
func threadSection(e bus.Event, th threadState, now time.Time) string {
	id := threadID(e)
	var sb strings.Builder
	sb.WriteString(tag("gray", "── thread ──") + "\n")
	switch {
	case th.Err != nil:
		sb.WriteString(tag("red", "thread could not be read: "+th.Err.Error()) + "\n")
		return sb.String()
	case !th.Loaded:
		sb.WriteString(tag("gray", "reading the thread…") + "\n")
		return sb.String()
	case len(th.Entries) == 0:
		sb.WriteString(tag("yellow", "no entry of this thread is in the retained cmd stream") + "\n")
		sb.WriteString(tag("gray", missingFidelity().Label) + "\n")
		return sb.String()
	}
	rootHere := false
	for _, t := range th.Entries {
		if t.ID == id {
			rootHere = true
		}
		sb.WriteString(threadLine(t, now, t.ID == e.ID) + "\n")
	}
	// Only a stream-id thread names an entry that could be missing. A subject
	// ref ("pr-42") has no root entry to lose, and reporting one as absent
	// would invent a gap in an exchange that is whole.
	if !rootHere && looksLikeStreamID(id) {
		sb.WriteString(tag("yellow", "the thread root "+id+" is "+missingFidelity().Label) + "\n")
	}
	return sb.String()
}

// threadLine is one transcript row: time, type, sender→target, text. The entry
// the operator opened is marked so it can be found in its own thread.
func threadLine(e bus.Event, now time.Time, self bool) string {
	marker := "  "
	if self {
		marker = tag("aqua", "▸ ")
	}
	at := entryTime(e.ID)
	typ := e.Type
	if typ == "" {
		typ = "cmd"
	}
	return fmt.Sprintf("%s%s %s %s %s",
		marker,
		tag("gray", at.Format("15:04:05")),
		tag("fuchsia", pad(typ, 9)),
		tag("gray", pad(e.From+"→"+e.Target, 16)),
		tview.Escape(clip(oneLine(e.Message), 60)))
}

// oneLine flattens a body for a transcript row; the full text stays one Enter
// away on the entry itself.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// detailPlain is what `y` copies: the message text exactly as stored, newlines
// intact, no colour tags, no metadata framing.
func detailPlain(e bus.Event) string { return messageBody(e) }

// threadPlain is what `Y` copies: the whole retained exchange as plain text, so
// a request interrupted halfway can be pasted somewhere else in one piece.
func threadPlain(e bus.Event, th threadState) string {
	if !th.Loaded || len(th.Entries) == 0 {
		return detailPlain(e)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "thread %s\n", threadID(e))
	for _, t := range th.Entries {
		fmt.Fprintf(&sb, "\n[%s] %s %s → %s\n%s\n",
			entryTime(t.ID).Format("2006-01-02 15:04:05"),
			t.Type, t.From, t.Target, messageBody(t))
	}
	return sb.String()
}

// detailTitle names the overlay after what is open, with the keys that work in
// it — the overlay is modal, so its own keys have to be visible from inside.
func detailTitle(e bus.Event) string {
	return fmt.Sprintf(" MESSAGE · %s  [gray][↑↓/jk scroll · y copy text · Y copy thread · Esc back][-] ",
		detailKind(e))
}
