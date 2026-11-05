package bus

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// {p}:seen (report-one-row §5 B, the person's decision): when the person
// opened a report notice addressed to them. A hash, notice id → "<ms>|<person>",
// the time from Redis TIME; the first opening wins; at most SeenMax fields,
// the oldest ids pruned. busmon writes it beside its local cache and never
// reads it back as the person's read state; `agentbus reports` reads it.
// Runtime (bus/keykinds.go), not in projectKinds, ignored by
// HasKeysBesidesDispatch; written only while the project's cmd stream exists,
// so a write racing a delete never revives a project.

// SeenMax caps {p}:seen.
const SeenMax = 512

// SeenKey is {p}:seen.
func SeenKey(project string) string { return project + ":seen" }

// Seen is one read receipt.
type Seen struct {
	At time.Time
	By string
}

// seenMark: KEYS[1] the cmd stream, KEYS[2] {p}:seen; ARGV[1] the max,
// ARGV[2] the person, then ids. 0 when the cmd stream is absent.
var seenMark = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
local t = redis.call('TIME')
local v = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)) .. '|' .. ARGV[2]
for i = 3, #ARGV do redis.call('HSETNX', KEYS[2], ARGV[i], v) end
local max = tonumber(ARGV[1])
if redis.call('HLEN', KEYS[2]) > max then
  local ids = redis.call('HKEYS', KEYS[2])
  local function parts(s)
    local ms, seq = string.match(s, '^(%d+)-(%d+)$')
    return tonumber(ms) or 0, tonumber(seq) or 0
  end
  table.sort(ids, function(a, b)
    local am, as = parts(a)
    local bm, bs = parts(b)
    if am ~= bm then return am < bm end
    return as < bs
  end)
  for i = 1, #ids - max do redis.call('HDEL', KEYS[2], ids[i]) end
end
return 1`)

// MarkSeen records that person opened the report notices ids.
func (b *Bus) MarkSeen(ctx context.Context, person string, ids ...string) error {
	if person == "" || strings.ContainsAny(person, "|\n") {
		return errors.New("seen: invalid person")
	}
	if len(ids) == 0 {
		return nil
	}
	args := []any{SeenMax, person}
	for _, id := range ids {
		if !validStreamID(id) {
			return errors.New("seen: not a stream id: " + id)
		}
		args = append(args, id)
	}
	return seenMark.Run(ctx, b.r, []string{StreamKey(b.project, "cmd"), SeenKey(b.project)}, args...).Err()
}

// Seen reads {p}:seen; a field it cannot parse is skipped.
func (b *Bus) Seen(ctx context.Context) (map[string]Seen, error) {
	raw, err := b.r.HGetAll(ctx, SeenKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Seen, len(raw))
	for id, v := range raw {
		ms, by, ok := strings.Cut(v, "|")
		n, err := strconv.ParseInt(ms, 10, 64)
		if !ok || err != nil {
			continue
		}
		out[id] = Seen{At: time.UnixMilli(n), By: by}
	}
	return out, nil
}
