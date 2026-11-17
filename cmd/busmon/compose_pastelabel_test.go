package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// busmon-paste-label (the person, via master): a paste's placeholder shows
// its first words, so two pastes can be told apart: the first line that is
// not blank, cut at a word near 24 characters, and the paste's line count.
func TestComposePasteLabelShowsTheFirstWords(t *testing.T) {
	for _, c := range []struct{ paste, want string }{
		{"a\nb", "[Pasted #1: a, 2 lines]"},
		{"\n\n  func main() {\n}\n", "[Pasted #1: func main() {, 4 lines]"},
		{"the dispatcher logged an error at startup\nline 2\nline 3", "[Pasted #1: the dispatcher logged an…, 3 lines]"},
		{"the dispatcher logged errors at startup\nx", "[Pasted #1: the dispatcher logged…, 2 lines]"},
		{"abcdefghijklmnopqrstuvwxyzabcdef\nx", "[Pasted #1: abcdefghijklmnopqrstuvwx…, 2 lines]"},
		{"\n\n", "[Pasted #1: 2 lines]"},
	} {
		m := composeWith(t, "")
		m = step(t, m, tea.PasteMsg{Content: c.paste})
		a := &m.ui.compose.area
		if got := string(a.text); got != c.want {
			t.Errorf("%q shown %q, want %q", c.paste, got, c.want)
		}
		if got := a.value(); got != c.paste {
			t.Errorf("%q: value %q", c.paste, got)
		}
	}
}
