package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// resume-record-profile (2026-11-16), the self-repair half: master swapped
// with --force to a profile outside its set, its record names that profile,
// and the self-repair must still resume its session, on master's own
// profile; a profile of another client stays refused (its session is not
// this client's).
func TestSelfRepairResumesOurOwnForcedSwap(t *testing.T) {
	m, _ := teamFixture(t)
	for _, c := range []struct {
		name, role       string
		rec              bus.ResumeRecord
		profile, session string
	}{
		{"same client, outside the set", "master", bus.ResumeRecord{Session: "4fa9d0ff-21", Profile: "claude-opus"}, "", "4fa9d0ff-21"},
		{"another client", "master", bus.ResumeRecord{Session: "4fa9d0ff-21", Profile: "codex-review"}, "", ""},
		{"a fallback resumes on it", "coder", bus.ResumeRecord{Session: "4fa9d0ff-21", Profile: "claude-alt"}, "claude-alt", "4fa9d0ff-21"},
	} {
		r := selfRepairReads{Manifest: m, Resumes: map[string]bus.ResumeRecord{c.role: c.rec}}
		profile, session, why := selfRepairResume(r, c.role)
		if profile != c.profile || session != c.session {
			t.Errorf("%s: profile %q session %q (%s); want %q %q", c.name, profile, session, why, c.profile, c.session)
		}
	}
}

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
