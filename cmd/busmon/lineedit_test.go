package main

import (
	"strings"
	"testing"
)

// busmon-input-cursor (Bernard): the one-line input has a cursor. The editor
// is pure: a text and a byte offset on a cluster boundary in, the same out.
// "|" marks the cursor in the cases below.
const (
	tWide = "\u65e5\u672c"         // two clusters, two cells each
	tAcc  = "e\u0301"              // one cluster of two runes, one cell
	tFlag = "\U0001F1EB\U0001F1F7" // one cluster, a regional-indicator pair
)

// at splits a case's "a|b" into its text and cursor.
func at(s string) (string, int) {
	i := strings.Index(s, "|")
	return s[:i] + s[i+1:], i
}

// show is a text with its cursor marked.
func show(s string, cur int) string { return s[:cur] + "|" + s[cur:] }

func TestLineEditMovesByCluster(t *testing.T) {
	for _, c := range []struct {
		op       string
		in, want string
	}{
		{"left", "ab|", "a|b"},
		{"left", "|ab", "|ab"}, // the start stays
		{"left", "a" + tWide + "|", "a" + tWide[:3] + "|" + tWide[3:]},
		{"left", "x" + tAcc + "|", "x|" + tAcc}, // the mark with its letter
		{"left", "x" + tFlag + "|", "x|" + tFlag},
		{"right", "|ab", "a|b"},
		{"right", "ab|", "ab|"}, // the end stays
		{"right", "|" + tWide + "z", tWide[:3] + "|" + tWide[3:] + "z"},
		{"right", "|" + tAcc + "z", tAcc + "|z"},
		{"right", "|" + tFlag + "z", tFlag + "|z"},
		{"home", "ab|c", "|abc"},
		{"home", "|abc", "|abc"},
		{"end", "a|bc", "abc|"},
		{"end", "abc|", "abc|"},
	} {
		s, cur := at(c.in)
		var got int
		switch c.op {
		case "left":
			got = lineLeft(s, cur)
		case "right":
			got = lineRight(s, cur)
		case "home":
			got = 0
		case "end":
			got = len(s)
		}
		if got := show(s, got); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.op, c.in, got, c.want)
		}
	}
}

func TestLineEditChangesAtTheCursor(t *testing.T) {
	for _, c := range []struct {
		op, arg  string
		in, want string
	}{
		{"insert", "X", "a|b", "aX|b"},
		{"insert", "X", "|ab", "X|ab"},
		{"insert", "X", "ab|", "abX|"},
		{"insert", tWide, "a|b", "a" + tWide + "|b"},
		{"backspace", "", "ab|c", "a|c"},
		{"backspace", "", "|abc", "|abc"}, // nothing before the start
		{"backspace", "", "a" + tWide + "|", "a" + tWide[:3] + "|"},
		{"backspace", "", "x" + tAcc + "|y", "x|y"}, // the whole cluster
		{"backspace", "", "x" + tFlag + "|", "x|"},
		{"delete", "", "a|bc", "a|c"},
		{"delete", "", "abc|", "abc|"}, // nothing past the end
		{"delete", "", "|" + tWide, "|" + tWide[3:]},
		{"delete", "", "x|" + tAcc + "y", "x|y"},
		{"delete", "", "|" + tFlag + "z", "|z"},
		{"word", "", "@coder review lot|", "@coder review |"},
		{"word", "", "@coder review lot  |", "@coder review |"}, // the spaces with the word
		{"word", "", "@coder re|view", "@coder |view"},
		{"word", "", "|abc", "|abc"},
		{"word", "", "a " + tWide + "|", "a |"},
		{"start", "", "@coder re|view", "|view"},
		{"start", "", "|view", "|view"},
	} {
		s, cur := at(c.in)
		switch c.op {
		case "insert":
			s, cur = lineInsert(s, cur, c.arg)
		case "backspace":
			s, cur = lineBackspace(s, cur)
		case "delete":
			s, cur = lineDelete(s, cur)
		case "word":
			s, cur = lineDeleteWord(s, cur)
		case "start":
			s, cur = s[cur:], 0
		}
		if got := show(s, cur); got != c.want {
			t.Errorf("%s %q %q: %q, want %q", c.op, c.arg, c.in, got, c.want)
		}
	}
}

// The cursor is kept as the bytes after it, so a text set whole (a draft, a
// completion, a test) has it at its end; a stale or mid-cluster one is put
// back on a boundary, never past the text.
func TestLineCursorFromTail(t *testing.T) {
	for _, c := range []struct {
		s          string
		tail, want int
	}{
		{"abc", 0, 3},
		{"abc", 1, 2},
		{"abc", 3, 0},
		{"abc", 9, 0},           // stale: longer than the text
		{"abc", -2, 3},          // never past the end
		{"a" + tWide, 1, 1 + 3}, // mid-cluster: the boundary before it
		{"x" + tAcc, 2, 1},
	} {
		if got := lineCursor(c.s, c.tail); got != c.want {
			t.Errorf("lineCursor(%q, %d) = %d, want %d", c.s, c.tail, got, c.want)
		}
	}
}
