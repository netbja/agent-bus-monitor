package main

import (
	"strings"
	"testing"
)

// A row is one line of exactly the width, the age at the right edge, at
// every width a page is drawn at.
func TestListRowFillsTheWidthWithTheAgeAtTheEdge(t *testing.T) {
	r := listRow{Sym: GlyphBlocked, Role: RoleAttention, Name: "Tracked secret-like files", State: "a private-key path is tracked",
		Note: Line{S(RoleAccent, "git rm --cached <path>")}, Age: "3h"}
	for _, w := range []int{40, 66, 80, 101, 136} {
		for _, noteW := range []int{0, 26} {
			l := r.line(false, w, 28, noteW)
			if l.Width() != w {
				t.Errorf("width %d note %d: row is %d cells: %q", w, noteW, l.Width(), l.text())
			}
			if !strings.HasSuffix(l.text(), " 3h") {
				t.Errorf("width %d: age not at the edge: %q", w, l.text())
			}
		}
	}
	if got := r.line(false, 101, 28, 26).text(); !strings.Contains(got, "git rm --cached <path>") {
		t.Errorf("wide row lost its note: %q", got)
	}
	if got := r.line(false, 50, 28, 26).text(); strings.Contains(got, "git rm") || !strings.Contains(got, "a priv") {
		t.Errorf("narrow row kept the note over the state: %q", got)
	}
	if got := r.line(true, 80, 20, 0).text(); !strings.HasPrefix(got, GlyphSelect+" "+GlyphBlocked) {
		t.Errorf("selected row: %q", got)
	}
}

func TestListSummaryNamesOnlyWhatIsCounted(t *testing.T) {
	got := listSummary(listCount{2, "to do"}, listCount{0, "in hand"}, listCount{11, "fine"})
	if got != "2 to do "+GlyphNone+" 11 fine" {
		t.Errorf("summary = %q", got)
	}
	if got := listSummary(listCount{0, "to do"}); got != "" {
		t.Errorf("empty summary = %q", got)
	}
}

func TestListFoldNamesItsKey(t *testing.T) {
	if got := listFold("fine", 11, "a").text(); !strings.Contains(got, "fine "+GlyphNone+" 11  (a shows them)") {
		t.Errorf("fold = %q", got)
	}
}
