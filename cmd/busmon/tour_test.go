package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/netbja/agent-bus-monitor/bus"
)

var (
	kRight = tea.Key{Code: tea.KeyRight}
	kF1    = tea.Key{Code: tea.KeyF1}
)

// keyMsg is one key press: a rune or a named key.
func keyMsg(k any) tea.Msg {
	if r, ok := k.(rune); ok {
		return tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)})
	}
	return tea.KeyPressMsg(k.(tea.Key))
}

// tourShell is the fixture shell with a tour marker at path (seen: its
// version), before its first poll when poll is false.
func tourShell(t *testing.T, w, h, seen int, poll bool) (Model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "busmon", tourMarkerFile)
	m := newShell(shellConfig{project: "agent-bus-monitor", self: "charlie", tour: &tourMarker{Seen: seen, path: path}})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(t, m, tailMsg{events: shellEvents(shellNow)})
	if poll {
		m = tourPoll(t, m)
	}
	return m, path
}

func tourPoll(t *testing.T, m Model) Model {
	t.Helper()
	snap := shellFixture(shellNow)
	for _, a := range snap.agents {
		a.judgedAt = shellNow
	}
	return step(t, m, pollMsg{snap: snap, at: shellNow})
}

func readMarker(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	var mk struct{ Seen int }
	if err := json.Unmarshal(b, &mk); err != nil {
		t.Fatal(err)
	}
	return mk.Seen
}

// The tour opens when the marker is absent (seen 0) or older than the tour,
// on every page then; not when it is current, nor without a marker at all
// (a shell started by a test or a CLI path).
func TestTourOpensWhenTheMarkerIsBehind(t *testing.T) {
	m, _ := tourShell(t, 120, 30, 0, true)
	if m.ui.tour == nil || len(m.ui.tour.pages) != len(tourBook) {
		t.Fatalf("absent marker: tour %+v, want every page", m.ui.tour)
	}
	if m.mode() != ModeTour {
		t.Errorf("mode %v, want ModeTour", m.mode())
	}
	if m, _ := tourShell(t, 120, 30, tourVersion, true); m.ui.tour != nil {
		t.Errorf("current marker opened the tour")
	}
	if m := fixtureShell(t, 120, 30); m.ui.tour != nil {
		t.Errorf("no marker configured, yet the tour opened")
	}
}

// Every way out writes the marker at the tour's version: esc skips, enter
// past the last page finishes. The marker is the machine's, never a project's.
func TestTourSkipAndFinishWriteTheMarker(t *testing.T) {
	m, path := tourShell(t, 120, 30, 0, true)
	next, cmd := m.Update(keyMsg(kEsc))
	m = run(t, next.(Model), cmd)
	if m.ui.tour != nil {
		t.Fatal("esc left the tour open")
	}
	if got := readMarker(t, path); got != tourVersion {
		t.Errorf("after esc the marker says %d, want %d", got, tourVersion)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("marker mode: %v %v", info, err)
	}

	m, path = tourShell(t, 120, 30, 0, true)
	for range tourBook {
		next, cmd := m.Update(keyMsg(kEnter))
		m = run(t, next.(Model), cmd)
	}
	if m.ui.tour != nil {
		t.Fatal("enter on the last page left the tour open")
	}
	if got := readMarker(t, path); got != tourVersion {
		t.Errorf("after finishing the marker says %d", got)
	}
	// A second project at the same version: the marker it loads is this one.
	mk, err := loadTourMarker(path)
	if err != nil || mk.Seen != tourVersion {
		t.Fatalf("reload: %+v %v", mk, err)
	}
	m2 := newShell(shellConfig{project: "another", self: "charlie", tour: mk})
	m2 = step(t, m2, tea.WindowSizeMsg{Width: 120, Height: 30})
	if m2 = tourPoll(t, m2); m2.ui.tour != nil {
		t.Error("a second project reopened the tour")
	}
}

// The marker can never be a project's cache: a project named like it would
// read it as its own cache and overwrite it.
func TestTourMarkerIsNoProjectCache(t *testing.T) {
	p := tourMarkerPath()
	if filepath.Dir(p) != filepath.Dir(busmonCachePath("x")) {
		t.Errorf("marker %s not beside the per-project caches", p)
	}
	name := strings.TrimSuffix(filepath.Base(p), ".json")
	if bus.ValidName(name) {
		t.Errorf("marker name %q is a valid project name", name)
	}
}

// A version bump opens only the pages changed since the marker.
func TestTourShowsOnlyTheChangedPages(t *testing.T) {
	book := []tourPage{{title: "a", since: 1}, {title: "b", since: 3}, {title: "c", since: 2}}
	for _, tc := range []struct {
		seen int
		want []int
	}{{0, []int{0, 1, 2}}, {1, []int{1, 2}}, {2, []int{1}}, {3, nil}} {
		if got := pagesSince(book, tc.seen); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("seen %d: %v, want %v", tc.seen, got, tc.want)
		}
	}
	newest := 0
	for _, p := range tourBook {
		if p.since > tourVersion {
			t.Errorf("page %q changed in %d, after the tour's version %d", p.title, p.since, tourVersion)
		}
		newest = max(newest, p.since)
	}
	if newest != tourVersion {
		t.Errorf("no page changed in version %d: the bump would show nothing", tourVersion)
	}
}

// ModeTour owns the keyboard: no shell key acts underneath; enter and the
// arrows move, ← stops on the first page.
func TestTourOwnsTheKeyboard(t *testing.T) {
	m, _ := tourShell(t, 120, 30, 0, true)
	tab, focus := m.ui.tab, m.ui.focus
	for _, r := range []any{'q', '2', 'I', 'i', '!', 'R', '/', kTab} {
		next, cmd := m.Update(keyMsg(r))
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Fatalf("%q quit under the tour", r)
			}
		}
		m = next.(Model)
	}
	if m.ui.tab != tab || m.ui.focus != focus || m.ui.compose != nil || m.ui.line != lineNone || m.rd.open() {
		t.Errorf("a shell key acted under the tour: tab %v focus %v compose %v line %v reader %v", m.ui.tab, m.ui.focus, m.ui.compose != nil, m.ui.line, m.rd.open())
	}
	if m.ui.tour == nil || m.ui.tour.at != 0 {
		t.Fatalf("tour moved or closed: %+v", m.ui.tour)
	}
	m = keys(t, m, kLeft)
	if m.ui.tour.at != 0 {
		t.Errorf("← moved before the first page")
	}
	m = keys(t, m, kRight)
	m = keys(t, m, kEnter)
	if m.ui.tour.at != 2 {
		t.Errorf("→ then enter: page %d, want 2", m.ui.tour.at)
	}
	m = keys(t, m, kLeft)
	if m.ui.tour.at != 1 {
		t.Errorf("← : page %d, want 1", m.ui.tour.at)
	}
	if foot := footerHints(m.mode(), m.ui.focus, m.layout.Tier, m.width).text(); !strings.Contains(foot, "skip") || strings.Contains(foot, "help") {
		t.Errorf("footer under the tour: %q", foot)
	}
}

// F1 then t starts the tour from its first page, every page, the help closed.
func TestTourReopensFromTheHelp(t *testing.T) {
	m := fixtureShell(t, 120, 30)
	m = keys(t, m, kF1)
	if !m.rd.open() || m.rd.top().kind != ovHelp {
		t.Fatal("F1 opened no help")
	}
	help := ""
	for _, l := range helpText() {
		help += l.text() + "\n"
	}
	if !strings.Contains(help, "tour again") {
		t.Error("the help does not advertise the tour")
	}
	m = keys(t, m, 't')
	if m.ui.tour == nil || m.ui.tour.at != 0 || len(m.ui.tour.pages) != len(tourBook) {
		t.Fatalf("F1 then t: tour %+v", m.ui.tour)
	}
	if m.rd.open() {
		t.Error("the help stayed open under the tour")
	}
	// t is the help's: on another page it does nothing.
	m = keys(t, keys(t, fixtureShell(t, 120, 30), 'r'), 't')
	if m.ui.tour != nil {
		t.Error("t on the Requests page opened the tour")
	}
}

// The tour waits while the person is busy (an ask page, the compose window,
// a draft, the pause popup, the reminder), and on the update a bell rang;
// it opens on the next calm update.
func TestTourWaitsForTheCalm(t *testing.T) {
	busy := map[string]func(m *Model){
		"compose":  func(m *Model) { m.openMessageCompose("", "") },
		"draft":    func(m *Model) { m.ui.msgDraft = "half a thought" },
		"pause":    func(m *Model) { m.ui.pausePopup = &pauseDialog{} },
		"reminder": func(m *Model) { m.ui.reminder = &reminderDialog{} },
	}
	for name, set := range busy {
		t.Run(name, func(t *testing.T) {
			m, _ := tourShell(t, 120, 30, 0, false)
			set(&m)
			m = tourPoll(t, m)
			if m.ui.tour != nil {
				t.Fatalf("the tour opened over %s", name)
			}
			m.ui.compose, m.ui.msgDraft, m.ui.pausePopup, m.ui.reminder = nil, "", nil, nil
			m.closeReader()
			m = step(t, m, tickMsg(shellNow))
			if m.ui.tour == nil {
				t.Errorf("the tour did not open once %s ended", name)
			}
		})
	}
	t.Run("ask", func(t *testing.T) {
		m := fixtureShell(t, 120, 30)
		m = keys(t, m, '!') // the open questions of the fixture's waiting agent
		if !m.rd.open() || m.rd.top().kind != ovAsk {
			t.Fatal("! opened no ask page")
		}
		m.cfg.tour = &tourMarker{path: filepath.Join(t.TempDir(), tourMarkerFile)}
		if m = step(t, m, tickMsg(shellNow)); m.ui.tour != nil {
			t.Fatal("the tour opened over the ask page")
		}
		m.closeReader()
		if m = step(t, m, tickMsg(shellNow)); m.ui.tour == nil {
			t.Error("the tour did not open once the ask page closed")
		}
	})
	t.Run("bell", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), tourMarkerFile)
		m := newShell(shellConfig{project: "agent-bus-monitor", self: "charlie", settings: busmonSettings{Bell: true},
			tour: &tourMarker{path: path}})
		m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
		m = tourPoll(t, m) // the fixture's waiting agents ring
		if m.ui.tour != nil {
			t.Fatal("the tour opened over a bell")
		}
		if m = step(t, m, tickMsg(shellNow)); m.ui.tour == nil {
			t.Error("the tour did not open on the next calm update")
		}
	})
}

// A click on next/back/skip, in the modal's key line or in the footer.
func TestTourClicks(t *testing.T) {
	m, path := tourShell(t, 120, 30, 0, true)
	r := m.tourRect()
	clickWord := func(m Model, y int, word string) Model {
		t.Helper()
		row := ""
		g := m.grid()
		for x := 0; x < m.width; x++ {
			row += g.at(x, y).text
		}
		i := strings.Index(row, word)
		if i < 0 {
			t.Fatalf("no %q on row %d: %q", word, y, row)
		}
		x := Width(row[:i])
		next, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return run(t, next.(Model), cmd)
	}
	m = clickWord(m, r.Y+r.H-2, "next")
	if m.ui.tour.at != 1 {
		t.Fatalf("click on next: page %d", m.ui.tour.at)
	}
	m = clickWord(m, m.layout.Footer.Y, "back")
	if m.ui.tour.at != 0 {
		t.Fatalf("click on the footer's back: page %d", m.ui.tour.at)
	}
	m = clickWord(m, r.Y+r.H-2, "skip")
	if m.ui.tour != nil || readMarker(t, path) != tourVersion {
		t.Error("click on skip did not close the tour and write the marker")
	}
}

// Every (mode, action) the tour names has a binding: a removed key cannot
// stay advertised.
func TestTourKeysExist(t *testing.T) {
	n := 0
	for _, pg := range tourBook {
		for _, ref := range pg.refs() {
			n++
			if tourKeys(ref) == "" {
				t.Errorf("page %q names mode %v action %v, which has no binding", pg.title, ref.m, ref.act)
			}
		}
	}
	if n < 20 {
		t.Errorf("only %d key cells: the pages lost their keys", n)
	}
}

// bareKey is a key name typed into prose instead of drawn from a table.
var bareKey = regexp.MustCompile(`^(F[0-9]{1,2}|\^[A-Z]|esc|enter|tab|shift-tab|pgup|pgdn|home|end|[!?/:@` + GlyphArrowL + GlyphArrowR + `]|[B-Zb-z]|[0-9](-[0-9])?)$`)

func TestTourMentionsNoBareKey(t *testing.T) {
	for _, pg := range tourBook {
		for _, s := range pg.prose() {
			for _, w := range strings.Fields(s) {
				w = strings.TrimRight(w, ",.;")
				if bareKey.MatchString(w) {
					t.Errorf("page %q: %q in %q is a key outside a binding cell", pg.title, w, s)
				}
			}
		}
	}
	if !bareKey.MatchString("F4") || !bareKey.MatchString("^S") || !bareKey.MatchString("!") || bareKey.MatchString("a") {
		t.Error("bareKey misses the keys it is for")
	}
}

// The header page draws each pill with the header's own functions.
func TestTourHeaderPageUsesTheHeaderPills(t *testing.T) {
	plain := func(l Line) string { return l.text() }
	got := ""
	for _, l := range tourLines(tourBook[len(tourBook)-1], 72, "charlie") {
		got += l.text() + "\n"
	}
	for _, want := range []string{plain(healthSeg(health{ok: true}, shellNow, false)), plain(dispatchSeg("up", false)),
		plain(missionsSeg("up", false)), plain(guardianSeg(tourGuardian(), 100)), humanLabel("charlie", "")} {
		if !strings.Contains(got, want) {
			t.Errorf("header page lacks the pill %q:\n%s", want, got)
		}
	}
}

// Each page at 120 and 80 columns: the text once, the styled content in
// every palette.
func TestTourGoldens(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{120, 30}, {80, 24}} {
		for i := range tourBook {
			for _, pal := range []struct {
				name    string
				noColor bool
				colors  int
			}{{"dark", false, 1 << 24}, {"light", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
				t.Run(fmt.Sprintf("page%d-%dx%d-%s-%d-%v", i+1, size[0], size[1], pal.name, pal.colors, pal.noColor), func(t *testing.T) {
					withPalette(t, pal.name, pal.noColor, pal.colors)
					m, _ := tourShell(t, size[0], size[1], 0, true)
					for range i {
						m = keys(t, m, kEnter)
					}
					m.paint = newPainter(th)
					content := m.View().Content
					plain := m.grid().plain()
					for _, method := range []ansi.Method{ansi.WcWidth, ansi.GraphemeWidth} {
						if got := placed(content, size[0], size[1], method); got != plain {
							t.Fatalf("width method %v places the View otherwise", method)
						}
					}
					base := fmt.Sprintf("tour%d-%dx%d", i+1, size[0], size[1])
					if pal.name == "dark" && !pal.noColor && pal.colors == 1<<24 {
						checkGolden(t, base+".txt", plain+"\n")
					}
					suffix := pal.name
					switch {
					case pal.noColor:
						suffix = "none"
					case pal.colors == 16:
						suffix = "16"
					}
					checkGolden(t, base+"-"+suffix+".ansi", content+"\n")
				})
			}
		}
	}
}

// The modal stays straight at every size and hostile input, its prose
// inside it: at 60 columns it is the width minus 8, wrapped.
func TestTourAlignment(t *testing.T) {
	pinLocal(t)
	for _, sz := range shellAlignSizes {
		for i := range tourBook {
			m := hostileShell(t, sz[0], sz[1], false)
			if m.layout.Tier == TierTooSmall {
				continue
			}
			m.openTour(true)
			m.ui.tour.at = i
			m.refresh()
			r := m.tourRect()
			if !m.tourFits() { // too small to draw: only the footer's keys show
				continue
			}
			if sz[0] < 84 && r.W != sz[0]-8 {
				t.Errorf("%dx%d: modal %d wide, want %d", sz[0], sz[1], r.W, sz[0]-8)
			}
			g := m.grid()
			checkFrameAt(t, func(x, y int) string { return g.at(x, y).text }, fmt.Sprintf("tour page %d at %dx%d", i+1, sz[0], sz[1]), r, false)
			for _, l := range tourLines(tourBook[i], r.W-4, "charlie") {
				if l.Width() > r.W-4 {
					t.Errorf("%dx%d page %d: line %q is %d cells, wider than %d", sz[0], sz[1], i+1, l.text(), l.Width(), r.W-4)
				}
			}
		}
	}
}

// At every alignment size the auto-opened tour shows each of its pages
// whole, or does not open (it waits for a size later).
func TestTourAutoOpensOnlyWhenEveryPageFits(t *testing.T) {
	opened, waited := 0, 0
	for _, sz := range shellAlignSizes {
		m, _ := tourShell(t, sz[0], sz[1], 0, true)
		fits := true
		for i := range tourBook {
			if !m.pageFits(i) {
				fits = false
			}
		}
		switch {
		case m.ui.tour != nil && !fits:
			t.Errorf("%dx%d: the tour opened though a page is cut", sz[0], sz[1])
		case m.ui.tour == nil && fits && m.layout.Tier != TierTooSmall:
			t.Errorf("%dx%d: every page fits, yet the tour did not open", sz[0], sz[1])
		case m.ui.tour != nil:
			opened++
			for i := range tourBook {
				r := m.tourRectFor(i)
				if n := len(tourLines(tourBook[i], r.W-4, "charlie")); n > r.H-5 {
					t.Errorf("%dx%d page %d: %d lines in %d rows", sz[0], sz[1], i+1, n, r.H-5)
				}
			}
		default:
			waited++
		}
	}
	if opened == 0 || waited == 0 {
		t.Errorf("opened at %d sizes, waited at %d: the sizes no longer test both", opened, waited)
	}
}

// F1 then t on a short screen: a cut page says so in a muted row, and the
// marker is not written from a tour whose page was cut.
func TestTourCutPageIsNeverMarkedSeen(t *testing.T) {
	m, path := tourShell(t, 80, 11, 0, true)
	if m.ui.tour != nil {
		t.Fatal("the tour auto-opened on 11 rows")
	}
	m = keys(t, keys(t, m, kF1), 't')
	if m.ui.tour == nil {
		t.Fatal("F1 then t opened no tour")
	}
	if !strings.Contains(m.grid().plain(), "more") {
		t.Error("the cut page does not say so")
	}
	next, cmd := m.Update(keyMsg(kEsc))
	m = run(t, next.(Model), cmd)
	if m.ui.tour != nil {
		t.Fatal("esc left the tour open")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a tour with a cut page wrote the marker")
	}
}
