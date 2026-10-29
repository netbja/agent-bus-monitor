package main

import "github.com/rivo/uniseg"

// The one-line input's editor (busmon-input-cursor): a text and its cursor, a
// byte offset on a grapheme-cluster boundary, so a wide rune or a letter with
// its marks moves and goes as one. The Model keeps the cursor as the bytes
// after it (ui.tail): a text set whole, with tail 0, has the cursor at its end.

// clusterBounds are the byte offsets where s's clusters start, and len(s).
func clusterBounds(s string) []int {
	out := []int{0}
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		_, to := g.Positions()
		out = append(out, to)
	}
	return out
}

// lineCursor is the cursor of s kept as tail bytes after it: the last
// boundary at or before it, so a stale tail lands in the text.
func lineCursor(s string, tail int) int {
	cur := len(s) - tail
	at := 0
	for _, b := range clusterBounds(s) {
		if b > cur {
			break
		}
		at = b
	}
	return at
}

// lineLeft is the boundary before cur (the start stays).
func lineLeft(s string, cur int) int {
	prev := 0
	for _, b := range clusterBounds(s) {
		if b >= cur {
			break
		}
		prev = b
	}
	return prev
}

// lineRight is the boundary after cur (the end stays).
func lineRight(s string, cur int) int {
	for _, b := range clusterBounds(s) {
		if b > cur {
			return b
		}
	}
	return len(s)
}

// lineInsert puts ins at cur; the cursor follows it.
func lineInsert(s string, cur int, ins string) (string, int) {
	return s[:cur] + ins + s[cur:], cur + len(ins)
}

// lineBackspace removes the cluster before cur.
func lineBackspace(s string, cur int) (string, int) {
	from := lineLeft(s, cur)
	return s[:from] + s[cur:], from
}

// lineDelete removes the cluster at cur.
func lineDelete(s string, cur int) (string, int) {
	return s[:cur] + s[lineRight(s, cur):], cur
}

// lineDeleteWord removes back to the start of the word before cur, with the
// spaces after it (Ctrl+W).
func lineDeleteWord(s string, cur int) (string, int) {
	from := cur
	for from > 0 && s[lineLeft(s, from):from] == " " {
		from = lineLeft(s, from)
	}
	for from > 0 && s[lineLeft(s, from):from] != " " {
		from = lineLeft(s, from)
	}
	return s[:from] + s[cur:], from
}
