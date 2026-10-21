package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// forYouHint reports whether an idle agent's status message names the person
// (busmon's identity) as a whole word: "lot 1 delivered; awaiting bernard".
// It is a hint, never a state: busmon does not relabel the declaration, ring
// or count it (spec 2026-10-21 busmon UX, item 5). The default identity
// "pilot" is a bus word (the pilot lease), so it never matches.
func forYouHint(state, message, person string) bool {
	if state != "idle" || person == "" || person == defaultHuman {
		return false
	}
	low, name := strings.ToLower(message), strings.ToLower(person)
	for i := 0; ; {
		j := strings.Index(low[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		before, _ := utf8.DecodeLastRuneInString(low[:start])
		after, _ := utf8.DecodeRuneInString(low[end:])
		if start == 0 || !nameRune(before) && !strings.ContainsRune("/.:@", before) {
			if end == len(low) || !nameRune(after) && after != '/' {
				return true
			}
		}
		i = start + 1
	}
}

// nameRune is a rune that continues a word or a bus name.
func nameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// declare records an agent's declared state and message, and the hint that
// goes with them. Callers hold st.mu.
func declare(a *agentState, state, message, person string) {
	a.state, a.message = state, message
	a.forYou = forYouHint(state, message, person)
}
