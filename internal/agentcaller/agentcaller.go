// Package agentcaller tells whether a registered agent runs the calling
// process: the one rule both binaries apply before an act that is the
// person's alone (notify-guided-setup §2: writing notify.toml, showing a
// topic, a test message). Defence in depth, not a boundary: the bus is no
// boundary against the local user.
package agentcaller

import (
	"context"
	"errors"
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
