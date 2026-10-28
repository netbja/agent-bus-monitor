package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Hand-off records (lifecycle spec, part A): an agent near a full context
// publishes its hand-off as a report, then declares it with `agentbus handoff
// <self> --report <id>`. The declaration is a fact of its own, tied to the
// verified identity, so nothing acts on a report's wording.

// HandoffKey is the {p}:handoff hash: agent → Handoff JSON. Like {p}:observed
// it proves nothing about the project: it is not in projectKinds and
// HasKeysBesidesDispatch ignores it.
func HandoffKey(project string) string { return project + ":handoff" }

// Handoff is one declared hand-off: the report that carries it, and the
// process and session that declared it. At is Redis TIME (ms).
type Handoff struct {
	Report   string `json:"report"`
	Session  string `json:"session"`
	PID      int    `json:"pid"`
	PIDStart uint64 `json:"pid_start"`
	At       int64  `json:"at"`
}

var (
	// ErrHandoffNotOwnReport: the report does not exist (or is trimmed), or
	// another agent wrote it.
	ErrHandoffNotOwnReport = errors.New("handoff: the report is not one of this agent's")
	// ErrHandoffNotVerified: the caller is not the agent's verified process in
	// its proved session.
	ErrHandoffNotVerified = errors.New("handoff: not the agent's verified process and session")
)

var streamIDPattern = regexp.MustCompile(`^[0-9]{1,20}-[0-9]{1,20}$`)

// handoffDeclare: 'report' or 'ident' on a refusal, else the record written.
// The record is built from validated ARGV (digits, a stream id, a session id),
// never cjson.encode, which would rewrite a large pid_start as a float.
var handoffDeclare = redis.NewScript(`
local e = redis.call('XRANGE', KEYS[1], ARGV[2], ARGV[2])
if #e == 0 then return 'report' end
local f, by = e[1][2], nil
for i = 1, #f, 2 do if f[i] == 'agent' then by = f[i+1] end end
if by ~= ARGV[1] then return 'report' end
local raw = redis.call('HGET', KEYS[2], ARGV[1])
if not raw then return 'ident' end
local ok, s = pcall(cjson.decode, raw)
if not ok or type(s) ~= 'table' then return 'ident' end
if s['pid'] ~= tonumber(ARGV[3]) or s['pid_start'] ~= tonumber(ARGV[4]) or s['ident_session'] ~= ARGV[5] then return 'ident' end
local t = redis.call('TIME')
local at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local rec = '{"report":"' .. ARGV[2] .. '","session":"' .. ARGV[5] .. '","pid":' .. ARGV[3] .. ',"pid_start":' .. ARGV[4] .. ',"at":' .. at .. '}'
redis.call('HSET', KEYS[3], ARGV[1], rec)
return rec`)

// DeclareHandoff records agent's hand-off, carried by its report reportID, in
// one script: only when the report is the agent's own and who (the caller's
// ambient identity, read as `status` reads it) is the stored verified process
// in its proved session. A new declaration replaces the agent's last one.
func (b *Bus) DeclareHandoff(ctx context.Context, agent, reportID string, who AgentIdent) (Handoff, error) {
	if !ValidName(agent) {
		return Handoff{}, fmt.Errorf("invalid agent %q", agent)
	}
	if !streamIDPattern.MatchString(reportID) {
		return Handoff{}, fmt.Errorf("handoff: %q is not a report id", reportID)
	}
	if who.PID <= 0 || who.PIDStart == 0 || !ValidSessionID(who.Session) {
		return Handoff{}, ErrHandoffNotVerified
	}
	res, err := handoffDeclare.Run(ctx, b.r,
		[]string{StreamKey(b.project, "report"), AgentsKey(b.project), HandoffKey(b.project)},
		agent, reportID, strconv.Itoa(who.PID), strconv.FormatUint(who.PIDStart, 10), who.Session).Text()
	if err != nil {
		return Handoff{}, err
	}
	switch res {
	case "report":
		return Handoff{}, ErrHandoffNotOwnReport
	case "ident":
		return Handoff{}, ErrHandoffNotVerified
	}
	var h Handoff
	if err := json.Unmarshal([]byte(res), &h); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

// Handoffs is every declared hand-off, void ones included; an unreadable
// field is skipped.
func (b *Bus) Handoffs(ctx context.Context) (map[string]Handoff, error) {
	raw, err := b.r.HGetAll(ctx, HandoffKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Handoff, len(raw))
	for a, v := range raw {
		var h Handoff
		if ValidName(a) && json.Unmarshal([]byte(v), &h) == nil {
			out[a] = h
		}
	}
	return out, nil
}

// VoidFor reports whether the record no longer stands for snap, the agent's
// current snapshot (ok false: none), and why. A void record is never acted
// on: the agent declared working after it, or its verified process or its
// proved session changed (it cleared or was relaunched by other means).
func (h Handoff) VoidFor(snap AgentSnapshot, ok bool) (bool, string) {
	switch {
	case !ok:
		return true, "no snapshot"
	case snap.PID != h.PID || snap.PIDStart != h.PIDStart:
		return true, "another process"
	case snap.IdentSession != h.Session:
		return true, "another session"
	case snap.State == "working" && snap.TS > h.At:
		return true, "working since"
	}
	return false, ""
}

// handoffDelete: 0 not the holder, else 1; the field is deleted only while it
// still holds the record judged (same report and time).
var handoffDelete = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local raw = redis.call('HGET', KEYS[2], ARGV[2])
if raw then
  local ok, h = pcall(cjson.decode, raw)
  if ok and type(h) == 'table' and h['report'] == ARGV[3] and h['at'] == tonumber(ARGV[4]) then
    redis.call('HDEL', KEYS[2], ARGV[2])
  end
end
return 1`)

// FencedDeleteHandoff deletes agent's record h while token holds the dispatch
// lease and the field still holds h (a newer declaration survives).
func (b *Bus) FencedDeleteHandoff(ctx context.Context, token, agent string, h Handoff) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	n, err := handoffDelete.Run(ctx, b.r, []string{DispatchKey(b.project), HandoffKey(b.project)},
		token, agent, h.Report, h.At).Int()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotHolder
	}
	return nil
}

// ClearMark is the {p}:quota field of an agent's auto-clear (lifecycle part
// B): "<session>|<at>|<report>|<status id>", the session of the record
// cleared, the Redis TIME of its reservation, the hand-off's report (named
// again if the record is gone by the proof) and the status stream's last id
// at the reservation, the boundary a later status is told apart by, to the
// sequence. One clear per session, whatever the dispatcher's restarts.
func ClearMark(agent string) string { return "clear:" + agent }

// ClearReservation is a ClearMark value, read.
type ClearReservation struct {
	Session  string
	At       int64
	Report   string
	StatusID string
}

// ParseClearMark reads a ClearMark value.
func ParseClearMark(v string) (ClearReservation, bool) {
	parts := strings.Split(v, "|")
	if len(parts) != 4 || parts[0] == "" {
		return ClearReservation{}, false
	}
	at, err := strconv.ParseInt(parts[1], 10, 64)
	return ClearReservation{Session: parts[0], At: at, Report: parts[2], StatusID: parts[3]}, err == nil
}

// StatusAfter reports whether agent wrote a status after the stream id after:
// exact to the sequence, so two entries in one millisecond are told apart.
// The stream is capped (~1000), so the scan is bounded, and it fails closed:
// once the boundary entry itself is trimmed away, what followed it may be
// lost too, and that reads as written (reviewer-codex). "0-0" is a boundary
// taken on an empty stream (nothing to trim); "" (no boundary recorded, not
// written by today's reservation) scans everything, also erring on written.
func (b *Bus) StatusAfter(ctx context.Context, agent, after string) (bool, error) {
	key := StreamKey(b.project, "status")
	start := "-"
	if after != "" {
		start = "(" + after
	}
	if after != "" && after != "0-0" {
		kept, err := b.r.XRangeN(ctx, key, after, after, 1).Result()
		if err != nil {
			return false, err
		}
		if len(kept) == 0 {
			return true, nil // the boundary was trimmed: history after it is incomplete
		}
	}
	for {
		msgs, err := b.r.XRangeN(ctx, key, start, "+", 200).Result()
		if err != nil {
			return false, err
		}
		for _, m := range msgs {
			if a, _ := m.Values["agent"].(string); a == agent {
				return true, nil
			}
		}
		if len(msgs) < 200 {
			return false, nil
		}
		start = "(" + msgs[len(msgs)-1].ID
	}
}

// clearReserve: -1 not the holder; 0 the record changed or this session was
// already cleared; else the Redis TIME (ms) of the reservation, with the mark
// set.
var clearReserve = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local raw = redis.call('HGET', KEYS[2], ARGV[2])
if not raw then return 0 end
local ok, h = pcall(cjson.decode, raw)
if not ok or type(h) ~= 'table' or h['report'] ~= ARGV[3] or h['at'] ~= tonumber(ARGV[4]) then return 0 end
local mark = redis.call('HGET', KEYS[3], 'clear:' .. ARGV[2])
if mark and string.sub(mark, 1, #ARGV[5] + 1) == ARGV[5] .. '|' then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local last = redis.call('XREVRANGE', KEYS[4], '+', '-', 'COUNT', 1)
local sid = '0-0'
if #last > 0 then sid = last[1][1] end
redis.call('HSET', KEYS[3], 'clear:' .. ARGV[2], ARGV[5] .. '|' .. now .. '|' .. ARGV[3] .. '|' .. sid)
return now`)

// FencedReserveClear reserves the clear of agent's record h while token holds
// the lease: only while the record stored is still h, and once per h.Session.
// It returns the reservation's Redis TIME and whether it reserved.
func (b *Bus) FencedReserveClear(ctx context.Context, token, agent string, h Handoff) (int64, bool, error) {
	if !ValidName(agent) || !ValidSessionID(h.Session) {
		return 0, false, fmt.Errorf("reserve clear: invalid agent %q or session", agent)
	}
	n, err := clearReserve.Run(ctx, b.r, []string{DispatchKey(b.project), HandoffKey(b.project), QuotaKey(b.project), StreamKey(b.project, "status")},
		token, agent, h.Report, h.At, h.Session).Int64()
	switch {
	case err != nil:
		return 0, false, err
	case n == -1:
		return 0, false, ErrNotHolder
	case n == 0:
		return 0, false, nil
	}
	return n, true, nil
}
