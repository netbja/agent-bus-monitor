package main

import (
	"context"
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// agentbus pop <role> [--profile <p>] [--resume <session>] (herdr exit
// §4.2): the master's pop, agent-spawn's successor. It starts the role as a
// new terminal of the project's holder, which builds the command from the
// role's launch template (its own profile or one of its fallback_profiles;
// a session only for a client whose resume is verified). Exit 0, 1 when the
// holder refuses, 2 on a usage error.
func runPop(ctx context.Context, h teamHost, project string, args []string, out, errw io.Writer) int {
	const usage = "usage: agentbus pop <role> [--profile <profile>] [--resume <session-id>]"
	args, profile := extractFlag(args, "--profile")
	args, session := extractFlag(args, "--resume")
	switch {
	case len(args) != 1 || !bus.ValidName(args[0]):
		fmt.Fprintln(errw, usage)
		return 2
	case profile != "" && !bus.ValidName(profile):
		fmt.Fprintf(errw, "pop: invalid profile %q\n", profile)
		return 2
	case session != "" && !bus.ValidSessionID(session):
		fmt.Fprintf(errw, "pop: invalid session id %q\n", session)
		return 2
	}
	role := args[0]
	t, err := h.Start(ctx, termhost.Spec{Project: project, Name: role, Profile: profile, ResumeSession: session})
	if err != nil {
		fmt.Fprintf(errw, "pop %s: %v\n", role, err)
		return 1
	}
	fmt.Fprintf(out, "%s: started (%s); agentbus attach %s shows it\n", role, t.ID, role)
	return 0
}
