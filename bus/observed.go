package bus

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// Observed (v0.14.3): what the dispatcher SAW in each agent's pane, kept
// apart from what the agent DECLARED. An agent that works without declaring
// `working` still shows as busy; its declared state is never rewritten.

// ObservedKey is the {p}:observed hash: agent → Observation JSON. The whole
// hash expires ObservedTTL after the last write, so a stopped dispatcher
// leaves no stale "busy" behind.
func ObservedKey(project string) string { return project + ":observed" }

// ObservedTTL bounds how long an observation outlives its dispatcher.
const ObservedTTL int64 = 120000 // ms

// Observation is one agent's pane as last inspected.
type Observation struct {
	Busy  bool   `json:"busy"`            // the client is running a turn (herdr status or busy signature)
	State string `json:"state,omitempty"` // herdr's agent status: working, idle, blocked, done, unknown
	At    int64  `json:"at"`              // Unix ms, Redis TIME of the inspection
}

// observedReplace: 0 not the holder, else 1; the hash becomes exactly the
// given agent/observation pairs (none = deleted) and expires after ARGV[2] ms.
var observedReplace = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
if #ARGV > 2 then
  redis.call('HSET', KEYS[2], unpack(ARGV, 3))
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
end
return 1`)

// FencedPutObserved replaces {p}:observed while token holds the dispatch lease.
func (b *Bus) FencedPutObserved(ctx context.Context, token string, obs map[string]Observation) error {
	argv := []interface{}{token, ObservedTTL}
	for a, o := range obs {
		if !ValidName(a) {
			continue
		}
		v, err := json.Marshal(o)
		if err != nil {
			return err
		}
		argv = append(argv, a, string(v))
	}
	n, err := observedReplace.Run(ctx, b.r, []string{DispatchKey(b.project), ObservedKey(b.project)}, argv...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

// Observed reads {p}:observed; a malformed entry is skipped.
func (b *Bus) Observed(ctx context.Context) (map[string]Observation, error) {
	raw, err := b.r.HGetAll(ctx, ObservedKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Observation, len(raw))
	for a, v := range raw {
		var o Observation
		if json.Unmarshal([]byte(v), &o) == nil {
			out[a] = o
		}
	}
	return out, nil
}
