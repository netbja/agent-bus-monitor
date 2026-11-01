package main

import (
	"context"
	"fmt"
	"strconv"
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
}

// add appends one event to the feed (with a day separator on a date
// change), keeping the last feedCap lines.
func (f *activityFeed) add(e bus.Event, self string) {
	et := entryTime(e.ID)
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
	if personEntry(e, self) {
		f.unread.forPerson[e.ID] = true
	}
	f.seq++
	fl := feedLine{id: strconv.Itoa(f.seq), seq: f.seq, text: plainLine(e, et), ev: e, at: et}
	f.feed = append(f.feed, fl)
	if len(f.feed) > feedCap {
		f.feed = f.feed[len(f.feed)-feedCap:]
	}
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
