package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Self-repair's intent (docs/superpowers/specs/2026-11-09-self-repair-watcher-design.md,
// 4.1, delivery step 2). The dispatcher's watcher holds the role's swap
// intent ({p}:swap:<role>) while it relaunches a dead agent, so a reload, a
// swap, a retire, another self-repair or an `up` never relaunches next to
// it. Unlike a swap's, the intent is fenced: taken, checked and ended only
// by the dispatch lease holder, and ended only by its own nonce. Its TTL
// (SwapTTL) outlives a lost runner: a dispatcher that takes the lease over
// sees the intent and waits for its expiry.

// SwapKindSelfRepair marks a self-repair's intent and ledger entries.
const SwapKindSelfRepair = "self-repair"

// ErrNotOwnIntent: the role's intent is not this self-repair's (another
// kind, or another nonce); nothing was changed.
var ErrNotOwnIntent = errors.New("the role's intent is not this self-repair's")

// beginSelfRepair: -1 the lease is not ARGV[1]'s; -2 the role has no
// snapshot (a deleted project is never revived); 0 an intent exists; 1 the
// intent set and its begin logged. KEYS: dispatch lease, intent, ledger,
// agents. ARGV: token, role, intent, ttl ms, maxlen, ledger fields...
var beginSelfRepair = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('HEXISTS', KEYS[4], ARGV[2]) == 0 then return -2 end
if redis.call('SET', KEYS[2], ARGV[3], 'NX', 'PX', ARGV[4]) == false then return 0 end
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[5], '*', unpack(ARGV, 6))
return 1`)

// ownSelfRepair (shared by the check and the end): -1 the lease is not
// ARGV[1]'s; 0 no intent; 2 an intent that is not this self-repair's; 1 its
// own, deleted when ARGV[3] is "end". KEYS: dispatch lease, intent. ARGV:
// token, nonce, "end" or "check".
var ownSelfRepair = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if not v then return 0 end
local ok, d = pcall(cjson.decode, v)
if not ok or type(d) ~= 'table' or d['kind'] ~= 'self-repair' or d['nonce'] ~= ARGV[2] then return 2 end
if ARGV[3] == 'end' then redis.call('DEL', KEYS[2]) end
return 1`)

// BeginSelfRepair takes role's intent for a self-repair holding the
// dispatch lease token, with nonce, and logs its begin (ev's details, kind
// self-repair) in the same script. ErrNotHolder without the lease,
// ErrNoSnapshot for a role with no snapshot, ErrSwapInProgress when any
// intent holds the role; nothing is written then.
func (b *Bus) BeginSelfRepair(ctx context.Context, token, role, nonce string, ev SwapEvent) error {
	if !ValidName(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	if token == "" || nonce == "" {
		return fmt.Errorf("self-repair intent: token and nonce required")
	}
	v, err := json.Marshal(SwapIntent{By: ev.By, OldPID: ev.OldPID, Kind: SwapKindSelfRepair, Nonce: nonce})
	if err != nil {
		return err
	}
	ev.Role, ev.Event, ev.Kind = role, SwapBegin, SwapKindSelfRepair
	if err := ev.valid(); err != nil {
		return err
	}
	args := append([]interface{}{token, role, string(v), SwapTTL.Milliseconds(), streamMaxLen}, ev.fields()...)
	n, err := beginSelfRepair.Run(ctx, b.r,
		[]string{DispatchKey(b.project), SwapKey(b.project, role), SwapsKey(b.project), AgentsKey(b.project)}, args...).Int()
	if err != nil {
		return err
	}
	switch n {
	case -1:
		return ErrNotHolder
	case -2:
		return ErrNoSnapshot
	case 0:
		return ErrSwapInProgress
	}
	return nil
}

// SelfRepairHeld reports whether role's intent is still this self-repair's
// (its nonce) and token still holds the dispatch lease: read right before
// the split, else the repair stops with nothing done.
func (b *Bus) SelfRepairHeld(ctx context.Context, token, role, nonce string) (bool, error) {
	n, err := b.ownSelfRepair(ctx, token, role, nonce, "check")
	return n == 1, err
}

// EndSelfRepair ends role's intent, only by the lease holder and only when it
// is this self-repair's (its nonce): ErrNotHolder, ErrNotOwnIntent otherwise,
// nothing changed. An intent already gone (expired) is no error.
func (b *Bus) EndSelfRepair(ctx context.Context, token, role, nonce string) error {
	n, err := b.ownSelfRepair(ctx, token, role, nonce, "end")
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

func (b *Bus) ownSelfRepair(ctx context.Context, token, role, nonce, op string) (int, error) {
	if !ValidName(role) {
		return 0, fmt.Errorf("invalid role %q", role)
	}
	if token == "" || nonce == "" {
		return 0, fmt.Errorf("self-repair intent: token and nonce required")
	}
	return ownSelfRepair.Run(ctx, b.r, []string{DispatchKey(b.project), SwapKey(b.project, role)}, token, nonce, op).Int()
}

// SwapIntentOf is role's intent, if one is held (of any kind: a swap, a
// reload, a retire or a self-repair); an unreadable one counts as a swap.
func (b *Bus) SwapIntentOf(ctx context.Context, role string) (SwapIntent, bool, error) {
	if !ValidName(role) {
		return SwapIntent{}, false, fmt.Errorf("invalid role %q", role)
	}
	v, err := b.r.Get(ctx, SwapKey(b.project, role)).Result()
	if err == redis.Nil {
		return SwapIntent{}, false, nil
	}
	if err != nil {
		return SwapIntent{}, false, err
	}
	var in SwapIntent
	_ = json.Unmarshal([]byte(v), &in)
	return in, true, nil
}

// SwapKindWord names a ledger entry's or an intent's kind as the person
// reads it: "reload", "retire", "self-repair", else "swap".
func SwapKindWord(kind string) string {
	switch kind {
	case SwapKindReload, SwapKindRetire, SwapKindSelfRepair:
		return kind
	}
	return "swap"
}
