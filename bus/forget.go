package bus

import (
	"context"
	"fmt"
	"strings"
)

// ForgetAgent removes what the bus holds about one agent: its snapshot and
// usage, its wake record, armed, gate and receiver keys, and its cmd consumer
// group. Liveness is passive, so a departed agent otherwise keeps showing its
// last declared state forever; forgetting is the explicit way to retire it.
// The streams are history and stay. A dispatcher may recreate an empty group
// later if a retained cmd entry still names the agent; that group starts at
// the horizon floor, so old entries never wake anyone. It reports whether
// anything was there.
func (b *Bus) ForgetAgent(ctx context.Context, agent string) (bool, error) {
	if !ValidName(agent) {
		return false, fmt.Errorf("invalid agent %q", agent)
	}
	p := b.project
	pipe := b.r.TxPipeline()
	agents := pipe.HDel(ctx, AgentsKey(p), agent)
	usage := pipe.HDel(ctx, UsageKey(p), agent)
	keys := pipe.Del(ctx, WakeKey(p, agent), ArmedKey(p, agent), GateKey(p, agent), ReceiverKey(p, agent))
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	found := agents.Val()+usage.Val()+keys.Val() > 0
	n, err := b.r.XGroupDestroy(ctx, StreamKey(p, "cmd"), agent).Result()
	if err != nil && !strings.Contains(err.Error(), "no such key") && !strings.Contains(err.Error(), "NOGROUP") {
		return found, err
	}
	return found || n > 0, nil
}
