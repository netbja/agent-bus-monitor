package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/redis/go-redis/v9"
)

// Check-backs (master-idle §4, docs/superpowers/specs/2026-11-15-master-idle-design.md):
// an agent with work in flight off the bus (a long test run, a build) says
// once when to look again, so a guard can see it: `agentbus checkback 30m
// "<what>"`. One field per agent in {p}:checkback, an agent's write (no
// lease, as status); a new one replaces the old one, --clear and forgetting
// the agent delete it. It never expires by itself. Only master's is judged
// (idle-master); another role's is listed.

// CheckbackKey is {p}:checkback.
func CheckbackKey(project string) string { return project + ":checkback" }

// CheckbackTextMax bounds what a check-back says (runes).
const CheckbackTextMax = 200

// Checkback is one agent's check-back. DueMs is Redis TIME plus the
// duration, SetID the write's Redis TIME (Unix ms). Count is how many
// check-backs in a row said the same what (trimmed, case folded): a new
// what or a clear starts again at 1; FirstSetID is the SetID of the one that
// started the count.
type Checkback struct {
	Agent      string `json:"-"`
	DueMs      int64  `json:"due_ms"`
	What       string `json:"what"`
	SetID      int64  `json:"set_id"`
	Count      int    `json:"count"`
	FirstSetID int64  `json:"first_set_id"`
	Norm       string `json:"norm"` // What trimmed and case folded: what Count compares
}

// checkbackSet: KEYS[1] {p}:checkback; ARGV[1] agent, ARGV[2] duration ms,
// ARGV[3] what, ARGV[4] its normal form. Returns the stored JSON.
var checkbackSet = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local count, first = 1, now
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if raw then
  local ok, old = pcall(cjson.decode, raw)
  if ok and type(old) == 'table' and old.norm == ARGV[4] and type(old.count) == 'number' and type(old.first_set_id) == 'number' then
    count, first = old.count + 1, old.first_set_id
  end
end
local v = cjson.encode({due_ms = now + tonumber(ARGV[2]), what = ARGV[3], set_id = now, count = count, first_set_id = first, norm = ARGV[4]})
redis.call('HSET', KEYS[1], ARGV[1], v)
return v`)

// SetCheckback sets agent's check-back, due in d.
func (b *Bus) SetCheckback(ctx context.Context, agent string, d time.Duration, what string) (Checkback, error) {
	what = strings.TrimSpace(what)
	switch {
	case !ValidName(agent):
		return Checkback{}, fmt.Errorf("invalid agent %q", agent)
	case d <= 0:
		return Checkback{}, errors.New("a check-back is due after a positive duration")
	case what == "" || utf8.RuneCountInString(what) > CheckbackTextMax || !health.SafeText(what) || strings.ContainsAny(what, "\n\t"):
		return Checkback{}, fmt.Errorf("a check-back says what is in flight in one line of plain text, at most %d characters, with no credential", CheckbackTextMax)
	}
	raw, err := checkbackSet.Run(ctx, b.r, []string{CheckbackKey(b.project)}, agent, d.Milliseconds(), what, strings.ToLower(what)).Text()
	if err != nil {
		return Checkback{}, err
	}
	var c Checkback
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Checkback{}, err
	}
	c.Agent = agent
	return c, nil
}

// ClearCheckback deletes agent's check-back; false when it had none.
func (b *Bus) ClearCheckback(ctx context.Context, agent string) (bool, error) {
	n, err := b.r.HDel(ctx, CheckbackKey(b.project), agent).Result()
	return n > 0, err
}

// Checkbacks are every agent's check-back; a corrupt field is skipped.
func (b *Bus) Checkbacks(ctx context.Context) (map[string]Checkback, error) {
	h, err := b.r.HGetAll(ctx, CheckbackKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]Checkback{}
	for agent, raw := range h {
		var c Checkback
		if json.Unmarshal([]byte(raw), &c) == nil && ValidName(agent) {
			c.Agent = agent
			out[agent] = c
		}
	}
	return out, nil
}
