package main

import (
	"path/filepath"
	"strings"
	"unicode"
)

// The @ in the compose window's text (compose-at-file, spec
// docs/superpowers/specs/2026-11-08-compose-at-file-design.md): the pure
// parts, the trigger, the path's text and the insert. The popup is step 3.

// atOpeners are the runes after which an @ opens the query (§4.1), besides
// whitespace and the start of the text: so a@b.c, an email, never does.
const atOpeners = "([\"'"

// atToken is the query an @ opened before cur: the runes from the @ to the
// cursor, which hold no whitespace, and the @'s index. ok is false when no
// such @ is open there.
func atToken(text []rune, cur int) (query string, start int, ok bool) {
	cur = min(max(cur, 0), len(text))
	for i := cur - 1; i >= 0; i-- {
		switch r := text[i]; {
		case unicode.IsSpace(r):
			return "", 0, false
		case r == '@':
			if i > 0 && !unicode.IsSpace(text[i-1]) && !strings.ContainsRune(atOpeners, text[i-1]) {
				return "", 0, false
			}
			return string(text[i+1 : cur]), i, true
		}
	}
	return "", 0, false
}

// atQuoted are the runes that make a path go in backticks (§5): it then still
// reads as one path, and renders as code in busmon's Markdown.
const atQuoted = " \t()[]{}<>\"'"

// atPathText is the text the pick of rel inserts (§5, the person's answers):
// the absolute path under root (any form but "relative": absolute is the
// default) or rel itself (form "relative"), a path under home always absolute; a directory ends
// with the separator; backticks around a path that needs them. A path that
// holds a backtick is never inserted: why says so.
func atPathText(root, home, rel string, dir, fromHome bool, form string) (text, why string) {
	p := filepath.FromSlash(rel)
	switch {
	case fromHome:
		p = filepath.Join(home, p)
	case form != "relative":
		p = filepath.Join(root, p)
	}
	if dir {
		p += string(filepath.Separator)
	}
	if strings.ContainsRune(p, '`') { // the whole path, root and home included (reviewer-codex, foureyes)
		return "", "unusual name: type it yourself"
	}
	if strings.ContainsAny(p, atQuoted) {
		p = "`" + p + "`"
	}
	return p, ""
}

// replaceAt replaces the runes [start, cur) (the @ and its query) with s and
// one space, unless the rune after it is whitespace or punctuation already,
// in one undo step (§5). It inserts nothing and returns false when the
// result would pass the cap: a path is never cut in two.
func (t *textArea) replaceAt(start, cur int, s string) bool {
	if start < 0 || cur > len(t.text) || start > cur {
		return false
	}
	add := []rune(s)
	if cur == len(t.text) || !unicode.IsSpace(t.text[cur]) && !unicode.IsPunct(t.text[cur]) {
		add = append(add, ' ')
	}
	if t.limit > 0 && len(t.text)-(cur-start)+len(add) > t.limit {
		return false
	}
	t.edit("", func() {
		t.unmark()
		t.text = append(append(append([]rune(nil), t.text[:start]...), add...), t.text[cur:]...)
		t.cur = start + len(add)
		t.changed()
	})
	return true
}
