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
	r := &RunRecord{StartedAt: n("started_at"), PausedMs: n("paused_ms"), SeenAt: n("seen_at"), Deliveries: int(n("deliveries")),
		State: h["state"], Since: n("since"), Why: h["why"], Drains: int(n("drains")), Counted: map[string]bool{}}
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

// ResetRun deletes the run (the person's `agentbus run reset`): the next
// merge-queue pass starts a new one with a new baseline.
func (b *Bus) ResetRun(ctx context.Context) error {
	return b.r.Del(ctx, RunKey(b.project)).Err()
}
