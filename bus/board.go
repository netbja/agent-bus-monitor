// Shared task-ownership board. {project}:board is a Redis hash: field = task
// slug, value = JSON BoardEntry. It answers "who owns what" so agents on the
// same project stop duplicating work. The claim is the only guard — the team
// is trusted, so state updates and drops carry no owner check.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// BoardKey is the per-project hash of task ownership ({task} → JSON
// BoardEntry). It is a hash, not a stream: it survives Bus.Purge (--reset)
// exactly like the agents/usage/budget hashes.
func BoardKey(project string) string { return project + ":board" }

// BoardEntry is one task's ownership record. State is any ValidName word
// (working, review, done, blocked, …) — the board does not hardcode a state
// list, only "done" is special (a done task can be re-claimed by anyone).
// Updated is unix seconds.
type BoardEntry struct {
	Owner   string       `json:"owner"`
	State   string       `json:"state"`
	Branch  string       `json:"branch,omitempty"`
	Updated int64        `json:"updated"`
	Request *RequestInfo `json:"request,omitempty"`
}

// Board returns task → entry for the whole board. Unparseable fields are
// skipped, like Agents/Usage.
func (b *Bus) Board(ctx context.Context) (map[string]BoardEntry, error) {
	raw, err := b.r.HGetAll(ctx, BoardKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]BoardEntry, len(raw))
	for task, v := range raw {
		var e BoardEntry
		if json.Unmarshal([]byte(v), &e) == nil {
			out[task] = e
		}
	}
	if err := b.requestAvailability(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// BoardClaim takes ownership of task for owner, setting state to "working".
// If the task exists with a state other than "done" and a different owner, the
// claim fails loudly — surfacing that conflict is the entire point of the
// board. A re-claim by the same owner just refreshes the entry (branch,
// timestamp), and a "done" task can be picked up by anyone.
func (b *Bus) BoardClaim(ctx context.Context, task, owner, branch string) error {
	if !ValidName(task) {
		return fmt.Errorf("invalid task %q", task)
	}
	if !ValidName(owner) {
		return fmt.Errorf("invalid owner %q", owner)
	}
	return boardMutation.Run(ctx, b.r, []string{BoardKey(b.project)}, "claim", task, owner, branch, time.Now().Unix()).Err()
}

// BoardState updates the state (and timestamp) of an existing entry, keeping
// its owner and branch. There is deliberately no owner check — the team is
// trusted; the claim is the guard. An unknown task is an error, so a typo
// fails loud instead of inventing an entry with no owner.
func (b *Bus) BoardState(ctx context.Context, task, state string) error {
	if !ValidName(task) {
		return fmt.Errorf("invalid task %q", task)
	}
	if !ValidName(state) {
		return fmt.Errorf("invalid board state %q (a word like working|review|done|blocked)", state)
	}
	return boardMutation.Run(ctx, b.r, []string{BoardKey(b.project)}, "state", task, state, "", time.Now().Unix()).Err()
}

// BoardDrop removes task from the board. Dropping an unknown task is a no-op
// success, like ReleasePilot/Disarm.
func (b *Bus) BoardDrop(ctx context.Context, task string) error {
	if !ValidName(task) {
		return fmt.Errorf("invalid task %q", task)
	}
	return b.r.HDel(ctx, BoardKey(b.project), task).Err()
}

// boardEntry returns the entry stored for task, or nil when the field is
// absent. A corrupt (unparseable) field is an error — guessing at it would
// defeat the conflict check.
func (b *Bus) boardEntry(ctx context.Context, key, task string) (*BoardEntry, error) {
	v, err := b.r.HGet(ctx, key, task).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e BoardEntry
	if err := json.Unmarshal([]byte(v), &e); err != nil {
		return nil, fmt.Errorf("board: %s holds a corrupt entry: %w", task, err)
	}
	return &e, nil
}

func (b *Bus) boardSet(ctx context.Context, key, task string, e BoardEntry) error {
	v, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return b.r.HSet(ctx, key, task, v).Err()
}

// The whole read/check/write runs atomically and preserves unknown JSON fields.
var boardMutation = redis.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[2])
local e = nil
if raw then e = cjson.decode(raw) end
if e and e.request then return redis.error_reply('tracked request: use request accept/block/done or explicit board drop') end
if ARGV[1] == 'claim' then
 if e and e.state ~= 'done' and e.owner ~= ARGV[3] then
  return redis.error_reply('board: '..ARGV[2]..' owned by '..e.owner..' ('..e.state..')')
 end
 e = e or {}
 e.owner = ARGV[3]; e.state = 'working'; e.branch = ARGV[4]
else
 if not e then return redis.error_reply('board: no task '..ARGV[2]..' (claim it first)') end
 e.state = ARGV[3]
end
e.updated = tonumber(ARGV[5])
return redis.call('HSET', KEYS[1], ARGV[2], cjson.encode(e))`)
