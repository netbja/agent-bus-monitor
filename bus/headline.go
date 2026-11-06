package bus

import (
	"strings"
	"unicode"
)

// ReportHeadline is a report's first sentence of substance (report-one-row §4):
// blank lines, Markdown headings, code fences (and what they hold) and table
// lines are skipped; the first other line loses a list marker and is cut after
// its first sentence. With no such line it is the first heading's text, and
// with no heading either "", so the caller keeps its own text. text is the
// report's full text when it keeps one, else its one-line preview.
func ReportHeadline(text string) string {
	heading := ""
	fence := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "```"):
			fence = "```"
			continue
		case strings.HasPrefix(line, "~~~"):
			fence = "~~~"
			continue
		case strings.HasPrefix(line, "|"):
			continue
		}
		if h, ok := headingText(line); ok {
			if heading == "" {
				heading = h
			}
			continue
		}
		return firstSentence(stripListMarker(line))
	}
	return heading
}

// headingText reads an ATX heading: one to six #, then a space or the end.
func headingText(line string) (string, bool) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return "", false
	}
	if n < len(line) && line[n] != ' ' && line[n] != '\t' {
		return "", false
	}
	return strings.TrimSpace(line[n:]), true
}

// stripListMarker drops a leading "- ", "* ", "+ " or "1. ".
func stripListMarker(line string) string {
	for _, m := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, m) {
			return strings.TrimSpace(line[len(m):])
		}
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && strings.HasPrefix(line[i:], ". ") {
		return strings.TrimSpace(line[i+2:])
	}
	return line
}

// firstSentence cuts after the first ". ", "! " or "? " that a capital letter
// or a digit follows; else the whole line.
func firstSentence(line string) string {
	rs := []rune(line)
	for i := 0; i+2 < len(rs); i++ {
		if (rs[i] == '.' || rs[i] == '!' || rs[i] == '?') && rs[i+1] == ' ' {
			if next := rs[i+2]; unicode.IsUpper(next) || unicode.IsDigit(next) {
				return string(rs[:i+1])
			}
		}
	}
	return line
}
