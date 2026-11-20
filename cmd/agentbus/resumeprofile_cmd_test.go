package main

import (
	"testing"
)

// rolecfg.ResumeProfile, the one rule up, the self-repair and the holder
// share: where a resume recorded on a profile runs.
func TestResumeProfile(t *testing.T) {
	m, _ := teamFixture(t)
	for _, c := range []struct {
		role, profile, run string
		onOwn, err         bool
	}{
		{"master", "", "", false, false},
		{"master", "claude", "", false, false},
		{"coder", "claude-alt", "claude-alt", false, false},
		{"master", "claude-opus", "", true, false},
		{"master", "codex-review", "", false, true},
		{"master", "gone", "", false, true},
		{"master", "Bad Name", "", false, true},
		{"reviewer-codex", "claude-opus", "", false, true},
	} {
		run, onOwn, err := m.ResumeProfile(c.role, c.profile)
		if run != c.run || onOwn != c.onOwn || (err != nil) != c.err {
			t.Errorf("%s on %q: %q %v %v", c.role, c.profile, run, onOwn, err)
		}
	}
}
