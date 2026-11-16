package main

// recoveryHint is the command a person runs to bring role back after a
// failed relaunch (P1-7): a new terminal of the project's holder, on the
// profile it ran on ("" its own) and resuming its session when there is
// one, or the whole team with up --resume. Never agent-launch typed in a
// pane: since the herdr exit a pane the holder did not start is one the
// dispatcher cannot wake, and the person who followed that hint started
// three agents as master.
func recoveryHint(project, role, profile, session string) string {
	cmd := "agentbus --project " + project + " pop " + role
	if profile != "" {
		cmd += " --profile " + profile
	}
	if session != "" {
		cmd += " --resume " + session
	}
	return "recover with " + cmd + ", or agentbus up " + project + " --resume"
}
