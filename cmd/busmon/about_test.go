package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The packet's route: from master's node down onto the bus, along it to
// coder's node, then back the same way; every frame one cell from the last.
func TestAboutRoute(t *testing.T) {
	r := aboutRoute()
	if r[0] != (aboutPoint{aboutMasterX, 0}) || r[len(r)-1] != (aboutPoint{aboutMasterX, 0}) {
		t.Fatalf("route starts %v, ends %v", r[0], r[len(r)-1])
	}
	reached := false
	for i := 1; i < len(r); i++ {
		dx, dy := r[i].x-r[i-1].x, r[i].y-r[i-1].y
		if dx*dx+dy*dy != 1 {
			t.Fatalf("frame %d jumps from %v to %v", i, r[i-1], r[i])
		}
		if r[i] == (aboutPoint{aboutCoderX, 0}) {
			reached = true
		}
	}
	if !reached {
		t.Fatal("the packet never reaches coder")
	}
}

// The drawing says where the run is: ready before, the packet on its way
// (a directive out, a reply back), delivered once done, no packet then; a
// still drawing has no packet whatever the step, and says why.
func TestAboutBody(t *testing.T) {
	a := aboutState{versions: aboutVersions{Project: "demo", Build: []aboutRow{{Label: "busmon", Value: "dev"}, agentbusRow("v0.26.1", "dev")}}, loaded: true}
	text := func(step int, still bool) string { return tokLinesText(aboutBody(a, step, still, 80)) }
	if b := text(0, false); !strings.Contains(b, "bus ready") || strings.Contains(b, GlyphWorking) || !strings.Contains(b, "v0.26.1") {
		t.Fatalf("step 0:\n%s", b)
	}
	if b := text(3, false); !strings.Contains(b, "a directive") || !strings.Contains(b, GlyphWorking) {
		t.Fatalf("on its way:\n%s", b)
	}
	if b := text(len(aboutRoute())-3, false); !strings.Contains(b, "a reply") {
		t.Fatalf("coming back:\n%s", b)
	}
	b := text(len(aboutRoute()), false)
	if !strings.Contains(b, "delivered") || strings.Contains(b, GlyphWorking) || !strings.Contains(b, "master") || !strings.Contains(b, "coder") {
		t.Fatalf("done:\n%s", b)
	}
	if b := text(5, true); strings.Contains(b, GlyphWorking) || !strings.Contains(b, "bus ready") || !strings.Contains(b, "still:") {
		t.Fatalf("still:\n%s", b)
	}
	for _, w := range []int{20, 36, 40, 80, 140} {
		for _, l := range aboutBody(a, 5, false, w) {
			if l.Width() > w {
				t.Fatalf("a line is %d cells at %d", l.Width(), w)
			}
		}
	}
}

// about-design: the wordmark first (the name in words when it does not
// fit), then the versions in two sections whose values start in one column.
func TestAboutLayout(t *testing.T) {
	for _, row := range aboutLogo {
		if Width(row) != aboutLogoW {
			t.Fatalf("a wordmark row is %d cells, not %d", Width(row), aboutLogoW)
		}
	}
	a := aboutState{loaded: true, versions: aboutVersions{Project: "ansible-devtools",
		Build: []aboutRow{{Label: "busmon", Value: "v1.0.0"}, agentbusRow("v1.0.0", "v1.0.0"), {Label: "protocol", Value: "v1"}},
		Host:  []aboutRow{{Label: "redis", Value: "8.10.2"}, {Label: "herdr", Value: "0.9.5"}}}}
	lines := strings.Split(tokLinesText(aboutBody(a, 0, true, 100)), "\n")
	logo := -1
	for i, l := range lines {
		if strings.Contains(l, aboutLogo[0]) {
			logo = i
			break
		}
	}
	if logo < 0 || logo > 2 {
		t.Fatalf("the wordmark is not at the top (line %d):\n%s", logo, strings.Join(lines, "\n"))
	}
	col := -1
	for _, v := range []string{"ansible-devtools", "v1.0.0  ", "v1\n", "8.10.2", "0.9.5"} {
		for _, l := range lines {
			if i := strings.Index(l+"\n", v); i >= 0 {
				switch c := Width(l[:i]); {
				case col < 0:
					col = c
				case c != col:
					t.Fatalf("%q starts at %d, not %d", v, c, col)
				}
				break
			}
		}
	}
	body := strings.Join(lines, "\n")
	for _, s := range []string{"THIS BUILD", "THIS HOST", GlyphDone + " same build"} {
		if !strings.Contains(body, s) {
			t.Fatalf("no %q:\n%s", s, body)
		}
	}
	if narrow := tokLinesText(aboutBody(a, 0, true, 30)); strings.Contains(narrow, aboutLogo[0]) || !strings.Contains(narrow, "agent-bus monitor") {
		t.Fatalf("narrow:\n%s", narrow)
	}
}

// agentbus beside busmon is marked the same build or not; the update state is
// marked up to date or newer, and muted otherwise.
func TestAboutRowMarks(t *testing.T) {
	if r := agentbusRow("v2", "v1"); !strings.Contains(r.Note, GlyphNotEqual) || r.NoteRole != RoleAttention {
		t.Fatalf("differs: %+v", r)
	}
	if r := agentbusRow("agentbus ?", "v1"); r.Value != "?" || r.Note != "" {
		t.Fatalf("unknown: %+v", r)
	}
	if r := updateRow("up to date (v1, checked Nov 3 18:37)"); r.Role != RoleOK || !strings.HasPrefix(r.Value, GlyphDone) {
		t.Fatalf("up to date: %+v", r)
	}
	if r := updateRow("update v2 available (checked Nov 3 18:37)"); r.Role != RoleAttention || !strings.HasPrefix(r.Value, GlyphUp) {
		t.Fatalf("newer: %+v", r)
	}
	if r := updateRow("update unknown (no check recorded)"); r.Role != RoleMuted {
		t.Fatalf("unknown: %+v", r)
	}
}

func aboutShell(t *testing.T, fade bool) Model {
	t.Helper()
	m := fixtureShell(t, 120, 40)
	m.cfg.settings.Fade, m.cfg.settings.UpdateCheck = fade, true
	m.cfg.about = aboutSources{
		agentbus: func() string { return "agentbus version v9.9.9" },
		herdr:    func() string { return "herdr 0.9.5" },
		plugin:   func() string { return "0.26.1" },
		update:   func() string { return "update v9.9.10 available" },
	}
	return m
}

// openAboutIn presses A and feeds the versions read; it returns whether a
// fade frame was scheduled.
func openAboutIn(t *testing.T, m Model) (Model, bool) {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'A', Text: "A"}))
	m = next.(Model)
	return feedAbout(t, m, cmd)
}

func feedAbout(t *testing.T, m Model, cmd tea.Cmd) (Model, bool) {
	t.Helper()
	fade := false
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		case fadeMsg:
			fade = true
		case aboutVersionsMsg:
			next, more := m.Update(msg)
			m = next.(Model)
			walk(more)
		}
	}
	walk(cmd)
	return m, fade
}

// A opens About with every version the plan names (y copies them), and in
// true colour with fade on the packet rides the fade schedule: the run
// starts at the first frame, moves with the frames' time, ends delivered and
// stops asking for frames; r plays it again.
func TestAboutPageRidesTheFadeSchedule(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	calm := aboutShell(t, true)
	calm.snap.agents, calm.notice.cur, calm.bars = map[string]*agentState{}, nil, nil // nothing else moves
	if calm.animated() {
		t.Fatal("the calm shell moves")
	}
	m, fade := openAboutIn(t, calm)
	if !m.animated() {
		t.Fatal("About does not count as a motion of the fade schedule")
	}
	p := m.rd.top()
	if p == nil || p.kind != ovAbout {
		t.Fatal("A opened no About page")
	}
	for _, s := range []string{"project agent-bus-monitor", "busmon ", "agentbus version v9.9.9", "protocol v", "redis ", "herdr 0.9.5", "plugin 0.26.1", "update v9.9.10 available"} {
		if !strings.Contains(p.plain, s) {
			t.Errorf("y copies no %q:\n%s", s, p.plain)
		}
	}
	if !fade || !m.pageOffers(ActReplay) {
		t.Fatalf("no fade frame scheduled (%v) or r not offered", fade)
	}
	t0 := shellNow.Add(time.Second)
	frame := func(at time.Time) tea.Cmd {
		next, cmd := m.Update(fadeMsg(at))
		m = next.(Model)
		return cmd
	}
	frame(t0)
	if a := m.rd.top().about; !a.start.Equal(t0) || a.step(m.now) != 0 {
		t.Fatalf("the run did not start at the first frame: %+v", a)
	}
	frame(t0.Add(10 * aboutCell))
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, GlyphWorking) || !strings.Contains(body, "a directive") {
		t.Fatalf("mid-run:\n%s", body)
	}
	cmd := frame(t0.Add(time.Duration(len(aboutRoute())+1) * aboutCell))
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "delivered both ways") {
		t.Fatalf("end:\n%s", body)
	}
	_ = cmd // the fixture's own motions (a waiting agent) may still ask for frames
	if m.aboutPlaying() {
		t.Fatal("a finished run still asks for frames")
	}
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"}))
	m = next.(Model)
	if !m.rd.top().about.start.IsZero() || !m.aboutPlaying() {
		t.Fatal("r did not restart the run")
	}
	switch { // the next fade frame starts it: one already scheduled, or r's
	case cmd == nil && !m.sig.fading:
		t.Fatal("r scheduled no fade frame")
	case cmd != nil:
		if _, isFade := cmd().(fadeMsg); !isFade {
			t.Fatal("r's command is not a fade frame")
		}
	}
}

// With fade off, in 16 colours or with no colour the drawing is still: no
// fade frame is scheduled, a frame moves nothing, r says there is nothing
// to replay.
func TestAboutStillWithoutMotion(t *testing.T) {
	for _, c := range []struct {
		name          string
		fade, noColor bool
		colors        int
	}{{"fade off", false, false, 1 << 24}, {"16 colours", true, false, 16}, {"no colour", true, true, 1 << 24}} {
		t.Run(c.name, func(t *testing.T) {
			withPalette(t, "dark", c.noColor, c.colors)
			m, fade := openAboutIn(t, aboutShell(t, c.fade))
			if fade || m.animated() && m.aboutMotion() {
				t.Fatal("a fade frame was scheduled")
			}
			next, _ := m.Update(fadeMsg(shellNow.Add(2 * time.Second))) // another motion's frame
			m = next.(Model)
			body := tokLinesText(m.rd.top().body)
			if strings.Contains(body, GlyphWorking) || !strings.Contains(body, "still:") || !m.rd.top().about.start.IsZero() {
				t.Fatalf("moved:\n%s", body)
			}
			next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"}))
			m = next.(Model)
			if cmd != nil || !strings.Contains(m.ui.flash, "nothing to replay") {
				t.Fatalf("r: cmd %v, flash %q", cmd != nil, m.ui.flash)
			}
		})
	}
}

// The plugin's version from the first herdr-plugin.toml found; the update
// state from the cache only.
func TestAboutSources(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(b, "herdr-plugin.toml"), []byte("id = \"x\"\nversion = \"1.2.3\"  # bumped\n"), 0o600)
	if v := pluginVersion([]string{a, b}); v != "1.2.3" {
		t.Fatalf("plugin %q", v)
	}
	if v := pluginVersion([]string{a}); v != "?" {
		t.Fatalf("no file: %q", v)
	}
	cache := filepath.Join(a, "latest.json")
	if s := updateState(cache, "v1.0.0"); !strings.Contains(s, "unknown") {
		t.Fatalf("no cache: %q", s)
	}
	os.WriteFile(cache, []byte(`{"checked_at":"2026-11-04T08:00:00Z","tag":"v1.1.0"}`), 0o600)
	if s := updateState(cache, "v1.0.0"); !strings.Contains(s, "v1.1.0 available") {
		t.Fatalf("newer: %q", s)
	}
	if s := updateState(cache, "v1.1.0"); !strings.HasPrefix(s, "up to date") {
		t.Fatalf("current: %q", s)
	}
	if s := updateState(cache, "dev"); !strings.Contains(s, "dev build") {
		t.Fatalf("dev: %q", s)
	}
}
