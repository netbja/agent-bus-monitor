package mission

import "testing"

func TestMatchPath(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"cmd/busmon/state*.go", "cmd/busmon/state_model.go", true},
		{"cmd/busmon/state*.go", "cmd/busmon/sub/state.go", false},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "docsx/a", false},
		{"docs/**", "docs", false},
		{"README.md", "README.md", true},
		{"cmd/*/main.go", "cmd/busmon/main.go", true},
	} {
		if got := MatchPath(c.pattern, c.path); got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v", c.pattern, c.path, got)
		}
	}
}

func TestOverlapPrefixRule(t *testing.T) {
	if !Overlap([]string{"cmd/busmon/new*.go"}, []string{"cmd/busmon/**"}, nil) {
		t.Error("prefix rule missed")
	}
	if Overlap([]string{"cmd/a/*"}, []string{"cmd/b/*"}, nil) {
		t.Error("disjoint dirs overlap")
	}
	if !Overlap([]string{"*.md"}, []string{"README*"}, []string{"README.md"}) {
		t.Error("a tree file matching both was missed")
	}
}
