package bus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// An agent's background work (progress-line §4): "2 checks running", in its
// own record, {p}:background:<agent>, which expires on the Redis clock. Not
// the status message (an agent's turn hooks rewrite that at every turn), not
// the {p}:agents snapshot, not the board.

// BackgroundKey is the agent's background record: a hash of text, since
// (Unix ms, Redis TIME) and by.
func BackgroundKey(project, agent string) string { return project + ":background:" + agent }

const (
	BackgroundTextMax    = 60
	BackgroundMaxFor     = 24 * time.Hour
	BackgroundDefaultFor = 30 * time.Minute
)

// Background is one agent's background work.
type Background struct {
	Text  string
	Since time.Time
	By    string
}

// setBackground replaces the record in one step: since from Redis TIME, the
// expiry by PEXPIRE (the Redis clock, never the writer's).
var setBackground = redis.NewScript(`
local t = redis.call('TIME')
local ms = string.format('%d', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
redis.call('DEL', KEYS[1])
redis.call('HSET', KEYS[1], 'text', ARGV[1], 'since', ms, 'by', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return ms
`)

// SetBackground sets or replaces agent's background work for d (0 < d <=
// 24h); text is one line of at most BackgroundTextMax characters.
func (b *Bus) SetBackground(ctx context.Context, agent, text, by string, d time.Duration) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return errors.New("empty background text: say what runs, or use --clear")
	case strings.ContainsFunc(text, unicode.IsControl):
		return errors.New("the background text is one line, with no control characters")
	case d < time.Millisecond: // PEXPIRE takes whole ms: 0 would expire it at once (reviewer-codex)
		return fmt.Errorf("--for %v: give how long it may run (at least 1ms)", d)
	case d > BackgroundMaxFor:
		return fmt.Errorf("--for %v: at most 24h", d)
	}
	if err := ValidateText(text, BackgroundTextMax); err != nil {
		return err
	}
	return setBackground.Run(ctx, b.r, []string{BackgroundKey(b.project, agent)}, text, by, d.Milliseconds()).Err()
}

// ClearBackground removes agent's background work: it ended.
func (b *Bus) ClearBackground(ctx context.Context, agent string) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	return b.r.Del(ctx, BackgroundKey(b.project, agent)).Err()
}

// Backgrounds reads the background work of each agent in one round trip;
// an agent with none, or whose record expired, is absent.
func (b *Bus) Backgrounds(ctx context.Context, agents []string) (map[string]Background, error) {
	pipe := b.r.Pipeline()
	cmds := map[string]*redis.MapStringStringCmd{}
	for _, a := range agents {
		if ValidName(a) {
			cmds[a] = pipe.HGetAll(ctx, BackgroundKey(b.project, a))
		}
	}
	if len(cmds) == 0 {
		return map[string]Background{}, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := map[string]Background{}
	for a, c := range cmds {
		h := c.Val()
		if h["text"] == "" {
			continue
		}
		ms, _ := strconv.ParseInt(h["since"], 10, 64)
		out[a] = Background{Text: h["text"], Since: time.UnixMilli(ms), By: h["by"]}
	}
	return out, nil
}
