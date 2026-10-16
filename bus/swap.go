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
}

// ErrSwapInProgress: another swap of the same role holds the intent.
var ErrSwapInProgress = fmt.Errorf("a swap of this role is already in progress")

// BeginSwap writes the intent (SET NX PX); a second swap is refused.
func (b *Bus) BeginSwap(ctx context.Context, role string, in SwapIntent) error {
	if !ValidName(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	v, err := json.Marshal(in)
	if err != nil {
		return err
	}
	ok, err := b.r.SetNX(ctx, SwapKey(b.project, role), string(v), SwapTTL).Result()
	if err != nil {
		return err
	}
	if !ok {
		return ErrSwapInProgress
	}
	return nil
}

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

// refuseWhileSwapping (v0.15 §6.1.3): work keyed by an agent's name is not
// opened while `agentbus swap` replaces it; the swap re-checks just before
// it closes the pane, so a request or challenge either lands before (and the
// swap refuses) or is refused here.
func (b *Bus) refuseWhileSwapping(ctx context.Context, agent string) error {
	if !ValidName(agent) {
		return nil
	}
	swapping, err := b.Swapping(ctx, agent)
	if err != nil {
		return err
	}
	if swapping {
		return fmt.Errorf("%s is being swapped (agentbus swap): retry in a minute", agent)
	}
	return nil
}
