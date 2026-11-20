package dispatchsvc

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// Runtime is the dispatcher's runtime on the project's holder
// (herdr exit, step 10, spec 4.2-4.3): termhost gives the facts, agentstate
// judges them. An agent's client is its live terminal's profile, the one the
// holder started it on; the hooks' subagent facts are not read yet (nil: the
// background judgement never takes "not read" for "none").
func Runtime(project string, getenv func(string) string) (*dispatch.HostRuntime, error) {
	h, err := holder.NewHost(project, holder.BuildID(), getenv)
	if err != nil {
		return nil, err
	}
	r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
	return &dispatch.HostRuntime{Host: h, Project: project, PersonQuiet: r.Duration("dispatch.person_quiet"),
		Client: func(agent string) string { return termProfile(h, project, agent) }}, nil
}

// termProfile is agent's live terminal's profile on host, "" when it has
// none or the host cannot be read.
func termProfile(h termhost.Host, project, agent string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	terms, err := h.List(ctx, project)
	if err != nil {
		return ""
	}
	for _, t := range terms {
		if t.Name == agent && t.Alive {
			return t.Profile
		}
	}
	return ""
}
