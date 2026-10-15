package main

import (
	"regexp"
	"strings"
)

// Markdown readable in a terminal (v0.14, spec §7): long texts — reports,
// status messages, question context, message bodies — rendered for an overlay
// body at a given width: headings, lists with a hanging indent, re-wrapped
// paragraphs, quotes, code spans and verbatim code blocks. The output is
// colour-tagged through tg(), like every overlay body; it only moves
// whitespace and drops the Markdown markers themselves.

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
)

// renderMarkdown turns src into tagged lines no wider than width cells.
func renderMarkdown(src string, width int) string {
	width = max(width, 20)
	src = DetailText(src)
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
// following ones with hang (a hanging indent). Words longer than a line are cut.
func wrapSpans(spans []mdSpan, width int, first, hang string) []string {
	type word struct {
		text string
		role Role
		bold bool
	}
	var words []word
	for _, sp := range spans {
		parts := strings.Fields(sp.text)
		for i, p := range parts {
			// keep a span's leading/trailing adjacency simple: words separated by one space
			_ = i
			words = append(words, word{p, sp.role, sp.bold})
		}
	}
	var lines []string
	prefix := first
	var line strings.Builder
	used := Width(prefix)
	line.WriteString(tg(RoleMuted, prefix))
	empty := true
	push := func() {
		lines = append(lines, line.String())
		line.Reset()
		prefix = hang
		line.WriteString(tg(RoleMuted, prefix))
		used = Width(prefix)
		empty = true
	}
	for _, w := range words {
		text := w.text
		for Width(text) > width-Width(hang) && Width(text) > 0 { // a word longer than any line
			if !empty {
				push()
			}
			room := width - used
			head := Fit(text, room)
			head = strings.TrimSuffix(head, GlyphEllipsis)
			if head == "" {
				break
			}
			line.WriteString(styled(head, w.role, w.bold))
			used += Width(head)
			empty = false
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
		if !empty {
			line.WriteString(" ")
			used++
		}
		line.WriteString(styled(text, w.role, w.bold))
		used += Width(text)
		empty = false
	}
	if !empty {
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
