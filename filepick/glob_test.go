package filepick

import "testing"

// MatchGlob reads one .gitignore-style pattern from the repository root:
// a name without a slash matches at any depth, dir/** what is inside dir,
// **/ any depth of directories.
func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		glob, path string
		want       bool
	}{
		{"*.md", "README.md", true},
		{"*.md", "docs/a/b.md", true},
		{"*.md", "docs/a.go", false},
		{"docs/**", "docs/a/b.json", true},
		{"docs/**", "docs", false},
		{"docs/**", "cmd/docs/a.md", false},
		{"bus/**", "bus/stream.go", true},
		{"**/*.acl", "users.acl", true},
		{"**/*.acl", "deploy/x/users.acl", true},
		{"setup/rotate.go", "setup/rotate.go", true},
		{"setup/rotate.go", "x/setup/rotate.go", false},
		{"docker-compose.yml", "docker-compose.yml", true},
		{"!bus/**", "bus/x.go", false}, // a negation is never a match
		{"", "x", false},
		{"#bus/**", "bus/x.go", false}, // a comment
		{"[", "[", false},              // unreadable: never matches
	} {
		if got := MatchGlob(c.glob, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v", c.glob, c.path, got)
		}
	}
}
