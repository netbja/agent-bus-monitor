package bus

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
)

// Context fill (context-alert, ca1): each agent's context fill as its
// client gave it (Claude Code's statusLine used_percentage, recorded locally
// by agentbus statusline), published by the dispatcher. An agent with no
// reading is absent: unknown, never 0.

// ContextKey is the {p}:context hash: agent → ContextFill JSON. Replaced
// whole by the dispatcher and expiring ContextTTL after its last write, like
// {p}:observed; not in projectKinds.
func ContextKey(project string) string { return project + ":context" }

// ContextTTL bounds how long a published fill outlives its dispatcher.
const ContextTTL int64 = 120000 // ms

// ContextFill is one agent's last context fill.
type ContextFill struct {
	Pct        float64 `json:"pct"`         // 0-100, as the client computed it
	ObservedAt int64   `json:"observed_at"` // Unix ms, when the client's status line gave it
	Session    string  `json:"session"`     // the session it belongs to (a new session is a new episode)
	At         int64   `json:"at"`          // Unix ms, Redis TIME of the publication
}

// contextReplace: 0 not the holder, else 1; the hash becomes exactly the
// given agent/fill pairs (none = deleted) and expires after ARGV[2] ms.
var contextReplace = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
if #ARGV > 2 then
  redis.call('HSET', KEYS[2], unpack(ARGV, 3))
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
end
return 1`)

// FencedPutContext replaces {p}:context while token holds the dispatch lease.
func (b *Bus) FencedPutContext(ctx context.Context, token string, fills map[string]ContextFill) error {
	argv := []interface{}{token, ContextTTL}
	for a, f := range fills {
		if !ValidName(a) {
			continue
		}
		v, err := json.Marshal(f)
		if err != nil {
			return err
		}
		argv = append(argv, a, string(v))
	}
	n, err := contextReplace.Run(ctx, b.r, []string{DispatchKey(b.project), ContextKey(b.project)}, argv...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

// Context reads {p}:context; a malformed entry is skipped.
func (b *Bus) Context(ctx context.Context) (map[string]ContextFill, error) {
	raw, err := b.r.HGetAll(ctx, ContextKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]ContextFill, len(raw))
	for a, v := range raw {
		var f ContextFill
		if json.Unmarshal([]byte(v), &f) == nil {
			out[a] = f
		}
	}
	return out, nil
}
