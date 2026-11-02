package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

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

// BoardOpenStates are the board states that still owe work (a request not
// answered, accepted, working or blocked); done and superseded do not.
var BoardOpenStates = map[string]bool{"requested": true, "accepted": true, "working": true, "blocked": true}

// ForgetResult is what ForgetIfClear did: Forgot, or Held with what it found.
type ForgetResult struct {
	Forgot bool
	Held   []string
}

// forgetIfClear: KEYS = swap, cmd, asks, board, gate, agents, usage, wake,
// armed, receiver; ARGV = own intent, role. Changes nothing unless the
// stored intent is byte-equal to ARGV[1] and nothing waits for the role;
// then deletes its registration and keeps its consumer group.
var forgetIfClear = redis.NewScript(`
local held = {}
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return {'its retire intent no longer holds'}
end
local role = ARGV[2]
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
  if okj and type(e) == 'table' and e['owner'] == role then
    local s = e['state']
    if s == 'requested' or s == 'accepted' or s == 'working' or s == 'blocked' then
      table.insert(held, 'board task ' .. board[i] .. ' (' .. s .. ')')
    end
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
// step 3): only while own is still its stored intent and nothing waits for
// it (pending or unread waking entries, an open ask, an owned open board
// task, a gate). It deletes what ForgetAgent deletes but the consumer group,
// whose cursor a later spawn reads on from.
func (b *Bus) ForgetIfClear(ctx context.Context, role, own string) (ForgetResult, error) {
	if !ValidName(role) {
		return ForgetResult{}, fmt.Errorf("invalid agent %q", role)
	}
	p := b.project
	keys := []string{SwapKey(p, role), StreamKey(p, "cmd"), AsksKey(p), BoardKey(p), GateKey(p, role),
		AgentsKey(p), UsageKey(p), WakeKey(p, role), ArmedKey(p, role), ReceiverKey(p, role)}
	held, err := forgetIfClear.Run(ctx, b.r, keys, own, role).StringSlice()
	if err != nil {
		return ForgetResult{}, err
	}
	return ForgetResult{Forgot: len(held) == 0, Held: held}, nil
}
