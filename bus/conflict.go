package bus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The conflict hand-off's record (spec
// docs/superpowers/specs/2026-11-21-conflict-owner-design.md §4):
// {p}:conflicts, one field per PR ("pr:<n>") or, in the git fallback, per
// branch digest ("br:<digest>"): last|count|first|escalated. last is the
// digest of the episode last counted (it names the head), count the
// hand-offs counted, first the Redis time the field was made, escalated 1
// once master has it. The directives themselves go through FencedCmdOnce,
// each with its mark in {p}:quota (ConflictMarks).

// ConflictsKey is the record, runtime.
func ConflictsKey(project string) string { return project + ":conflicts" }

// ErrConflictBadType: {p}:conflicts holds another type; nothing written.
var ErrConflictBadType = errors.New("conflicts: the record holds another type")

// ConflictEntry is one field of {p}:conflicts.
type ConflictEntry struct {
	Last      string // the digest last counted, "" when none was
	Count     int    // hand-offs counted
	First     int64  // Redis time the field was made, Unix seconds
	Escalated bool   // master has it: no more hand-offs
}

// ConflictTrack is one call of the track script: Count counts Digest when it
// is not the last one counted; Escalate marks the field escalated. Neither:
// the field is made (first set) and nothing else changes (a held owner).
type ConflictTrack struct {
	Field, Digest   string
	Count, Escalate bool
}

// ConflictMarks are a field's FencedCmdOnce marks: the owner's hand-off,
// master's copy, the escalation.
func ConflictMarks(field string) [3]string {
	return [3]string{"conflict:" + field, "conflict-copy:" + field, "conflict-esc:" + field}
}

// conflictTrackScript: the lease; the type; the field (made with first from
// TIME); an escalated field is never counted again; count when the digest is
// not the last counted; escalate. It returns the field.
//
// KEYS: dispatch lease, {p}:conflicts. ARGV: token, field, digest, count, escalate.
var conflictTrackScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 'not-holder' end
local t = redis.call('TYPE', KEYS[2]).ok
if t ~= 'hash' and t ~= 'none' then return 'bad-type' end
local last, count, first, esc = '', 0, nil, '0'
local v = redis.call('HGET', KEYS[2], ARGV[2])
if v then
  local l, c, f, e = string.match(v, '^([^|]*)|([^|]*)|([^|]*)|(.*)$')
  if l then last, count, first, esc = l, tonumber(c) or 0, f, e end
end
if not first or first == '' then first = tostring(redis.call('TIME')[1]) end
if esc ~= '1' then
  if ARGV[4] == '1' and last ~= ARGV[3] then
    count = count + 1
    last = ARGV[3]
  end
  if ARGV[5] == '1' then esc = '1' end
end
local val = last .. '|' .. count .. '|' .. first .. '|' .. esc
redis.call('HSET', KEYS[2], ARGV[2], val)
return val`)

// FencedConflictTrack updates one field under the dispatch lease and returns it.
func (b *Bus) FencedConflictTrack(ctx context.Context, token string, tr ConflictTrack) (ConflictEntry, error) {
	if tr.Field == "" || strings.ContainsAny(tr.Field, "|\n") || len(tr.Digest) != 40 {
		return ConflictEntry{}, fmt.Errorf("conflict track: invalid field %q or digest", tr.Field)
	}
	flag := func(on bool) string {
		if on {
			return "1"
		}
		return "0"
	}
	v, err := conflictTrackScript.Run(ctx, b.r, []string{DispatchKey(b.project), ConflictsKey(b.project)},
		token, tr.Field, tr.Digest, flag(tr.Count), flag(tr.Escalate)).Text()
	if err != nil {
		return ConflictEntry{}, err
	}
	switch v {
	case "not-holder":
		return ConflictEntry{}, ErrNotHolder
	case "bad-type":
		return ConflictEntry{}, ErrConflictBadType
	}
	e, ok := parseConflict(v)
	if !ok {
		return ConflictEntry{}, fmt.Errorf("conflict track: unreadable field %q", v)
	}
	return e, nil
}

func parseConflict(v string) (ConflictEntry, bool) {
	parts := strings.SplitN(v, "|", 4)
	if len(parts) != 4 {
		return ConflictEntry{}, false
	}
	c, err1 := strconv.Atoi(parts[1])
	f, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		return ConflictEntry{}, false
	}
	return ConflictEntry{Last: parts[0], Count: c, First: f, Escalated: parts[3] == "1"}, true
}

// ConflictRecord reads {p}:conflicts, field → entry; a field that does not
// parse is left out.
func (b *Bus) ConflictRecord(ctx context.Context) (map[string]ConflictEntry, error) {
	h, err := b.r.HGetAll(ctx, ConflictsKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := map[string]ConflictEntry{}
	for k, v := range h {
		if e, ok := parseConflict(v); ok {
			out[k] = e
		}
	}
	return out, nil
}

// conflictForgetScript deletes fields of {p}:conflicts and marks of
// {p}:quota, fenced. KEYS: lease, {p}:conflicts, {p}:quota. ARGV: token,
// the number of fields, the fields, then the marks.
var conflictForgetScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local n = tonumber(ARGV[2])
for i = 3, 2 + n do redis.call('HDEL', KEYS[2], ARGV[i]) end
for i = 3 + n, #ARGV do redis.call('HDEL', KEYS[3], ARGV[i]) end
return 1`)

// FencedConflictForget removes resolved fields and their marks (§4 step 1):
// a later conflict on the same PR starts afresh.
func (b *Bus) FencedConflictForget(ctx context.Context, token string, fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	args := []interface{}{token, len(fields)}
	for _, f := range fields {
		args = append(args, f)
	}
	for _, f := range fields {
		for _, m := range ConflictMarks(f) {
			args = append(args, m)
		}
	}
	n, err := conflictForgetScript.Run(ctx, b.r, []string{DispatchKey(b.project), ConflictsKey(b.project), QuotaKey(b.project)}, args...).Int()
	if err != nil {
		return err
	}
	if n == -1 {
		return ErrNotHolder
	}
	return nil
}
