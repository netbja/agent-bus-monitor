package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Quota (v0.13, spec 2026-10-15-quota-awareness-design.md §7): the dispatcher
// publishes provider readings to {p}:budget, keeps its warn and hold marks in
// {p}:quota, and may pause the project for the coordinator's provider. Every
// one of these writes checks the dispatch lease in the same script.

// QuotaKey is the {p}:quota hash: "warn:<provider>:<window>" → that window's
// resets_at, "hold:<provider>" → the resets_at of the window that held it.
func QuotaKey(project string) string { return project + ":quota" }

// quotaHSet: 0 not the holder, else 1. ARGV[1] token, then field/value pairs.
var quotaHSet = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[2], unpack(ARGV, 2))
return 1`)

// quotaHDel: 0 not the holder, else 1. ARGV[1] token, then fields.
var quotaHDel = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('HDEL', KEYS[2], unpack(ARGV, 2))
return 1`)

func (b *Bus) fencedHash(ctx context.Context, script *redis.Script, token, key string, args ...string) error {
	if len(args) == 0 {
		return nil
	}
	argv := append([]interface{}{token}, toAny(args)...)
	n, err := script.Run(ctx, b.r, []string{DispatchKey(b.project), key}, argv...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

func toAny(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// quotaReplace: 0 not the holder, else 1; the hash becomes exactly the given
// field/value pairs (none = deleted).
var quotaReplace = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
if #ARGV > 1 then redis.call('HSET', KEYS[2], unpack(ARGV, 2)) end
return 1`)

// FencedPutBudgets replaces {p}:budget with the given provider readings while
// token holds the dispatch lease: a provider no longer used, or a legacy row,
// does not linger.
func (b *Bus) FencedPutBudgets(ctx context.Context, token string, snaps map[string]BudgetSnapshot) error {
	argv := []interface{}{token}
	for p, s := range snaps {
		v, err := json.Marshal(s)
		if err != nil {
			return err
		}
		argv = append(argv, p, string(v))
	}
	n, err := quotaReplace.Run(ctx, b.r, []string{DispatchKey(b.project), BudgetKey(b.project)}, argv...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

// QuotaMarks reads {p}:quota.
func (b *Bus) QuotaMarks(ctx context.Context) (map[string]string, error) {
	return b.r.HGetAll(ctx, QuotaKey(b.project)).Result()
}

// FencedQuotaSet sets {p}:quota fields (field, value, …) under the lease.
func (b *Bus) FencedQuotaSet(ctx context.Context, token string, fieldValues ...string) error {
	if len(fieldValues)%2 != 0 {
		return fmt.Errorf("FencedQuotaSet: odd field/value list")
	}
	return b.fencedHash(ctx, quotaHSet, token, QuotaKey(b.project), fieldValues...)
}

// FencedQuotaDel deletes {p}:quota fields under the lease.
func (b *Bus) FencedQuotaDel(ctx context.Context, token string, fields ...string) error {
	return b.fencedHash(ctx, quotaHDel, token, QuotaKey(b.project), fields...)
}

// QuotaPauseReason is the pause reason the dispatcher writes for a provider.
func QuotaPauseReason(provider string) string { return "quota " + provider }

// fencedQuotaPause: -1 not the holder; 0 another pause is in place (a person's
// or master's, or another provider's): never replaced; 1 written.
var fencedQuotaPause = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if v then
  local ok, p = pcall(cjson.decode, v)
  if not ok or p['reason'] ~= ARGV[2] then return 0 end
end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('SET', KEYS[2], cjson.encode({['until']=tonumber(ARGV[3]), reason=ARGV[2], by='dispatch', notify=ARGV[4], at=now}))
return 1`)

// FencedQuotaPause pauses the project for a provider's quota until untilMS,
// unless another pause is in place. It reports whether it wrote.
func (b *Bus) FencedQuotaPause(ctx context.Context, token, provider, notify string, untilMS int64) (bool, error) {
	if !ValidName(notify) {
		return false, fmt.Errorf("invalid notify %q", notify)
	}
	n, err := fencedQuotaPause.Run(ctx, b.r, []string{DispatchKey(b.project), PauseKey(b.project)},
		token, QuotaPauseReason(provider), strconv.FormatInt(untilMS, 10), notify).Int()
	if err != nil {
		return false, err
	}
	if n == -1 {
		return false, ErrNotHolder
	}
	return n == 1, nil
}

// fencedQuotaUnpause: -1 not the holder; 0 no quota pause of this provider;
// 1 deleted.
var fencedQuotaUnpause = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if not v then return 0 end
local ok, p = pcall(cjson.decode, v)
if not ok or p['reason'] ~= ARGV[2] then return 0 end
redis.call('DEL', KEYS[2])
if ARGV[3] ~= '' then
  redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[4], '*', unpack(ARGV, 5))
end
return 1`)

// FencedQuotaUnpause ends this provider's quota pause early (a reading back
// under the threshold, a call served after the limit hit); it never touches
// another pause. Like FencedResume, it sends the pause's notify target the
// resume directive in the same script, so the coordinator learns the team
// may go on.
func (b *Bus) FencedQuotaUnpause(ctx context.Context, token, provider, from string) (bool, error) {
	reason := QuotaPauseReason(provider)
	target, text := "", ""
	if raw, err := b.r.Get(ctx, PauseKey(b.project)).Result(); err == nil {
		var p PauseRecord
		if json.Unmarshal([]byte(raw), &p) == nil && p.Reason == reason && ValidName(p.Notify) {
			target, text = p.Notify, resumeText(p, "The provider's limit was lifted before the announced reset.")
		}
	}
	n, err := fencedQuotaUnpause.Run(ctx, b.r, []string{DispatchKey(b.project), PauseKey(b.project), StreamKey(b.project, "cmd")},
		token, reason, target, streamMaxLen,
		"from", from, "target", target, "type", CmdDirective, "ref", "", "command", text, "text_complete", "yes").Int()
	if err != nil {
		return false, err
	}
	if n == -1 {
		return false, ErrNotHolder
	}
	return n == 1, nil
}
