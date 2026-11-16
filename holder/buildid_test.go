package holder

import (
	"runtime/debug"
	"testing"
)

// build-id-untracked: the build id's "+dirty" follows the tree setup stamped
// (git's tracked view), not Go's vcs.modified, which counts untracked files;
// an unstamped build (a plain go build) keeps vcs.modified.
func TestBuildIDFollowsTheStampedTree(t *testing.T) {
	settings := func(modified string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}, {Key: "vcs.modified", Value: modified}}
	}
	for _, c := range []struct {
		tree, modified, want string
	}{
		{"clean", "true", "v1+0123456789ab"},        // untracked notes: not dirty
		{"dirty", "true", "v1+0123456789ab+dirty"},  // a modified tracked file
		{"dirty", "false", "v1+0123456789ab+dirty"}, // the stamp wins
		{"", "true", "v1+0123456789ab+dirty"},       // unstamped: Go's view
		{"", "false", "v1+0123456789ab"},
	} {
		if got := buildID("v1", c.tree, settings(c.modified)); got != c.want {
			t.Errorf("tree %q, vcs.modified %s: %q, want %q", c.tree, c.modified, got, c.want)
		}
	}
}
