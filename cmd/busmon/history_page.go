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
	entries   []history.Entry // merged, oldest first
	days      int
	loaded    bool
	cut       bool   // a day was read from its end
	noArchive bool   // no archive directory on this host
	liveErr   string // the live streams could not be read
	err       string // the archive could not be read
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
		muted("older lines not loaded (" + humanBytes(histReadCap) + " read)")
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
	return fmt.Sprintf("%s not archived: %s to %s (%s) %s", h, from.Format("15:04"), to.Format("15:04"), e.F["of"], h)
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

// historyLoadCmd reads the last days of the archive (capped, gaps whatever
// their days) and the live streams, merged by (stream, id).
func (m *Model) historyLoadCmd(serial, days int) tea.Cmd {
	dir, ctx := m.cfg.histDir, m.cfg.ctx
	live, _ := m.cfg.w.(liveStreamReader)
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		now := time.Now()
		v := histView{days: days, loaded: true}
		var archive, gaps []history.Entry
		if _, err := os.Stat(dir); dir == "" || err != nil {
			v.noArchive = true
		} else {
			var err error
			if archive, v.cut, err = history.ReadDays(dir, now.AddDate(0, 0, -days), now, histReadCap); err != nil {
				v.err = err.Error()
			}
			gaps, _ = history.ReadGaps(dir)
		}
		var fresh []history.Entry
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
					fresh = append(fresh, history.Entry{Stream: s, ID: r.ID, F: r.Fields})
				}
			}
		}
		since := history.Filter{Since: now.AddDate(0, 0, -days)}
		for _, e := range history.Merge(archive, gaps, fresh) {
			if since.Match(e) {
				v.entries = append(v.entries, e)
			}
		}
		return historyMsg{serial: serial, v: v}
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

// historyOlder loads histDaysStep days more.
func (m *Model) historyOlder(p *page) tea.Cmd {
	days := histDaysStep
	if p.hist != nil {
		days = p.hist.days + histDaysStep
		p.hist.loaded, p.dirty = false, true
	}
	m.refresh()
	return m.historyLoadCmd(p.serial, days)
}

// openHistoryEntry opens the selected entry in the message page.
func (m *Model) openHistoryEntry(p *page) tea.Cmd {
	if p.sel < 0 || p.sel >= len(p.hrows) {
		return nil
	}
	e := p.hrows[p.sel].e
	ev := bus.ParseEntry(bus.StreamKey(m.cfg.project, e.Stream), e.ID, e.F)
	return m.push(page{kind: ovMessage, key: ev.ID, ev: ev, at: history.At(e)})
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
