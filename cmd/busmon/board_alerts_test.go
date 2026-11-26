package main

import (
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// views-redesign §3: two settings say when a tracked request is late,
// board.no_accept_after (30 minutes) and board.no_reply_after (2 hours), 0
// off; in Shift+C's Display group, applied at once.
func TestBoardAlertSettings(t *testing.T) {
	got := loadBusmonSettings(settingsDir(t, "", "", nil), settingsFlags{})
	if got.NoAcceptAfter != 30*time.Minute || got.NoReplyAfter != 2*time.Hour {
		t.Fatalf("defaults: %v %v", got.NoAcceptAfter, got.NoReplyAfter)
	}
	got = loadBusmonSettings(settingsDir(t, "[board]\nno_accept_after = \"0\"\nno_reply_after = \"45m\"\n", "", nil), settingsFlags{})
	if got.NoAcceptAfter != 0 || got.NoReplyAfter != 45*time.Minute {
		t.Fatalf("file: %v %v", got.NoAcceptAfter, got.NoReplyAfter)
	}
	for _, name := range []string{"board.no_accept_after", "board.no_reply_after"} {
		k, ok := config.Lookup(name)
		if !ok || k.Group() != config.GroupDisplay || k.Kind != config.Duration || !strings.Contains(k.ReadBy, "busmon") {
			t.Fatalf("%s: %+v", name, k)
		}
		if settingLabels[name] == "" || settingHelps[name] == "" {
			t.Fatalf("%s: no label or help on the page", name)
		}
	}
	m := &Model{cfg: shellConfig{getenv: settingsDir(t, "[board]\nno_accept_after = \"10m\"\nno_reply_after = \"1h\"\n", "", nil)}}
	if w := m.applySettings("board.no_accept_after"); w != "applied and saved" || m.cfg.settings.NoAcceptAfter != 10*time.Minute {
		t.Fatalf("no_accept_after: %q %v", w, m.cfg.settings.NoAcceptAfter)
	}
	if w := m.applySettings("board.no_reply_after"); w != "applied and saved" || m.cfg.settings.NoReplyAfter != time.Hour {
		t.Fatalf("no_reply_after: %q %v", w, m.cfg.settings.NoReplyAfter)
	}
}

// boardAlertFixture: one late acceptance (40m, sent before the reply's
// accept), one late reply (accepted 3h ago), one lost body, one on time, one
// plain board task, one done.
func boardAlertFixture() map[string]bus.BoardEntry {
	ms := func(d time.Duration) int64 { return boardNow.Add(-d).UnixMilli() }
	at := func(d time.Duration) int64 { return boardNow.Add(-d).Unix() }
	return map[string]bus.BoardEntry{
		"late-accept": {Owner: "coder", State: "requested", Updated: at(40 * time.Minute),
			Request: &bus.RequestInfo{ID: "1-0", From: "master", Target: "coder", CreatedAt: ms(40 * time.Minute)}},
		"late-reply": {Owner: "coder2", State: "accepted", Updated: at(time.Minute),
			Request: &bus.RequestInfo{ID: "2-0", From: "master", Target: "coder2", CreatedAt: ms(4 * time.Hour), AcceptedAt: ms(3 * time.Hour)}},
		"lost": {Owner: "docs", State: "requested", Updated: at(2 * time.Minute),
			Request: &bus.RequestInfo{ID: "3-0", From: "master", Target: "docs", CreatedAt: ms(2 * time.Minute), Availability: "missing"}},
		"on-time": {Owner: "coder", State: "accepted", Updated: at(5 * time.Minute),
			Request: &bus.RequestInfo{ID: "4-0", From: "master", Target: "coder", CreatedAt: ms(20 * time.Minute), AcceptedAt: ms(10 * time.Minute)}},
		"plain":    {Owner: "architect", State: "working", Updated: at(3 * time.Minute)},
		"finished": {Owner: "coder", State: "done", Updated: at(time.Minute)},
	}
}

var boardDefaults = boardOpts{noAccept: 30 * time.Minute, noReply: 2 * time.Hour}

// views-redesign §3: one list, the alerts first (oldest alert first), then
// the others newest first; done hidden by default; the alert column only
// while a row has an alert; the label counts open and done.
func TestBoardViewAlerts(t *testing.T) {
	v := boardViewOf(boardAlertFixture(), boardNow, 120, boardDefaults)
	if got := keysOf(v.items); strings.Join(got, " ") != "late-reply late-accept lost plain on-time" {
		t.Fatalf("order %q", got)
	}
	if h := v.header.text(); !strings.HasPrefix(h, "ALERT") || !strings.Contains(h, "TASK") || !strings.Contains(h, "OWNER") || !strings.Contains(h, "STATE") || !strings.Contains(h, "CHANGED") || strings.Contains(h, "AGE") {
		t.Fatalf("header %q", h)
	}
	want := map[string]string{"late-reply": GlyphWarn + " no reply 3h since accept", "late-accept": GlyphWarn + " no acceptance 40m since request", "lost": GlyphWarn + " body lost (request 2m)"}
	for _, it := range v.items {
		text := it.Line.text()
		if w, ok := want[it.Key]; ok != strings.HasPrefix(text, w) && ok {
			t.Errorf("%s: %q, want it to start with %q", it.Key, text, w)
		}
		if _, ok := want[it.Key]; !ok && strings.Contains(text, GlyphWarn) {
			t.Errorf("%s has an alert: %q", it.Key, text)
		}
		if it.Line.Width() != 120 {
			t.Errorf("%s width %d", it.Key, it.Line.Width())
		}
	}
	if l := v.label.text(); l != "5 open "+GlyphNone+" 1 done (d)" {
		t.Fatalf("label %q", l)
	}
	if f := v.footer.text(); f != "enter diagnostics "+GlyphNone+" d done "+GlyphNone+" resend (dispatch) "+GlyphNone+" cancel/reassign (master)" {
		t.Fatalf("footer %q", f)
	}
	// d shows the done tasks, after the others.
	o := boardDefaults
	o.showDone = true
	if got := keysOf(boardViewOf(boardAlertFixture(), boardNow, 120, o).items); got[len(got)-1] != "finished" || len(got) != 6 {
		t.Fatalf("with done: %q", got)
	}
	// 0 turns a setting off; a lost body is said at once, always.
	o = boardOpts{}
	v = boardViewOf(boardAlertFixture(), boardNow, 120, o)
	if keysOf(v.items)[0] != "lost" || strings.Count(strings.Join(texts(v.items), "\n"), GlyphWarn) != 1 {
		t.Fatalf("settings off: %q", texts(v.items))
	}
}

// No alert: no alert column, never a column of blanks; the footer has no
// action words.
func TestBoardViewNoAlertNoColumn(t *testing.T) {
	m := boardAlertFixture()
	delete(m, "late-accept")
	delete(m, "late-reply")
	delete(m, "lost")
	v := boardViewOf(m, boardNow, 80, boardDefaults)
	if h := v.header.text(); !strings.HasPrefix(h, "TASK") || strings.Contains(h, "ALERT") {
		t.Fatalf("header %q", h)
	}
	if f := v.footer.text(); f != "enter diagnostics "+GlyphNone+" d done" {
		t.Fatalf("footer %q", f)
	}
}

// At 80 columns the alert takes the first 12 cells, cut with …; the task
// gives up its room first.
func TestBoardViewNarrowAlert(t *testing.T) {
	v := boardViewOf(boardAlertFixture(), boardNow, 80, boardDefaults)
	first := v.items[0].Line.text()
	if !strings.HasPrefix(first, GlyphWarn+" no reply"+GlyphEllipsis+" ") || Width(first[:strings.Index(first, "late-reply")]) != 12 {
		t.Fatalf("row %q", first)
	}
	if !strings.Contains(first, "coder2") || !strings.Contains(first, "accepted") {
		t.Fatalf("owner or state cut: %q", first)
	}
}

// Master down (views-redesign §3, the person's Q2 and Q8): an alert row says
// self-repair relaunches it; cancel and reassign leave the footer, resend
// (the dispatcher's) stays.
func TestBoardViewMasterDown(t *testing.T) {
	o := boardDefaults
	o.masterDown = true
	v := boardViewOf(boardAlertFixture(), boardNow, 160, o)
	if !strings.Contains(v.items[0].Line.text(), "master is down; self-repair relaunches it") {
		t.Fatalf("alert row %q", v.items[0].Line.text())
	}
	if f := v.footer.text(); strings.Contains(f, "cancel") || !strings.Contains(f, "resend (dispatch)") {
		t.Fatalf("footer %q", f)
	}
	for _, it := range v.items[3:] { // no alert: nothing to say
		if strings.Contains(it.Line.text(), "master is down") {
			t.Fatalf("%s: %q", it.Key, it.Line.text())
		}
	}
	a := map[string]*agentState{"master": {state: "working", pane: "t1", host: hostFacts{termsRead: true, live: true}}}
	if masterDown(a, "master") {
		t.Fatal("a live master is down")
	}
	if !masterDown(nil, "master") || !masterDown(map[string]*agentState{"master": {state: "working", pane: "t1", host: hostFacts{termsRead: true, live: false}}}, "master") {
		t.Fatal("a master with no terminal, or none at all, is not down")
	}
}

func texts(its []ListItem) []string {
	var out []string
	for _, it := range its {
		out = append(out, it.Line.text())
	}
	return out
}

// In the shell: the Board's footer is on its frame; d shows and folds the
// done tasks; Enter on a tracked request opens its diagnostics (the request's
// page), on a plain task its task page.
func TestBoardShellKeys(t *testing.T) {
	pinLocal(t)
	m := shellWith(t, 140, 40, shellConfig{}, false)
	snap := m.snap
	snap.board = maps.Clone(snap.board)
	snap.board["late"] = bus.BoardEntry{Owner: "coder2", State: "requested", Updated: shellNow.Add(-40 * time.Minute).Unix(),
		Request: &bus.RequestInfo{ID: "9-0", From: "master", Target: "coder2", CreatedAt: shellNow.Add(-40 * time.Minute).UnixMilli()}}
	m.cfg.settings.NoAcceptAfter = 30 * time.Minute
	m = keys(t, step(t, m, pollMsg{snap: snap, at: shellNow}), '2')
	if m.ui.focus != PanelBoard {
		t.Fatalf("focus %v", m.ui.focus)
	}
	if got := keysOf(m.lists.board.items); strings.Join(got, " ") != "late busmon-charm-b" {
		t.Fatalf("rows %q", got)
	}
	if !strings.Contains(m.grid().plain(), "enter diagnostics "+GlyphNone+" d done "+GlyphNone+" resend (dispatch)") {
		t.Fatalf("no footer on the frame:\n%s", m.grid().plain())
	}
	m = keys(t, m, 'd')
	if got := keysOf(m.lists.board.items); !m.ui.boardDone || got[len(got)-1] != "grok-adapter" {
		t.Fatalf("d: %v %q", m.ui.boardDone, got)
	}
	if m.rd.open() {
		t.Fatal("d on the Board opened a page")
	}
	m = keys(t, m, 'd')
	if m.ui.boardDone {
		t.Fatal("d again: the done tasks stay")
	}
	m.lists.board.Select("late")
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p == nil || p.kind != ovRequest || p.key != "req:late" {
		t.Fatalf("enter on a tracked request: %+v", p)
	}
	m = keys(t, m, kEsc)
	m.lists.board.Select("busmon-charm-b")
	if p := keys(t, m, kEnter).rd.top(); p == nil || p.kind != ovTask || p.key != "busmon-charm-b" {
		t.Fatalf("enter on a plain task: %+v", p)
	}
}

// The Board as the mock-ups draw it (views-redesign §3): at 160 columns one
// alert, its column first and the actions on the bottom border; at 80
// columns no alert, no alert column. The golden is the Board's frame only.
func TestBoardGoldens(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	at := func(d time.Duration) int64 { return shellNow.Add(-d).Unix() }
	ms := func(d time.Duration) int64 { return shellNow.Add(-d).UnixMilli() }
	req := func(target string, created, accepted time.Duration) *bus.RequestInfo {
		r := &bus.RequestInfo{ID: "1-0", From: "master", Target: target, CreatedAt: ms(created)}
		if accepted > 0 {
			r.AcceptedAt = ms(accepted)
		}
		return r
	}
	board := map[string]bus.BoardEntry{
		"quota-step-3":         {Owner: "coder", State: "accepted", Updated: at(3 * time.Hour), Request: req("coder", 4*time.Hour, 3*time.Hour)},
		"ask-refuses-person":   {Owner: "coder2", State: "working", Updated: at(12 * time.Minute)},
		"agent-screen-blocked": {Owner: "coder", State: "working", Updated: at(40 * time.Minute)},
		"views-redesign-spec":  {Owner: "coder2", State: "accepted", Updated: at(5 * time.Minute), Request: req("coder2", 6*time.Minute, 5*time.Minute)},
		"holder-g1-part2":      {Owner: "coder", State: "requested", Updated: at(time.Hour)},
		"grok-adapter":         {Owner: "coder", State: "done", Updated: at(3 * time.Hour)},
	}
	for _, c := range []struct {
		name string
		w    int
		drop string
	}{{"board-160x20-alert.txt", 160, ""}, {"board-80x20-no-alert.txt", 80, "quota-step-3"}} {
		m := shellWith(t, c.w, 20, shellConfig{}, false)
		m.cfg.settings.NoAcceptAfter, m.cfg.settings.NoReplyAfter = 30*time.Minute, 2*time.Hour
		snap := m.snap
		snap.board = maps.Clone(board)
		delete(snap.board, c.drop)
		m = step(t, m, pollMsg{snap: snap, at: shellNow})
		key := rune('2')
		if m.layout.Tier == TierNarrow { // one view at a time: the Board is the fourth
			key = '4'
		}
		m = keys(t, m, key)
		r := m.layout.rect(PanelBoard)
		if m.ui.focus != PanelBoard || r.W == 0 {
			t.Fatalf("%s: no Board (focus %v, rect %+v)", c.name, m.ui.focus, r)
		}
		lines := strings.Split(m.grid().plain(), "\n")
		var frame []string
		for y := r.Y; y < r.Y+r.H && y < len(lines); y++ {
			frame = append(frame, strings.TrimRight(string([]rune(lines[y])[r.X:r.X+r.W]), " "))
		}
		checkGolden(t, c.name, strings.Join(frame, "\n")+"\n")
	}
}

// foureyes: a closed request has no alert, its body lost or not (the
// closed-unaccepted request shown under d): no ⚠, no alert column.
func TestBoardViewClosedRequestNoAlert(t *testing.T) {
	ms := boardNow.Add(-time.Hour).UnixMilli()
	m := map[string]bus.BoardEntry{
		"lost-done":   {Owner: "coder", State: "done", Updated: boardNow.Unix(), Request: &bus.RequestInfo{ID: "1-0", Target: "coder", CreatedAt: ms, Availability: "missing", ClosedUnaccepted: "body missing"}},
		"superseded":  {Owner: "coder", State: "superseded", Updated: boardNow.Unix(), Request: &bus.RequestInfo{ID: "2-0", Target: "coder", CreatedAt: ms, Availability: "missing"}},
		"plain-alive": {Owner: "coder2", State: "working", Updated: boardNow.Unix()},
	}
	v := boardViewOf(m, boardNow, 120, boardOpts{noAccept: time.Minute, noReply: time.Minute, showDone: true})
	if h := v.header.text(); strings.Contains(h, "ALERT") {
		t.Fatalf("header %q", h)
	}
	if all := strings.Join(texts(v.items), "\n"); strings.Contains(all, GlyphWarn) {
		t.Fatalf("a closed request alerts:\n%s", all)
	}
}

// foureyes: the shell computes master's liveness from the agents. With
// master's terminal gone, an alert row says so and cancel/reassign leave the
// footer; with master gone from the agents, the same.
func TestBoardShellMasterDown(t *testing.T) {
	pinLocal(t)
	for _, c := range []struct {
		name   string
		master func(map[string]*agentState)
	}{
		{"no terminal", func(a map[string]*agentState) {
			c := *a["master"]
			c.pane, c.host.termsRead, c.host.live = "t9", true, false
			a["master"] = &c
		}},
		{"no entry", func(a map[string]*agentState) { delete(a, "master") }},
	} {
		m := shellWith(t, 160, 40, shellConfig{}, false)
		m.cfg.settings.NoAcceptAfter = 30 * time.Minute
		snap := m.snap
		snap.board = maps.Clone(snap.board)
		snap.board["late"] = bus.BoardEntry{Owner: "coder2", State: "requested", Updated: shellNow.Add(-40 * time.Minute).Unix(),
			Request: &bus.RequestInfo{ID: "9-0", From: "master", Target: "coder2", CreatedAt: shellNow.Add(-40 * time.Minute).UnixMilli()}}
		snap.agents = maps.Clone(snap.agents)
		c.master(snap.agents)
		m = keys(t, step(t, m, pollMsg{snap: snap, at: shellNow}), '2')
		screen := m.grid().plain()
		if !strings.Contains(screen, "master is down; self-repair relaunches it") || strings.Contains(screen, "cancel/reassign (master)") || !strings.Contains(screen, "resend (dispatch)") {
			t.Fatalf("%s:\n%s", c.name, screen)
		}
	}
	// A live master: the footer names master's actions, no row says it is down.
	m := shellWith(t, 160, 40, shellConfig{}, false)
	m.cfg.settings.NoAcceptAfter = 30 * time.Minute
	snap := m.snap
	snap.board = maps.Clone(snap.board)
	snap.board["late"] = bus.BoardEntry{Owner: "coder2", State: "requested", Updated: shellNow.Add(-40 * time.Minute).Unix(),
		Request: &bus.RequestInfo{ID: "9-0", From: "master", Target: "coder2", CreatedAt: shellNow.Add(-40 * time.Minute).UnixMilli()}}
	m = keys(t, step(t, m, pollMsg{snap: snap, at: shellNow}), '2')
	if screen := m.grid().plain(); strings.Contains(screen, "master is down") || !strings.Contains(screen, "cancel/reassign (master)") {
		t.Fatalf("live master:\n%s", screen)
	}
}

// board-drift §7 (step 4): no reply is the accepted request's alert alone; a
// request in review waits on its reviewers, a blocked one on someone else.
func TestBoardNoReplyOnlyWhenAccepted(t *testing.T) {
	ms := func(d time.Duration) int64 { return boardNow.Add(-d).UnixMilli() }
	for _, state := range []string{"review", "blocked"} {
		m := map[string]bus.BoardEntry{"t": {Owner: "coder", State: state, Updated: boardNow.Add(-time.Minute).Unix(),
			Request: &bus.RequestInfo{ID: "2-0", From: "master", Target: "coder", CreatedAt: ms(4 * time.Hour), AcceptedAt: ms(3 * time.Hour)}}}
		if v := boardViewOf(m, boardNow, 120, boardDefaults); strings.Contains(v.items[0].Line.text(), GlyphWarn) {
			t.Errorf("%s: %q", state, v.items[0].Line.text())
		}
	}
}

// board-drift §7: the task detail lists the request's times, each from its
// own field; the board's own time is Changed.
func TestTaskDetailRequestTimes(t *testing.T) {
	ms := func(d time.Duration) int64 { return boardNow.Add(-d).UnixMilli() }
	e := bus.BoardEntry{Owner: "coder", State: "review", Updated: boardNow.Add(-20 * time.Minute).Unix(),
		Request: &bus.RequestInfo{ID: "2-0", From: "master", Target: "coder", CreatedAt: ms(4 * time.Hour), AcceptedAt: ms(3 * time.Hour)}}
	_, lines, _ := taskDetail(e, "t", boardNow)
	got := plainLines(lines)
	for _, want := range []string{"Changed", "20m ago", "Requested", "4h ago", "Accepted", "3h ago", "In review", "since 20m ago"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "Responded") || strings.Contains(got, "Updated") {
		t.Fatalf("a time the request has not, or the old word:\n%s", got)
	}
	e.State, e.Request.RespondedAt = "done", ms(5*time.Minute)
	if _, lines, _ = taskDetail(e, "t", boardNow); !strings.Contains(plainLines(lines), "Responded") || strings.Contains(plainLines(lines), "In review") {
		t.Fatalf("done:\n%s", plainLines(lines))
	}
}

// board-drift §7 with step 3: no reply counts from the newer of the accept
// and the last report on the request (report --task), and says which; an
// older report changes nothing. The task detail lists Reported.
func TestBoardNoReplySinceReport(t *testing.T) {
	ms := func(d time.Duration) int64 { return boardNow.Add(-d).UnixMilli() }
	entry := func(reported int64) map[string]bus.BoardEntry {
		return map[string]bus.BoardEntry{"t": {Owner: "coder", State: "accepted", Updated: boardNow.Add(-5 * time.Hour).Unix(),
			Request: &bus.RequestInfo{ID: "2-0", From: "master", Target: "coder", CreatedAt: ms(6 * time.Hour), AcceptedAt: ms(5 * time.Hour), ReportedAt: reported, ReportID: "9-0"}}}
	}
	for _, c := range []struct {
		name     string
		reported int64
		want     string // "" no alert
	}{
		{"a report 3h ago", ms(3 * time.Hour), GlyphWarn + " no reply 3h since report"},
		{"a report 20m ago", ms(20 * time.Minute), ""}, // under the 2h default
		{"no report", 0, GlyphWarn + " no reply 5h since accept"},
		{"a report before the accept", ms(5*time.Hour + time.Minute), GlyphWarn + " no reply 5h since accept"},
	} {
		got := boardViewOf(entry(c.reported), boardNow, 120, boardDefaults).items[0].Line.text()
		if c.want == "" && strings.Contains(got, GlyphWarn) || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	e := entry(ms(40 * time.Minute))["t"]
	if _, lines, _ := taskDetail(e, "t", boardNow); !strings.Contains(plainLines(lines), "Reported") || !strings.Contains(plainLines(lines), "40m ago") {
		t.Fatalf("no Reported time:\n%s", plainLines(lines))
	}
}
