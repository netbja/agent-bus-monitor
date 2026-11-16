package bus

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The run (spec docs/superpowers/specs/2026-11-15-run-budget-design.md §2):
// one record per project, {p}:run, written only by the dispatcher's
// merge-queue pass, fenced, on Redis TIME. It counts the run's time (the
// paused intervals left out) and its deliveries (approved heads the git
// test finds in the base, each once). `agentbus run reset` deletes it, and
// the next pass starts a new run with a new baseline.

// RunKey is {p}:run (a hash).
func RunKey(project string) string { return project + ":run" }

// The run's states.
const (
	RunRunning  = "running"
	RunDraining = "draining"
	RunStopped  = "stopped"
)

// RunRecord is the run as read; times are Redis TIME, Unix ms. Counted are
// the heads its d:<head> fields hold: the baseline and every delivery.
type RunRecord struct {
	StartedAt  int64
	PausedMs   int64
	SeenAt     int64
	Deliveries int
	State      string
	Since      int64
	Why        string
	Drains     int
	Counted    map[string]bool
}

// Elapsed is the run's time at nowMs (Redis time): since its start, less
// the paused intervals summed. The interval since the last pass counts as
// run time until the next pass judges it (to within one pass).
func (r RunRecord) Elapsed(nowMs int64) time.Duration {
	d := time.Duration(nowMs-r.StartedAt-r.PausedMs) * time.Millisecond
	return max(d, 0)
}

// Run reads the run; nil when there is none.
func (b *Bus) Run(ctx context.Context) (*RunRecord, error) {
	h, err := b.r.HGetAll(ctx, RunKey(b.project)).Result()
	if err != nil || len(h) == 0 {
		return nil, err
	}
	return parseRun(h), nil
}

func parseRun(h map[string]string) *RunRecord {
	n := func(k string) int64 { v, _ := strconv.ParseInt(h[k], 10, 64); return v }
	// Counts are parsed as int (no narrowing conversion, CodeQL): out of
	// range, negative or not a number reads 0.
	count := func(k string) int {
		v, err := strconv.Atoi(h[k])
		if err != nil || v < 0 {
			return 0
		}
		return v
	}
	r := &RunRecord{StartedAt: n("started_at"), PausedMs: n("paused_ms"), SeenAt: n("seen_at"), Deliveries: count("deliveries"),
		State: h["state"], Since: n("since"), Why: h["why"], Drains: count("drains"), Counted: map[string]bool{}}
	for k := range h {
		if head, ok := strings.CutPrefix(k, "d:"); ok {
			r.Counted[head] = true
		}
	}
	return r
}

// runPassScript: KEYS[1] the dispatch lease, KEYS[2] {p}:run, KEYS[3] {p}:pause;
// ARGV[1] token, ARGV[2] "1" when the heads are known, ARGV[3] the gap (ms)
// past which an interval without a pass counts as paused, ARGV[4..] the
// delivered heads. No record: with the heads known, a new run whose
// baseline is every head given (0 deliveries), else nothing (0). A record:
// the interval since seen_at is added to paused_ms when the project was
// paused at its start or it is longer than the gap; each head not counted
// yet adds one delivery (2).
var runPassScript = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local known = ARGV[2] == '1'
if redis.call('EXISTS', KEYS[2]) == 0 then
  if not known then return 0 end
  redis.call('HSET', KEYS[2], 'started_at', now, 'paused_ms', 0, 'seen_at', now, 'deliveries', 0, 'state', 'running', 'since', now, 'drains', 0)
  for i = 4, #ARGV do redis.call('HSET', KEYS[2], 'd:' .. ARGV[i], now) end
  return 1
end
local seen = tonumber(redis.call('HGET', KEYS[2], 'seen_at')) or now
local gap = now - seen
if gap < 0 then gap = 0 end
local paused = false
local v = redis.call('GET', KEYS[3])
if v then
  local ok, p = pcall(cjson.decode, v)
  if ok and type(p) == 'table' and (p['kind'] == 'suspend' or (tonumber(p['until']) or 0) > seen) then paused = true end
end
if paused or gap > tonumber(ARGV[3]) then redis.call('HINCRBY', KEYS[2], 'paused_ms', gap) end
redis.call('HSET', KEYS[2], 'seen_at', now)
if known then
  for i = 4, #ARGV do
    if redis.call('HSETNX', KEYS[2], 'd:' .. ARGV[i], now) == 1 then redis.call('HINCRBY', KEYS[2], 'deliveries', 1) end
  end
end
return 2`)

// FencedRunPass is one merge-queue pass's step of the run (§2): heads are
// the approved heads (within 72 h) the git test finds in the base, known
// false when the pass could not judge them. It returns the record after the
// write and whether this pass started the run; only the lease holder writes.
func (b *Bus) FencedRunPass(ctx context.Context, token string, heads []string, known bool, gap time.Duration) (RunRecord, bool, error) {
	k := "0"
	if known {
		k = "1"
	}
	args := []interface{}{token, k, gap.Milliseconds()}
	for _, h := range heads {
		if !ValidHead(h) {
			return RunRecord{}, false, fmt.Errorf("run: invalid head %q", h)
		}
		args = append(args, h)
	}
	n, err := runPassScript.Run(ctx, b.r, []string{DispatchKey(b.project), RunKey(b.project), PauseKey(b.project)}, args...).Int()
	if err != nil {
		return RunRecord{}, false, notHolder(err)
	}
	if n == 0 {
		return RunRecord{}, false, nil
	}
	rec, err := b.Run(ctx)
	if err != nil || rec == nil {
		return RunRecord{}, n == 1, err
	}
	return *rec, n == 1, nil
}

// The run's reasons to drain (§3, §4).
const (
	RunWhyDuration   = "duration"
	RunWhyDeliveries = "deliveries"
)

// RunWhyQuota is a drain for provider's quota (§4).
func RunWhyQuota(provider string) string { return "quota:" + provider }

// PauseKindRun is the pause a stopped run writes (§3): a year ahead, never
// lifted by a quota release, lifted by run reset.
const PauseKindRun = "run"

// runYearMs is how far ahead the run's pause ends: in effect, never.
const runYearMs = 365 * 24 * 3600 * 1000

// runDrainScript: KEYS[1] the lease, KEYS[2] {p}:run; ARGV[1] token, ARGV[2]
// why. A running run drains: drains grows by one and the episode
// <started_at>:<drains> is returned with 1. A run draining for a quota whose
// new reason is a limit takes that reason, no new drain (0). Else 0.
var runDrainScript = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local s = redis.call('HGET', KEYS[2], 'state')
if s == 'running' then
  local n = redis.call('HINCRBY', KEYS[2], 'drains', 1)
  redis.call('HSET', KEYS[2], 'state', 'draining', 'why', ARGV[2], 'since', now)
  return {1, (redis.call('HGET', KEYS[2], 'started_at') or '0') .. ':' .. n}
end
if s == 'draining' then
  local w = redis.call('HGET', KEYS[2], 'why') or ''
  if string.sub(w, 1, 6) == 'quota:' and string.sub(ARGV[2], 1, 6) ~= 'quota:' then
    redis.call('HSET', KEYS[2], 'why', ARGV[2])
  end
end
return {0, ''}`)

// FencedRunDrain drains a running run for why (§3); drained reports a new
// drain, whose once-marks take episode. A limit reached while the run
// drains for a quota replaces its reason, so the quota's release never
// returns it to running.
func (b *Bus) FencedRunDrain(ctx context.Context, token, why string) (episode string, drained bool, err error) {
	res, err := runDrainScript.Run(ctx, b.r, []string{DispatchKey(b.project), RunKey(b.project)}, token, why).Slice()
	if err != nil {
		return "", false, notHolder(err)
	}
	if len(res) != 2 {
		return "", false, fmt.Errorf("run drain: unexpected reply %v", res)
	}
	n, _ := res[0].(int64)
	ep, _ := res[1].(string)
	return ep, n == 1, nil
}

// runQuotaReleaseScript: a run draining for exactly ARGV[2] runs again.
var runQuotaReleaseScript = redis.NewScript(fenceCheck + `
if redis.call('HGET', KEYS[2], 'state') ~= 'draining' or redis.call('HGET', KEYS[2], 'why') ~= ARGV[2] then return 0 end
local t = redis.call('TIME')
redis.call('HSET', KEYS[2], 'state', 'running', 'why', '', 'since', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
return 1`)

// FencedRunQuotaRelease returns a run drained for provider's quota, and for
// nothing else, to running (§4: at that provider's release).
func (b *Bus) FencedRunQuotaRelease(ctx context.Context, token, provider string) (bool, error) {
	n, err := runQuotaReleaseScript.Run(ctx, b.r, []string{DispatchKey(b.project), RunKey(b.project)}, token, RunWhyQuota(provider)).Int()
	if err != nil {
		return false, notHolder(err)
	}
	return n == 1, nil
}

// runStopScript: KEYS[1] the lease, KEYS[2] {p}:run, KEYS[3] {p}:pause;
// ARGV[1] token, ARGV[2] notify, ARGV[3] the pause's length (ms). A draining
// run stops, and the project pauses with kind run, unless it is suspended
// (the suspend stays). 1 stopped, 0 not draining.
var runStopScript = redis.NewScript(fenceCheck + `
if redis.call('HGET', KEYS[2], 'state') ~= 'draining' then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('HSET', KEYS[2], 'state', 'stopped', 'since', now)
local v = redis.call('GET', KEYS[3])
if v then
  local ok, p = pcall(cjson.decode, v)
  if ok and type(p) == 'table' and p['kind'] == 'suspend' then return 1 end
end
redis.call('SET', KEYS[3], cjson.encode({['until']=now + tonumber(ARGV[3]), reason='run budget', by='dispatch', notify=ARGV[2], at=now, kind='run'}))
return 1`)

// FencedRunStop stops a draining run (§3): its state stopped and a kind-run
// pause a year ahead, which replaces any pause but a suspend.
func (b *Bus) FencedRunStop(ctx context.Context, token, notify string) (bool, error) {
	if !ValidName(notify) {
		return false, fmt.Errorf("run stop: invalid notify %q", notify)
	}
	n, err := runStopScript.Run(ctx, b.r, []string{DispatchKey(b.project), RunKey(b.project), PauseKey(b.project)}, token, notify, runYearMs).Int()
	if err != nil {
		return false, notHolder(err)
	}
	return n == 1, nil
}

// runResetScript: the run deleted, and the project's pause with it when it
// is the run's own (kind run); any other pause stays.
var runResetScript = redis.NewScript(`
redis.call('DEL', KEYS[1])
local v = redis.call('GET', KEYS[2])
if v then
  local ok, p = pcall(cjson.decode, v)
  if ok and type(p) == 'table' and p['kind'] == 'run' then redis.call('DEL', KEYS[2]) end
end
return 1`)

// ResetRun deletes the run (the person's `agentbus run reset`) and lifts a
// stopped run's pause: the next merge-queue pass starts a new run with a
// new baseline.
func (b *Bus) ResetRun(ctx context.Context) error {
	return runResetScript.Run(ctx, b.r, []string{RunKey(b.project), PauseKey(b.project)}).Err()
}
