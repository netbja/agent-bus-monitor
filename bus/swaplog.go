package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The swap ledger and the fallback state (v0.15.1, spec
// 2026-10-17-swaps-history-and-model-drift-design.md §3, §11.2). Written by
// `agentbus swap` and `agentbus retire` themselves, never by the dispatcher.

// SwapsKey is the append-only swap ledger (a stream, MAXLEN ~ like the others).
func SwapsKey(project string) string { return project + ":swaps" }

// FallbackKey is the hash role → FallbackRecord of roles running on a
// profile other than their own.
func FallbackKey(project string) string { return project + ":fallback" }

// Swap ledger events.
const (
	SwapBegin   = "begin"
	SwapDone    = "done"
	SwapFailed  = "failed"
	SwapWarning = "warning"
	SwapBack    = "back"
	SwapRetire  = "retire"
	// SwapReloaded ends a reload (lifecycle part C): the new process proved;
	// unlike done and back, it never touches the fallback state.
	SwapReloaded = "reloaded"
)

// SwapEvent is one ledger entry. Empty fields are not written.
type SwapEvent struct {
	ID           string `json:"id,omitempty"` // stream id, on read
	Role         string `json:"role"`
	Event        string `json:"event"`
	FromProfile  string `json:"from_profile,omitempty"`
	ToProfile    string `json:"to_profile,omitempty"`
	FromProvider string `json:"from_provider,omitempty"`
	ToProvider   string `json:"to_provider,omitempty"`
	ToModel      string `json:"to_model,omitempty"`
	By           string `json:"by,omitempty"`
	Reason       string `json:"reason,omitempty"`
	OldPID       int    `json:"old_pid,omitempty"`
	OldPane      string `json:"old_pane,omitempty"`
	NewPID       int    `json:"new_pid,omitempty"`
	NewPane      string `json:"new_pane,omitempty"`
	Episode      string `json:"episode,omitempty"`
	Error        string `json:"error,omitempty"`
	Kind         string `json:"kind,omitempty"` // "" a swap, SwapKindReload a reload
}

// SwapReasonMax bounds a ledger reason or error.
const SwapReasonMax = 200

func (e SwapEvent) fields() []interface{} {
	out := []interface{}{"role", e.Role, "event", e.Event}
	add := func(k, v string) {
		if v != "" {
			out = append(out, k, v)
		}
	}
	addInt := func(k string, v int) {
		if v != 0 {
			out = append(out, k, strconv.Itoa(v))
		}
	}
	add("from_profile", e.FromProfile)
	add("to_profile", e.ToProfile)
	add("from_provider", e.FromProvider)
	add("to_provider", e.ToProvider)
	add("to_model", e.ToModel)
	add("by", e.By)
	add("reason", clip(e.Reason, SwapReasonMax))
	addInt("old_pid", e.OldPID)
	add("old_pane", e.OldPane)
	addInt("new_pid", e.NewPID)
	add("new_pane", e.NewPane)
	add("episode", e.Episode)
	add("error", clip(e.Error, SwapReasonMax))
	add("kind", e.Kind)
	return out
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (e SwapEvent) valid() error {
	if !ValidName(e.Role) {
		return fmt.Errorf("invalid role %q", e.Role)
	}
	switch e.Event {
	case SwapBegin, SwapDone, SwapFailed, SwapWarning, SwapBack, SwapRetire, SwapReloaded:
		return nil
	}
	return fmt.Errorf("invalid swap event %q", e.Event)
}

// FallbackRecord is a role running on another profile than its own.
type FallbackRecord struct {
	OwnProfile  string `json:"own_profile"`
	OwnProvider string `json:"own_provider"`
	Profile     string `json:"profile"`
	Provider    string `json:"provider"`
	Since       int64  `json:"since"` // Unix ms, Redis TIME
	By          string `json:"by,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// HeldEpisode is hold:<own_provider> at the swap's begin: the episode
	// whose release earns master the swap-back notice; empty if not held.
	HeldEpisode string `json:"held_episode,omitempty"`
}

// beginSwap: 0 an intent exists (nothing written); 1 intent set and begin
// logged, in one script; -1 (a reload, given the services key and the
// installed field as KEYS[3] and ARGV[4]) the project is not installed.
var beginSwap = redis.NewScript(`
local first = 4
if #KEYS > 2 then
  if redis.call('HEXISTS', KEYS[3], ARGV[4]) == 0 then return -1 end
  first = 5
end
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', ARGV[2]) == false then return 0 end
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[3], '*', unpack(ARGV, first))
return 1`)

// BeginSwap writes the intent (SET NX PX) and the ledger's begin entry
// together; a second swap of the role is refused with nothing written. ev
// (optional) carries the begin entry's details.
func (b *Bus) BeginSwap(ctx context.Context, role string, in SwapIntent, ev ...SwapEvent) error {
	if !ValidName(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	v, err := json.Marshal(in)
	if err != nil {
		return err
	}
	e := SwapEvent{ToProfile: in.ToProfile, By: in.By, OldPID: in.OldPID}
	if len(ev) > 0 {
		e = ev[0]
	}
	e.Role, e.Event = role, SwapBegin
	keys := []string{SwapKey(b.project, role), SwapsKey(b.project)}
	args := []interface{}{string(v), SwapTTL.Milliseconds(), streamMaxLen}
	if in.Kind == SwapKindReload {
		e.Kind = SwapKindReload
		keys = append(keys, ServicesKey(b.project))
		args = append(args, ServicesInstalledField)
	}
	args = append(args, e.fields()...)
	n, err := beginSwap.Run(ctx, b.r, keys, args...).Int()
	if err != nil {
		return err
	}
	switch n {
	case 0:
		return ErrSwapInProgress
	case -1:
		return ErrNotInstalled
	}
	return nil
}

// LogSwap appends one ledger entry (failed, warning, retire, ...).
func (b *Bus) LogSwap(ctx context.Context, e SwapEvent) error {
	if err := e.valid(); err != nil {
		return err
	}
	args := append([]interface{}{SwapsKey(b.project), "MAXLEN", "~", streamMaxLen, "*"}, e.fields()...)
	return b.r.Do(ctx, append([]interface{}{"XADD"}, args...)...).Err()
}

// swapDone: the done (or back) entry and the fallback set (ARGV[2] != ”)
// or cleared, in one script. KEYS: ledger, fallback. ARGV: role, record
// JSON or ”, maxlen, fields... The record's since is Redis TIME.
var swapDone = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
if ARGV[2] ~= '' then
  local r = cjson.decode(ARGV[2])
  r['since'] = now
  redis.call('HSET', KEYS[2], ARGV[1], cjson.encode(r))
else
  redis.call('HDEL', KEYS[2], ARGV[1])
end
return redis.call('XADD', KEYS[1], 'MAXLEN', '~', ARGV[3], '*', unpack(ARGV, 4))`)

// FinishSwap records the proof: the done entry (back when fb is nil, the
// role being on its own profile again) and the fallback state, together.
func (b *Bus) FinishSwap(ctx context.Context, e SwapEvent, fb *FallbackRecord) error {
	e.Event = SwapDone
	rec := ""
	if fb == nil {
		e.Event = SwapBack
	} else {
		v, err := json.Marshal(fb)
		if err != nil {
			return err
		}
		rec = string(v)
	}
	if err := e.valid(); err != nil {
		return err
	}
	args := append([]interface{}{e.Role, rec, streamMaxLen}, e.fields()...)
	return swapDone.Run(ctx, b.r, []string{SwapsKey(b.project), FallbackKey(b.project)}, args...).Err()
}

// Fallbacks returns role → record.
func (b *Bus) Fallbacks(ctx context.Context) (map[string]FallbackRecord, error) {
	raw, err := b.r.HGetAll(ctx, FallbackKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]FallbackRecord, len(raw))
	for role, v := range raw {
		var r FallbackRecord
		if ValidName(role) && json.Unmarshal([]byte(v), &r) == nil {
			out[role] = r
		}
	}
	return out, nil
}

// SwapLog returns up to n ledger entries, newest first, for role ("" = all).
func (b *Bus) SwapLog(ctx context.Context, role string, n int) ([]SwapEvent, error) {
	msgs, err := b.r.XRevRangeN(ctx, SwapsKey(b.project), "+", "-", 1000).Result()
	if err != nil {
		return nil, err
	}
	var out []SwapEvent
	for _, m := range msgs {
		e := swapEventFrom(m)
		if role != "" && e.Role != role {
			continue
		}
		out = append(out, e)
		if n > 0 && len(out) == n {
			break
		}
	}
	return out, nil
}

func swapEventFrom(m redis.XMessage) SwapEvent {
	s := func(k string) string { v, _ := m.Values[k].(string); return v }
	i := func(k string) int { v, _ := strconv.Atoi(s(k)); return v }
	return SwapEvent{ID: m.ID, Role: s("role"), Event: s("event"), FromProfile: s("from_profile"), ToProfile: s("to_profile"),
		FromProvider: s("from_provider"), ToProvider: s("to_provider"), ToModel: s("to_model"), By: s("by"), Reason: s("reason"),
		OldPID: i("old_pid"), OldPane: s("old_pane"), NewPID: i("new_pid"), NewPane: s("new_pane"), Episode: s("episode"), Error: s("error"),
		Kind: s("kind")}
}

// Interrupted reports whether role's last ledger entry is a begin with no
// live intent: a swap whose caller died before a terminal entry (§11.2).
func (b *Bus) Interrupted(ctx context.Context, role string) (bool, SwapEvent, error) {
	last, err := b.SwapLog(ctx, role, 1)
	if err != nil || len(last) == 0 || last[0].Event != SwapBegin {
		return false, SwapEvent{}, err
	}
	live, err := b.Swapping(ctx, role)
	if err != nil {
		return false, SwapEvent{}, err
	}
	return !live, last[0], nil
}

// FinishReload records a reload's proof: the reloaded entry, and nothing
// else (the fallback state is a swap's, never a reload's).
func (b *Bus) FinishReload(ctx context.Context, e SwapEvent) error {
	e.Event, e.Kind = SwapReloaded, SwapKindReload
	return b.LogSwap(ctx, e)
}

// RetireSwap ends a retire: the retire entry and the role's fallback record
// removed, in one script (a retired agent runs on no profile at all).
func (b *Bus) RetireSwap(ctx context.Context, e SwapEvent) error {
	e.Event = SwapRetire
	if err := e.valid(); err != nil {
		return err
	}
	args := append([]interface{}{e.Role, "", streamMaxLen}, e.fields()...)
	return swapDone.Run(ctx, b.r, []string{SwapsKey(b.project), FallbackKey(b.project)}, args...).Err()
}
