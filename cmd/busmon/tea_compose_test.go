package main

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"

	"github.com/netbja/agent-bus-monitor/bus"
)

var (
	kCtrlS = tea.Key{Code: 's', Mod: tea.ModCtrl}
	kCtrlO = tea.Key{Code: 'o', Mod: tea.ModCtrl}
)

// The window's cap is in runes, enforced in Update: a paste past it is cut
// to what is left, never mid-rune, and a rune typed at the limit is refused,
// whatever its width (bubbles' textarea counted cells and stopped wide runes
// at half the cap).
func TestShellComposeRuneCap(t *testing.T) {
	pinLocal(t)
	for _, r := range []string{"a", "é", "日", "😀"} {
		m := keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'I', kTab) // the text
		m = step(t, m, tea.PasteMsg{Content: strings.Repeat(r, bus.CmdMaxRunes+10)})
		v := m.ui.compose.area.value()
		if n := utf8.RuneCountInString(v); n != bus.CmdMaxRunes || !utf8.ValidString(v) {
			t.Fatalf("%q pasted: %d runes (cap %d), valid %v", r, n, bus.CmdMaxRunes, utf8.ValidString(v))
		}
		if !strings.Contains(m.ui.flash, "10 dropped") {
			t.Fatalf("%q: flash %q", r, m.ui.flash)
		}
		if m = keys(t, m, []rune(r)[0]); utf8.RuneCountInString(m.ui.compose.area.value()) != bus.CmdMaxRunes {
			t.Fatalf("%q typed at the limit was taken", r)
		}
		m = keys(t, m, kBackspce, []rune(r)[0])
		if utf8.RuneCountInString(m.ui.compose.area.value()) != bus.CmdMaxRunes {
			t.Fatalf("%q: a rune typed under the limit was refused", r)
		}
	}
}

// I opens the window on the recipient field; Tab takes its completion and
// moves to the text; Enter is a new line; Ctrl+S sends recipient and body as
// fields through the send path, in a command, and the window closes.
func TestShellComposeSends(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), 'I')
	if c := m.ui.compose; c == nil || !c.onTo || m.mode() != ModeCompose {
		t.Fatal("I did not open the window on its recipient")
	}
	m = typeText(t, m, "@cod")
	if m = keys(t, m, kTab); m.ui.compose.to.value() != "@coder" || m.ui.compose.onTo {
		t.Fatalf("Tab: to %q, onTo %v", m.ui.compose.to.value(), m.ui.compose.onTo)
	}
	m = typeText(t, m, "line one")
	m = keys(t, m, kEnter)
	m = typeText(t, m, "line two")
	if got := m.grid().plain(); !strings.Contains(got, "line one") || !strings.Contains(got, "to: @coder") || !strings.Contains(got, "directive") {
		t.Fatalf("the window does not show its fields:\n%s", got)
	}
	m, cmd := pressCmd(t, m, kCtrlS)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"cmd coder line one\nline two"}) || m.ui.compose != nil {
		t.Fatalf("sent %q, window open %v", fw.sent, m.ui.compose != nil)
	}
	if m.composeHasDraft() {
		t.Fatal("the sent text stayed as a draft")
	}
}

// Ctrl+O moves the line's text to the window; Esc closes it, the draft
// kept, and i (a window draft is never flattened into the line) reopens it.
func TestShellComposeFromTheLine(t *testing.T) {
	pinLocal(t)
	m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'i'), "@docs first part")
	m = keys(t, m, kCtrlO)
	c := m.ui.compose
	if c == nil || c.to.value() != "@docs" || c.area.value() != "first part" || m.ui.line != lineNone {
		t.Fatalf("Ctrl+O: %+v", c)
	}
	m = keys(t, m, kEsc)
	if m.ui.compose != nil || m.ui.msgCompose.body != "first part" {
		t.Fatalf("Esc: open %v, draft %+v", m.ui.compose != nil, m.ui.msgCompose)
	}
	if m = keys(t, m, 'i'); m.ui.compose == nil || m.ui.compose.area.value() != "first part" {
		t.Fatal("i did not reopen the window on its draft")
	}
}

// Clicks: on the recipient row it takes the keyboard, in the text the text
// does (and the cursor goes there), outside the window it closes, the draft
// kept.
func TestShellComposeClicks(t *testing.T) {
	pinLocal(t)
	m := typeText(t, keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'I', kTab), "hello world")
	f, tr := m.composeRects()
	if m = clickAt(t, m, f.X+6, f.Y+1); !m.ui.compose.onTo {
		t.Fatal("a click on the recipient row did not focus it")
	}
	if m = clickAt(t, m, tr.X+5, tr.Y); m.ui.compose.onTo || m.ui.compose.area.cur != 5 {
		t.Fatalf("a click in the text: onTo %v, cursor %d", m.ui.compose.onTo, m.ui.compose.area.cur)
	}
	m = typeText(t, m, ",")
	if got := m.ui.compose.area.value(); got != "hello, world" {
		t.Fatalf("typed at the click: %q", got)
	}
	if m = clickAt(t, m, 0, m.height-1); m.ui.compose != nil || m.ui.msgCompose.body != "hello, world" {
		t.Fatal("a click outside did not close the window with its draft")
	}
}

// A free answer written in the window is sent as the question's answer, and
// the window closes on its success.
func TestShellComposeAnswers(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{left: 1}
	m := shellWith(t, 140, 40, shellConfig{w: fw}, false)
	id := m.snap.asks[0].ID
	m = keys(t, m, '!', 'o') // Other: the window on the first question
	if c := m.ui.compose; c == nil || c.kind != composeAnswer || c.qid != "go" {
		t.Fatalf("o: %+v", m.ui.compose)
	}
	m = typeText(t, m, "only after the tests")
	m, cmd := pressCmd(t, m, kCtrlS)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"answer " + id + " go only after the tests"}) || m.ui.compose != nil {
		t.Fatalf("sent %q, window open %v", fw.sent, m.ui.compose != nil)
	}
	if p := m.rd.top(); p == nil || p.kind != ovAsk || p.ask.q != 1 {
		t.Fatalf("the ask did not move to its next question: %+v", p)
	}
}

// The text area wraps at a space, moves by row with the arrows and keeps the
// cursor in view.
func TestShellComposeTextArea(t *testing.T) {
	ta := textArea{limit: 100}
	ta.insert("one two three four")
	if rs := ta.prepare(9); len(rs) != 3 || string(ta.text[rs[1].from:rs[1].to]) != "three " {
		var got []string
		for _, r := range rs {
			got = append(got, string(ta.text[r.from:r.to]))
		}
		t.Fatalf("rows at 9: %q", got)
	}
	ta.vertical(ta.prepare(9), -1)
	if r, _ := ta.cursorAt(ta.prepare(9)); r != 1 {
		t.Fatalf("up: row %d", r)
	}
	ta.keepCursor(9, 1)
	if ta.top != 1 {
		t.Fatalf("keepCursor: top %d", ta.top)
	}
	ta.set("a日b")
	ta.left(ta.prepare(9))
	ta.backspace(ta.prepare(9))
	if ta.value() != "ab" || ta.cur != 1 {
		t.Fatalf("backspace by cluster: %q at %d", ta.value(), ta.cur)
	}
	// Across a newline: left from a row's start lands on the newline,
	// backspace there joins the lines.
	ta.set("ab\ncd")
	ta.cur = 3
	ta.backspace(ta.prepare(9))
	if ta.value() != "abcd" || ta.cur != 2 {
		t.Fatalf("backspace over a newline: %q at %d", ta.value(), ta.cur)
	}
	// The one-pass wrap agrees with the old per-row wrap on a mixed text.
	ta.set("日本語のテキスト and some words, 😀 emoji\n\nlast")
	var got []string
	for _, r := range ta.prepare(7) {
		got = append(got, string(ta.text[r.from:r.to]))
	}
	want := []string{"日本語", "のテキ", "スト ", "and ", "some ", "words, ", "😀 ", "emoji", "", "last"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows at 7: %q, want %q", got, want)
	}
}

// A key in a full window stays cheap: the text is wrapped once per change,
// in one pass, on the update path, and View reads the rows (foureyes'
// measure was 0.6 s a key at the cap, quadratic in the text).
func TestShellComposeKeyCost(t *testing.T) {
	pinLocal(t)
	m := keys(t, shellWith(t, 140, 40, shellConfig{}, false), 'I', kTab)
	m = step(t, m, tea.PasteMsg{Content: strings.Repeat("word ", bus.CmdMaxRunes/5)})
	m = keys(t, m, tea.Key{Code: tea.KeyHome}, kUp, kUp)
	calls := wrapCalls
	start := time.Now()
	m = keys(t, m, kBackspce)
	_ = m.View()
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Errorf("one key and its frame at the cap took %v", d)
	}
	if n := wrapCalls - calls; n != 1 {
		t.Errorf("one key wrapped the text %d times, want once", n)
	}
	// A fade frame (ten a second while an agent waits) wraps nothing.
	calls = wrapCalls
	m = step(t, m, fadeMsg(shellNow.Add(100*time.Millisecond)))
	_ = m.View()
	if n := wrapCalls - calls; n != 0 {
		t.Errorf("a fade frame with the window open wrapped the text %d times", n)
	}
}

// Every row of wrap fits its width (a single cluster wider than the width
// excepted: it takes a row of its own), and the rows cover the text in order.
func TestShellWrapRowsFit(t *testing.T) {
	alphabet := []string{"a", "b", " ", "日", "😀", "é", "\n", "本", " "}
	check := func(text []rune, width int) {
		t.Helper()
		pos := 0
		for _, r := range wrap(text, width) {
			if r.from < pos || r.to < r.from {
				t.Fatalf("%q at %d: rows out of order: %v", string(text), width, wrap(text, width))
			}
			for pos < r.from { // only newlines between rows
				if text[pos] != '\n' {
					t.Fatalf("%q at %d: rune %d lost", string(text), width, pos)
				}
				pos++
			}
			s := string(text[r.from:r.to])
			if w := Width(s); w > width && uniseg.GraphemeClusterCount(s) > 1 {
				t.Fatalf("%q at %d: row %q is %d cells", string(text), width, s, w)
			}
			pos = r.to
		}
		if pos != len(text) {
			t.Fatalf("%q at %d: the rows end at %d of %d", string(text), width, pos, len(text))
		}
	}
	check([]rune(" bcd日"), 4) // foureyes' case: a wide cluster right after a break at a space
	seed := uint32(1)
	for n := 0; n < 3000; n++ {
		var b strings.Builder
		for i := 0; i < 1+n%23; i++ {
			seed = seed*1664525 + 1013904223
			b.WriteString(alphabet[seed>>24%uint32(len(alphabet))])
		}
		check([]rune(b.String()), 1+n%10)
	}
}

// replaceAsk is the snapshot after the asking agent replaced its first ask
// (ask --replace): the old ask reads replaced, pointing at the new id, and
// edit changes the new ask's questions.
func replaceAsk(m Model, newID string, edit func(qs []bus.AskQuestionState)) (Model, bus.Ask) {
	old := m.snap.asks[0]
	nw := old
	nw.ID, nw.State = newID, "open"
	nw.Questions = slices.Clone(old.Questions)
	if edit != nil {
		edit(nw.Questions)
	}
	old.State, old.ReplacedBy = "replaced", newID
	m.snap.asks = append([]bus.Ask{old, nw}, m.snap.asks[1:]...)
	return m, old
}

// The person's case (ask-replaced-answer): they type a free answer, the agent
// replaces the ask meanwhile with that question word for word, and their send
// goes to the new ask, as the bus itself follows replaced_by.
func TestComposeAnswerFollowsAReplacedAsk(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{left: 1}
	m := keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), '!', 'o')
	m = typeText(t, m, "a long considered answer")
	m, _ = replaceAsk(m, "900-0", nil)
	m, cmd := pressCmd(t, m, kCtrlS)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"answer 900-0 go a long considered answer"}) || m.ui.compose != nil {
		t.Fatalf("sent %q, window open %v, flash %q", fw.sent, m.ui.compose != nil, m.ui.flash)
	}
}

// The question changed in the replacement, or the ask closed: nothing is
// sent, the text stays, the flash says so and where it can go; a second send
// turns it into a message to the asking agent, the text kept, for the person
// to read before sending.
func TestComposeAnswerKeepsTheTextWhenTheQuestionIsGone(t *testing.T) {
	pinLocal(t)
	for name, change := range map[string]func(Model) (Model, bus.Ask){
		"reworded": func(m Model) (Model, bus.Ask) {
			return replaceAsk(m, "900-0", func(qs []bus.AskQuestionState) { qs[0].Question = "Merge lot 1 tomorrow?" })
		},
		"dropped": func(m Model) (Model, bus.Ask) {
			return replaceAsk(m, "900-0", func(qs []bus.AskQuestionState) { qs[0].ID = "later" })
		},
		"closed": func(m Model) (Model, bus.Ask) {
			a := m.snap.asks[0]
			a.State = "withdrawn"
			m.snap.asks = append([]bus.Ask{a}, m.snap.asks[1:]...)
			return m, a
		},
	} {
		t.Run(name, func(t *testing.T) {
			fw := &fakeWriter{left: 1}
			m := keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), '!', 'o')
			m = typeText(t, m, "a long considered answer")
			m, old := change(m)
			m, cmd := pressCmd(t, m, kCtrlS)
			m = run(t, m, cmd)
			c := m.ui.compose
			if len(fw.sent) != 0 || c == nil || c.kind != composeAnswer || c.area.value() != "a long considered answer" {
				t.Fatalf("first send: sent %q, window %+v", fw.sent, c)
			}
			if !strings.Contains(m.ui.flash, "kept") || !strings.Contains(m.ui.flash, old.From) {
				t.Errorf("the flash does not say the text is kept and where it can go: %q", m.ui.flash)
			}
			m, cmd = pressCmd(t, m, kCtrlS)
			m = run(t, m, cmd)
			c = m.ui.compose
			if len(fw.sent) != 0 || c == nil || c.kind != composeMessage || c.recipient() != old.From || c.area.value() != "a long considered answer" {
				t.Fatalf("second send: sent %q, window %+v", fw.sent, c)
			}
		})
	}
}

// A draft left in the window (Esc) before the replacement comes back when the
// same, unchanged question of the new ask is answered.
func TestAnswerDraftFollowsAReplacedAsk(t *testing.T) {
	pinLocal(t)
	m := keys(t, shellWith(t, 140, 40, shellConfig{w: &fakeWriter{left: 1}}, false), '!', 'o')
	m = typeText(t, m, "half written")
	m = keys(t, m, kEsc)
	if m.ui.compose != nil {
		m = keys(t, m, kEsc)
	}
	m, _ = replaceAsk(m, "900-0", nil)
	m = keys(t, m, kEsc, kEsc, '!', 'o')
	if c := m.ui.compose; c == nil || c.askID != "900-0" || c.area.value() != "half written" {
		t.Fatalf("the draft did not follow: %+v", c)
	}
}

// orphanAnswer is the window after a free answer was typed and its question
// closed meanwhile: the first send left it an orphan, the text in it.
func orphanAnswer(t *testing.T, fw *fakeWriter) (Model, string) {
	t.Helper()
	m := keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), '!', 'o')
	m = typeText(t, m, "a long considered answer")
	a := m.snap.asks[0]
	a.State = "withdrawn"
	m.snap.asks = append([]bus.Ask{a}, m.snap.asks[1:]...)
	m, cmd := pressCmd(t, m, kCtrlS)
	m = run(t, m, cmd)
	if c := m.ui.compose; c == nil || !c.orphan {
		t.Fatalf("not an orphan: %+v", c)
	}
	return m, a.From
}

// escapeAll closes the window and every page with Esc.
func escapeAll(t *testing.T, m Model) Model {
	t.Helper()
	for range 5 {
		if m.ui.compose == nil && m.rd.top() == nil {
			return m
		}
		m = keys(t, m, kEsc)
	}
	t.Fatal("Esc does not close the window and the pages")
	return m
}

// Esc on an orphan answer keeps its text where the person finds it again: a
// message draft to the asking agent, which the message window opens on.
func TestOrphanAnswerEscKeepsAMessageDraft(t *testing.T) {
	pinLocal(t)
	m, from := orphanAnswer(t, &fakeWriter{left: 1})
	m = escapeAll(t, m)
	if !m.composeHasDraft() {
		t.Fatal("the orphan's text is no draft")
	}
	m = keys(t, m, 'I')
	if c := m.ui.compose; c == nil || c.kind != composeMessage || c.recipient() != from || c.area.value() != "a long considered answer" {
		t.Fatalf("the message window did not open on the orphan's text: %+v", c)
	}
}

// An earlier message draft is never overwritten: neither when the orphan
// becomes a message (second send) nor when it is closed (Esc); each draft
// opens in turn, the earlier one again once the other is sent.
func TestOrphanAnswerKeepsAnEarlierMessageDraft(t *testing.T) {
	pinLocal(t)
	earlier := composeDraft{to: "@docs", body: "an earlier draft"}
	t.Run("converted", func(t *testing.T) {
		fw := &fakeWriter{left: 1}
		m, from := orphanAnswer(t, fw)
		m.ui.msgCompose = earlier
		m, cmd := pressCmd(t, m, kCtrlS)
		m = run(t, m, cmd)
		if c := m.ui.compose; c == nil || c.kind != composeMessage || c.recipient() != from {
			t.Fatalf("not converted: %+v", c)
		}
		if !strings.Contains(m.ui.flash, "earlier") {
			t.Errorf("the flash does not say the earlier draft is kept: %q", m.ui.flash)
		}
		if c := m.ui.compose; c != nil {
			text := c.area.value()
			c.area.set("")
			if !m.composeHasDraft() {
				t.Error("the earlier draft does not count while the converted window is empty")
			}
			c.area.set(text)
		}
		for range 2 { // master's other open ask holds the first send once
			m, cmd = pressCmd(t, m, kCtrlS)
			m = run(t, m, cmd)
		}
		if !slices.Equal(fw.sent, []string{"cmd " + from + " a long considered answer"}) || m.ui.msgCompose != earlier {
			t.Fatalf("sent %q, draft %+v, flash %q, open %v", fw.sent, m.ui.msgCompose, m.ui.flash, m.ui.compose != nil)
		}
	})
	t.Run("emptied", func(t *testing.T) { // the converted text cleared, then Esc: the earlier draft opens
		m, _ := orphanAnswer(t, &fakeWriter{left: 1})
		m.ui.msgCompose = earlier
		m, cmd := pressCmd(t, m, kCtrlS)
		m = run(t, m, cmd)
		c := m.ui.compose
		if c == nil || c.kind != composeMessage {
			t.Fatalf("not converted: %+v", c)
		}
		c.area.set("")
		m = escapeAll(t, m)
		m = keys(t, m, 'I')
		if c := m.ui.compose; c == nil || c.recipient() != "docs" || c.area.value() != earlier.body {
			t.Fatalf("the earlier draft does not open after the emptied window: %+v", c)
		}
	})
	t.Run("closed", func(t *testing.T) {
		fw := &fakeWriter{left: 1}
		m, from := orphanAnswer(t, fw)
		m.ui.msgCompose = earlier
		m = escapeAll(t, m)
		m = keys(t, m, 'I')
		if c := m.ui.compose; c == nil || c.area.value() != earlier.body {
			t.Fatalf("the earlier draft does not open first: %+v", c)
		}
		m, cmd := pressCmd(t, m, kCtrlS)
		m = run(t, m, cmd)
		if !slices.Equal(fw.sent, []string{"cmd docs an earlier draft"}) {
			t.Fatalf("sent %q", fw.sent)
		}
		m = keys(t, m, 'I')
		if c := m.ui.compose; c == nil || c.recipient() != from || c.area.value() != "a long considered answer" {
			t.Fatalf("the orphan's text does not open next: %+v", c)
		}
	})
}
