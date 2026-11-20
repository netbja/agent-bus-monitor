package dispatch

import (
	"context"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// DeclaredFrom is HostRuntime.Declared from the bus: the agent's declared
// record, set and unexpired (the Redis clock expires it). A failed read is
// no declaration: the other sources still judge (background-work-visible §4b).
func DeclaredFrom(b *bus.Bus) func(agent string) (string, bool) {
	return func(agent string) (string, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		all, err := b.Backgrounds(ctx, []string{agent})
		if err != nil {
			return "", false
		}
		d, ok := all[agent]
		return d.Text, ok && d.Text != ""
	}
}
