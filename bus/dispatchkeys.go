package bus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// The dispatcher lease ({p}:dispatch) and its per-agent wake records
// ({p}:wake:{agent}). Every dispatcher write goes through a Lua script that
// first checks GET {p}:dispatch == token, so a dispatcher that lost its lease —
// or whose project was deleted — writes nothing and recreates no key (spec §3.3,
// §3.4, §5.4).

// ErrNotHolder means the caller's token does not hold the project's dispatch
// lease; the write was refused and nothing was changed.
var ErrNotHolder = errors.New("dispatch lease not held")

// DispatchKey is the per-project dispatcher lease (a string holding the token).
func DispatchKey(project string) string { return project + ":dispatch" }

// WakeKey is one agent's wake record (a hash). Two colons, so the project
// scanner can never attribute it to a phantom project.
func WakeKey(project, agent string) string { return project + ":wake:" + agent }

// WakeRecord is the dispatcher's durable per-agent wake state.
type WakeRecord struct {
	State         string // prompting | prompted | unobserved | deferred
	At            int64  // server ms of last reservation; 0 = never prompted
	Tries         int
	HighID        string
	PID           int
	PIDStart      uint64
	Escalated     string // "" | "pending" | "true"
	LastDelivered string // progress marks at last reservation
	PendingTotal  int64
	GapNotified   bool
	Reason        string
}

// fields flattens the record into hash field/value pairs. withAt is false for
// ReserveWake, whose script stamps "at" from the server clock.
func (w WakeRecord) fields(withAt bool) []interface{} {
	out := []interface{}{
		"state", w.State,
		"tries", strconv.Itoa(w.Tries),
		"high_id", w.HighID,
		"pid", strconv.Itoa(w.PID),
		"pid_start", strconv.FormatUint(w.PIDStart, 10),
		"escalated", w.Escalated,
		"last_delivered", w.LastDelivered,
		"pending_total", strconv.FormatInt(w.PendingTotal, 10),
		"gap_notified", strconv.FormatBool(w.GapNotified),
		"reason", w.Reason,
	}
	if withAt {
		out = append(out, "at", strconv.FormatInt(w.At, 10))
	}
	return out
}

func wakeFromHash(h map[string]string) WakeRecord {
	at, _ := strconv.ParseInt(h["at"], 10, 64)
	tries, _ := strconv.Atoi(h["tries"])
	pid, _ := strconv.Atoi(h["pid"])
	pidStart, _ := strconv.ParseUint(h["pid_start"], 10, 64)
	pending, _ := strconv.ParseInt(h["pending_total"], 10, 64)
	gap, _ := strconv.ParseBool(h["gap_notified"])
	return WakeRecord{
		State: h["state"], At: at, Tries: tries, HighID: h["high_id"], PID: pid, PIDStart: pidStart,
		Escalated: h["escalated"], LastDelivered: h["last_delivered"], PendingTotal: pending,
		GapNotified: gap, Reason: h["reason"],
	}
}

// flatHash converts a Lua HGETALL reply (flat field/value array) to a map.
func flatHash(v interface{}) map[string]string {
	arr, _ := v.([]interface{})
	h := make(map[string]string, len(arr)/2)
	for i := 0; i+1 < len(arr); i += 2 {
		k, _ := arr[i].(string)
		val, _ := arr[i+1].(string)
		h[k] = val
	}
	return h
}

var renewDispatch = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('PEXPIRE', KEYS[1], ARGV[2])`)

var releaseDispatch = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)

// reserveWake: {-1} not holder; {0, hash} not reserved (low PTTL or inside the
// retry window); {1, hash} reserved. "at" is stamped from TIME.
var reserveWake = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return {-1} end
if redis.call('PTTL', KEYS[1]) < tonumber(ARGV[2]) then return {0, redis.call('HGETALL', KEYS[2])} end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local at = tonumber(redis.call('HGET', KEYS[2], 'at') or '0') or 0
if at ~= 0 and now - at < tonumber(ARGV[3]) then return {0, redis.call('HGETALL', KEYS[2])} end
redis.call('DEL', KEYS[2])
redis.call('HSET', KEYS[2], 'at', tostring(now), unpack(ARGV, 4))
return {1, redis.call('HGETALL', KEYS[2])}`)

var putWake = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
redis.call('HSET', KEYS[2], unpack(ARGV, 2))
return 1`)

var deleteWake = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
return 1`)

// fencedAdd: 0 when not holder, else the new entry id. ARGV[2] is the MAXLEN.
var fencedAdd = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[2], '*', unpack(ARGV, 3))`)

// AcquireDispatch takes the project's dispatch lease (SET NX PX). false means
// another dispatcher holds it.
func (b *Bus) AcquireDispatch(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("empty dispatch token")
	}
	return b.r.SetNX(ctx, DispatchKey(b.project), token, ttl).Result()
}

// RenewDispatch extends the lease only if token still holds it; false = lost.
func (b *Bus) RenewDispatch(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	n, err := renewDispatch.Run(ctx, b.r, []string{DispatchKey(b.project)}, token, ttl.Milliseconds()).Int()
	return n == 1, err
}

// ReleaseDispatch deletes the lease only if token holds it; otherwise a no-op.
func (b *Bus) ReleaseDispatch(ctx context.Context, token string) error {
	return releaseDispatch.Run(ctx, b.r, []string{DispatchKey(b.project)}, token).Err()
}

// DispatchAlive reports whether any dispatcher holds the project's lease.
func (b *Bus) DispatchAlive(ctx context.Context) (bool, error) {
	n, err := b.r.Exists(ctx, DispatchKey(b.project)).Result()
	return n > 0, err
}

// HoldsDispatch is a plain read: does token currently hold the lease?
func (b *Bus) HoldsDispatch(ctx context.Context, token string) (bool, error) {
	v, err := b.r.Get(ctx, DispatchKey(b.project)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == token, nil
}

// Wake reads an agent's wake record; exists is false when there is none.
func (b *Bus) Wake(ctx context.Context, agent string) (WakeRecord, bool, error) {
	if !ValidName(agent) {
		return WakeRecord{}, false, fmt.Errorf("invalid agent %q", agent)
	}
	h, err := b.r.HGetAll(ctx, WakeKey(b.project, agent)).Result()
	if err != nil || len(h) == 0 {
		return WakeRecord{}, false, err
	}
	return wakeFromHash(h), true, nil
}

// ReserveWake is the fenced reservation of spec §3.3 step 1. It writes next
// (with At stamped from the server's TIME; next.At is ignored) only when token
// holds the lease with at least minPTTL left and the stored record is outside
// the retry window (At == 0 or now-At >= retry). It returns the stored record
// and whether it reserved. Not holding the lease is ErrNotHolder; a low PTTL or
// a closed window is (current record, false, nil).
func (b *Bus) ReserveWake(ctx context.Context, agent, token string, next WakeRecord, retry, minPTTL time.Duration) (WakeRecord, bool, error) {
	if !ValidName(agent) {
		return WakeRecord{}, false, fmt.Errorf("invalid agent %q", agent)
	}
	args := append([]interface{}{token, minPTTL.Milliseconds(), retry.Milliseconds()}, next.fields(false)...)
	res, err := reserveWake.Run(ctx, b.r, []string{DispatchKey(b.project), WakeKey(b.project, agent)}, args...).Slice()
	if err != nil {
		return WakeRecord{}, false, err
	}
	code, _ := res[0].(int64)
	if code < 0 {
		return WakeRecord{}, false, ErrNotHolder
	}
	var rec WakeRecord
	if len(res) > 1 {
		rec = wakeFromHash(flatHash(res[1]))
	}
	return rec, code == 1, nil
}

// PutWake replaces an agent's wake record, fenced by the lease.
func (b *Bus) PutWake(ctx context.Context, agent, token string, rec WakeRecord) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	args := append([]interface{}{token}, rec.fields(true)...)
	n, err := putWake.Run(ctx, b.r, []string{DispatchKey(b.project), WakeKey(b.project, agent)}, args...).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

// DeleteWake removes an agent's wake record, fenced by the lease.
func (b *Bus) DeleteWake(ctx context.Context, agent, token string) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	n, err := deleteWake.Run(ctx, b.r, []string{DispatchKey(b.project), WakeKey(b.project, agent)}, token).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

func (b *Bus) fencedAdd(ctx context.Context, token, kind string, fields ...interface{}) (string, error) {
	args := append([]interface{}{token, streamMaxLen}, fields...)
	v, err := fencedAdd.Run(ctx, b.r, []string{DispatchKey(b.project), StreamKey(b.project, kind)}, args...).Result()
	if err != nil {
		return "", err
	}
	id, ok := v.(string)
	if !ok {
		return "", ErrNotHolder
	}
	return id, nil
}

// FencedCmd is Bus.Cmd (empty ref, same fields and validation) written only
// while token holds the dispatch lease.
func (b *Bus) FencedCmd(ctx context.Context, token, from, target, typ, command string) (string, error) {
	if err := ValidateText(command, CmdMaxRunes); err != nil {
		return "", err
	}
	if !ValidName(target) {
		return "", fmt.Errorf("invalid target %q", target)
	}
	if !validCmdTypes[typ] {
		return "", fmt.Errorf("invalid cmd type %q", typ)
	}
	return b.fencedAdd(ctx, token, "cmd",
		"from", from, "target", target, "type", typ, "ref", "", "command", command, "text_complete", "yes")
}

// FencedNotify is Bus.Notify written only while token holds the dispatch lease.
// Unlike Bus.Notify it bounds the message (UTF-8, CmdMaxRunes) before the script.
func (b *Bus) FencedNotify(ctx context.Context, token, from, message string) (string, error) {
	if err := ValidateText(message, CmdMaxRunes); err != nil {
		return "", err
	}
	return b.fencedAdd(ctx, token, "notify", "from", from, "message", message)
}

// fencedGroup: -1 not holder; 0 group already there; 1 created at the floor
// (server TIME minus ARGV[3] ms, never below 0), MKSTREAM.
var fencedGroup = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local t = redis.call('TIME')
local floor = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000) - tonumber(ARGV[3])
if floor < 0 then floor = 0 end
local r = redis.pcall('XGROUP', 'CREATE', KEYS[2], ARGV[2], string.format('%d', floor) .. '-0', 'MKSTREAM')
if type(r) == 'table' and r.err then
  if string.find(r.err, 'BUSYGROUP') then return 0 end
  return redis.error_reply(r.err)
end
return 1`)

// FencedEnsureGroup is EnsureGroup written only while token holds the dispatch
// lease: XGROUP CREATE … MKSTREAM would otherwise recreate the cmd stream of a
// project deleted under a running dispatcher. An existing group is untouched.
func (b *Bus) FencedEnsureGroup(ctx context.Context, token, agent string, horizon time.Duration) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	n, err := fencedGroup.Run(ctx, b.r, []string{DispatchKey(b.project), StreamKey(b.project, "cmd")},
		token, agent, horizon.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if n < 0 {
		return ErrNotHolder
	}
	return nil
}
