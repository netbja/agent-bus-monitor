package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The ask panel (v0.14, spec §6 and §11): an agent's open questions, one tab
// per question, options to pick, free text always possible. Each answer is
// sent as soon as it is given (Bus.Answer) and the panel moves to the next
// open question. It is its own key mode (ModeAsk); "?" stays help.

// askPanel is the panel's cursor: which ask, which question, which option
// (len(options) = "Other…", written in the compose window since v0.16).
type askPanel struct {
	id     string
	q, opt int
}

// openAsks are the asks still open, oldest first.
func openAsks(asks []bus.Ask) []bus.Ask {
	var out []bus.Ask
	for _, a := range asks {
		if a.State == "open" {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func findAsk(asks []bus.Ask, id string) (bus.Ask, bool) {
	for _, a := range asks {
		if a.ID == id {
			return a, true
		}
	}
	return bus.Ask{}, false
}

// firstOpenQuestion is the index of the first unanswered question from i
// (wrapping), or -1.
func firstOpenQuestion(a bus.Ask, from int) int {
	n := len(a.Questions)
	for k := 0; k < n; k++ {
		i := (from + k) % n
		if a.Questions[i].Answer == nil {
			return i
		}
	}
	return -1
}

// openAskPanel opens the ask of agent (or, with "", the oldest open ask).
func (u *ui) openAskPanel(agent string) {
	u.st.mu.Lock()
	asks := openAsks(u.st.asks)
	u.st.mu.Unlock()
	var target *bus.Ask
	for i := range asks {
		if agent == "" || asks[i].From == agent {
			target = &asks[i]
			break
		}
	}
	if target == nil {
		u.flash, u.flashErr = "no open question", false
		return
	}
	q := max(firstOpenQuestion(*target, 0), 0)
	u.ask = askPanel{id: target.ID, q: q, opt: recommendedIndex(target.Questions[q].AskQuestion)}
	u.showOverlay(ovAsk, askTitle(*target), nil, "", "")
	u.renderAsk()
}

// agentHasAsk reports whether the agent has an open ask.
func (u *ui) agentHasAsk(agent string) bool {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	a := u.st.agents[agent]
	return a != nil && a.openAsk > 0
}

func askTitle(a bus.Ask) string {
	return fmt.Sprintf("%s %s asks %s %s", GlyphHuman, Preview(a.From), GlyphNone, Preview(a.Title))
}

func recommendedIndex(q bus.AskQuestion) int {
	for i, o := range q.Options {
		if o.Recommended {
			return i
		}
	}
	return 0
}

// renderAsk redraws the panel body from the shared asks, following a
// replacement to the open version (a reload when it is not known yet).
func (u *ui) renderAsk() {
	a, ok := u.currentAsk()
	if !ok {
		u.reloadAsks()
		if a, ok = u.currentAsk(); !ok {
			u.closeOverlay()
			return
		}
	}
	if a.Open() == 0 && a.State != "open" {
		u.closeOverlay()
		return
	}
	if u.ask.q >= len(a.Questions) {
		u.ask.q = 0
	}
	u.overlay.SetText(linesToTview(askPanelBody(a, u.ask, max(30, overlayInnerWidth(u.width)))))
	q := a.Questions[u.ask.q]
	plain := q.Question
	if q.Context != "" {
		plain += "\n\n" + q.Context
	}
	for i, o := range q.Options {
		plain += fmt.Sprintf("\n%d. %s", i+1, o.Label)
		if o.Description != "" {
			plain += " - " + o.Description
		}
	}
	u.ovPlain = CopyText(plain)
}

// currentAsk is the panel's ask, following replaced_by to the open version.
func (u *ui) currentAsk() (bus.Ask, bool) {
	u.st.mu.Lock()
	defer u.st.mu.Unlock()
	id := u.ask.id
	for hops := 0; hops < 8; hops++ {
		a, ok := findAsk(u.st.asks, id)
		if !ok {
			return bus.Ask{}, false
		}
		if a.State == "replaced" && a.ReplacedBy != "" {
			id = a.ReplacedBy
			continue
		}
		if id != u.ask.id {
			u.ask.id = id
			u.ask.q = max(firstOpenQuestion(a, 0), 0)
			u.ask.opt = recommendedIndex(a.Questions[u.ask.q].AskQuestion)
		}
		return a, true
	}
	return bus.Ask{}, false
}

// reloadAsks reads the asks now (after a refusal, or to find a replacement
// the poll has not brought yet).
func (u *ui) reloadAsks() {
	ctx, cancel := context.WithTimeout(u.ctx, pollBudget)
	defer cancel()
	asks, err := u.w.Asks(ctx)
	if err != nil {
		return
	}
	u.st.mu.Lock()
	u.st.asks = asks
	u.st.mu.Unlock()
}

// askPanelBody is the panel's text: the tab strip, the question, its context
// (Markdown), then its options (or its recorded answer). Pure, for tests.
func askPanelBody(a bus.Ask, p askPanel, width int) []Line {
	var b body
	// Tab strip, fit to the width: ✓ answered, ● current, ○ open.
	var tabs []Line
	for i, q := range a.Questions {
		g := GlyphIdle
		switch {
		case i == p.q:
			g = GlyphWorking
		case q.Answer != nil:
			g = GlyphDone
		}
		role := RoleMuted
		if i == p.q {
			role = RoleAccent
		}
		tabs = append(tabs, Line{S(role, g+" "+Preview(q.Header))})
	}
	var strip Line
	for i, t := range tabs {
		if i > 0 {
			strip = append(strip, S(RoleText, "   "))
		}
		strip = append(strip, t...)
	}
	if strip.Width() > width { // too wide: the current tab and its position
		strip = append(append(Line{}, tabs[p.q]...), S(RoleMuted, fmt.Sprintf("   (%d/%d, %s%s for the others)", p.q+1, len(a.Questions), GlyphArrowL, GlyphArrowR)))
	}
	b.segs(strip...).text("\n\n")
	if p.q < 0 || p.q >= len(a.Questions) {
		return b.Lines()
	}
	q := a.Questions[p.q]
	b.join(wrapPlain(q.Question, width, RoleText, true)).text("\n")
	if strings.TrimSpace(q.Context) != "" {
		b.join(renderMarkdown(q.Context, width)).text("\n")
	}
	b.text("\n")
	if q.Answer != nil {
		b.seg(RoleOK, GlyphDone+" answered: "+Preview(q.Answer.Value())).
			seg(RoleMuted, fmt.Sprintf(" (by %s)", Preview(q.Answer.By))).text("\n")
		return b.Lines()
	}
	for i, o := range q.Options {
		mark := "  "
		role := RoleText
		if i == p.opt {
			mark, role = GlyphSelect+" ", RoleAccent
		}
		line := fmt.Sprintf("%s%d. %s", mark, i+1, o.Label)
		if o.Recommended {
			line += " (recommended)"
		}
		b.join(wrapPlain(line, width, role, false)).text("\n")
		if o.Description != "" {
			b.join(wrapIndented(o.Description, width, "      ")).text("\n")
		}
	}
	other := len(q.Options)
	mark, role := "  ", RoleText
	if p.opt == other {
		mark, role = GlyphSelect+" ", RoleAccent
	}
	b.join(wrapPlain(fmt.Sprintf("%s%d. Other... (type your answer)", mark, other+1), width, role, false)).text("\n")
	b.text("\n").seg(RoleMuted, GlyphUp+GlyphDownArrow+" or 1-9 choose · Enter answer · "+GlyphArrowL+GlyphArrowR+" question · Esc close").text("\n")
	return b.Lines()
}

// wrapPlain wraps text (no Markdown) to width in one role.
func wrapPlain(s string, width int, role Role, bold bool) []Line {
	return wrapSpans([]mdSpan{{text: DetailText(s), role: role, bold: bold}}, width, "", "")
}

func wrapIndented(s string, width int, indent string) []Line {
	return wrapSpans([]mdSpan{{text: DetailText(s), role: RoleMuted}}, width, indent, indent)
}

// askKey is the panel's key handler (ModeAsk).
func (u *ui) askKey(ev *tcell.EventKey) *tcell.EventKey {
	u.st.mu.Lock()
	a, ok := findAsk(u.st.asks, u.ask.id)
	u.st.mu.Unlock()
	if !ok {
		u.closeOverlay()
		return nil
	}
	p := &u.ask
	if p.q >= len(a.Questions) {
		p.q = 0
	}
	q := a.Questions[p.q]
	act := lookup(ModeAsk, u.focus, u.tier(), keyOf(ev))
	switch act {
	case ActQuit:
		u.app.Stop()
	case ActClose:
		u.closeOverlay()
		return nil
	case ActHelp:
		u.showOverlay(ovHelp, "Help", helpText(), "", "")
		return nil
	case ActCopy:
		u.copyText(u.ovPlain, "question")
	case ActUp:
		p.opt = max(p.opt-1, 0)
	case ActDown:
		p.opt = min(p.opt+1, len(q.Options))
	case ActAskNext, ActAskPrev:
		step := 1
		if act == ActAskPrev {
			step = len(a.Questions) - 1
		}
		p.q = (p.q + step) % len(a.Questions)
		p.opt = recommendedIndex(a.Questions[p.q].AskQuestion)
	case ActAskPick:
		if n := int(ev.Rune() - '1'); n >= 0 && n <= len(q.Options) {
			p.opt = n
		}
	case ActSend:
		if q.Answer != nil {
			break
		}
		if p.opt == len(q.Options) {
			u.openAnswerComposer(a, q, "")
			return nil
		} else {
			label := q.Options[p.opt].Label
			send := func() {
				u.sendAnswer(a, q, label, "")
				if u.ov == ovAsk {
					u.renderAsk()
				}
			}
			if u.pauseGate("answer|"+a.ID+"|"+q.ID+"|"+label, send) {
				return nil
			}
			u.sendAnswer(a, q, label, "")
		}
	}
	if u.ov == ovAsk {
		u.renderAsk()
	}
	return nil
}

// sendAnswer sends one answer now, records it locally, and moves on to the
// next open question; the last one closes the panel. It reports whether the
// answer was accepted: a failure keeps the caller's text (v0.16 §6).
func (u *ui) sendAnswer(a bus.Ask, q bus.AskQuestionState, choice, text string) bool {
	ctx, cancel := context.WithTimeout(u.ctx, pollBudget)
	defer cancel()
	id, left, err := u.w.Answer(ctx, a.ID, q.ID, u.self, choice, text)
	if err == nil {
		u.pauseAck = "" // the acknowledgement was for this one send
	}
	if err != nil { // say why, and show the ask as it is now (answered elsewhere, replaced)
		u.flash, u.flashErr = "answer not sent: "+Preview(err.Error()), true
		u.reloadAsks()
		return false
	}
	value := choice
	if value == "" {
		value = text
	}
	u.st.mu.Lock()
	for i := range u.st.asks {
		if u.st.asks[i].ID != id {
			continue
		}
		for j := range u.st.asks[i].Questions {
			if u.st.asks[i].Questions[j].ID == q.ID {
				if choice != "" {
					u.st.asks[i].Questions[j].Answer = &bus.AskAnswer{Choice: choice, By: u.self, At: time.Now().UnixMilli()}
				} else {
					u.st.asks[i].Questions[j].Answer = &bus.AskAnswer{Text: text, By: u.self, At: time.Now().UnixMilli()}
				}
			}
		}
		if left == 0 {
			u.st.asks[i].State = "answered"
		}
	}
	updated, known := findAsk(u.st.asks, id)
	u.st.mu.Unlock()
	if !known { // answered on a replacement the poll has not brought yet
		u.reloadAsks()
		u.st.mu.Lock()
		updated, _ = findAsk(u.st.asks, id)
		u.st.mu.Unlock()
	}
	u.flash, u.flashErr = fmt.Sprintf("%s %s: %s sent to %s", GlyphDone, Preview(q.Header), Preview(value), Preview(a.From)), false
	if left == 0 {
		u.closeOverlay()
		return true
	}
	u.ask.id = id
	if n := firstOpenQuestion(updated, u.ask.q+1); n >= 0 {
		u.ask.q = n
		u.ask.opt = recommendedIndex(updated.Questions[n].AskQuestion)
	}
	return true
}

// askSummary leads an agent's detail when it has an open ask.
func askSummary(a bus.Ask) []Line {
	var b body
	b.seg(RoleAccent, fmt.Sprintf("Questions for you %s %s", GlyphNone, Preview(a.Title))).text("\n")
	for _, q := range a.Questions {
		if q.Answer != nil {
			b.seg(RoleMuted, fmt.Sprintf("  %s %s: %s", GlyphDone, Preview(q.Header), Preview(q.Answer.Value()))).text("\n")
		} else {
			b.seg(RoleAttention, fmt.Sprintf("  %s %s", GlyphIdle, Preview(q.Header))).seg(RoleText, " "+Preview(q.Question)).text("\n")
		}
	}
	b.seg(RoleAttention, "  enter").seg(RoleMuted, " (or ! or w) to answer").text("\n\n")
	return b.Lines()
}

// openAskPrefilled opens agent's ask on its first open question, with text as
// a free answer being typed: nothing is sent before the panel's own submit.
func (u *ui) openAskPrefilled(agent, text string) {
	u.openAskPanel(agent)
	if u.ov != ovAsk || text == "" {
		return
	}
	if a, ok := u.currentAsk(); ok && u.ask.q < len(a.Questions) && a.Questions[u.ask.q].Answer == nil {
		u.openAnswerComposer(a, a.Questions[u.ask.q], text)
	}
}

// askChoice is the pending pick between several recipients' questions.
type askChoice struct {
	holders []string
	body    string
}
