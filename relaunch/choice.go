package relaunch

import (
	"fmt"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Moved from cmd/agentbus unchanged (dispatchsvc P1): what reload, swap,
// self-repair and up share about a relaunched role.

// Replaced: the snapshot proves a new process of the chosen profile.
func Replaced(s, old bus.AgentSnapshot, p rolecfg.Profile, procStart func(int) (uint64, error)) bool {
	if s.PID == 0 || s.PID == old.PID && s.PIDStart == old.PIDStart || s.Pane == "" || s.Pane == old.Pane {
		return false
	}
	if start, err := procStart(s.PID); err != nil || start != s.PIDStart {
		return false
	}
	return s.Client == p.Client && s.Provider == p.Provider
}

// ResumeChoice applies §4.7's two conditions to role's record, before any
// Start: its profile ("" the role's own) must be the role's own or one of
// its fallback_profiles in the local manifest, never a broker key; its
// session must be a valid id of a client whose resume is verified. invalid
// says why the record is not used ("" when it is, or there is none).
//
// A profile outside that set (a swap --force, which records the profile it
// ran on: resume-record-profile) still resumes its session when the local
// manifest defines it for the role on the role's own client: on the role's
// own profile, never the recorded one, so a broker writer still chooses no
// profile; note says so. A profile of another client is refused: its
// session is not this client's.
func ResumeChoice(m *rolecfg.Manifest, role string, rec *bus.ResumeRecord) (profile, session, invalid, note string) {
	if rec == nil {
		return "", "", "", ""
	}
	own, err := m.Resolve(role)
	if err != nil {
		return "", "", err.Error(), ""
	}
	run, onOwn, err := m.ResumeProfile(role, rec.Profile)
	if err != nil {
		return "", "", err.Error(), ""
	}
	p := own
	if run != "" {
		if p, err = m.ResolveWith(role, run); err != nil {
			return "", "", err.Error(), ""
		}
		profile = run
	}
	if onOwn { // an invalid session returns below, with no note
		note = fmt.Sprintf("its recorded profile %s is neither %s's own nor one of its fallback_profiles: session %s resumes on its own profile %s", rec.Profile, role, rec.Session, own.Profile)
	}
	switch {
	case !bus.ValidSessionID(rec.Session):
		return "", "", "its session id is not valid", ""
	case !rolecfg.ResumeVerified(p.Client):
		return "", "", fmt.Sprintf("client %s has no verified resume", p.Client), ""
	}
	return profile, rec.Session, "", note
}

// RecoveryHint is the command a person runs to bring role back after a
// failed relaunch (P1-7): a new terminal of the project's holder, on the
// profile it ran on ("" its own) and resuming its session when there is
// one, or the whole team with up --resume. Never agent-launch typed in a
// pane: since the herdr exit a pane the holder did not start is one the
// dispatcher cannot wake, and the person who followed that hint started
// three agents as master.
func RecoveryHint(project, role, profile, session string) string {
	cmd := "agentbus --project " + project + " pop " + role
	if profile != "" {
		cmd += " --profile " + profile
	}
	if session != "" {
		cmd += " --resume " + session
	}
	return "recover with " + cmd + ", or agentbus up " + project + " --resume"
}
