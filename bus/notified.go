package bus

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// {p}:notified (notifications spec, "Where it runs"): what this host's
// notifier sent, per target. Fields <kind>|<episode>|<target> (Redis TIME in
// ms of the send, or "baseline") and _target|<target> (the target's baseline
// taken, Redis TIME). Every write is fenced on the dispatch lease. It holds
// kind, episode and time only: never a message, a URL or a token. Runtime
// (bus/keykinds.go), not in projectKinds, ignored by HasKeysBesidesDispatch.

// NotifiedKey is {p}:notified.
func NotifiedKey(project string) string { return project + ":notified" }

// Notified reads {p}:notified.
func (b *Bus) Notified(ctx context.Context) (map[string]string, error) {
	return b.r.HGetAll(ctx, NotifiedKey(b.project)).Result()
}

// notifiedSet: 0 not the holder, else 1. ARGV[1] token, ARGV[2] the value
// ("" = Redis TIME in ms), then fields.
var notifiedSet = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local v = ARGV[2]
if v == '' then
  local t = redis.call('TIME')
  v = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
end
for i = 3, #ARGV do redis.call('HSET', KEYS[2], ARGV[i], v) end
return 1`)

// notifiedClear: 0 not the holder, else 1.
var notifiedClear = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
return 1`)

// FencedNotifiedSent records fields as sent now (one target's success).
func (b *Bus) FencedNotifiedSent(ctx context.Context, token string, fields ...string) error {
	return b.fencedHash(ctx, notifiedSet, token, NotifiedKey(b.project), append([]string{""}, fields...)...)
}

// notifiedBaseline: 0 not the holder, else 1. ARGV[1] token, ARGV[2] the
// target's _target|<target> field (Redis TIME), then the fields recorded as
// "baseline": one script, so a baseline is never half taken (foureyes).
var notifiedBaseline = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
for i = 3, #ARGV do redis.call('HSET', KEYS[2], ARGV[i], 'baseline') end
local t = redis.call('TIME')
redis.call('HSET', KEYS[2], ARGV[2], tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)))
return 1`)

// FencedNotifiedBaseline takes target's baseline: fields recorded as
// "baseline" and _target|<target> set, in one script.
func (b *Bus) FencedNotifiedBaseline(ctx context.Context, token, target string, fields ...string) error {
	return b.fencedHash(ctx, notifiedBaseline, token, NotifiedKey(b.project), append([]string{"_target|" + target}, fields...)...)
}

// FencedNotifiedDel deletes fields (a fact or a target gone).
func (b *Bus) FencedNotifiedDel(ctx context.Context, token string, fields ...string) error {
	return b.fencedHash(ctx, quotaHDel, token, NotifiedKey(b.project), fields...)
}

// FencedNotifiedClear deletes the whole record (notify.on turned false).
func (b *Bus) FencedNotifiedClear(ctx context.Context, token string) error {
	n, err := notifiedClear.Run(ctx, b.r, []string{DispatchKey(b.project), NotifiedKey(b.project)}, token).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}
