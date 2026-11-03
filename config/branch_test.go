package config

import "testing"

// delivery.base is a Git branch name (git check-ref-format --branch's rules).
func TestValidBranchName(t *testing.T) {
	for _, ok := range []string{"main", "release/v1", "dev-2", "feat/x_y"} {
		if !ValidBranchName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-main", "a..b", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "a/", "/a", "a.lock", "a//b", "a@{b", "@", "a.", ".a", "a/.b", "a\x01", "HEAD"} {
		if ValidBranchName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
