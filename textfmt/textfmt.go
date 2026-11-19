// Package textfmt holds the small text helpers both binaries and several
// packages had copied (architect's cmd extraction map, step 0b). A function
// that only shares a name with one of these and does something else keeps
// its own name: plan's oneLine(s, max) bool is a check, projfiles' plural
// takes an irregular plural, quota's firstLine keeps the first line only.
package textfmt

import (
	"fmt"
	"strconv"
	"strings"
)

// OneLine is s on one line: every run of white space, newlines included,
// one space; none at either end.
func OneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Plural is word, with an s unless n is 1: "agent", "agents".
func Plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// Count is n and word, plural unless n is 1: "1 agent", "3 agents".
func Count(n int, word string) string { return strconv.Itoa(n) + " " + Plural(n, word) }

// TokCount is a token count in a few characters: 0, 873, 206k, 5.1M.
func TokCount(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}
