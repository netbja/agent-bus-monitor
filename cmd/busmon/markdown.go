package main

import (
	"regexp"
	"strings"
	"unicode"
)

// Markdown readable in a terminal (v0.14, spec §7): long texts — reports,
// status messages, question context, message bodies — rendered for an overlay
// body at a given width: headings, lists with a hanging indent, re-wrapped
// paragraphs, quotes, code spans and verbatim code blocks. The output is
// colour-tagged through tg(), like every overlay body; it only moves
// whitespace and drops the Markdown markers themselves.

// mdWidth is the overlay text width, kept by relayout (overlay bodies are
// built outside a draw, from the last known terminal size).
var mdWidth = 96

type mdSpan struct {
	text string
	role Role
	bold bool
}

var (
	mdHeading = regexp.MustCompile(`^(#{1,3})\s+(.*)$`)
	mdBullet  = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	mdNumber  = regexp.MustCompile(`^(\s*)(\d{1,3})[.)]\s+(.*)$`)
	mdQuote   = regexp.MustCompile(`^\s*>\s?(.*)$`)
	mdFence   = regexp.MustCompile("^\\s*(```|~~~)")

	// flatLabel is an upper-case label opening a sentence of a one-line text:
	// "DECISION 2 - ", "CE QUE FAIT L'OUVERTURE: ", "VERIFIED: ".
	flatLabel = regexp.MustCompile(`([.!?;])\s+((?:\p{Lu}[\p{Lu}\d'\x{2019}]*\s+){0,4}\p{Lu}[\p{Lu}\d'\x{2019}]{2,}(?:\s+\d{1,2})?(?::|\s[-\x{2013}\x{2014}]))\s+`)
	// flatItem is an enumerated choice inside a sentence: " (1) ", " (X) ".
	flatItem = regexp.MustCompile(`\s\((\d{1,2}|\p{Lu})\)\s`)
)

// flatMin is the length from which a text written on one line is given the
// structure its author typed inline.
const flatMin = 240

// structureFlat gives a long one-line text (most agents' reports, written
// before v0.14 asked for Markdown) the paragraphs and lists it spells inline:
// a paragraph at each upper-case label opening a sentence, shown in bold, and
// a list item at each enumerated choice when there are at least two. A text
// with a line break, or a short one, is returned unchanged.
func structureFlat(s string) string {
	if strings.Contains(strings.TrimSpace(s), "\n") || Width(s) < flatMin {
		return s
	}
	s = strings.TrimSpace(s)
	if end := strings.Index(s, ". "); end > 0 && end <= 80 && shouting(s[:end]) {
		s = "**" + s[:end+1] + "**\n\n" + s[end+2:] // an upper-case opening sentence is its title
	}
	s = flatLabel.ReplaceAllString(s, "$1\n\n**$2** ")
	if items := flatItem.FindAllStringSubmatch(s, -1); len(items) >= 2 && items[0][1] != items[1][1] {
		s = flatItem.ReplaceAllString(s, "\n- ($1) ")
	}
	return s
}

// shouting reports whether most letters of s are upper case.
func shouting(s string) bool {
	upper, letters := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters >= 6 && upper*10 >= letters*8
}

// renderMarkdown turns src into tagged lines no wider than width cells.
func renderMarkdown(src string, width int) string {
	width = max(width, 20)
	src = structureFlat(DetailText(src))
	var out []string
	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, wrapSpans(inlineSpans(strings.Join(para, " "), RoleText), width, "", "")...)
			para = nil
		}
	}
	lines := strings.Split(src, "\n")
	inCode := false
	for _, ln := range lines {
		if mdFence.MatchString(ln) {
			flush()
			inCode = !inCode
			continue
		}
		if inCode {
			out = append(out, tg(RoleMuted, strings.TrimRight("  "+Fit(strings.TrimRight(ln, " \t"), width-2), " ")))
			continue
		}
		trimmed := strings.TrimSpace(ln)
		switch {
		case trimmed == "":
			flush()
			blank()
		case mdHeading.MatchString(trimmed):
			flush()
			blank()
			m := mdHeading.FindStringSubmatch(trimmed)
			spans := inlineSpans(m[2], RoleAccent)
			for i := range spans {
				spans[i].bold = true
				if spans[i].role == RoleText {
					spans[i].role = RoleAccent
				}
			}
			out = append(out, wrapSpans(spans, width, "", "")...)
		case mdBullet.MatchString(ln) || mdNumber.MatchString(ln):
			flush()
			var indent, marker, body string
			if m := mdBullet.FindStringSubmatch(ln); m != nil {
				indent, marker, body = m[1], GlyphNone, m[3]
			} else {
				m := mdNumber.FindStringSubmatch(ln)
				indent, marker, body = m[1], m[2]+".", m[3]
			}
			pad := strings.Repeat("  ", min(len(strings.ReplaceAll(indent, "\t", "  "))/2, 4))
			first := pad + marker + " "
			hang := pad + strings.Repeat(" ", Width(marker)+1)
			out = append(out, wrapSpans(inlineSpans(body, RoleText), width, first, hang)...)
		case mdQuote.MatchString(ln):
			flush()
			m := mdQuote.FindStringSubmatch(ln)
			gutter := glyphVertical + " "
			spans := inlineSpans(m[1], RoleMuted)
			for i := range spans {
				spans[i].role = RoleMuted
			}
			out = append(out, wrapSpans(spans, width, gutter, gutter)...)
		default:
			para = append(para, trimmed)
		}
	}
	flush()
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// inlineSpans splits **bold**, __bold__, *em*, _em_ and `code`; the markers go.
func inlineSpans(s string, base Role) []mdSpan {
	var out []mdSpan
	var cur strings.Builder
	bold := false
	emit := func(role Role, b bool) {
		if cur.Len() > 0 {
			out = append(out, mdSpan{cur.String(), role, b})
			cur.Reset()
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '`':
			end := indexRune(rs, '`', i+1)
			if end < 0 {
				cur.WriteRune(r)
				continue
			}
			emit(base, bold)
			out = append(out, mdSpan{string(rs[i+1 : end]), RoleMuted, false})
			i = end
		case (r == '*' || r == '_') && i+1 < len(rs) && rs[i+1] == r:
			emit(base, bold)
			bold = !bold
			i++
		case r == '*' && i+1 < len(rs) && rs[i+1] != ' ' && (i == 0 || rs[i-1] == ' ') && indexRune(rs, '*', i+1) > i+1:
			// *em*: shown as plain text, markers dropped (terminals differ on italics)
			end := indexRune(rs, '*', i+1)
			cur.WriteString(string(rs[i+1 : end]))
			i = end
		default:
			cur.WriteRune(r)
		}
	}
	emit(base, bold)
	return out
}

func indexRune(rs []rune, r rune, from int) int {
	for j := from; j < len(rs); j++ {
		if rs[j] == r {
			return j
		}
	}
	return -1
}

// wrapSpans word-wraps spans to width: the first line starts with first, the
// following ones with hang (a hanging indent). Words longer than a line are
// cut. Consecutive words of one style share one colour tag.
func wrapSpans(spans []mdSpan, width int, first, hang string) []string {
	type word struct {
		text string
		role Role
		bold bool
	}
	var words []word
	for _, sp := range spans {
		for _, p := range strings.Fields(sp.text) {
			words = append(words, word{p, sp.role, sp.bold})
		}
	}
	var lines []string
	var line strings.Builder // finished runs of the current line
	var run strings.Builder  // the run being built, in runRole/runBold
	runRole, runBold := RoleText, false
	used, empty := 0, true
	flushRun := func() {
		if run.Len() > 0 {
			line.WriteString(styled(run.String(), runRole, runBold))
			run.Reset()
		}
	}
	start := func(prefix string) {
		line.Reset()
		if prefix != "" {
			line.WriteString(tg(RoleMuted, prefix))
		}
		used, empty = Width(prefix), true
	}
	push := func() {
		flushRun()
		lines = append(lines, line.String())
		start(hang)
	}
	add := func(text string, role Role, bold bool, space bool) {
		if role != runRole || bold != runBold {
			flushRun()
			runRole, runBold = role, bold
		}
		if space {
			run.WriteString(" ")
			used++
		}
		run.WriteString(text)
		used += Width(text)
		empty = false
	}
	start(first)
	for _, w := range words {
		text := w.text
		for Width(text) > width-Width(hang) && Width(text) > 0 { // a word longer than any line
			if !empty {
				push()
			}
			head := strings.TrimSuffix(Fit(text, width-used), GlyphEllipsis)
			if head == "" {
				break
			}
			add(head, w.role, w.bold, false)
			text = text[len(head):]
			push()
		}
		need := Width(text)
		if !empty {
			need++
		}
		if used+need > width && !empty {
			push()
		}
		add(text, w.role, w.bold, !empty)
	}
	if !empty {
		flushRun()
		lines = append(lines, line.String())
	}
	return lines
}

func styled(s string, r Role, bold bool) string {
	t := tg(r, s)
	if bold {
		return "[::b]" + t + "[::-]"
	}
	return t
}
