package bus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// The dispatcher's own end of a done pop (pop-retire-auto, decision
// 2026-11-13-dispatcher-ends-done-pop): the same steps as `retire --forget`,
// each write fenced by the dispatch lease. Its intent is a retire's (kind
// retire, a nonce), so ForgetIfClear and every other relauncher read it as
// one; it is taken, checked and ended only by the lease holder.

// beginAutoRetire: -1 the lease is not ARGV[1]'s; -2 the role has no
// snapshot; 0 an intent exists; 1 the intent set and its begin logged.
// KEYS: dispatch lease, intent, ledger, agents. ARGV: token, role, intent,
// ttl ms, maxlen, ledger fields...
var beginAutoRetire = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('HEXISTS', KEYS[4], ARGV[2]) == 0 then return -2 end
if redis.call('SET', KEYS[2], ARGV[3], 'NX', 'PX', ARGV[4]) == false then return 0 end
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[5], '*', unpack(ARGV, 6))
return 1`)

// ownAutoRetire: -1 the lease is not ARGV[1]'s; 0 no intent; 2 an intent
// that is not this retire's (another kind or nonce); 1 its own, deleted when
// ARGV[3] is "end". KEYS: dispatch lease, intent.
var ownAutoRetire = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if not v then return 0 end
local ok, d = pcall(cjson.decode, v)
if not ok or type(d) ~= 'table' or d['kind'] ~= 'retire' or d['nonce'] ~= ARGV[2] then return 2 end
if ARGV[3] == 'end' then redis.call('DEL', KEYS[2]) end
return 1`)

// fencedRetireSwap: RetireSwap's entry and the fallback record cleared, by
// the lease holder (-1 otherwise). KEYS: dispatch lease, ledger, fallback.
// ARGV: token, role, maxlen, fields...
var fencedRetireSwap = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
redis.call('HDEL', KEYS[3], ARGV[2])
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[3], '*', unpack(ARGV, 4))
return 1`)

// BeginAutoRetire takes role's retire intent for the lease holder token,
// with nonce, the old process (ev.OldPID, oldStart) and its begin logged
// (ev's details, kind retire), in one script. It returns the intent as
// stored: ForgetIfClearFenced compares those bytes. ErrNotHolder without the
// lease, ErrNoSnapshot for a role with no snapshot, ErrSwapInProgress when
// any intent holds the role; nothing is written then.
func (b *Bus) BeginAutoRetire(ctx context.Context, token, role, nonce string, oldStart uint64, ev SwapEvent) (string, error) {
	if !ValidName(role) {
		return "", fmt.Errorf("invalid role %q", role)
	}
	if token == "" || nonce == "" {
		return "", fmt.Errorf("retire intent: token and nonce required")
	}
	v, err := json.Marshal(SwapIntent{By: ev.By, OldPID: ev.OldPID, OldStart: oldStart, Kind: SwapKindRetire, Nonce: nonce})
	if err != nil {
		return "", err
	}
	ev.Role, ev.Event, ev.Kind = role, SwapBegin, SwapKindRetire
	if err := ev.valid(); err != nil {
		return "", err
	}
	args := append([]interface{}{token, role, string(v), SwapTTL.Milliseconds(), streamMaxLen}, ev.fields()...)
	n, err := beginAutoRetire.Run(ctx, b.r,
		[]string{DispatchKey(b.project), SwapKey(b.project, role), SwapsKey(b.project), AgentsKey(b.project)}, args...).Int()
	if err != nil {
		return "", err
	}
	switch n {
	case -1:
		return "", ErrNotHolder
	case -2:
		return "", ErrNoSnapshot
	case 0:
		return "", ErrSwapInProgress
	}
	return string(v), nil
}

// AutoRetireHeld: role's intent is still this retire's (kind retire, its
// nonce) and token still holds the lease.
func (b *Bus) AutoRetireHeld(ctx context.Context, token, role, nonce string) (bool, error) {
	n, err := b.ownAutoRetire(ctx, token, role, nonce, "check")
	return n == 1, err
}

// EndAutoRetire ends role's intent, only by the lease holder and only when
// it is this retire's: ErrNotHolder, ErrNotOwnIntent otherwise, nothing
// changed. An intent already gone is no error.
func (b *Bus) EndAutoRetire(ctx context.Context, token, role, nonce string) error {
	n, err := b.ownAutoRetire(ctx, token, role, nonce, "end")
	switch {
	case err != nil:
		return err
	case n == -1:
		return ErrNotHolder
	case n == 2:
		return ErrNotOwnIntent
	}
	return nil
}

func (b *Bus) ownAutoRetire(ctx context.Context, token, role, nonce, op string) (int, error) {
	if !ValidName(role) {
		return 0, fmt.Errorf("invalid role %q", role)
	}
	if token == "" || nonce == "" {
		return 0, fmt.Errorf("retire intent: token and nonce required")
	}
	return ownAutoRetire.Run(ctx, b.r, []string{DispatchKey(b.project), SwapKey(b.project, role)}, token, nonce, op).Int()
}

// FencedLogSwap appends one ledger entry for the lease holder (ErrNotHolder
// otherwise), through self-repair's fencedLogSwap.
func (b *Bus) FencedLogSwap(ctx context.Context, token string, e SwapEvent) error {
	if err := e.valid(); err != nil {
		return err
	}
	args := append([]interface{}{token, streamMaxLen}, e.fields()...)
	n, err := fencedLogSwap.Run(ctx, b.r, []string{DispatchKey(b.project), SwapsKey(b.project)}, args...).Int()
	if err == nil && n == -1 {
		return ErrNotHolder
	}
	return err
}

// FencedRetireSwap is RetireSwap for the lease holder: the retire entry and
// the role's fallback record cleared, in one script.
func (b *Bus) FencedRetireSwap(ctx context.Context, token string, e SwapEvent) error {
	e.Event = SwapRetire
	if err := e.valid(); err != nil {
		return err
	}
	args := append([]interface{}{token, e.Role, streamMaxLen}, e.fields()...)
	n, err := fencedRetireSwap.Run(ctx, b.r, []string{DispatchKey(b.project), SwapsKey(b.project), FallbackKey(b.project)}, args...).Int()
	if err == nil && n == -1 {
		return ErrNotHolder
	}
	return err
}
