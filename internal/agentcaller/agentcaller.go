// Package agentcaller tells whether a registered agent runs the calling
// process: the one rule both binaries apply before an act that is the
// person's alone (notify-guided-setup §2: writing notify.toml, showing a
// topic, a test message). Defence in depth, not a boundary: the bus is no
// boundary against the local user.
package agentcaller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
)

// ErrNoAncestry: this host has no /proc to read the caller's ancestors.
var ErrNoAncestry = errors.New("no process ancestry on this host")

// Ancestor is "<project>/<agent>" for the registered agent, in any project
// of the broker, whose verified pid and start are an ancestor of this
// process ("" when none is): clearing AGENT_BUS_AGENT is no way out of the
// agent refusal (reviewer-codex, master).
func Ancestor(ctx context.Context) (string, error) {
	chain := dispatch.Ancestors(os.Getpid())
	if len(chain) == 0 {
		return "", ErrNoAncestry
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := bus.ConnectContext(ctx, "")
	if err != nil {
		return "", err
	}
	defer r.Close()
	projects, err := bus.Projects(ctx, r)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		b, err := bus.Open(r, p.Project)
		if err != nil {
			return "", err
		}
		snaps, err := b.Agents(ctx)
		if err != nil {
			return "", err
		}
		for _, name := range slices.Sorted(maps.Keys(snaps)) {
			s := snaps[name]
			if s.PID == 0 || s.PIDStart == 0 {
				continue // no verified identity to match
			}
			for _, a := range chain {
				if a.PID == s.PID && a.Start == s.PIDStart {
					return p.Project + "/" + name, nil
				}
			}
		}
	}
	return "", nil
}

// Refuse is the refusal of an agent caller before the person's act what
// (run as command from the person's shell), "" for the person: AGENT_BUS_AGENT
// set, or a registered agent among the ancestors (ancestor, nil = not
// checked). An ancestry that cannot be read refuses, except on a host with
// none (ErrNoAncestry), where the env rule alone applies.
func Refuse(ctx context.Context, getenv func(string) string, ancestor func(context.Context) (string, error), what, command string) string {
	if self := getenv("AGENT_BUS_AGENT"); self != "" {
		return fmt.Sprintf("%s is the person's act, not %s's (run %s from your own shell)", what, self, command)
	}
	if ancestor == nil {
		return ""
	}
	switch caller, err := ancestor(ctx); {
	case errors.Is(err, ErrNoAncestry):
	case err != nil:
		return fmt.Sprintf("cannot check that no registered agent runs this (%v)", err)
	case caller != "":
		return what + " is the person's act, and this runs under the agent " + caller
	}
	return ""
}
