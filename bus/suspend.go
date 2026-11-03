package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// A suspend (spec docs/superpowers/specs/2026-11-03-suspend-resume-design.md)
// is a pause of kind "suspend": the project sleeps until a person resumes it.
// It has no deadline in meaning; its Until is far ahead only so that a
// dispatcher of an older release, which does not know the kind, never ends it.

// PauseKindSuspend is PauseRecord.Kind for a suspend.
const PauseKindSuspend = "suspend"

// suspendHorizon is a suspend's Until, counted from its At.
const suspendHorizon = 365 * 24 * time.Hour

var (
	// ErrSuspended: a timed pause or its end was asked of a suspended project.
	ErrSuspended = errors.New("project suspended: agentbus resume brings it back")
	// ErrNotSuspended: a suspend's end was asked of a record that is not one.
	ErrNotSuspended = errors.New("the project is not suspended")
	// ErrPauseChanged: the pause record changed since it was read.
	ErrPauseChanged = errors.New("the pause record changed since it was read")
	// ErrPaused: no wake is reserved while the project is paused.
	ErrPaused = errors.New("the project is paused")
)

// Suspended reports whether the record is a suspend.
func (p PauseRecord) Suspended() bool { return p.Kind == PauseKindSuspend }

// SuspendSpec is what the person's suspend writes: who, whom the resume
// directive goes to, why, the host and its boot, and the services' state read
// before anything is stopped.
type SuspendSpec struct {
	By, Notify, Reason, Host, Boot string
	Services                       map[string]bool
}

// suspendScript writes the suspend in one step: an existing suspend is left
// and returned (0); a timed pause becomes its prev; at and until come from
// Redis TIME (1).
var suspendScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
local prev = nil
if v then
  local ok, p = pcall(cjson.decode, v)
  if ok and type(p) == 'table' then
    if p['kind'] == 'suspend' then return {0, v} end
    prev = p
  end
end
local r = cjson.decode(ARGV[1])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
r['at'] = now
r['until'] = now + tonumber(ARGV[2])
if prev then r['prev'] = prev end
local out = cjson.encode(r)
redis.call('SET', KEYS[1], out)
return {1, out}`)

// Suspend puts the project to sleep. It returns the stored record and its raw
// text (what Unsuspend and SuspendCancel compare against), and whether a
// suspend was already there, in which case nothing was written.
func (b *Bus) Suspend(ctx context.Context, s SuspendSpec) (PauseRecord, string, bool, error) {
	if !ValidName(s.By) || !ValidName(s.Notify) {
		return PauseRecord{}, "", false, fmt.Errorf("invalid name %q or %q", s.By, s.Notify)
	}
	if err := ValidateText(s.Reason, CmdMaxRunes); err != nil {
		return PauseRecord{}, "", false, err
	}
	in, _ := json.Marshal(PauseRecord{Kind: PauseKindSuspend, By: s.By, Notify: s.Notify, Reason: s.Reason,
		Host: s.Host, Boot: s.Boot, Services: s.Services})
	res, err := suspendScript.Run(ctx, b.r, []string{PauseKey(b.project)}, in, suspendHorizon.Milliseconds()).Slice()
	if err != nil {
		return PauseRecord{}, "", false, err
	}
	raw, _ := res[1].(string)
	var p PauseRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return PauseRecord{}, raw, false, fmt.Errorf("%s: %w", PauseKey(b.project), err)
	}
	code, _ := res[0].(int64)
	return p, raw, code == 0, nil
}

// unsuspendScript ends the suspend read as ARGV[1]: -1 the record changed;
// -2 not a suspend; 1 its previous timed pause, still due, put back (no
// directive); 2 deleted, with the directive XADDed when a target is given.
var unsuspendScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v ~= ARGV[1] then return {-1} end
local p = cjson.decode(v)
if p['kind'] ~= 'suspend' then return {-2} end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local prev = p['prev']
if type(prev) == 'table' and (tonumber(prev['until']) or 0) > now then
  redis.call('SET', KEYS[1], cjson.encode(prev))
  return {1}
end
redis.call('DEL', KEYS[1])
if ARGV[3] == '' then return {2, ''} end
return {2, redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[2], '*', unpack(ARGV, 3))}`)

// Unsuspend ends the suspend whose record was read as raw, from the person by:
// its previous timed pause comes back when it is still due (restored, and no
// directive), else the pause is gone and the record's notify target gets the
// resume directive with message, as a person's (human=yes) waking entry.
func (b *Bus) Unsuspend(ctx context.Context, raw, by, message string) (restored bool, id string, err error) {
	return b.unsuspend(ctx, raw, by, func(p PauseRecord) string {
		s := "Project resumed after a suspend"
		if p.Reason != "" {
			s += " (" + p.Reason + ")"
		}
		s += ". Resume the team's work."
		if message != "" {
			s += " " + message
		}
		return s
	})
}

// SuspendCancel ends a suspend that did not complete (its wait ran out, or it
// was interrupted), the same way as Unsuspend, with why in the directive.
func (b *Bus) SuspendCancel(ctx context.Context, raw, by, why string) (restored bool, id string, err error) {
	return b.unsuspend(ctx, raw, by, func(PauseRecord) string {
		s := "The suspend was cancelled"
		if why != "" {
			s += " (" + why + ")"
		}
		return s + ": the project runs again. Resume the team's work."
	})
}

func (b *Bus) unsuspend(ctx context.Context, raw, by string, text func(PauseRecord) string) (bool, string, error) {
	if !ValidName(by) {
		return false, "", fmt.Errorf("invalid sender %q", by)
	}
	var p PauseRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil || !p.Suspended() {
		return false, "", ErrNotSuspended
	}
	args := []any{raw, streamMaxLen}
	if ValidName(p.Notify) {
		command := text(p)
		if err := ValidateText(command, CmdMaxRunes); err != nil {
			return false, "", err
		}
		args = append(args, "from", by, "target", p.Notify, "type", CmdDirective, "ref", "",
			"command", command, "text_complete", "yes", "human", "yes")
	} else {
		args = append(args, "")
	}
	res, err := unsuspendScript.Run(ctx, b.r, []string{PauseKey(b.project), StreamKey(b.project, "cmd")}, args...).Slice()
	if err != nil {
		return false, "", err
	}
	code, _ := res[0].(int64)
	switch code {
	case -1:
		return false, "", ErrPauseChanged
	case -2:
		return false, "", ErrNotSuspended
	case 1:
		return true, "", nil
	}
	id, _ := res[1].(string)
	return false, id, nil
}

