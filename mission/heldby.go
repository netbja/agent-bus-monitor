package mission

import (
	"context"
	"fmt"

	"github.com/netbja/agent-bus-monitor/bus"
)

// HeldBy lists the mission attempts agent is assigned to, in progress.
func HeldBy(ctx context.Context, b *bus.Bus, agent string) ([]string, error) {
	st := Store{B: b}
	changes, err := st.Changes(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range changes {
		ms, err := st.Missions(ctx, c)
		if err != nil {
			return nil, err
		}
		for id, r := range ms {
			switch r.State {
			case Requested, Accepted, Delivered, Checking:
				if r.Current.Agent == agent {
					out = append(out, fmt.Sprintf("mission %s/%s (%s)", c, id, r.State))
				}
			}
		}
	}
	return out, nil
}
