package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
)

// resume-record-profile (2026-11-16), the self-repair half: master swapped
// with --force to a profile outside its set, its record names that profile,
// and the self-repair must still resume its session, on master's own
// profile; a profile of another client stays refused (its session is not
// this client's).
func TestSelfRepairResumesOurOwnForcedSwap(t *testing.T) {
	m, _ := cmdtest.TeamManifest(t)
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
