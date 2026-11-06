package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// What the shell and its helpers share: the writer it sends through, the
// reader's page kinds, a removal waiting for its y, the send results, and the
// Activity feed the tail fills.

// busWriter is what the shell sends and reads on demand. *bus.Bus satisfies it.
type busWriter interface {
	HumanCmd(ctx context.Context, from, target, command string) (string, error)
	HumanNotify(ctx context.Context, from, message string) (string, error)
	HumanClose(ctx context.Context, from, target, ref, message string) (string, error)
	Answer(ctx context.Context, askID, qid, by, choice, text string) (string, int, error)
	Asks(ctx context.Context) ([]bus.Ask, error)
	ForgetAgent(ctx context.Context, agent string) (bool, error)
	Thread(ctx context.Context, threadID string) ([]bus.Event, error)
	Resume(ctx context.Context, by, message string) (bool, error)
	SetRequestPriority(ctx context.Context, task, level, by string) (bool, error) // the person's, request-priority §6
}

// overlayKind is a reader page's kind (tea_reader.go): one per overlay that
// shows text.
type overlayKind int

const (
	ovRecap overlayKind = iota
	ovMessage
	ovAgent
	ovTask
	ovRequest
	ovRequests
	ovServices
	ovHealth
	ovHelp
	ovAsk
	ovPlan       // the batch's plan (P)
	ovPlanTask   // one task of the plan; Esc returns to the plan
	ovNotices    // a Requests notice group: its notices, one line each
	ovServiceLog // a service's log tail, opened from its F3 card
	ovRedis      // the Redis card's page (busmon-redis-detail), opened from F3
	ovTokens     // tokens (U): account windows, projects side by side, the open project by agent
	ovHistory    // history (H): the archive, then the live streams, newest first
	ovSettings   // settings (C): the registry's keys by group, changed in place (settings_ui.go)
	ovAbout      // about (A): the versions and the bus line (about.go)
	ovMerge      // a change waiting for the person's merge (merge-queue), its key the head
)

// pendingRemoval is a d that waits for its y: the next key confirms or drops it.
type pendingRemoval struct {
	key     string // "agent:<name>", a Requests row key
	label   string
	threads []reqThread // the Requests threads y closes, each once
}

// sendResult is what one send did (v0.16 §7); the caller closes or keeps.
type sendResult int

const (
	sendSent    sendResult = iota // everything sent: close, clear the draft
	sendPartial                   // @all, some served: close, clear the draft (a retry would send twice)
	sendHeld                      // a warning waits for a second Enter / Ctrl+S: keep the draft
	sendFailed                    // nothing sent: keep the draft
	sendRefused                   // empty body, unknown recipient: keep the draft
)

// draftKey identifies a held message: the recipient and the body.
func draftKey(tgt, body string) string { return tgt + "\x00" + body }

// ingestEvent folds a tailed status or report into the agents' states.
func ingestEvent(st *shared, e bus.Event) {
	if e.Kind != "status" && e.Kind != "report" {
		return
	}
	et := entryTime(e.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	a := st.agents[e.Agent]
	if a == nil {
		a = &agentState{}
		st.agents[e.Agent] = a
	}
	// A report proves the process is alive; it never refreshes the declared
	// state or the message beside it.
	if e.Kind == "status" {
		declare(a, e.State, e.Message, st.person)
		a.stateAt = et
	}
	a.lastSeen = et
}

// activityFeed is the Activity panel's lines: the tail's events, day
// separators and the unread marks.
type activityFeed struct {
	feed    []feedLine
	seq     int
	lastDay string
	unread  unreadState   // the person's unread marks (unread.go)
	rows    activityCache // its rows as last laid out (the lag fix)
	// The report fold (report-one-row §2), by report id: the seq of the line
	// showing that report, and of a notice line still waiting for it. A seq,
	// never an index: add re-slices the feed.
	reportLine map[string]int
	noticeLine map[string]int
}

// noticeOf is the report id a cmd entry is a notice of: a directive whose
// subject parses as reportNotice's and whose report id is its ref.
func noticeOf(e bus.Event) (string, bool) {
	if e.Kind != "cmd" || e.Type != bus.CmdDirective {
		return "", false
	}
	_, id, ok := reportSubject(e.Message)
	if !ok || id != e.Ref {
		return "", false
	}
	return id, true
}

// lineAt is the index of the line with that seq (the feed is in seq order).
func (f *activityFeed) lineAt(seq int) (int, bool) {
	i := sort.Search(len(f.feed), func(i int) bool { return f.feed[i].seq >= seq })
	return i, i < len(f.feed) && f.feed[i].seq == seq
}

// add appends one event to the feed (with a day separator on a date
// change), keeping the last feedCap lines. A report notice joins its report's
// line, and a report takes over a waiting notice's line in place.
func (f *activityFeed) add(e bus.Event, self string) {
	if f.reportLine == nil {
		f.reportLine, f.noticeLine = map[string]int{}, map[string]int{}
	}
	et := entryTime(e.ID)
	if personEntry(e, self) {
		f.unread.forPerson[e.ID] = true
	}
	if id, ok := noticeOf(e); ok {
		for _, seq := range []int{f.reportLine[id], f.noticeLine[id]} {
			if i, found := f.lineAt(seq); seq > 0 && found {
				fl := &f.feed[i]
				fl.notices = append(fl.notices, e)
				if f.unread.forPerson[e.ID] {
					fl.personNotices = append(fl.personNotices, e.ID)
				}
				fl.text = foldedPlain(*fl)
				return
			}
		}
	}
	if e.Kind == "report" {
		if seq := f.noticeLine[e.ID]; seq > 0 {
			if i, found := f.lineAt(seq); found {
				fl := &f.feed[i]
				fl.notices = append([]bus.Event{fl.ev}, fl.notices...)
				fl.ev, fl.at = e, et
				fl.text = foldedPlain(*fl)
				delete(f.noticeLine, e.ID)
				f.reportLine[e.ID] = seq
				return
			}
		}
	}
	if day := et.Format("2006-01-02"); day != f.lastDay {
		f.lastDay = day
		f.seq++
		_, plain := daySeparator(et)
		f.feed = append(f.feed, feedLine{id: strconv.Itoa(f.seq), seq: f.seq, sep: true, text: plain, at: et})
	}
	if last := len(f.feed) - 1; last >= 0 && sameFanOut(f.feed[last], e, et) {
		fl := &f.feed[last]
		if len(fl.to) == 0 {
			fl.to = []string{fl.ev.Target}
		}
		fl.to = append(fl.to, e.Target)
		fl.text = plainLine(fl.ev, fl.at) + " (+" + strconv.Itoa(len(fl.to)-1) + " more)"
		return
	}
	f.seq++
	fl := feedLine{id: strconv.Itoa(f.seq), seq: f.seq, text: plainLine(e, et), ev: e, at: et}
	if e.Kind == "report" {
		f.reportLine[e.ID] = f.seq
	} else if id, ok := noticeOf(e); ok {
		f.noticeLine[id] = f.seq
		if f.unread.forPerson[e.ID] {
			fl.personNotices = []string{e.ID}
		}
	}
	f.feed = append(f.feed, fl)
	if len(f.feed) > feedCap {
		f.feed = f.feed[len(f.feed)-feedCap:]
		floor := f.feed[0].seq
		for _, m := range []map[string]int{f.reportLine, f.noticeLine} {
			for id, seq := range m {
				if seq < floor {
					delete(m, id)
				}
			}
		}
	}
}

// foldedPlain is a folded line's plain text: its own, then the notices'
// targets (report-one-row §2, what y copies).
func foldedPlain(fl feedLine) string {
	s := plainLine(fl.ev, fl.at)
	if fl.ev.Kind != "report" {
		return s
	}
	return s + " -> " + strings.Join(noticeTargets(fl), ", ")
}

// lineOf is the event line that holds entry id, as its own or as a notice.
func (f *activityFeed) lineOf(id string) (feedLine, bool) {
	for _, fl := range f.feed {
		if !fl.sep && slices.Contains(lineIDs(fl), id) {
			return fl, true
		}
	}
	return feedLine{}, false
}

// lineIDs are every entry a line stands for: its own, then its notices'.
func lineIDs(fl feedLine) []string {
	ids := []string{fl.ev.ID}
	for _, n := range fl.notices {
		ids = append(ids, n.ID)
	}
	return ids
}

// unreadKey is a line's For you key: its first notice to the person when it
// has one (report-one-row §3), else its own entry.
func unreadKey(fl feedLine) string {
	if len(fl.personNotices) > 0 {
		return "unread:" + fl.personNotices[0]
	}
	return "unread:" + fl.ev.ID
}

// lineHeadline is what a report line or a notice line reads as: the
// report's headline (bus.ReportHeadline over its full text, else its
// preview), a notice's subject's headline; the text itself when it has none.
func lineHeadline(e bus.Event) string {
	switch {
	case e.Kind == "report":
		text := e.Message
		if e.Full != "" {
			text = e.Full
		}
		if h := bus.ReportHeadline(text); h != "" {
			return h
		}
		return e.Message
	case e.Kind == "cmd":
		if s, _, ok := reportSubject(e.Message); ok {
			if h := bus.ReportHeadline(s); h != "" {
				return h
			}
			return s
		}
	}
	return e.Message
}

// noticeTargets are the targets of a line's folded notices, each once.
func noticeTargets(fl feedLine) []string {
	var out []string
	for _, n := range fl.notices {
		if !slices.Contains(out, n.Target) {
			out = append(out, n.Target)
		}
	}
	return out
}

// plainLine is what y copies from an Activity line: the v0.8 plain format,
// unchanged (spec §3.4).
func plainLine(e bus.Event, et time.Time) string {
	ts := et.Format("15:04:05")
	switch e.Kind {
	case "status":
		s := ts + " [" + e.Agent + "] " + e.State
		if e.Message != "" {
			s += " | " + e.Message
		}
		return s
	case "notify":
		return ts + " [notify] " + e.Message
	case "cmd":
		label := "[" + e.Type + " " + e.From + "->" + e.Target
		if e.Ref != "" {
			label += " " + e.Ref
		}
		return ts + " " + label + "] " + e.Message
	case "report":
		return ts + " [report:" + e.RKind + "->" + e.Agent + "] " + e.Message + reportMarker(e.Full)
	}
	return ts + " " + e.Message
}

var panelNames = map[PanelID]string{PanelAgents: "Agents", PanelActivity: "Activity", PanelBoard: "Board", PanelRequests: "Requests", PanelPlan: "Plan"}

// panelLabel is a panel's name as the shell shows it, For you included.
func panelLabel(p PanelID) string {
	if p == PanelForYou {
		return "For you"
	}
	return panelNames[p]
}

// agentsLabel is the Agents frame's label: counts, the dormant hint and the
// scroll position.
func agentsLabel(shown, working, blocked, hidden int, showAll bool, sc ScrollState) string {
	label := fmt.Sprintf("%d agents %s %d working %s %d blocked", shown, GlyphNone, working, GlyphNone, blocked)
	if hidden > 0 {
		label += fmt.Sprintf(" %s %d dormant (a shows)", GlyphNone, hidden)
	} else if showAll {
		label += fmt.Sprintf(" %s all (a hides dormant)", GlyphNone)
	}
	if sc.Total > sc.Visible && sc.Visible > 0 {
		label += fmt.Sprintf(" %s %d-%d of %d", GlyphNone, sc.Top+1, min(sc.Total, sc.Top+sc.Visible), sc.Total)
	}
	return label
}
