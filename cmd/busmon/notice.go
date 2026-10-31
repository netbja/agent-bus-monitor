package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// The notice (spec docs/superpowers/specs/2026-10-31-busmon-notice-design.md):
// a quiet line at the right end of the footer for what happened by itself
// while busmon runs. One at a time, no bell, no mark, no count; never a
// decision, never the person's own act. Its queue and clock live in the
// Model, set on the update path; View draws m.now against them.

type noticeLevel int

const (
	noticeInfo      noticeLevel = iota // something started or changed on its own
	noticeDone                         // something finished well
	noticeAttention                    // something to look at, not an emergency
)

const (
	noticeShow     = 4 * time.Second // alone
	noticeShort    = 2 * time.Second // while others wait
	noticeBlend    = time.Second     // the last part of either, blended out (true colour)
	noticeQueueMax = 5               // waiting; the rest collapse into one line
	swapLedgerPage = 50
)

type notice struct {
	text  string
	level noticeLevel
}

func (n notice) glyph() string {
	switch n.level {
	case noticeDone:
		return GlyphDone
	case noticeAttention:
		return GlyphBlocked
	}
	return GlyphNone
}

func (n notice) role() Role {
	switch n.level {
	case noticeDone:
		return RoleOK
	case noticeAttention:
		return RoleAttention
	}
	return RoleMuted
}

// noticeState is the queue, the notice on screen and the last poll's facts
// the next poll is compared with, behind a pointer like the lists.
type noticeState struct {
	queue []notice
	more  int       // collapsed past noticeQueueMax
	cur   *notice   // on screen
	start time.Time // when cur was put on screen, moved on by the time it was held
	held  time.Time // since when cur is held under a flash or a dialog; zero = not held

	pauseKnown bool // a pause read succeeded: the next one can be a transition
	pause      *bus.PauseRecord
	health     map[string]guardian.Record
}

func (n *noticeState) push(x notice) {
	if len(n.queue) < noticeQueueMax {
		n.queue = append(n.queue, x)
		return
	}
	n.more++
}

// duration is how long the notice on screen stays: shorter while others wait.
func (n *noticeState) duration() time.Duration {
	if len(n.queue) > 0 || n.more > 0 {
		return noticeShort
	}
	return noticeShow
}

// advance drops the notice whose time is up and, unless held, puts the next
// on screen at now; it reports whether one went on screen. The one on screen
// does not age while held: it keeps the time it had left.
func (n *noticeState) advance(now time.Time, held bool) bool {
	if n.cur != nil {
		switch {
		case held && n.held.IsZero():
			n.held = now
		case !held && !n.held.IsZero():
			n.start = n.start.Add(now.Sub(n.held))
			n.held = time.Time{}
		}
		if held {
			return false
		}
	}
	if n.cur != nil && now.Sub(n.start) >= n.duration() {
		n.cur = nil
	}
	if n.cur != nil || held {
		return false
	}
	switch {
	case len(n.queue) > 0:
		x := n.queue[0]
		n.queue = n.queue[1:]
		n.cur = &x
	case n.more > 0:
		what := "actions"
		if n.more == 1 {
			what = "action"
		}
		n.cur = &notice{text: fmt.Sprintf("%d more automatic %s (R: recap)", n.more, what), level: noticeInfo}
		n.more = 0
	default:
		return false
	}
	n.start = now
	return true
}

// automatic is an author whose act may be a notice: the dispatcher or a
// registered agent, never the person.
func automatic(s snapshot, self, by string) bool {
	if by == "" || by == self {
		return false
	}
	if by == "dispatch" {
		return true
	}
	a, ok := s.agents[by]
	return ok && a.registered
}

// fromPoll queues the notices of a poll against the last one (none at the
// first: it is the baseline) and remembers this poll's facts.
func (n *noticeState) fromPoll(s snapshot, self string, swaps []bus.SwapEvent, lost bool) {
	if !s.pauseFailed {
		if p := s.pause; n.pauseKnown && n.pause == nil && p != nil && automatic(s, self, p.By) {
			n.push(pauseNotice(*p))
		}
		n.pause, n.pauseKnown = s.pause, true
	}
	if s.guardianHealth != nil {
		if n.health != nil {
			for _, x := range healthNotices(n.health, s.guardianHealth, s.guardianNotes) {
				n.push(x)
			}
		}
		n.health = s.guardianHealth
	}
	for _, e := range swaps {
		if automatic(s, self, e.By) {
			if x, ok := swapNotice(e); ok {
				n.push(x)
			}
		}
	}
	if lost {
		n.push(notice{text: "some reloads or swaps were trimmed before busmon read them (agentbus swaps)", level: noticeAttention})
	}
}

func pauseNotice(p bus.PauseRecord) notice {
	until := p.UntilTime().Local().Format("15:04")
	if prov, ok := strings.CutPrefix(p.Reason, "quota "); ok && p.By == "dispatch" && prov != "" && p.Reason == bus.QuotaPauseReason(prov) {
		return notice{text: fmt.Sprintf("dispatch: paused for %s's quota until %s", Preview(prov), until), level: noticeAttention}
	}
	text := fmt.Sprintf("%s: project paused until %s", Preview(p.By), until)
	if p.Reason != "" {
		text += " (" + Preview(p.Reason) + ")"
	}
	return notice{text: text, level: noticeInfo}
}

// healthNotices are the checks that worsened or recovered between two reads;
// a check absent from the last read is a baseline. A fail now ok with a
// sentinel note written since the fail began shows that note, attributed to
// its author and never read as a claimed restart (the record names no
// service; master's ruling); otherwise the check is ok again.
func healthNotices(prev, cur map[string]guardian.Record, notes map[string]guardian.Note) []notice {
	ids := make([]string, 0, len(cur))
	for id := range cur {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []notice
	for _, id := range ids {
		old, ok := prev[id]
		r := cur[id]
		if !ok || old.Status == r.Status {
			continue
		}
		detail := ""
		if r.Detail != "" {
			detail = " (" + Preview(r.Detail) + ")"
		}
		switch {
		case r.Status == guardian.Fail:
			out = append(out, notice{text: "health: " + id + " fails" + detail, level: noticeAttention})
		case old.Status == guardian.OK && r.Status == guardian.Warn:
			out = append(out, notice{text: "health: " + id + " warns" + detail, level: noticeAttention})
		case r.Status == guardian.OK && (old.Status == guardian.Warn || old.Status == guardian.Fail):
			if note, ok := notes[id]; ok && old.Status == guardian.Fail && note.Author == "sentinel" && note.At > old.Since && note.Text != "" {
				out = append(out, notice{text: "sentinel: " + Preview(note.Text), level: noticeDone})
				continue
			}
			out = append(out, notice{text: "health: " + id + " ok again", level: noticeDone})
		}
	}
	return out
}

// swapNotice is a ledger entry's line; the entries the spec names, no other.
func swapNotice(e bus.SwapEvent) (notice, bool) {
	role, reason := Preview(e.Role), ""
	if e.Reason != "" {
		reason = ": " + Preview(e.Reason)
	}
	reload := e.Kind == bus.SwapKindReload
	switch {
	case reload && e.Event == bus.SwapBegin:
		return notice{text: Preview(e.By) + " reloads " + role + reason, level: noticeInfo}, true
	case reload && e.Event == bus.SwapReloaded:
		return notice{text: role + " reloaded", level: noticeDone}, true
	case reload && e.Event == bus.SwapFailed:
		return notice{text: role + ": reload failed (agentbus swaps)", level: noticeAttention}, true
	case !reload && e.Event == bus.SwapDone:
		return notice{text: role + " now runs on " + Preview(e.ToProfile) + reason, level: noticeInfo}, true
	case !reload && e.Event == bus.SwapBack:
		return notice{text: role + " back on its own profile", level: noticeDone}, true
	}
	return notice{}, false
}

// tailNotice is a live directive's line: the dispatcher's resume and its
// automatic /clear, by their exact prefixes; nothing else.
func tailNotice(e bus.Event) (notice, bool) {
	if e.Kind != "cmd" || e.From != "dispatch" || e.Human || e.Type != bus.CmdDirective {
		return notice{}, false
	}
	switch {
	case strings.HasPrefix(e.Message, "Project resumed: the pause"):
		return notice{text: "project resumed", level: noticeDone}, true
	case strings.HasPrefix(e.Message, "context cleared:"):
		return notice{text: Preview(e.Target) + ": context cleared after its hand-off", level: noticeInfo}, true
	}
	return notice{}, false
}

// swapCursor is busmon's place in the ledger: the first read records it and
// the count (the baseline), each later one passes its events on.
type swapCursor struct {
	ok    bool
	last  string
	added int64
}

// after is the cursor to read from; "" before the baseline (no events read).
func (c *swapCursor) after() string {
	if !c.ok {
		return ""
	}
	return c.last
}

// advance takes a read: its new events, and whether entries were trimmed
// before they were read (entries-added grew by more than the events read).
// A count that went down (the ledger began again) is a new baseline.
func (c *swapCursor) advance(ev []bus.SwapEvent, added int64, last string) ([]bus.SwapEvent, bool) {
	if !c.ok || added < c.added {
		*c = swapCursor{ok: true, last: last, added: added}
		return nil, false
	}
	lost := added-c.added > int64(len(ev))
	c.last, c.added = last, added
	return ev, lost
}

// takeSwapNews hands the ledger's new entries over once.
func takeSwapNews(st *shared) ([]bus.SwapEvent, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	ev, lost := st.swapNews, st.swapLost
	st.swapNews, st.swapLost = nil, false
	return ev, lost
}

// noticeHeld: a flash or a dialog is up, so the next notice waits.
func (m *Model) noticeHeld() bool {
	u := m.ui
	return u.flash != "" || u.compose != nil || u.pausePopup != nil || u.reminder != nil || u.summary ||
		u.tour != nil || u.confirm != nil || u.askChoose != nil
}

// noticeLine is the notice on screen, at most width cells, blended out over
// its last second in true colour with the fade on; nil when none shows.
func (m Model) noticeLine(width int) Line {
	n := m.notice.cur
	if n == nil {
		return nil
	}
	fade := 0
	if m.cfg.settings.Fade && th.Animated() {
		d := m.notice.duration()
		at := m.now
		if !m.notice.held.IsZero() { // held: it stays as it was when held
			at = m.notice.held
		}
		if e := at.Sub(m.notice.start); e > d-noticeBlend {
			fade = max(1, int(100*(1-float64(e-(d-noticeBlend))/float64(noticeBlend))))
		}
	}
	l := Line{{Text: n.glyph(), Role: n.role(), Fade: fade}, {Text: " " + n.text, Role: n.role(), Fade: fade}}
	return FitLine(l, min(l.Width(), width))
}

// footerWithNotice is the footer with the notice at its right end: the hints
// give way from the right, as they do for lack of room.
func (m Model) footerWithNotice(n Line) Line {
	room := m.width / 2
	n = FitLine(n, min(n.Width(), room))
	nw := n.Width() + 1 // a blank column after it
	hints := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, m.width-nw-2, m.pageOffers)
	l := append(Line{}, hints...)
	l = append(l, Seg{Text: strings.Repeat(" ", max(0, m.width-hints.Width()-nw))})
	l = append(l, n...)
	l = append(l, Seg{Text: " "})
	return FitLine(l, m.width)
}
