package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// A project pause (v0.12.5): nobody is woken until a deadline, then the
// dispatcher sends one resume directive. It exists for quota windows: agents
// stop, and the resume is a fact on the bus that busmon can show, instead of
// a sleep inside one agent's session.

// PauseKey is the {p}:pause string (a JSON PauseRecord); absent = not paused.
func PauseKey(project string) string { return project + ":pause" }

// PauseRecord is a pause: until (Unix ms, from Redis TIME), why, who set it,
// and who receives the resume directive.
type PauseRecord struct {
	Until  int64  `json:"until"`
	Reason string `json:"reason,omitempty"`
	By     string `json:"by"`
	Notify string `json:"notify"`
	At     int64  `json:"at"`
}

// UntilTime is the deadline as a time.
func (p PauseRecord) UntilTime() time.Time { return time.UnixMilli(p.Until) }

// SetPause pauses the project until the deadline. A later call replaces it.
func (b *Bus) SetPause(ctx context.Context, by, notify, reason string, until time.Time) (PauseRecord, error) {
	if !ValidName(by) || !ValidName(notify) {
		return PauseRecord{}, fmt.Errorf("invalid name %q or %q", by, notify)
	}
	if err := ValidateText(reason, CmdMaxRunes); err != nil {
		return PauseRecord{}, err
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		return PauseRecord{}, err
	}
	if !until.After(now) {
		return PauseRecord{}, fmt.Errorf("the pause must end in the future (server time %s)", now.Format(time.RFC3339))
	}
	p := PauseRecord{Until: until.UnixMilli(), Reason: reason, By: by, Notify: notify, At: now.UnixMilli()}
	v, _ := json.Marshal(p)
	return p, b.r.Set(ctx, PauseKey(b.project), v, 0).Err()
}

// Pause reads the project's pause; nil when it is not paused.
func (b *Bus) Pause(ctx context.Context) (*PauseRecord, error) {
	v, err := b.r.Get(ctx, PauseKey(b.project)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p PauseRecord
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return nil, fmt.Errorf("%s: %w", PauseKey(b.project), err)
	}
	return &p, nil
}

// Resume ends a pause now: it deletes the record and sends its notify target
// the resume directive, from by. It reports false when nothing was paused.
func (b *Bus) Resume(ctx context.Context, by, message string) (bool, error) {
	if !ValidName(by) {
		return false, fmt.Errorf("invalid sender %q", by)
	}
	v, err := b.r.GetDel(ctx, PauseKey(b.project)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var p PauseRecord
	_ = json.Unmarshal([]byte(v), &p)
	if !ValidName(p.Notify) {
		return true, nil
	}
	_, err = b.cmd(ctx, by, p.Notify, CmdDirective, "", resumeText(p, message), nil)
	return true, err
}

func resumeText(p PauseRecord, message string) string {
	s := "Project resumed: the pause"
	if p.Reason != "" {
		s += " (" + p.Reason + ")"
	}
	s += " is over. Resume the team's work."
	if message != "" {
		s += " " + message
	}
	return s
}

// fencedResume: -1 not the dispatch holder; 0 nothing to do (no pause, a
// different pause, or not yet due); else the resume entry id. One script, so a
// pause replaced while the dispatcher looked is never ended by it.
var fencedResume = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local v = redis.call('GET', KEYS[2])
if not v or v ~= ARGV[2] then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
if now < tonumber(ARGV[3]) then return 0 end
redis.call('DEL', KEYS[2])
return redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[4], '*', unpack(ARGV, 5))`)

// FencedResume ends a due pause from the dispatcher (holding token): it
// deletes the record and sends the resume directive in one script. "" = not
// due or not this pause; ErrNotHolder when the lease is lost.
func (b *Bus) FencedResume(ctx context.Context, token, from string) (string, error) {
	raw, err := b.r.Get(ctx, PauseKey(b.project)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var p PauseRecord
	if err := json.Unmarshal([]byte(raw), &p); err != nil || !ValidName(p.Notify) {
		return "", fmt.Errorf("%s: unreadable pause record", PauseKey(b.project))
	}
	res, err := fencedResume.Run(ctx, b.r,
		[]string{DispatchKey(b.project), PauseKey(b.project), StreamKey(b.project, "cmd")},
		token, raw, strconv.FormatInt(p.Until, 10), streamMaxLen,
		"from", from, "target", p.Notify, "type", CmdDirective, "ref", "", "command", resumeText(p, ""), "text_complete", "yes").Result()
	if err != nil {
		return "", err
	}
	switch v := res.(type) {
	case int64:
		if v == -1 {
			return "", ErrNotHolder
		}
		return "", nil
	case string:
		return v, nil
	}
	return "", fmt.Errorf("fencedResume: unexpected %T", res)
}
