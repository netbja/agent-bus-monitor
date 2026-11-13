package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// SwapKindRetire marks the intent `retire --forget` holds (pop-auto-retire
// §4): a swap to the dispatcher, recognised exactly by its Nonce.
const SwapKindRetire = "retire"

// NewIntentNonce is 16 random bytes in hex, drawn once per retire run.
func NewIntentNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Encode is the intent as BeginSwap stores it: the bytes retire --forget
// keeps as its own intent and compares the stored one with.
func (in SwapIntent) Encode() string {
	v, _ := json.Marshal(in)
	return string(v)
}

// SwapIntentRaw is role's stored intent, exactly as stored ("" = none).
func (b *Bus) SwapIntentRaw(ctx context.Context, role string) (string, error) {
	v, err := b.r.Get(ctx, SwapKey(b.project, role)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

// PopDoneMark is the {p}:quota mark of the dispatcher's pop-end directive
// (pop-auto-retire §3); its value is the episode it was sent for, which
// busmon reads to flag the pop done.
func PopDoneMark(agent string) string { return "popdone:" + agent }

// BoardOwes reports whether a board task in state still owes work: any
// state but done (the board's states are free words, only done is special;
// foureyes on pop-auto-retire-code), so an unknown word fails closed.
func BoardOwes(state string) bool { return state != "done" }

// RetiredAs is the registration retire --forget judged and closed: the old
// process and the episode it was told. ForgetIfClear deletes only that one.
type RetiredAs struct {
	PID      int
	PIDStart uint64
	Episode  int64 // the snapshot's declaration time (dispatch.PopEpisode)
}

// ForgetResult is what ForgetIfClear did: Forgot, or Held with what it found.
type ForgetResult struct {
	Forgot bool
	Held   []string
}

// forgetIfClear: KEYS = swap, cmd, asks, board, gate, agents, usage, wake,
// armed, receiver; ARGV = own intent, role, pid, pid_start, episode. Changes
// nothing unless the stored intent is byte-equal to ARGV[1], the stored
// registration is still that process in that episode, and nothing waits for
// the role; then deletes its registration and keeps its consumer group.
var forgetIfClear = redis.NewScript(`
local held = {}
if ARGV[6] ~= '' and redis.call('GET', KEYS[11]) ~= ARGV[6] then
  return {'\0not the dispatch lease holder'}
end
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return {'its retire intent no longer holds'}
end
local role = ARGV[2]
local raw = redis.call('HGET', KEYS[6], role)
if not raw then return {'no registration'} end
local okj, snap = pcall(cjson.decode, raw)
if not okj or type(snap) ~= 'table' then return {'an unreadable registration'} end
if tostring(snap['pid']) ~= ARGV[3] or tostring(snap['pid_start']) ~= ARGV[4] then
  return {'another process is registered (a respawn): kept'}
end
if tostring(snap['ts']) ~= ARGV[5] then
  return {'another episode (declared again since): kept'}
end
local ok, groups = pcall(redis.call, 'XINFO', 'GROUPS', KEYS[2])
local last = nil
if ok then
  for _, g in ipairs(groups) do
    local info = {}
    for i = 1, #g, 2 do info[g[i]] = g[i + 1] end
    if info['name'] == role then
      last = info['last-delivered-id']
      if tonumber(info['pending']) > 0 then
        table.insert(held, info['pending'] .. ' pending (delivered, not acknowledged)')
      end
    end
  end
end
if not last then
  table.insert(held, 'no consumer group')
else
  local unread = {}
  for _, e in ipairs(redis.call('XRANGE', KEYS[2], '(' .. last, '+')) do
    local f = {}
    for i = 1, #e[2], 2 do f[e[2][i]] = e[2][i + 1] end
    if f['target'] == role and f['wake'] ~= 'no' then table.insert(unread, e[1]) end
  end
  if #unread > 0 then table.insert(held, 'unread ' .. table.concat(unread, ' ')) end
end
if redis.call('HEXISTS', KEYS[3], 'open:' .. role) == 1 then
  table.insert(held, 'an open ask')
end
local board = redis.call('HGETALL', KEYS[4])
for i = 1, #board, 2 do
  local okj, e = pcall(cjson.decode, board[i + 1])
  if okj and type(e) == 'table' and e['owner'] == role and e['state'] ~= 'done' then
    table.insert(held, 'board task ' .. board[i] .. ' (' .. tostring(e['state']) .. ')')
  end
end
if redis.call('HLEN', KEYS[5]) > 0 then
  table.insert(held, 'a review gate holds it')
end
if #held > 0 then return held end
redis.call('HDEL', KEYS[6], role)
redis.call('HDEL', KEYS[7], role)
redis.call('DEL', KEYS[8], KEYS[9], KEYS[10])
return {}`)

// ForgetIfClear unregisters a retired pop in one script (pop-auto-retire §4
// step 3): only while own is still its stored intent, the registration is
// still the one retired (was: a respawn's is kept whole, reviewer-codex), and
// nothing waits for it (pending or unread waking entries with no floor, an
// open ask, an owned board task not done, a gate). It deletes what
// ForgetAgent deletes but the consumer group, whose cursor a later spawn
// reads on from.
func (b *Bus) ForgetIfClear(ctx context.Context, role, own string, was RetiredAs) (ForgetResult, error) {
	return b.forgetIfClear(ctx, "", role, own, was)
}

// ForgetIfClearFenced is ForgetIfClear for the dispatcher (pop-retire-auto):
// the same script, refused (ErrNotHolder, nothing changed) unless token
// holds the dispatch lease.
func (b *Bus) ForgetIfClearFenced(ctx context.Context, token, role, own string, was RetiredAs) (ForgetResult, error) {
	if token == "" {
		return ForgetResult{}, fmt.Errorf("fenced forget: token required")
	}
	return b.forgetIfClear(ctx, token, role, own, was)
}

func (b *Bus) forgetIfClear(ctx context.Context, token, role, own string, was RetiredAs) (ForgetResult, error) {
	if !ValidName(role) {
		return ForgetResult{}, fmt.Errorf("invalid agent %q", role)
	}
	p := b.project
	keys := []string{SwapKey(p, role), StreamKey(p, "cmd"), AsksKey(p), BoardKey(p), GateKey(p, role),
		AgentsKey(p), UsageKey(p), WakeKey(p, role), ArmedKey(p, role), ReceiverKey(p, role), DispatchKey(p)}
	held, err := forgetIfClear.Run(ctx, b.r, keys, own, role,
		strconv.Itoa(was.PID), strconv.FormatUint(was.PIDStart, 10), strconv.FormatInt(was.Episode, 10), token).StringSlice()
	if err != nil {
		return ForgetResult{}, err
	}
	if len(held) == 1 && held[0] == "\x00not the dispatch lease holder" {
		return ForgetResult{}, ErrNotHolder
	}
	return ForgetResult{Forgot: len(held) == 0, Held: held}, nil
}
