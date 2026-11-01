package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
	"github.com/netbja/agent-bus-monitor/service"
)

// The History page (H; spec docs/superpowers/specs/2026-11-01-history-design.md,
// part c): the archive on this host, then the live streams, newest first under
// day headings. Loaded in a command (historyLoadCmd), never in View.

// histDaysStep is what the page loads at open and what o adds.
const histDaysStep = 3

// histReadCap bounds one load's archive read; a busy day is read from its end.
const histReadCap = 8 << 20

// histView is the page's loaded facts.
type histView struct {
	entries   []history.Entry // shown: the three sources merged, inside the window, oldest first
	archive   []history.Entry // what the day files gave so far
	gaps      []history.Entry // gaps.jsonl, read again at each load
	live      []history.Entry // the live streams, read at open
	cursor    history.Cursor  // where the archive read stopped: o reads before it
	days      int
	loaded    bool
	cut       bool   // a day was read from its end
	noArchive bool   // no archive directory on this host
	liveErr   string // the live streams could not be read
	err       string // the archive could not be read
	filter    string // the filter as typed, for the title
}

// histItem is one selectable row: its body line and its entry.
type histItem struct {
	line int
	e    history.Entry
}

// histShortKinds are the narrow tier's kinds (below histWide cells).
var histShortKinds = map[string]string{"directive": "dir", "reply": "rep", "verdict": "ver", "verdict-notice": "vnt",
	"report": "rpt", "status": "sts", "notify": "ntf", "health": "hlt", "swap": "swp", "escalation": "esc", "challenge": "chl"}

const histWide = 80

// historyBody lays the page out: its title, its body and its selectable rows
// (the selected one marked).
func historyBody(v histView, sel int, loc *time.Location, width int) (string, []Line, []histItem) {
	n := 0
	for _, e := range v.entries {
		if e.Stream != history.GapStream {
			n++
		}
	}
	title := fmt.Sprintf("History %s %d days %s %d entries", GlyphNone, v.days, GlyphNone, n)
	if v.filter != "" {
		title += " " + GlyphNone + " filter: " + v.filter
	}
	var body []Line
	muted := func(s string) { body = append(body, Line{S(RoleMuted, "  "+s)}) }
	if v.noArchive {
		muted("no archive on this host: live streams only")
	}
	if v.liveErr != "" {
		muted("broker unreachable: archive only")
	}
	if v.err != "" {
		muted("the archive could not be read: " + v.err)
	}
	if !v.loaded {
		muted("loading" + GlyphEllipsis)
		return title, body, nil
	}
	whoW, kindW := 22, 14
	if width < histWide {
		whoW, kindW = 14, 3
	}
	var items []histItem
	day := ""
	for i := len(v.entries) - 1; i >= 0; i-- {
		e := v.entries[i]
		at := history.At(e).In(loc)
		if d := at.Format("Mon 2006-01-02"); d != day {
			day = d
			body = append(body, listSection(d))
		}
		if e.Stream == history.GapStream {
			body = append(body, Line{S(RoleMuted, "  "+histGapText(e, loc))})
			continue
		}
		who, text := history.WhoText(e)
		kind := history.Kind(e)
		if width < histWide {
			if k, ok := histShortKinds[kind]; ok {
				kind = k
			}
		}
		idx := len(items)
		row := append(planMark(idx == sel), S(RoleMuted, at.Format("15:04")+"  "),
			S(RoleText, Fit(Preview(who), whoW)+"  "), S(RoleAccent, Fit(kind, kindW)+"  "))
		row = append(row, S(RoleText, Fit(Preview(strings.Join(strings.Fields(text), " ")), max(0, width-row.Width()))))
		items = append(items, histItem{line: len(body), e: e})
		body = append(body, row)
	}
	if v.cut {
		// The archive is paged by position, in write order, not by id: what is
		// left may hold newer entries than the oldest shown (reviewer-codex).
		muted("more archive lines not read (in write order, some may be newer): o reads them")
	}
	muted(fmt.Sprintf("o loads the %d days before", histDaysStep))
	return title, body, items
}

// histGapText is a gap line as the page says it.
func histGapText(e history.Entry, loc *time.Location) string {
	h := strings.Repeat(glyphHorizontal, 2)
	to := history.At(history.Entry{ID: e.F["to"]}).In(loc)
	if e.F["from"] == "unknown" {
		return fmt.Sprintf("%s archive restarted: nothing archived before %s %s", h, to.Format("Mon 15:04"), h)
	}
	from := history.At(history.Entry{ID: e.F["from"]}).In(loc)
	layout := "15:04"
	if from.Format("2006-01-02") != to.Format("2006-01-02") { // across midnight: the dates too (foureyes)
		layout = "Mon 01-02 15:04"
	}
	return fmt.Sprintf("%s not archived: %s to %s (%s) %s", h, from.Format(layout), to.Format(layout), e.F["of"], h)
}

// historyDirOf is the project's archive directory on this host ("" when the
// state directory cannot be named).
func historyDirOf(project string) string {
	paths, err := service.PathsFrom(os.Getenv)
	if err != nil {
		return ""
	}
	return history.Dir(paths.StateDir(project))
}

// liveStreamReader reads a project stream forward (*bus.Bus); the page reads
// the live streams through it when the writer has it.
type liveStreamReader interface {
	StreamAfter(ctx context.Context, kind, after string, count int64) ([]bus.RawEntry, error)
}

// historyMsg is a load's result for the page of that serial.
type historyMsg struct {
	serial int
	v      histView
}

func (m *Model) openHistory() tea.Cmd {
	m.push(page{kind: ovHistory})
	return m.historyLoadCmd(m.rd.top().serial, histDaysStep)
}

// historyLoadCmd reads the last days of the archive (capped), every gap and
// the live streams.
func (m *Model) historyLoadCmd(serial, days int) tea.Cmd {
	dir, ctx, readCap := m.cfg.histDir, m.cfg.ctx, m.historyCap()
	live, _ := m.cfg.w.(liveStreamReader)
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		now := time.Now()
		v := histView{days: days, loaded: true}
		if _, err := os.Stat(dir); dir == "" || err != nil {
			v.noArchive = true
		} else {
			var err error
			v.archive, v.cursor, v.cut, err = history.ReadBack(dir, now.AddDate(0, 0, -days),
				history.Cursor{Day: now.UTC().Format("2006-01-02"), Offset: -1}, readCap)
			if err != nil {
				v.err = err.Error()
			}
			readHistGaps(dir, &v)
		}
		if live == nil {
			v.liveErr = "no broker"
		} else {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			for _, s := range history.Streams {
				raw, err := live.StreamAfter(c, s, "", 20000)
				if err != nil {
					v.liveErr = err.Error()
					break
				}
				for _, r := range raw {
					v.live = append(v.live, history.Entry{Stream: s, ID: r.ID, F: r.Fields})
				}
			}
		}
		v.window(now)
		return historyMsg{serial: serial, v: v}
	}
}

// readHistGaps reads every gap again; a failed read is said (reviewer-codex).
func readHistGaps(dir string, v *histView) {
	gaps, err := history.ReadGaps(dir)
	if err != nil {
		v.err = "its gaps could not be read: " + err.Error()
		return
	}
	v.gaps = gaps
}

// window sets what is shown: the three sources merged by (stream, id), kept
// inside the last v.days days (a gap by the stretch it covers).
func (v *histView) window(now time.Time) {
	since := history.Filter{Since: now.AddDate(0, 0, -v.days)}
	v.entries = nil
	for _, e := range history.Merge(v.archive, v.gaps, v.live) {
		if since.Match(e) {
			v.entries = append(v.entries, e)
		}
	}
}

// gotHistory puts a load in its page, wherever it is on the stack.
func (m *Model) gotHistory(msg historyMsg) {
	i := slices.IndexFunc(m.rd.pages, func(p page) bool { return p.serial == msg.serial })
	if i < 0 || m.rd.pages[i].kind != ovHistory {
		return
	}
	p := &m.rd.pages[i]
	v := msg.v
	p.hist, p.dirty = &v, true
	m.refresh()
}

// historyOlder widens the window by histDaysStep days: the archive is read on
// from where the last read stopped, by position (a day file is not in id
// order, foureyes), under a cap of its own; the gaps are read again and the
// live streams already read are windowed again (reviewer-codex).
func (m *Model) historyOlder(p *page) tea.Cmd {
	if p.hist == nil || !p.hist.loaded {
		return nil
	}
	cur := *p.hist
	p.hist.loaded, p.dirty = false, true
	m.refresh()
	dir, readCap, serial := m.cfg.histDir, m.historyCap(), p.serial
	return func() tea.Msg {
		now := time.Now()
		v := cur
		v.loaded, v.days = true, cur.days+histDaysStep
		if dir != "" && !v.noArchive {
			older, next, cut, err := history.ReadBack(dir, now.AddDate(0, 0, -v.days), cur.cursor, readCap)
			if err != nil {
				v.err = err.Error()
			}
			v.archive, v.cursor, v.cut = history.Merge(older, cur.archive), next, cut
			readHistGaps(dir, &v)
		}
		v.window(now)
		return historyMsg{serial: serial, v: v}
	}
}

// historyCap is a load's archive cap (histReadCap; a test may lower it).
func (m *Model) historyCap() int64 {
	if m.histCap > 0 {
		return m.histCap
	}
	return histReadCap
}

// openHistoryEntry opens the selected entry in the message page.
func (m *Model) openHistoryEntry(p *page) tea.Cmd {
	if p.sel < 0 || p.sel >= len(p.hrows) {
		return nil
	}
	e := p.hrows[p.sel].e
	ev := bus.ParseEntry(bus.StreamKey(m.cfg.project, e.Stream), e.ID, e.F)
	return m.push(page{kind: ovMessage, key: ev.ID, ev: ev, at: history.At(e), fromHist: true})
}

func histLines(p *page) func() []int {
	return func() []int {
		lines := make([]int, len(p.hrows))
		for i, r := range p.hrows {
			lines[i] = r.line
		}
		return lines
	}
}

// historyFiltered keeps the entries the page's filter passes: @agent, a thread
// id and words, as Activity's filter reads them.
func historyFiltered(es []history.Entry, f feedFilter) []history.Entry {
	if f.empty() {
		return es
	}
	hf := history.Filter{Agent: f.agent, Thread: f.thread, Grep: f.text}
	var out []history.Entry
	for _, e := range es {
		if hf.Match(e) {
			out = append(out, e)
		}
	}
	return out
}

// historyThread narrows the page to the selected entry's thread (t): its ref,
// else its own id.
func (m *Model) historyThread(p *page) {
	if p.sel < 0 || p.sel >= len(p.hrows) {
		return
	}
	e := p.hrows[p.sel].e
	id := e.F["ref"]
	if id == "" {
		id = e.ID
	}
	p.histFilter, p.sel, p.dirty = parseFilter(id), 0, true
	m.refresh()
}

// historyStep is n/p on an entry opened from History: the page's next or
// previous row, in its place.
func (m *Model) historyStep(next bool) tea.Cmd {
	if len(m.rd.pages) < 2 || m.rd.pages[len(m.rd.pages)-2].kind != ovHistory {
		return nil
	}
	hp := &m.rd.pages[len(m.rd.pages)-2]
	to := hp.sel - 1
	if next {
		to = hp.sel + 1
	}
	if to < 0 || to >= len(hp.hrows) {
		m.ui.flash, m.ui.flashErr = "no previous item", false
		if next {
			m.ui.flash = "no next item"
		}
		return nil
	}
	m.pop()
	hp = m.rd.top()
	hp.sel, hp.dirty = to, true
	m.refresh()
	return m.openHistoryEntry(hp)
}
