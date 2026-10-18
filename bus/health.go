package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/redis/go-redis/v9"
)

const healthLogMaxLen = 1000

var ErrProjectNotInstalled = errors.New("project services are not installed")

func HealthKey(project string) string      { return project + ":health" }
func HealthLogKey(project string) string   { return project + ":health:log" }
func HealthNotesKey(project string) string { return project + ":health:notes" }
func HealthDailyKey(project string) string { return project + ":health:daily" }

// fencedHealthPut updates one measurement, transition record and fail wake in
// a single lease-fenced operation. Key types and the services marker are
// checked before any mutation, since Redis scripts are atomic but do not roll
// back commands after a runtime error.
var fencedHealthPut = redis.NewScript(`
local function keytype(key, expected)
  local t = redis.call('TYPE', key).ok
  return t == 'none' or t == expected
end
if not keytype(KEYS[1], 'string') then return -3 end
if not keytype(KEYS[2], 'hash') or not keytype(KEYS[3], 'stream') or
   not keytype(KEYS[4], 'hash') or not keytype(KEYS[5], 'stream') then return -3 end
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('HEXISTS', KEYS[6], 'installed') == 0 then return -2 end
local old = redis.call('HGET', KEYS[2], ARGV[2])
local previous, since = '', 0
if old then
  local ok, rec = pcall(cjson.decode, old)
  if ok and type(rec) == 'table' then
    previous = rec.status or ''
    since = tonumber(rec.since or '0') or 0
  end
end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
if previous ~= ARGV[4] or since == 0 then since = now end
local rec = cjson.decode(ARGV[3])
rec.observed_at = now
rec.since = since
redis.call('HSET', KEYS[2], ARGV[2], cjson.encode(rec))
if previous ~= ARGV[4] then
  redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[8], '*',
    'check', ARGV[2], 'previous', previous, 'status', ARGV[4],
    'fingerprint', ARGV[5], 'at', tostring(now))
  if ARGV[4] == 'fail' then
    local target = ARGV[7]
    if redis.call('HEXISTS', KEYS[4], 'sentinel') == 1 then target = 'sentinel' end
    local message = 'Health check ' .. ARGV[2] .. ' entered fail (' .. ARGV[5] .. '); inspect with agentbus --project ' .. ARGV[6] .. ' health detail ' .. ARGV[2]
    redis.call('XADD', KEYS[5], 'MAXLEN', '~', '1000', '*',
      'from', 'dispatch', 'target', target, 'type', 'directive', 'ref', '',
      'command', message, 'text_complete', 'yes')
  end
end
return 1
`)

// FencedHealthPut stores one check and atomically logs/wakes on a status
// transition. A fail wake is published only when entering fail, never on a
// repeated observation of the same failure.
func (b *Bus) FencedHealthPut(ctx context.Context, token, fallback string, rec health.Record) error {
	if !ValidName(fallback) {
		return fmt.Errorf("invalid health escalation recipient")
	}
	if _, ok := health.ByID(rec.ID); !ok {
		return fmt.Errorf("unknown health check")
	}
	if rec.Status != health.OK && rec.Status != health.Warn && rec.Status != health.Fail && rec.Status != health.Unknown {
		return fmt.Errorf("invalid health status")
	}
	if !health.SafeText(rec.Detail) {
		return fmt.Errorf("health detail contains unsafe text")
	}
	rec.ObservedAt, rec.Since = 0, 0 // Redis stamps both values in the script.
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	n, err := fencedHealthPut.Run(ctx, b.r, []string{
		DispatchKey(b.project), HealthKey(b.project), HealthLogKey(b.project), AgentsKey(b.project),
		StreamKey(b.project, "cmd"), ServicesKey(b.project),
	}, token, rec.ID, string(raw), string(rec.Status), rec.Fingerprint, b.project, fallback, healthLogMaxLen).Int()
	if err != nil {
		return err
	}
	switch n {
	case 1:
		return nil
	case 0:
		return ErrNotHolder
	case -2:
		return ErrProjectNotInstalled
	default:
		return fmt.Errorf("health keys have unexpected Redis types")
	}
}

var fencedHealthDaily = redis.NewScript(`
local function keytype(key, expected)
  local t = redis.call('TYPE', key).ok
  return t == 'none' or t == expected
end
if not keytype(KEYS[1], 'string') or not keytype(KEYS[2], 'stream') or
   not keytype(KEYS[3], 'hash') or not keytype(KEYS[4], 'string') or
   not keytype(KEYS[5], 'hash') then return -3 end
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('HEXISTS', KEYS[3], 'installed') == 0 then return -2 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local day = math.floor(now / 86400000)
if redis.call('EXISTS', KEYS[4]) == 1 then return 0 end
redis.call('SET', KEYS[4], tostring(day), 'EX', 172800)
local target = ARGV[3]
if redis.call('HEXISTS', KEYS[5], 'sentinel') == 1 then target = 'sentinel' end
local message = 'Daily health review; run agentbus --project ' .. ARGV[2] .. ' health'
redis.call('XADD', KEYS[2], 'MAXLEN', '~', '1000', '*', 'from', 'dispatch',
  'target', target, 'type', 'directive', 'ref', '', 'command', message,
  'text_complete', 'yes')
return 1
`)

// FencedHealthDailyWake records a once-per-Redis-day mark and its directive
// atomically. target is used if sentinel is not registered.
func (b *Bus) FencedHealthDailyWake(ctx context.Context, token, target string) (bool, error) {
	if !ValidName(target) {
		return false, fmt.Errorf("invalid health escalation recipient")
	}
	n, err := fencedHealthDaily.Run(ctx, b.r, []string{
		DispatchKey(b.project), StreamKey(b.project, "cmd"), ServicesKey(b.project),
		HealthDailyKey(b.project), AgentsKey(b.project),
	}, token, b.project, target).Int()
	if err != nil {
		return false, err
	}
	switch n {
	case 1:
		return true, nil
	case 0:
		return false, nil
	case -2:
		return false, ErrProjectNotInstalled
	default:
		return false, fmt.Errorf("health keys have unexpected Redis types")
	}
}

// Health returns every well-formed current record. Unknown check ids are
// ignored so a newer writer remains readable by an older client.
func (b *Bus) Health(ctx context.Context) (map[string]health.Record, error) {
	raw, err := b.r.HGetAll(ctx, HealthKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]health.Record, len(raw))
	for id, value := range raw {
		var rec health.Record
		if json.Unmarshal([]byte(value), &rec) == nil && rec.ID == id {
			if _, ok := health.ByID(id); ok {
				out[id] = rec
			}
		}
	}
	return out, nil
}

// HealthNotes returns each well-formed annotation keyed by check id.
func (b *Bus) HealthNotes(ctx context.Context) (map[string]health.Note, error) {
	raw, err := b.r.HGetAll(ctx, HealthNotesKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]health.Note, len(raw))
	for id, value := range raw {
		var note health.Note
		if json.Unmarshal([]byte(value), &note) == nil && note.ID == id {
			out[id] = note
		}
	}
	return out, nil
}

// HealthTransitions returns events after afterID (or the newest limit events
// when empty), plus the retained history floor ID.
func (b *Bus) HealthTransitions(ctx context.Context, afterID string, limit int64) ([]health.Transition, string, error) {
	if limit <= 0 || limit > healthLogMaxLen {
		limit = healthLogMaxLen
	}
	floor, err := b.r.XRangeN(ctx, HealthLogKey(b.project), "-", "+", 1).Result()
	if err != nil && err != redis.Nil {
		return nil, "", err
	}
	floorID := ""
	if len(floor) > 0 {
		floorID = floor[0].ID
	}
	var rows []redis.XMessage
	if afterID == "" {
		rows, err = b.r.XRevRangeN(ctx, HealthLogKey(b.project), "+", "-", limit).Result()
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	} else {
		rows, err = b.r.XRangeN(ctx, HealthLogKey(b.project), "("+afterID, "+", limit).Result()
	}
	if err != nil && err != redis.Nil {
		return nil, floorID, err
	}
	out := make([]health.Transition, 0, len(rows))
	for _, row := range rows {
		f := toStringMap(row.Values)
		at, _ := strconv.ParseInt(f["at"], 10, 64)
		out = append(out, health.Transition{ID: f["check"], Previous: health.Status(f["previous"]),
			Status: health.Status(f["status"]), At: at, Fingerprint: f["fingerprint"], StreamID: row.ID})
	}
	return out, floorID, nil
}

var putHealthNote = redis.NewScript(`
local t = redis.call('TYPE', KEYS[1]).ok
if t ~= 'none' and t ~= 'hash' then return -3 end
if redis.call('HEXISTS', KEYS[2], 'installed') == 0 then return -2 end
local now = redis.call('TIME')
local ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local note = cjson.decode(ARGV[2])
note.at = ms
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(note))
return 1
`)

// PutHealthNote stores a bounded sentinel annotation separately from measured
// records. It has no ability to alter status or append transitions.
func (b *Bus) PutHealthNote(ctx context.Context, note health.Note) error {
	if _, ok := health.ByID(note.ID); !ok || !ValidName(note.Author) {
		return fmt.Errorf("invalid health note identity")
	}
	if !health.SafeText(note.Text) || strings.TrimSpace(note.Text) == "" {
		return fmt.Errorf("health note is empty or contains unsafe text")
	}
	note.At = 0
	raw, err := json.Marshal(note)
	if err != nil {
		return err
	}
	n, err := putHealthNote.Run(ctx, b.r, []string{HealthNotesKey(b.project), ServicesKey(b.project)}, note.ID, string(raw)).Int()
	if err != nil {
		return err
	}
	switch n {
	case 1:
		return nil
	case -2:
		return ErrProjectNotInstalled
	default:
		return fmt.Errorf("health note key has unexpected Redis type")
	}
}
