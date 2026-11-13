package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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

// selfRepairListMax bounds a role's failed episodes in {p}:selfrepair
// (newest kept: only the current death's is read).
const selfRepairListMax = 64

// selfRepairDayMs is how long an attempt counts (the per-day budget, 4.5):
// the begin's script drops older ones. No count cap below a day's budget,
// which takes any positive number (reviewer-codex); attemptsHardMax only
// bounds the field far above the most a role can make in a day (one per
// project pause of 2 min: 720).
const (
	selfRepairDayMs = 24 * 60 * 60 * 1000
	attemptsHardMax = 4096
)

// beginSelfRepair: -1 the lease is not ARGV[1]'s; -2 the role has no
// snapshot (a deleted project is never revived); 0 an intent exists; 1 the
// intent set, its begin logged and the attempt counted (Redis TIME, ms, in
// "attempts:<role>" of the self-repair hash; attempts older than ARGV[7] ms
// dropped, at most ARGV[6] kept). KEYS: dispatch lease, intent, ledger,
// agents, self-repair hash. ARGV: token, role, intent, ttl ms, maxlen, hard
// max, window ms, ledger fields...
var beginSelfRepair = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('HEXISTS', KEYS[4], ARGV[2]) == 0 then return -2 end
if redis.call('SET', KEYS[2], ARGV[3], 'NX', 'PX', ARGV[4]) == false then return 0 end
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[5], '*', unpack(ARGV, 8))
local t = redis.call('TIME')
local nowms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local now = string.format('%d', nowms)
local f = 'attempts:' .. ARGV[2]
local keep = {}
local old = redis.call('HGET', KEYS[5], f)
if old then
  for v in string.gmatch(old, '[^,]+') do
    local n = tonumber(v)
    if n and nowms - n < tonumber(ARGV[7]) then table.insert(keep, v) end
  end
end
table.insert(keep, now)
while #keep > tonumber(ARGV[6]) do table.remove(keep, 1) end
redis.call('HSET', KEYS[5], f, table.concat(keep, ','))
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
	args := append([]interface{}{token, role, string(v), SwapTTL.Milliseconds(), streamMaxLen, attemptsHardMax, selfRepairDayMs}, ev.fields()...)
	n, err := beginSelfRepair.Run(ctx, b.r,
		[]string{DispatchKey(b.project), SwapKey(b.project, role), SwapsKey(b.project), AgentsKey(b.project), SelfRepairKey(b.project)}, args...).Int()
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

// fencedLogSwap: -1 the lease is not ARGV[1]'s; 1 the entry appended. KEYS:
// dispatch lease, ledger. ARGV: token, maxlen, fields...
var fencedLogSwap = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[2], '*', unpack(ARGV, 3))
return 1`)

// LogWouldRelaunch records what the self-repair shadow would do (delivery
// step 6): a would-relaunch ledger entry, kind self-repair, for ev's role and
// death episode (the dead process, "pid:start"), written only by the
// dispatch lease holder (ErrNotHolder otherwise). It changes nothing else.
func (b *Bus) LogWouldRelaunch(ctx context.Context, token string, ev SwapEvent) error {
	ev.Event, ev.Kind = SwapWouldRelaunch, SwapKindSelfRepair
	if err := ev.valid(); err != nil {
		return err
	}
	if ev.Episode == "" {
		return errors.New("would-relaunch: no death episode")
	}
	args := append([]interface{}{token, streamMaxLen}, ev.fields()...)
	n, err := fencedLogSwap.Run(ctx, b.r, []string{DispatchKey(b.project), SwapsKey(b.project)}, args...).Int()
	if err != nil {
		return err
	}
	if n == -1 {
		return ErrNotHolder
	}
	return nil
}

// WouldRelaunchLogged reports whether role's death episode already has its
// would-relaunch entry among the ledger's last 1000, so the shadow records
// a death once, not once a pass.
func (b *Bus) WouldRelaunchLogged(ctx context.Context, role, episode string) (bool, error) {
	evs, err := b.SwapLog(ctx, role, 0)
	if err != nil {
		return false, err
	}
	for _, e := range evs {
		if e.Event == SwapWouldRelaunch && e.Episode == episode {
			return true, nil
		}
	}
	return false, nil
}

// SelfRepairKey is the hash of self-repair's per-role state (step 7), the
// budget's own record (the ledger's window is no budget: reviewer-codex):
// "attempts:<role>" its self-repair begins (Redis TIME, ms, comma-separated,
// oldest first), "failed:<role>" the death episodes whose relaunch failed
// its proof, "crashloop:<role>" the Redis TIME (ms) the dispatcher marked a
// crash loop. Written only by the fenced scripts that begin and end a
// self-repair and mark a crash loop; cleared by a reset.
func SelfRepairKey(project string) string { return project + ":selfrepair" }

// finishSelfRepair: -1 the lease is not ARGV[1]'s; 2 the intent is another's
// (nothing written); 1 the entry appended and the intent (its own) deleted,
// or the entry appended with the intent already gone; a failed one's episode
// (ARGV[5], when not empty) kept in "failed:<role>", the newest ARGV[6].
// KEYS: dispatch lease, intent, ledger, self-repair hash. ARGV: token,
// nonce, maxlen, role, failed episode, list max, fields...
var finishSelfRepair = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if v then
  local ok, d = pcall(cjson.decode, v)
  if not ok or type(d) ~= 'table' or d['kind'] ~= 'self-repair' or d['nonce'] ~= ARGV[2] then return 2 end
  redis.call('DEL', KEYS[2])
end
local id = redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[3], '*', unpack(ARGV, 8))
if ARGV[5] ~= '' then
  local f = 'failed:' .. ARGV[4]
  local keep = {}
  local old = redis.call('HGET', KEYS[4], f)
  if old then for e in string.gmatch(old, '[^,]+') do table.insert(keep, e) end end
  table.insert(keep, ARGV[5])
  while #keep > tonumber(ARGV[6]) do table.remove(keep, 1) end
  redis.call('HSET', KEYS[4], f, table.concat(keep, ','))
end
if ARGV[7] ~= '' then redis.call('HSET', KEYS[4], 'owed:' .. ARGV[4] .. ':' .. id, ARGV[7]) end
return 1`)

// FinishSelfRepair ends a self-repair: ev (relaunched, the proof; or failed,
// no proof), kind self-repair, appended to the ledger and the role's intent
// ended, in one script, only by the dispatch lease holder (ErrNotHolder) and
// only while the intent is this self-repair's (ErrNotOwnIntent; nothing
// written). An intent already expired still gets its entry: the relaunch
// happened.
func (b *Bus) FinishSelfRepair(ctx context.Context, token, nonce string, ev SwapEvent) error {
	return b.FinishSelfRepairOwing(ctx, token, nonce, ev, "")
}

// FinishSelfRepairOwing is FinishSelfRepair that also records, in the same
// script, what the self-repair owes master (owed, opaque to the bus) in
// "owed:<role>:<ledger id>" of the self-repair hash: one field per end, its
// own ledger entry's id, so a later end (another death, or the same one
// after a reset) never replaces an earlier one still owed. It stays, of any
// age, until AckSelfRepairOwed (reviewer-codex on 7b).
func (b *Bus) FinishSelfRepairOwing(ctx context.Context, token, nonce string, ev SwapEvent, owed string) error {
	if ev.Event != SwapRelaunched && ev.Event != SwapFailed {
		return fmt.Errorf("a self-repair ends relaunched or failed, not %q", ev.Event)
	}
	ev.Kind = SwapKindSelfRepair
	if err := ev.valid(); err != nil {
		return err
	}
	if token == "" || nonce == "" {
		return fmt.Errorf("self-repair finish: token and nonce required")
	}
	failed := ""
	if ev.Event == SwapFailed {
		if strings.Contains(ev.Episode, ",") {
			return fmt.Errorf("self-repair finish: episode %q", ev.Episode)
		}
		failed = ev.Episode
	}
	args := append([]interface{}{token, nonce, streamMaxLen, ev.Role, failed, selfRepairListMax, owed}, ev.fields()...)
	n, err := finishSelfRepair.Run(ctx, b.r,
		[]string{DispatchKey(b.project), SwapKey(b.project, ev.Role), SwapsKey(b.project), SelfRepairKey(b.project)}, args...).Int()
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

// SelfRepairRecord is what the budget reads of one role (4.5), since its
// last reset: its self-repair attempts (their begins, oldest first), the
// death episodes whose relaunch failed its proof (no retry for those), and a
// crash loop the dispatcher marked.
type SelfRepairRecord struct {
	Attempts    []time.Time
	Failed      map[string]bool
	CrashLoop   bool
	CrashLoopAt int64 // the mark's Redis TIME (ms): the crash loop's episode
}

// SelfRepairPast reads every role's SelfRepairRecord from {p}:selfrepair. A
// role with nothing to tell is absent.
func (b *Bus) SelfRepairPast(ctx context.Context) (map[string]SelfRepairRecord, error) {
	st, err := b.r.HGetAll(ctx, SelfRepairKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]SelfRepairRecord{}
	for f, v := range st {
		kind, role, ok := strings.Cut(f, ":")
		if !ok || !ValidName(role) {
			continue
		}
		r := out[role]
		if r.Failed == nil {
			r.Failed = map[string]bool{}
		}
		switch kind {
		case "crashloop":
			r.CrashLoop = true
			r.CrashLoopAt, _ = strconv.ParseInt(v, 10, 64)
		case "attempts":
			for _, ms := range strings.Split(v, ",") {
				if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n > 0 {
					r.Attempts = append(r.Attempts, time.UnixMilli(n))
				}
			}
			sort.Slice(r.Attempts, func(i, j int) bool { return r.Attempts[i].Before(r.Attempts[j]) })
		case "failed":
			for _, ep := range strings.Split(v, ",") {
				if ep != "" {
					r.Failed[ep] = true
				}
			}
		default:
			continue
		}
		out[role] = r
	}
	return out, nil
}

// markCrashLoop: -1 the lease is not ARGV[1]'s; 0 already marked; 1 marked
// now (Redis TIME). KEYS: dispatch lease, self-repair hash. ARGV: token, field.
var markCrashLoop = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local t = redis.call('TIME')
return redis.call('HSETNX', KEYS[2], ARGV[2], tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))`)

// MarkCrashLoop marks role's crash loop (a spent day, 4.5), only by the
// dispatch lease holder; fresh is true the first time, so the escalation is
// sent once. The mark holds until ResetSelfRepair.
func (b *Bus) MarkCrashLoop(ctx context.Context, token, role string) (fresh bool, err error) {
	if !ValidName(role) {
		return false, fmt.Errorf("invalid role %q", role)
	}
	n, err := markCrashLoop.Run(ctx, b.r, []string{DispatchKey(b.project), SelfRepairKey(b.project)}, token, "crashloop:"+role).Int()
	if err != nil {
		return false, err
	}
	if n == -1 {
		return false, ErrNotHolder
	}
	return n == 1, nil
}

// resetSelfRepair: the role's crash loop, attempts and failed episodes
// removed. Returns 1 when a crash loop was marked. KEYS: self-repair hash.
// ARGV: role.
var resetSelfRepair = redis.NewScript(`
redis.call('HDEL', KEYS[1], 'attempts:' .. ARGV[1], 'failed:' .. ARGV[1])
return redis.call('HDEL', KEYS[1], 'crashloop:' .. ARGV[1])`)

// ResetSelfRepair is `agentbus self-repair reset <agent>` (4.5): the person
// or master clears role's crash loop and restarts its budget, the failed
// proofs included. wasLoop: a crash loop was marked. Not the dispatcher's
// write: the CLI's, as a pause is.
func (b *Bus) ResetSelfRepair(ctx context.Context, role string) (wasLoop bool, err error) {
	if !ValidName(role) {
		return false, fmt.Errorf("invalid role %q", role)
	}
	n, err := resetSelfRepair.Run(ctx, b.r, []string{SelfRepairKey(b.project)}, role).Int()
	return n == 1, err
}

// Owed is one message a self-repair owes master: its role, the ledger id of
// the end it tells (unique, ordered: what a send dedups on), the field it
// waits in, and its value (opaque to the bus).
type Owed struct {
	Role, ID, Field, Value string
}

// SelfRepairOwed is every unacknowledged message, oldest end first.
func (b *Bus) SelfRepairOwed(ctx context.Context) ([]Owed, error) {
	all, err := b.r.HGetAll(ctx, SelfRepairKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []Owed
	for f, v := range all {
		rest, ok := strings.CutPrefix(f, "owed:")
		if !ok {
			continue
		}
		role, id, ok := strings.Cut(rest, ":")
		if ok && ValidName(role) && id != "" {
			out = append(out, Owed{Role: role, ID: id, Field: f, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

// ackOwed: -1 the lease is not ARGV[1]'s; else the owed field removed only
// when it still holds ARGV[2]. KEYS: dispatch lease, self-repair hash.
// ARGV: token, value, field.
var ackOwed = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('HGET', KEYS[2], ARGV[3]) == ARGV[2] then redis.call('HDEL', KEYS[2], ARGV[3]) end
return 1`)

// AckSelfRepairOwed acknowledges one death's message once written, only by
// the dispatch lease holder and only while it is still o.Value.
func (b *Bus) AckSelfRepairOwed(ctx context.Context, token string, o Owed) error {
	if !strings.HasPrefix(o.Field, "owed:") {
		return fmt.Errorf("invalid owed field %q", o.Field)
	}
	n, err := ackOwed.Run(ctx, b.r, []string{DispatchKey(b.project), SelfRepairKey(b.project)}, token, o.Value, o.Field).Int()
	if err != nil {
		return err
	}
	if n == -1 {
		return ErrNotHolder
	}
	return nil
}
