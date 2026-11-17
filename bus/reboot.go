package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// After a reboot (spec docs/superpowers/specs/2026-11-15-reboot-resume-design.md
// §3): the dispatch service judges the project's run record once, before its
// first pass, and records the verdict in {p}:reboot for busmon and
// self-repair. Every write is the dispatcher's, fenced by its lease; at comes
// from Redis TIME.

// The verdicts (§3's table), a closed set.
const (
	RebootIntentional = "intentional" // a clean shutdown: busmon asks, self-repair holds
	RebootSuspended   = "suspended"   // suspended before the shutdown: busmon asks to resume
	RebootRestarting  = "restarting"  // unplanned, restarted by itself: self-repair relaunches
	RebootUnplanned   = "unplanned"   // unplanned, not restarted (Why): busmon asks
	RebootStopped     = "stopped"     // the repository check or the limit (Why): suspended
)

// RebootRecord is {p}:reboot (§3's schema). Why only with unplanned and
// stopped; N, Max and Window (ms) only with restarting and a stopped by the
// limit.
type RebootRecord struct {
	Boot    string `json:"boot"`
	Verdict string `json:"verdict"`
	Why     string `json:"why,omitempty"`
	N       int    `json:"n,omitempty"`
	Max     int    `json:"max,omitempty"`
	Window  int64  `json:"window,omitempty"`
	At      int64  `json:"at"`
}

// RebootKey is the project's reboot record.
func RebootKey(project string) string { return project + ":reboot" }

// Reboot reads {p}:reboot; ok is false when there is none.
func (b *Bus) Reboot(ctx context.Context) (RebootRecord, bool, error) {
	raw, err := b.r.Get(ctx, RebootKey(b.project)).Result()
	if errors.Is(err, redis.Nil) {
		return RebootRecord{}, false, nil
	}
	if err != nil {
		return RebootRecord{}, false, err
	}
	var r RebootRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return RebootRecord{}, false, fmt.Errorf("%s: %w", RebootKey(b.project), err)
	}
	return r, true, nil
}

// fencedRebootPut: -1 not the holder; 1 written, at from Redis TIME.
var fencedRebootPut = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local r = cjson.decode(ARGV[2])
local t = redis.call('TIME')
r['at'] = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('SET', KEYS[2], cjson.encode(r))
return 1`)

// FencedPutReboot writes the verdict under the lease.
func (b *Bus) FencedPutReboot(ctx context.Context, token string, r RebootRecord) error {
	r.At = 0
	in, err := json.Marshal(r)
	if err != nil {
		return err
	}
	n, err := fencedRebootPut.Run(ctx, b.r, []string{DispatchKey(b.project), RebootKey(b.project)}, token, in).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}

// fencedRebootDel: -1 not the holder; 0 no record of that boot; 1 deleted.
var fencedRebootDel = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if not v then return 0 end
local ok, r = pcall(cjson.decode, v)
if ok and type(r) == 'table' and r['boot'] ~= ARGV[2] then return 0 end
redis.call('DEL', KEYS[2])
return 1`)

// FencedDelReboot deletes the record of boot under the lease (another
// boot's is left: a record unreadable as JSON is deleted).
func (b *Bus) FencedDelReboot(ctx context.Context, token, boot string) error {
	n, err := fencedRebootDel.Run(ctx, b.r, []string{DispatchKey(b.project), RebootKey(b.project)}, token, boot).Int()
	if err != nil {
		return err
	}
	if n < 0 {
		return ErrNotHolder
	}
	return nil
}

// fencedSuspendScript is suspendScript behind the lease check (§3.2's
// stop): -1 not the holder, then as suspendScript.
var fencedSuspendScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[3] then return {-1, ''} end
` + suspendBody)

// FencedSuspend is the dispatcher's suspend (reboot-resume §3.2): Suspend
// under the lease. An existing suspend (the person's) is left and returned.
func (b *Bus) FencedSuspend(ctx context.Context, token string, s SuspendSpec) (PauseRecord, bool, error) {
	if !ValidName(s.By) || !ValidName(s.Notify) {
		return PauseRecord{}, false, fmt.Errorf("invalid name %q or %q", s.By, s.Notify)
	}
	if err := ValidateText(s.Reason, CmdMaxRunes); err != nil {
		return PauseRecord{}, false, err
	}
	in, _ := json.Marshal(PauseRecord{Kind: PauseKindSuspend, By: s.By, Notify: s.Notify, Reason: s.Reason,
		Host: s.Host, Boot: s.Boot, Services: s.Services})
	res, err := fencedSuspendScript.Run(ctx, b.r, []string{PauseKey(b.project), DispatchKey(b.project)}, in, suspendHorizon.Milliseconds(), token).Slice()
	if err != nil {
		return PauseRecord{}, false, err
	}
	code, _ := res[0].(int64)
	if code < 0 {
		return PauseRecord{}, false, ErrNotHolder
	}
	raw, _ := res[1].(string)
	var p PauseRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return PauseRecord{}, false, fmt.Errorf("%s: %w", PauseKey(b.project), err)
	}
	return p, code == 0, nil
}
