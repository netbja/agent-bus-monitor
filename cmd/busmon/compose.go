package main

import (
	"slices"
	"strings"
	"unicode"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The compose window (v0.16, spec 2026-10-17-busmon-compose-window-design.md):
// a multi-line editor over the screen. One instance, created at startup; it
// writes a message (with a recipient) or a free answer to one question.

type composeKind int

const (
	composeMessage composeKind = iota
	composeAnswer
)

// answerMaxRunes is the free answer's cap (the question panel's, before v0.16).
const answerMaxRunes = 2000

// recipientCompletions is the one completion of the line and the compose
// window: "@<name> " for all, then every name in order, starting with what
// follows the "@". Nothing once a space is typed (the body has started).
func recipientCompletions(text string, names []string) []string {
	if !strings.HasPrefix(text, "@") || strings.ContainsAny(text, " \t\n") {
		return nil
	}
	sorted := slices.Sorted(slices.Values(names))
	var out []string
	for _, n := range append([]string{"all"}, sorted...) {
		if strings.HasPrefix(n, text[1:]) {
			out = append(out, "@"+n+" ")
		}
	}
	return out
}

// toCompletions is the to: field's completion: the field holds only a
// recipient, so a bare prefix ("ma") completes too (the line needs the "@":
// there plain text is a message). An empty field opens no list: Tab there
// moves to the text, and an open list would take the Tab instead.
func toCompletions(text string, names []string) []string {
	if text == "" {
		return nil
	}
	if !strings.HasPrefix(text, "@") {
		text = "@" + text
	}
	return recipientCompletions(text, names)
}

// cutRunes keeps the first n runes of s.
func cutRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// normalizeCompose is the text as it is sent: newlines unified, control
// characters other than newline and tab removed, trailing spaces of each line
// and blank lines at both ends trimmed.
func normalizeCompose(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// rect is where the window goes on a w×h screen below a header of headerH lines.
func composeRect(w, h, headerH int) Rect {
	if w < mediumMinWidth {
		top := min(headerH, h-minHeight+1)
		return Rect{0, top, w, h - top}
	}
	bw, bh := min(100, w-4), min(h-6, 16)
	bh = max(bh, min(h-2, 9)) // at least 6 text lines when the screen allows
	return Rect{(w - bw) / 2, (h - bh) / 2, bw, bh}
}

// --- the ui side ---

// splitRecipient reads the line's leading "@name" (an agent or all, with or
// without a body yet) as the window's recipient; only when the window opens.
func splitRecipient(line string) (to, body string) {
	if !strings.HasPrefix(line, "@") {
		return "", line
	}
	name, rest, _ := strings.Cut(line[1:], " ")
	if name != "all" && !bus.ValidName(name) {
		return "", line
	}
	return "@" + name, strings.TrimSpace(rest)
}
