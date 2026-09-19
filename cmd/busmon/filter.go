package main

import (
	"fmt"
	"strings"
)

// feedFilter narrows the ACTIVITY feed to one agent, one thread, or any words.
// It filters what is DISPLAYED, never what is retained: clearing it brings the
// whole journal back, because a filter that quietly dropped history would be a
// worse lie than a crowded screen.
type feedFilter struct {
	raw    string
	agent  string // "@name": author, sender or addressee
	thread string // a stream id or a bare millisecond cursor
	text   string // free words, matched case-insensitively against the body
}

// parseFilter reads a filter expression. Tokens are ANDed: "@coder rebase"
// means entries involving coder that also mention rebase.
func parseFilter(s string) feedFilter {
	f := feedFilter{raw: strings.TrimSpace(s)}
	var words []string
	for _, tok := range strings.Fields(f.raw) {
		switch {
		case strings.HasPrefix(tok, "@") && len(tok) > 1:
			f.agent = strings.ToLower(tok[1:])
		case looksLikeStreamID(tok):
			f.thread = tok
		default:
			words = append(words, tok)
		}
	}
	f.text = strings.ToLower(strings.Join(words, " "))
	return f
}

// looksLikeStreamID recognises "1758141663000-0" and the bare "1758141663000"
// cursor form, so a thread id pasted from `agentbus thread` filters directly.
func looksLikeStreamID(s string) bool {
	if i := strings.IndexByte(s, '-'); i > 0 {
		return isDigits(s[:i]) && isDigits(s[i+1:])
	}
	return isDigits(s) && len(s) >= 10
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (f feedFilter) empty() bool { return f.agent == "" && f.thread == "" && f.text == "" }

// match reports whether a feed line survives the filter. Day separators (a zero
// Event) are dropped while filtering: a date header over a filtered selection
// would claim a completeness the view no longer has.
func (f feedFilter) match(fl feedLine) bool {
	if f.empty() {
		return true
	}
	e := fl.ev
	if e.Kind == "" {
		return false
	}
	if f.agent != "" &&
		!strings.EqualFold(e.Agent, f.agent) &&
		!strings.EqualFold(e.From, f.agent) &&
		!strings.EqualFold(e.Target, f.agent) {
		return false
	}
	if f.thread != "" {
		id := threadID(e)
		if id != f.thread && e.ID != f.thread && e.Ref != f.thread &&
			!strings.HasPrefix(e.ID, f.thread+"-") && !strings.HasPrefix(id, f.thread+"-") {
			return false
		}
	}
	if f.text != "" && !strings.Contains(strings.ToLower(e.Message+" "+e.Full), f.text) {
		return false
	}
	return true
}

// describe names the filter in the words the operator typed it in.
func (f feedFilter) describe() string {
	parts := make([]string, 0, 3)
	if f.agent != "" {
		parts = append(parts, "@"+f.agent)
	}
	if f.thread != "" {
		parts = append(parts, "thread "+f.thread)
	}
	if f.text != "" {
		parts = append(parts, "\""+f.text+"\"")
	}
	return strings.Join(parts, " + ")
}

// filterTitle replaces the live/pause indicator while a filter is on, and says
// how many of the retained lines are hidden and how to get them back.
func filterTitle(f feedFilter, shown, total int) string {
	return fmt.Sprintf(" ACTIVITY  [aqua][filter %s · %d of %d lines · Esc clears][-] ",
		f.describe(), shown, total)
}
