package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Tokens (spec docs/superpowers/specs/2026-10-28-tokens-dashboard-design.md):
// the dispatch service's history of who spent what. Three keys, every write
// fenced on the dispatch lease, none in projectKinds and all ignored by
// HasKeysBesidesDispatch, so none discovers or revives a project.

// SessionsKey is {p}:sessions: session id → SessionRecord JSON, the sessions
// an agent proved (IdentSession), kept 8 days, at most SessionsCap fields.
func SessionsKey(project string) string { return project + ":sessions" }

// TokensKey is {p}:tokens: "<day>|<agent>|<provider>" → TokenRow JSON, plus
// TokensMetaField; replaced whole by each pass.
func TokensKey(project string) string { return project + ":tokens" }

// QuotaHistKey is {p}:quota:hist: a stream of account-window readings
// (provider, window, pct, at), capped at QuotaHistMax.
func QuotaHistKey(project string) string { return project + ":quota:hist" }

const (
	SessionsKeep    int64 = 8 * 24 * 3600 * 1000 // ms a session is kept after its last proof
	SessionsCap           = 512
	QuotaHistMax          = 2000
	TokensMetaField       = "_meta"
)

// SessionRecord is one proved session: whose, on what, first and last seen
// (Redis TIME, ms).
type SessionRecord struct {
	Agent    string `json:"agent"`
	Provider string `json:"provider,omitempty"`
	Client   string `json:"client,omitempty"`
	First    int64  `json:"first"`
	Last     int64  `json:"last"`
}

// sessionsRecord: 0 not the holder, else 1. ARGV: token, keep ms, cap, then
// session/agent/provider/client quadruples. A known session keeps its first;
// every given one gets last = TIME. Then sessions older than keep go, and the
// oldest last beyond cap.
var sessionsRecord = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
for i = 4, #ARGV, 4 do
  local first = now
  local old = redis.call('HGET', KEYS[2], ARGV[i])
  if old then
    local ok, rec = pcall(cjson.decode, old)
    if ok and type(rec) == 'table' and tonumber(rec.first) then first = tonumber(rec.first) end
  end
  redis.call('HSET', KEYS[2], ARGV[i], cjson.encode({agent = ARGV[i+1], provider = ARGV[i+2], client = ARGV[i+3], first = first, last = now}))
end
local all = redis.call('HGETALL', KEYS[2])
local kept = {}
for i = 1, #all, 2 do
  local ok, rec = pcall(cjson.decode, all[i+1])
  local last = ok and type(rec) == 'table' and tonumber(rec.last) or 0
  if now - last > tonumber(ARGV[2]) then
    redis.call('HDEL', KEYS[2], all[i])
  else
    table.insert(kept, {all[i], last})
  end
end
local cap = tonumber(ARGV[3])
if #kept > cap then
  table.sort(kept, function(a, b) return a[2] < b[2] end)
  for i = 1, #kept - cap do redis.call('HDEL', KEYS[2], kept[i][1]) end
end
return 1`)

// FencedRecordSessions records the proved sessions (session → its record's
// agent, provider, client; First and Last are set by the script) while token
// holds the dispatch lease, and prunes the record.
func (b *Bus) FencedRecordSessions(ctx context.Context, token string, recs map[string]SessionRecord) error {
	argv := []interface{}{token, SessionsKeep, SessionsCap}
	ids := make([]string, 0, len(recs))
	for id := range recs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := recs[id]
		if id == "" || !ValidName(r.Agent) {
			continue
		}
		argv = append(argv, id, r.Agent, r.Provider, r.Client)
	}
	return fencedOK(sessionsRecord.Run(ctx, b.r, []string{DispatchKey(b.project), SessionsKey(b.project)}, argv...))
}

// Sessions reads {p}:sessions; a malformed record is skipped.
func (b *Bus) Sessions(ctx context.Context) (map[string]SessionRecord, error) {
	raw, err := b.r.HGetAll(ctx, SessionsKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]SessionRecord, len(raw))
	for id, v := range raw {
		var r SessionRecord
		if json.Unmarshal([]byte(v), &r) == nil && r.Agent != "" {
			out[id] = r
		}
	}
	return out, nil
}

// TokenRow is one day's tokens of one agent on one provider: In (input),
// Write (cache creation), Read (cache read), Out (output), N (requests).
type TokenRow struct {
	In    int64 `json:"in"`
	Write int64 `json:"write"`
	Read  int64 `json:"read"`
	Out   int64 `json:"out"`
	N     int64 `json:"n"`
}

// TokensMeta says how complete {p}:tokens is: partial until every file has
// been read once since the dispatcher started; At is Redis TIME (ms).
type TokensMeta struct {
	Partial bool  `json:"partial"`
	At      int64 `json:"at"`
}

// TokenKey is a {p}:tokens field.
func TokenKey(day, agent, provider string) string { return day + "|" + agent + "|" + provider }

// tokensReplace: 0 not the holder, else 1; the hash becomes the given
// fields plus the meta field stamped with TIME. ARGV: token, partial, pairs.
var tokensReplace = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('DEL', KEYS[2])
if #ARGV > 2 then redis.call('HSET', KEYS[2], unpack(ARGV, 3)) end
redis.call('HSET', KEYS[2], '` + TokensMetaField + `', cjson.encode({partial = ARGV[2] == '1', at = now}))
return 1`)

// FencedPutTokens replaces {p}:tokens while token holds the dispatch lease.
func (b *Bus) FencedPutTokens(ctx context.Context, token string, rows map[string]TokenRow, partial bool) error {
	p := "0"
	if partial {
		p = "1"
	}
	argv := []interface{}{token, p}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, err := json.Marshal(rows[k])
		if err != nil {
			return err
		}
		argv = append(argv, k, string(v))
	}
	return fencedOK(tokensReplace.Run(ctx, b.r, []string{DispatchKey(b.project), TokensKey(b.project)}, argv...))
}

// Tokens reads {p}:tokens: the rows and the meta; ok false when the key is
// absent (no pass has written it yet).
func (b *Bus) Tokens(ctx context.Context) (rows map[string]TokenRow, meta TokensMeta, ok bool, err error) {
	raw, err := b.r.HGetAll(ctx, TokensKey(b.project)).Result()
	if err != nil || len(raw) == 0 {
		return nil, meta, false, err
	}
	rows = make(map[string]TokenRow, len(raw))
	for k, v := range raw {
		if k == TokensMetaField {
			_ = json.Unmarshal([]byte(v), &meta)
			continue
		}
		var r TokenRow
		if json.Unmarshal([]byte(v), &r) == nil {
			rows[k] = r
		}
	}
	return rows, meta, true, nil
}

// QuotaSample is one account-window reading of the history.
type QuotaSample struct {
	Provider, Window string
	Pct              float64
	At               int64 // Redis TIME, ms
}

// quotaHistAdd: 0 not the holder, else 1; one entry per provider/window/pct
// triple of ARGV (after token, before the cap, last), stamped with TIME.
var quotaHistAdd = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
for i = 2, #ARGV - 1, 3 do
  redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[#ARGV], '*', 'provider', ARGV[i], 'window', ARGV[i+1], 'pct', ARGV[i+2], 'at', now)
end
return 1`)

// FencedAddQuotaHist appends samples (their At is set by the script).
func (b *Bus) FencedAddQuotaHist(ctx context.Context, token string, samples []QuotaSample) error {
	if len(samples) == 0 {
		return nil
	}
	argv := []interface{}{token}
	for _, s := range samples {
		argv = append(argv, s.Provider, s.Window, strconv.FormatFloat(s.Pct, 'f', -1, 64))
	}
	argv = append(argv, QuotaHistMax)
	return fencedOK(quotaHistAdd.Run(ctx, b.r, []string{DispatchKey(b.project), QuotaHistKey(b.project)}, argv...))
}

// QuotaHist reads the samples at or after since (Unix ms), oldest first.
func (b *Bus) QuotaHist(ctx context.Context, since int64) ([]QuotaSample, error) {
	msgs, err := b.r.XRange(ctx, QuotaHistKey(b.project), fmt.Sprintf("%d-0", since), "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]QuotaSample, 0, len(msgs))
	for _, m := range msgs {
		f := toStringMap(m.Values)
		pct, err1 := strconv.ParseFloat(f["pct"], 64)
		at, err2 := strconv.ParseInt(f["at"], 10, 64)
		if err1 != nil || err2 != nil || f["provider"] == "" {
			continue
		}
		out = append(out, QuotaSample{Provider: f["provider"], Window: f["window"], Pct: pct, At: at})
	}
	return out, nil
}

// fencedOK maps a fenced script's 0 to ErrNotHolder.
func fencedOK(c *redis.Cmd) error {
	n, err := c.Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotHolder
	}
	return nil
}
