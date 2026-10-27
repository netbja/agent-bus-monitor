package main

import (
	"fmt"
	"sort"
	"strings"

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

// askPanelBody is the panel's text: the tab strip, the question, its context
// (Markdown), then its options (or its recorded answer). Pure, for tests.
func askPanelBody(a bus.Ask, p askPanel, width int) []Line {
	lines, _ := askPanelLayout(a, p, width)
	return lines
}

// lineSpan is body lines [from, to).
type lineSpan struct{ from, to int }

// askPanelLayout is askPanelBody with each option's lines, "Other" last:
// what the shell's reader hit-tests a click against. No spans when the
// question is answered (its options are not shown).
func askPanelLayout(a bus.Ask, p askPanel, width int) ([]Line, []lineSpan) {
	var spans []lineSpan
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
		return b.Lines(), nil
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
		return b.Lines(), nil
	}
	for i, o := range q.Options {
		from := b.at()
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
		spans = append(spans, lineSpan{from, b.at()})
	}
	other := len(q.Options)
	mark, role := "  ", RoleText
	if p.opt == other {
		mark, role = GlyphSelect+" ", RoleAccent
	}
	from := b.at()
	b.join(wrapPlain(fmt.Sprintf("%s%d. Other... (type your answer)", mark, other+1), width, role, false)).text("\n")
	spans = append(spans, lineSpan{from, b.at()})
	b.text("\n").seg(RoleMuted, GlyphUp+GlyphDownArrow+" or 1-9 choose · Enter answer · "+GlyphArrowL+GlyphArrowR+" question · Esc close").text("\n")
	return b.Lines(), spans
}

// wrapPlain wraps text (no Markdown) to width in one role.
func wrapPlain(s string, width int, role Role, bold bool) []Line {
	return wrapSpans([]mdSpan{{text: DetailText(s), role: role, bold: bold}}, width, "", "")
}

func wrapIndented(s string, width int, indent string) []Line {
	return wrapSpans([]mdSpan{{text: DetailText(s), role: RoleMuted}}, width, indent, indent)
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

// askChoice is the pending pick between several recipients' questions.
type askChoice struct {
	holders []string
	body    string
}
