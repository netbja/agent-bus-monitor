package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Swap intent (v0.15 §6.2): while `agentbus swap` replaces an agent by the
// same role on another profile, {p}:swap:<role> holds the intent, so the
// dispatcher never prompts the pane being closed and the release of a hold
// does not send a second resume. Two colons, like a wake record: it cannot
// forge a project, and it is not a project marker.

// SwapKey is the swap intent of one role.
func SwapKey(project, role string) string { return project + ":swap:" + role }

// SwapTTL bounds an intent whose caller died mid-swap.
const SwapTTL = 3 * time.Minute

// SwapIntent is the value of a swap intent.
type SwapIntent struct {
	ToProfile string `json:"to_profile"`
	By        string `json:"by"`
	OldPID    int    `json:"old_pid"`
	OldStart  uint64 `json:"old_start"`
	// Kind is "" for a swap, SwapKindReload for `agentbus reload` (lifecycle
	// spec, part C): the same intent, so the dispatcher's rules for a swap
	// (no prompt, escalation, resume or clear) apply to a reload unchanged.
	Kind string `json:"kind,omitempty"`
}

// SwapKindReload marks a reload's intent and ledger entries.
const SwapKindReload = "reload"

// ErrNoSnapshot: a reload's intent is refused for a role with no snapshot in
// {p}:agents (checked in the intent's script), so a deleted project is never
// revived; tab mode, which writes no services marker, still reloads.
var ErrNoSnapshot = fmt.Errorf("the role has no snapshot in {p}:agents (project deleted?)")

// ErrSwapInProgress: another swap of the same role holds the intent.
var ErrSwapInProgress = fmt.Errorf("a swap of this role is already in progress")

// EndSwap deletes the intent.
func (b *Bus) EndSwap(ctx context.Context, role string) error {
	return b.r.Del(ctx, SwapKey(b.project, role)).Err()
}

// Swapping reports whether a swap intent exists for role.
func (b *Bus) Swapping(ctx context.Context, role string) (bool, error) {
	n, err := b.r.Exists(ctx, SwapKey(b.project, role)).Result()
	return n == 1, err
}

// Swaps lists the roles under a swap intent (busmon's flag).
func (b *Bus) Swaps(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	prefix := b.project + ":swap:"
	iter := b.r.Scan(ctx, 0, prefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		if role := strings.TrimPrefix(iter.Val(), prefix); ValidName(role) {
			out[role] = true
		}
	}
	return out, iter.Err()
}

// SwapIntents is role → intent of every live intent (busmon tells a reload
// from a swap by its Kind); an unreadable intent counts as a swap.
func (b *Bus) SwapIntents(ctx context.Context) (map[string]SwapIntent, error) {
	roles, err := b.Swaps(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]SwapIntent, len(roles))
	for role := range roles {
		var in SwapIntent
		v, err := b.r.Get(ctx, SwapKey(b.project, role)).Result()
		if err == redis.Nil {
			continue // ended since the scan
		}
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(v), &in)
		out[role] = in
	}
	return out, nil
}

// ReviewsOf (v0.15 §6.1.3) lists the open 4-eyes challenges agent takes part
// in: those gating it (the author) and those it opened (the challenger). A
// gate is keyed by the gated agent and its meta is "<challenger>|<text>", so
// every gate is scanned.
func (b *Bus) ReviewsOf(ctx context.Context, agent string) ([]string, error) {
	var out []string
	prefix := b.project + ":gate:"
	iter := b.r.Scan(ctx, 0, prefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		gated := strings.TrimPrefix(key, prefix)
		if !ValidName(gated) {
			continue
		}
		h, err := b.r.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		for ref, meta := range h {
			challenger, _, _ := strings.Cut(meta, "|")
			switch {
			case gated == agent:
				out = append(out, fmt.Sprintf("challenge %s gates %s (opened by %s)", ref, agent, challenger))
			case challenger == agent:
				out = append(out, fmt.Sprintf("challenge %s opened by %s on %s", ref, agent, gated))
			}
		}
	}
	return out, iter.Err()
}

// ReviewsOfSince is ReviewsOf without the gates opened before horizon
// (lifecycle part B, rule 4: stale gates pile up and would hold a hand-off
// forever). A gate's open time is its latest challenge entry in :cmd; a gate
// whose challenge left the capped stream is older than everything retained,
// so it is dropped only when the oldest retained entry is itself past the
// horizon, and otherwise kept (its age is unknown).
func (b *Bus) ReviewsOfSince(ctx context.Context, agent string, horizon time.Duration) ([]string, error) {
	now, err := b.ServerNowMS(ctx)
	if err != nil {
		return nil, err
	}
	floor := now - horizon.Milliseconds()
	key := StreamKey(b.project, "cmd")
	msgs, err := b.r.XRange(ctx, key, "-", "+").Result()
	if err != nil {
		return nil, err
	}
	opened := map[string]int64{}
	oldest := int64(-1)
	for i, m := range msgs {
		ms, _ := splitID(m.ID)
		if i == 0 {
			oldest = ms
		}
		e := ParseEntry(key, m.ID, toStringMap(m.Values))
		if e.Type == CmdChallenge && e.Ref != "" {
			opened[e.Ref] = ms // the latest wins: a re-opened gate is dated by its last challenge
		}
	}
	var out []string
	prefix := b.project + ":gate:"
	iter := b.r.Scan(ctx, 0, prefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		gated := strings.TrimPrefix(key, prefix)
		if !ValidName(gated) {
			continue
		}
		h, err := b.r.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		for ref, meta := range h {
			at, retained := opened[ref]
			switch {
			case retained && at < floor:
				continue
			case !retained && oldest >= 0 && oldest < floor:
				continue // its challenge is older than the oldest retained entry
			}
			challenger, _, _ := strings.Cut(meta, "|")
			switch {
			case gated == agent:
				out = append(out, fmt.Sprintf("challenge %s gates %s (opened by %s)", ref, agent, challenger))
			case challenger == agent:
				out = append(out, fmt.Sprintf("challenge %s opened by %s on %s", ref, agent, gated))
			}
		}
	}
	return out, iter.Err()
}

// cmdOnce: 0 the mark already holds ARGV[2]; 1 XADDed and marked. No lease:
// `agentbus swap` shares the dispatcher's resume mark (§3.3) so a release in
// the same instant sends one resume, not two.
var cmdOnce = redis.NewScript(`
if redis.call('HGET', KEYS[1], ARGV[1]) == ARGV[2] then return 0 end
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[3], '*', unpack(ARGV, 4))
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
return 1`)

// CmdOnce writes a directive from `from` to target unless {p}:quota's mark
// already holds episode, and sets the mark with it. It reports whether it
// wrote.
func (b *Bus) CmdOnce(ctx context.Context, mark, episode, from, target, command string) (bool, error) {
	if err := ValidateText(command, CmdMaxRunes); err != nil {
		return false, err
	}
	if !ValidName(target) || !ValidName(from) {
		return false, fmt.Errorf("invalid sender or target")
	}
	if mark == "" || episode == "" {
		return false, fmt.Errorf("cmd once: mark and episode required")
	}
	n, err := cmdOnce.Run(ctx, b.r, []string{QuotaKey(b.project), StreamKey(b.project, "cmd")},
		mark, episode, streamMaxLen,
		"from", from, "target", target, "type", CmdDirective, "ref", "", "command", command, "text_complete", "yes").Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// openGate: SWAPPING when either side is being swapped, else the gate field.
var openGate = redis.NewScript(`
if redis.call('EXISTS',KEYS[2])==1 or redis.call('EXISTS',KEYS[3])==1 then return redis.error_reply('SWAPPING') end
return redis.call('HSET',KEYS[1],ARGV[1],ARGV[2])`)

// swapping turns a script's SWAPPING refusal (v0.15 §6.1.3: work keyed by an
// agent's name is not opened while `agentbus swap` replaces it, checked in
// the same script as the write) into a readable error.
func swapping(err error, who string) error {
	if err != nil && strings.Contains(err.Error(), "SWAPPING") {
		return fmt.Errorf("%s is being swapped (agentbus swap): retry in a minute", who)
	}
	return err
}

// PendingFor reports whether entry id of the cmd stream is in agent's
// pending list (delivered, not acknowledged): the next read reclaims it.
func (b *Bus) PendingFor(ctx context.Context, agent, id string) (bool, error) {
	res, err := b.r.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: StreamKey(b.project, "cmd"), Group: agent,
		Start: id, End: id, Count: 1}).Result()
	if err != nil {
		if strings.Contains(err.Error(), "NOGROUP") {
			return false, nil
		}
		return false, err
	}
	return len(res) == 1, nil
}

// EpisodeEntry is the id of the newest cmd entry to agent written under
// {p}:quota mark = episode (FencedCmdOnce's "episode" field), within the
// retained stream (all of it: MAXLEN ~ keeps more than its nominal cap);
// "" if none.
func (b *Bus) EpisodeEntry(ctx context.Context, agent, mark, episode string) (string, error) {
	want := mark + "=" + episode
	end := "+"
	for {
		msgs, err := b.r.XRevRangeN(ctx, StreamKey(b.project, "cmd"), end, "-", 500).Result()
		if err != nil {
			return "", err
		}
		for _, m := range msgs {
			if m.Values["target"] == agent && m.Values["episode"] == want {
				return m.ID, nil
			}
		}
		if len(msgs) < 500 {
			return "", nil
		}
		end = "(" + msgs[len(msgs)-1].ID
	}
}
