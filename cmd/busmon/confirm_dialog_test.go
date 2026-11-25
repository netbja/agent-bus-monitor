package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// busmon-confirm-dialog (the person, 2026-11-25): one yes/no dialog for
// every confirmation, its look and its keys shared.

func update(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// openTestDialog opens a dialog whose answers are recorded.
func openTestDialog(t *testing.T) (Model, *string) {
	t.Helper()
	m := shellWith(t, 140, 40, shellConfig{}, false)
	got := ""
	m.openConfirm(&confirmDialog{Title: "Do it?", Body: []string{"a line of detail"}, Yes: "Do", No: "Keep",
		OnYes: func(m *Model) tea.Cmd { got = "yes"; return nil },
		OnNo:  func(m *Model) tea.Cmd { got = "no"; return nil }})
	return m, &got
}

// 1, 2, 3: a solid box with padding, the question centred, the two buttons on
// one line, the safe one focused by default, the shortcuts in a hint line;
// what is behind is dimmed, never bleeding through.
func TestConfirmDialogLook(t *testing.T) {
	m, _ := openTestDialog(t)
	g := m.grid()
	d := m.ui.dialog
	box, yes, no, _ := d.layout(m.width, m.height)
	if d.focusYes {
		t.Fatal("the yes button is focused by default")
	}
	rows := map[int]string{}
	for y := box.Y; y < box.Y+box.H; y++ {
		var b strings.Builder
		for x := box.X; x < box.X+box.W; x++ {
			b.WriteString(g.at(x, y).text)
		}
		rows[y] = b.String()
	}
	title := []rune(rows[box.Y+2])
	i := strings.Index(string(title), "Do it?")
	if i >= 0 {
		i = len([]rune(string(title)[:i])) // in cells, not bytes: the border is 3 bytes
	}
	if i < 0 || abs(i-(box.W-len("Do it?"))/2) > 1 {
		t.Fatalf("title not centred: %q", title)
	}
	if rows[box.Y+1] != "│"+strings.Repeat(" ", box.W-2)+"│" {
		t.Fatalf("no padding row under the border: %q", rows[box.Y+1])
	}
	btn := rows[yes.Y]
	if yes.Y != no.Y || !strings.Contains(btn, "[ Do ]") || !strings.Contains(btn, "[ Keep ]") || strings.Index(btn, "[ Do ]") > strings.Index(btn, "[ Keep ]") {
		t.Fatalf("buttons %q", btn)
	}
	if !strings.Contains(rows[yes.Y+1], "y / n "+GlyphNone+" esc") {
		t.Fatalf("hint %q", rows[yes.Y+1])
	}
	if !g.at(no.X+2, no.Y).st.Pill || g.at(yes.X+2, yes.Y).st.Pill {
		t.Fatal("the focused button is not the highlighted one")
	}
	// The fill: every cell inside the box is the dialog's, on its surface.
	for y := box.Y + 1; y < box.Y+box.H-1; y++ {
		for x := box.X + 1; x < box.X+box.W-1; x++ {
			if c := g.at(x, y); !c.st.Surface && !c.st.Pill {
				t.Fatalf("cell %d,%d not on the dialog's surface: %+v", x, y, c)
			}
		}
	}
	// Dimmed behind: a cell outside the box carries a fade.
	if c := g.at(0, m.height-1); c.st.Fade == 0 && !c.st.Pill {
		t.Fatalf("the screen behind is not dimmed: %+v", c)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// 4: Left, Right and Tab move the focus; Enter activates the focused button;
// y answers yes, n and Esc no; any other key leaves it open.
func TestConfirmDialogKeys(t *testing.T) {
	for name, c := range map[string]struct {
		keys []tea.Key
		want string
	}{
		"enter: the safe default": {[]tea.Key{{Code: tea.KeyEnter}}, "no"},
		"tab then enter":          {[]tea.Key{{Code: tea.KeyTab}, {Code: tea.KeyEnter}}, "yes"},
		"left then enter":         {[]tea.Key{{Code: tea.KeyLeft}, {Code: tea.KeyEnter}}, "yes"},
		"left right enter":        {[]tea.Key{{Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyEnter}}, "no"},
		"tab tab enter":           {[]tea.Key{{Code: tea.KeyTab}, {Code: tea.KeyTab}, {Code: tea.KeyEnter}}, "no"},
		"y":                       {[]tea.Key{{Code: 'y', Text: "y"}}, "yes"},
		"Y":                       {[]tea.Key{{Code: 'Y', Text: "Y"}}, "yes"},
		"n":                       {[]tea.Key{{Code: 'n', Text: "n"}}, "no"},
		"esc":                     {[]tea.Key{{Code: tea.KeyEscape}}, "no"},
		"y after focus moves":     {[]tea.Key{{Code: tea.KeyRight}, {Code: 'y', Text: "y"}}, "yes"},
	} {
		m, got := openTestDialog(t)
		for _, k := range c.keys {
			m, _ = update(t, m, tea.KeyPressMsg(k))
		}
		if *got != c.want || m.ui.dialog != nil {
			t.Errorf("%s: answer %q (open %v), want %q", name, *got, m.ui.dialog != nil, c.want)
		}
	}
	m, got := openTestDialog(t)
	for _, k := range []tea.Key{{Code: 'j', Text: "j"}, {Code: 'q', Text: "q"}, {Code: tea.KeyUp}} {
		m, _ = update(t, m, tea.KeyPressMsg(k))
	}
	if *got != "" || m.ui.dialog == nil {
		t.Fatalf("another key answered or closed it: %q", *got)
	}
	if !m.busyForModal() {
		t.Fatal("the dialog does not hold the other modals")
	}
}

// 5: a click on a button activates it; a click outside stays; a click
// inside the box off the buttons does nothing.
func TestConfirmDialogMouse(t *testing.T) {
	click := func(x, y int) tea.MouseClickMsg { return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft} }
	m, got := openTestDialog(t)
	box, yes, no, _ := m.ui.dialog.layout(m.width, m.height)
	m, _ = update(t, m, click(box.X+1, box.Y+2))
	if *got != "" || m.ui.dialog == nil {
		t.Fatalf("a click inside the box off the buttons: %q", *got)
	}
	m, _ = update(t, m, click(yes.X+1, yes.Y))
	if *got != "yes" || m.ui.dialog != nil {
		t.Fatalf("a click on yes: %q", *got)
	}
	m, got = openTestDialog(t)
	m, _ = update(t, m, click(no.X+no.W-1, no.Y))
	if *got != "no" {
		t.Fatalf("a click on no: %q", *got)
	}
	m, got = openTestDialog(t)
	m, _ = update(t, m, click(0, 0))
	if *got != "no" || m.ui.dialog != nil {
		t.Fatalf("a click outside: %q", *got)
	}
	_ = m
}

// The quit popup is the dialog: [ Quit ] [ Stay ], Stay focused; Tab then
// Enter quits, a click on Quit quits, a click outside stays.
func TestQuitIsTheConfirmDialog(t *testing.T) {
	m := shellWith(t, 140, 40, shellConfig{}, false)
	m.cfg.settings.ConfirmQuit = true
	m, _ = update(t, m, tea.KeyPressMsg(kq))
	if m.ui.dialog == nil || !strings.Contains(m.grid().plain(), "[ Quit ]") || !strings.Contains(m.grid().plain(), "[ Stay ]") {
		t.Fatalf("no quit dialog:\n%s", m.grid().plain())
	}
	m2, _ := update(t, m, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if _, cmd := update(t, m2, tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})); !isQuit(cmd) {
		t.Fatal("Tab, Enter did not quit")
	}
	_, yes, _, _ := m.ui.dialog.layout(m.width, m.height)
	if _, cmd := update(t, m, tea.MouseClickMsg{X: yes.X + 1, Y: yes.Y, Button: tea.MouseLeft}); !isQuit(cmd) {
		t.Fatal("a click on Quit did not quit")
	}
	m3, cmd := update(t, m, tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if isQuit(cmd) || m3.ui.dialog != nil {
		t.Fatal("a click outside did not stay")
	}
}

// dialogText is the open dialog's question and detail lines, one string
// ("" none): what the migrated confirmations' tests read.
func dialogText(m Model) string {
	if d := m.ui.dialog; d != nil {
		return d.Title + " " + strings.Join(d.Body, " ")
	}
	return ""
}

// reviewer-codex: on a screen too narrow to draw the buttons, or the dialog
// at all, no click anywhere activates yes; a click outside the box is always
// no, checked first; the hit boxes are what is drawn.
func TestConfirmDialogNarrowNeverClicksYes(t *testing.T) {
	for _, size := range [][2]int{{13, 20}, {16, 20}, {20, 6}, {30, 4}, {8, 3}} {
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				m, got := openTestDialog(t)
				m.width, m.height = size[0], size[1]
				d := m.ui.dialog
				m.dialogClick(x, y)
				if *got == "yes" {
					box, yes, _, shown := d.layout(size[0], size[1])
					t.Fatalf("%dx%d: a click at %d,%d said yes (box %+v, yes %+v, shown %v)", size[0], size[1], x, y, box, yes, shown)
				}
			}
		}
	}
	// Shown: every drawn button cell is inside the box's interior.
	for _, w := range []int{20, 24, 30, 60, 140} {
		m, _ := openTestDialog(t)
		box, yes, no, shown := m.ui.dialog.layout(w, 20)
		if !shown {
			continue
		}
		in := func(r Rect) bool { return r.X > box.X && r.X+r.W < box.X+box.W && r.Y > box.Y && r.Y < box.Y+box.H-1 }
		if !in(yes) || !in(no) {
			t.Errorf("width %d: a shown button outside the box: box %+v yes %+v no %+v", w, box, yes, no)
		}
	}
}
