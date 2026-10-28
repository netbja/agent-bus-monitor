package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// The first-launch tour (spec docs/superpowers/specs/2026-10-28-busmon-onboarding-design.md):
// a few short pages over the screen, a modal with its own key table
// (ModeTour). "Seen" is one local file for the machine, never on the bus.
// The key cells come from the key tables and the header rows from the
// header's own functions, so the text cannot drift from the screen.

// tourVersion is bumped by a release that changes a page's meaning (each
// page carries the version it last changed in); a wording fix does not.
const tourVersion = 1

// tourMarkerFile sits beside the per-project caches; its name is no valid
// project name, so no project's cache can be read or written as it.
const tourMarkerFile = "_tour.json"

// tourMarker is the machine's record of the tour version seen. path "" never saves.
type tourMarker struct {
	Seen int `json:"seen"`
	path string
}

func tourMarkerPath() string {
	return filepath.Join(filepath.Dir(busmonCachePath("x")), tourMarkerFile)
}

// loadTourMarker reads the marker; an absent file has seen nothing. An
// unreadable one is an error and also reads as seen nothing: the tour shows.
func loadTourMarker(path string) (*tourMarker, error) {
	t := &tourMarker{path: path}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return t, nil
	}
	if err == nil {
		err = json.Unmarshal(b, t)
	}
	if err != nil {
		return &tourMarker{path: path}, fmt.Errorf("invalid tour marker")
	}
	return t, nil
}

func (t tourMarker) save() error {
	if t.path == "" {
		return nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return writeFileAtomic(t.path, append(b, '\n'))
}

// tourRef names a key cell by the mode whose table holds it and the action;
// alt adds the binding's alternate keys.
type tourRef struct {
	m   Mode
	act Action
	alt bool
}

// tourKeys is the cell a ref draws: its binding's keys, "" when none has it.
func tourKeys(r tourRef) string {
	for _, b := range bindings(r.m) {
		if b.Act == r.act {
			if r.alt && b.Alt != "" {
				return b.Keys + " " + b.Alt
			}
			return b.Keys
		}
	}
	return ""
}

// tourPart is prose or a key cell.
type tourPart struct {
	text string
	key  *tourRef
}

func tx(s string) tourPart                     { return tourPart{text: s} }
func ky(m Mode, a Action) tourPart             { return tourPart{key: &tourRef{m: m, act: a}} }
func kyAlt(m Mode, a Action) tourPart          { return tourPart{key: &tourRef{m: m, act: a, alt: true}} }
func cells(ps ...tourPart) []tourPart          { return ps }
func desc(ps ...tourPart) []tourPart           { return ps }
func para(ps ...tourPart) tourBlock            { return tourBlock{para: ps} }
func rows(rs ...tourRow) tourBlock             { return tourBlock{rows: rs} }
func krow(c []tourPart, d ...tourPart) tourRow { return tourRow{cell: c, desc: d} }

// tourRow is "cell  meaning": key cells, or a label the screen itself draws
// (a panel's name, a header pill).
type tourRow struct {
	cell  []tourPart
	label func(self string) Line
	desc  []tourPart
}

type tourBlock struct {
	para []tourPart
	rows []tourRow
}

type tourPage struct {
	title  string
	since  int // the tour version this page last changed in
	blocks []tourBlock
}

// parts are every part of the page, cells included.
func (p tourPage) parts() []tourPart {
	var out []tourPart
	for _, b := range p.blocks {
		out = append(out, b.para...)
		for _, r := range b.rows {
			out = append(append(out, r.cell...), r.desc...)
		}
	}
	return out
}

// refs are the page's key cells.
func (p tourPage) refs() []tourRef {
	var out []tourRef
	for _, part := range p.parts() {
		if part.key != nil {
			out = append(out, *part.key)
		}
	}
	return out
}

// prose is the page's own words (TestTourMentionsNoBareKey reads it).
func (p tourPage) prose() []string {
	var out []string
	for _, part := range p.parts() {
		if part.key == nil {
			out = append(out, part.text)
		}
	}
	return out
}

// panelRow is a panel's row, named and numbered as the shell names it.
func panelRow(p PanelID, what string) tourRow {
	return tourRow{label: func(string) Line {
		name := panelLabel(p)
		for i, t := range lowerTabs {
			if t == p {
				name = fmt.Sprintf("%d %s", i+1, name)
			}
		}
		return Line{{Text: name, Role: panelHue[p]}}
	}, desc: desc(tx(what))}
}

// pillRow is a header pill drawn by the header's own function.
func pillRow(pill func(self string) Line, what ...tourPart) tourRow {
	return tourRow{label: pill, desc: what}
}

// tourBudget is the header page's account window fixture.
var tourBudget = bus.BudgetSnapshot{SessionPct: 62, WeeklyPct: 40}

// tourGuardian is the header page's health fixture: two problems.
func tourGuardian() guardianSummary {
	return guardianSummary{Unhealthy: 2, Problems: 2, Total: len(guardian.Definitions()), Worst: guardian.Warn}
}

var tourBook = []tourPage{
	{title: "Welcome to busmon", since: 1, blocks: []tourBlock{
		para(tx("busmon shows what your agents are doing and what waits for you. A few short pages;"),
			ky(ModeTour, ActClose), tx("skips, and"), ky(ModeShell, ActHelp), tx("then"), ky(ModeOverlay, ActTour), tx("brings them back.")),
		rows(panelRow(PanelForYou, "what needs you: questions, waits, unread lines"),
			panelRow(PanelAgents, "each agent, its state and how fresh it is"),
			panelRow(PanelActivity, "everything said on the bus, live"),
			panelRow(PanelBoard, "who owns which task"),
			panelRow(PanelRequests, "who owes whom an answer"),
			panelRow(PanelPlan, "the batch's tasks by state")),
		para(ky(ModeShell, ActNextPanel), tx("moves the focus between panels;"), ky(ModeShell, ActTab1), tx("pick one of the tabs.")),
	}},
	{title: "Writing to an agent", since: 1, blocks: []tourBlock{
		para(ky(ModeShell, ActCompose), tx("opens the line at the bottom. Type @name then your text: that agent is woken and reads it. @all reaches every agent;"),
			ky(ModeInput, ActComplete), tx("completes a name.")),
		rows(krow(cells(ky(ModeShell, ActCompose)), tx("the line: @coder please rebase")),
			krow(cells(ky(ModeShell, ActComposeOpen), ky(ModeInput, ActComposeOpen)), tx("the compose window, several lines;"), ky(ModeCompose, ActSend), tx("sends"))),
		para(tx("Text without @name is a broadcast nobody reads: busmon asks for a second"), ky(ModeInput, ActSend), tx("before it sends one.")),
	}},
	{title: "Answering a question", since: 1, blocks: []tourBlock{
		para(tx("An agent that needs your decision asks a question. It shows in For you with " + GlyphDotFull + ", and the agent reads waiting.")),
		rows(krow(cells(kyAlt(ModeShell, ActAsk)), tx("open its questions")),
			krow(cells(ky(ModeShellAsk, ActAskPick)), tx("pick an answer")),
			krow(cells(ky(ModeShellAsk, ActAskOther)), tx("Other: write your own answer in the compose window"))),
		para(tx("Your answer wakes the agent; nothing else is needed.")),
	}},
	{title: "Useful keys", since: 1, blocks: []tourBlock{
		rows(krow(cells(ky(ModeShell, ActOpen)), tx("open a line (once: in place; twice: the full text)")),
			krow(cells(ky(ModeShell, ActNextUnread)), tx("the next unread line for you")),
			krow(cells(ky(ModeShell, ActRecap)), tx("where was I? the recap since your last visit")),
			krow(cells(ky(ModeShell, ActPlan)), tx("the batch's plan")),
			krow(cells(ky(ModeShell, ActFilter)), tx("filter Activity")),
			krow(cells(kyAlt(ModeShell, ActHelp)), tx("help, every key")),
			krow(cells(ky(ModeShell, ActRequests), ky(ModeShell, ActServices)), tx("requests, services")),
			krow(cells(ky(ModeShell, ActHealth)), tx("project health")),
			krow(cells(ky(ModeShell, ActQuit)), tx("quit"))),
	}},
	{title: "The header", since: 1, blocks: []tourBlock{
		rows(pillRow(func(string) Line { return healthSeg(health{ok: true}, time.Time{}, false) }, tx("busmon reads the bus")),
			pillRow(func(string) Line { return dispatchSeg("up", false) }, tx("the service that wakes idle agents runs")),
			pillRow(func(string) Line { return missionsSeg("up", false) }, tx("the conductor runs")),
			pillRow(func(string) Line { return guardianSeg(tourGuardian(), false) }, tx("two health problems;"), ky(ModeShell, ActHealth), tx("says which")),
			pillRow(func(string) Line { return budgetSeg("claude", tourBudget, TierNarrow) }, tx("your account's windows, shared by every agent")),
			pillRow(func(self string) Line { return Line{{Text: Preview(humanLabel(self, "")), Role: RoleText}} }, tx("you, as the agents see you"))),
		para(tx("That is all."), ky(ModeShell, ActHelp), tx("then"), ky(ModeOverlay, ActTour), tx("shows these pages again.")),
	}},
}

// pagesSince are the pages changed after version seen, in order.
func pagesSince(book []tourPage, seen int) []int {
	var out []int
	for i, p := range book {
		if p.since > seen {
			out = append(out, i)
		}
	}
	return out
}

// tourLines lays a page out at width cells: paragraphs wrapped, rows with
// their cells in one column and the meaning wrapped beside it.
func tourLines(p tourPage, width int, self string) []Line {
	spans := func(ps []tourPart) []mdSpan {
		var out []mdSpan
		for _, part := range ps {
			if part.key != nil {
				out = append(out, mdSpan{text: tourKeys(*part.key), role: RoleAccent, bold: true})
			} else {
				out = append(out, mdSpan{text: part.text, role: RoleText})
			}
		}
		return out
	}
	var out []Line
	for i, b := range p.blocks {
		if i > 0 {
			out = append(out, nil)
		}
		if b.para != nil {
			out = append(out, wrapSpans(spans(b.para), width, "", "")...)
		}
		cellsOf := make([]Line, len(b.rows))
		cw := 0
		for j, r := range b.rows {
			if r.label != nil {
				cellsOf[j] = r.label(self)
			} else {
				for k, s := range spans(r.cell) {
					if k > 0 {
						cellsOf[j] = append(cellsOf[j], S(RoleText, " "))
					}
					cellsOf[j] = append(cellsOf[j], Seg{Text: s.text, Role: s.role, Bold: s.bold})
				}
			}
			cw = max(cw, cellsOf[j].Width())
		}
		indent := strings.Repeat(" ", min(2+cw+2, width/2))
		for j, r := range b.rows {
			ls := wrapSpans(spans(r.desc), width, indent, indent)
			first := append(Line{S(RoleText, "  ")}, cellsOf[j]...)
			if pad := len(indent) - first.Width(); pad > 0 {
				first = append(first, S(RoleText, strings.Repeat(" ", pad)))
			} else {
				ls = append([]Line{nil}, ls...) // a cell wider than the indent: the meaning starts below
			}
			if len(ls) == 0 {
				ls = []Line{nil}
			}
			ls[0] = append(first, ls[0][min(1, len(ls[0])):]...)
			out = append(out, ls...)
		}
	}
	for i := range out {
		out[i] = FitLine(out[i], width)
	}
	return out
}

// tourState is the open tour: the pages shown and the one on screen.
type tourState struct {
	pages []int
	at    int
	cut   bool // a page was shown cut (the screen too short): the marker is not written
}

// openTour opens the tour on every page (all) or on those changed since the marker.
func (m *Model) openTour(all bool) {
	pages := pagesSince(tourBook, 0)
	if !all && m.cfg.tour != nil {
		pages = pagesSince(tourBook, m.cfg.tour.Seen)
	}
	if len(pages) > 0 {
		m.ui.tour = &tourState{pages: pages}
		m.noteCut()
	}
}

// noteCut records that the page on screen does not fit whole: a tour that
// showed a cut page is not marked seen.
func (m *Model) noteCut() {
	if t := m.ui.tour; t != nil && !m.pageFits(t.pages[t.at]) {
		t.cut = true
	}
}

// maybeTour opens the tour once per session when the marker is behind, on
// a calm update: never while the person is busy, nor on the update a bell rang.
func (m *Model) maybeTour() {
	s := m.sig
	if m.cfg.tour == nil || s.tourShown || m.ui.tour != nil || m.now.IsZero() || m.layout.Tier == TierTooSmall {
		return
	}
	if s.tourHold {
		s.tourHold = false
		return
	}
	if m.busyForModal() {
		return
	}
	m.openTour(false)
	if t := m.ui.tour; t != nil {
		for _, i := range t.pages {
			if !m.pageFits(i) { // a page would be cut: a size later
				m.ui.tour = nil
				return
			}
		}
	}
	s.tourShown = true
}

// closeTour closes it and records the version seen: a skip counts, the
// person chose; a tour that showed a cut page records nothing.
func (m *Model) closeTour() tea.Cmd {
	cut := m.ui.tour.cut
	m.ui.tour = nil
	mk := m.cfg.tour
	if cut {
		m.ui.flash, m.ui.flashErr = "tour not marked seen: a page did not fit the window", false
		return nil
	}
	if mk == nil {
		return nil
	}
	mk.Seen = tourVersion
	saved := *mk
	return func() tea.Msg { return cacheSavedMsg{err: saved.save(), note: "tour marker not saved"} }
}

func (m *Model) tourKey(k Key) tea.Cmd {
	return m.tourAct(lookup(ModeTour, m.ui.focus, m.layout.Tier, k))
}

func (m *Model) tourAct(act Action) tea.Cmd {
	t := m.ui.tour
	switch act {
	case ActNextItem:
		if t.at+1 >= len(t.pages) {
			return m.closeTour()
		}
		t.at++
		m.noteCut()
	case ActPrevItem:
		t.at = max(0, t.at-1)
		m.noteCut()
	case ActClose:
		return m.closeTour()
	}
	return nil
}

// tourHints is the modal's key line: the tour's table, as the footer pairs it.
func tourHints() Line {
	var l Line
	for _, b := range tourBindings {
		if b.Hint == "" {
			continue
		}
		if l != nil {
			l = append(l, S(RoleMuted, " "+GlyphNone+" "))
		}
		l = append(l, Seg{Text: b.Keys, Role: RoleAccent, Bold: true}, Seg{Text: " " + b.Hint, Role: RoleMuted})
	}
	return l
}

// hintAt is the tour action whose "keys hint" pair covers column x of l drawn from x0.
func hintAt(l Line, x0, x int) Action {
	pos := x0
	for i, s := range l {
		w := Width(s.Text)
		if i+1 < len(l) {
			for _, b := range tourBindings {
				if s.Text == b.Keys && l[i+1].Text == " "+b.Hint {
					if end := pos + w + Width(l[i+1].Text); x >= pos && x < end {
						return b.Act
					}
				}
			}
		}
		pos += w
	}
	return ActNone
}

// tourClick: next/back/skip in the modal's key line or in the footer.
func (m *Model) tourClick(x, y int) tea.Cmd {
	r := m.tourRect()
	switch y {
	case r.Y + r.H - 2:
		return m.tourAct(hintAt(m.tourKeyLine(), r.X+2, x))
	case m.layout.Footer.Y:
		return m.tourAct(hintAt(footerHints(ModeTour, m.ui.focus, m.layout.Tier, m.width), 0, x))
	}
	return nil
}

// tourKeyLine is the dots of the pages shown, then the key line.
func (m Model) tourKeyLine() Line {
	t := m.ui.tour
	var l Line
	for i := range t.pages {
		g, role := GlyphDotEmpty, RoleMuted
		if i == t.at {
			g, role = GlyphDotFull, RoleAccent
		}
		l = append(l, S(role, g+" "))
	}
	return append(append(l, S(RoleText, "  ")), tourHints()...)
}

func (m Model) tourPage() tourPage { return tourBook[m.ui.tour.pages[m.ui.tour.at]] }

// tourRect is the modal: 76 columns at most, 8 fewer than the screen, as
// tall as the page (border, blank, body, blank, keys, border), centred.
func (m Model) tourRect() Rect { return m.tourRectFor(m.ui.tour.pages[m.ui.tour.at]) }

// tourRectFor is the modal for page i of the book.
func (m Model) tourRectFor(i int) Rect {
	w := max(0, min(76, m.width-8))
	h := min(len(tourLines(tourBook[i], max(1, w-4), m.cfg.self))+5, m.height-2)
	return Rect{(m.width - w) / 2, max(0, (m.height-h)/2), w, h}
}

// pageFits: page i is drawn whole at this size.
func (m Model) pageFits(i int) bool {
	w := min(76, m.width-8)
	return w >= 20 && len(tourLines(tourBook[i], w-4, m.cfg.self))+5 <= m.height-2
}

// tourFits: the modal can be drawn (20 columns, 5 rows); below, only the
// footer shows the tour's keys, and it never opens by itself.
func (m Model) tourFits() bool {
	r := m.tourRect()
	return r.W >= 20 && r.H >= 5
}

func (m Model) drawTour(g *grid) {
	r, t := m.tourRect(), m.ui.tour
	if !m.tourFits() {
		return
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		drawLine(g, r.X, y, r.W, nil, false)
	}
	drawFrame(g, r.X, r.Y, r.W, r.H, Frame{Title: Line{{Text: m.tourPage().title, Role: RoleText}},
		Label: Line{{Text: fmt.Sprintf("%d/%d", t.at+1, len(t.pages)), Role: RoleMuted}}, Focused: true, Hue: RoleAccent})
	lines, rows := tourLines(m.tourPage(), r.W-4, m.cfg.self), r.H-5
	if len(lines) > rows && rows > 0 { // cut: the last row says so
		more := len(lines) - rows + 1
		lines = append(lines[:rows-1:rows-1], Line{S(RoleMuted, fmt.Sprintf("%s %d more lines: a taller window shows the page whole", GlyphEllipsis, more))})
	}
	for i, l := range lines {
		if i >= rows {
			break
		}
		drawLine(g, r.X+2, r.Y+2+i, r.W-4, FitLine(l, r.W-4), false)
	}
	drawLine(g, r.X+2, r.Y+r.H-2, r.W-4, m.tourKeyLine(), false)
}
