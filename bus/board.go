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
	Owner   string `json:"owner"`
	State   string `json:"state"`
	Branch  string `json:"branch,omitempty"`
	Updated int64  `json:"updated"`
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
	key := BoardKey(b.project)
	cur, err := b.boardEntry(ctx, key, task)
	if err != nil {
		return err
	}
	if cur != nil && cur.State != "done" && cur.Owner != owner {
		return fmt.Errorf("board: %s owned by %s (%s)", task, cur.Owner, cur.State)
	}
	return b.boardSet(ctx, key, task, BoardEntry{
		Owner: owner, State: "working", Branch: branch, Updated: time.Now().Unix(),
	})
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
	key := BoardKey(b.project)
	cur, err := b.boardEntry(ctx, key, task)
	if err != nil {
		return err
	}
	if cur == nil {
		return fmt.Errorf("board: no task %q (claim it first)", task)
	}
	cur.State = state
	cur.Updated = time.Now().Unix()
	return b.boardSet(ctx, key, task, *cur)
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
