package main

import (
	"context"
	"fmt"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// launchResumes reads the project's resume records ({p}:resume): the one
// broker read of a launch, and only of a resume. Tests replace it.
var launchResumes = func(project string) (map[string]bus.ResumeRecord, error) {
	b, done, err := openHookBus(project)
	if err != nil {
		return nil, err
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return b.ResumeRecords(ctx)
}

// launchGuard refuses the launches the herdr-era hint led to (P1-7), before
// anything is built or run:
//   - a role that is not the caller's: inside an agent's environment
//     (AGENT_BUS_AGENT, which its own launch sets, and the holder for the role
//     it starts) only that role launches, so agent-launch coder typed in
//     master's terminal is refused;
//   - a session recorded for another role: it resumes only as that role (the
//     person resumed coder's and foureyes' sessions as master). A resume whose
//     records cannot be read is refused too, since it cannot tell whose it is.
func launchGuard(env []string, role, project, session string, resumes func(project string) (map[string]bus.ResumeRecord, error)) error {
	if caller, _ := lookupEnv(env, "AGENT_BUS_AGENT"); caller != "" && caller != role {
		return fmt.Errorf("launch %s: this environment is %s's (AGENT_BUS_AGENT): an agent's terminal launches only its own role; start %s with agentbus --project %s pop %s",
			role, caller, role, project, role)
	}
	if session == "" {
		return nil
	}
	recs, err := resumes(project)
	if err != nil {
		return fmt.Errorf("launch %s --resume %s: the resume records of %s are unreadable, so whose session it is cannot be told: %v", role, session, project, err)
	}
	for agent, rec := range recs {
		if agent != role && rec.Session == session {
			return fmt.Errorf("launch %s --resume %s: that session is recorded for %s: it resumes only as %s (agentbus --project %s pop %s --resume %s)",
				role, session, agent, agent, project, agent, session)
		}
	}
	return nil
}
