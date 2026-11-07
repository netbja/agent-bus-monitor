package bus

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Entries written once per episode (v0.15 §3.3): the entry and its mark in
// {p}:quota go together in one script, so a pass that crashes between two
// recipients resends only the missing ones, and a flapping release sends
// nothing more.

// fencedEntryOnce: -1 not the holder; 0 the mark already holds ARGV[3] (sent
// in this episode); 2 the recipient is being swapped (KEYS[4] exists: the
// swap sends it, v0.15 §3.3); 1 XADDed and marked.
var fencedEntryOnce = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('EXISTS', KEYS[4]) == 1 then return 2 end
if redis.call('HGET', KEYS[2], ARGV[2]) == ARGV[3] then return 0 end
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[4], '*', unpack(ARGV, 5))
redis.call('HSET', KEYS[2], ARGV[2], ARGV[3])
return 1`)

func (b *Bus) fencedEntryOnce(ctx context.Context, token, kind, mark, episode, recipient string, fields ...interface{}) (bool, error) {
	if mark == "" || episode == "" {
		return false, fmt.Errorf("entry once: mark and episode required")
	}
	swapKey := b.project + ":swap:" // no role: never exists
	if recipient != "" {
		swapKey = SwapKey(b.project, recipient)
	}
	args := append([]interface{}{token, mark, episode, streamMaxLen}, fields...)
	n, err := fencedEntryOnce.Run(ctx, b.r,
		[]string{DispatchKey(b.project), QuotaKey(b.project), StreamKey(b.project, kind), swapKey}, args...).Int()
	if err != nil {
		return false, err
	}
	switch n {
	case -1:
		return false, ErrNotHolder
	case 2:
		return false, ErrSwapInProgress
	}
	return n == 1, nil
}

// FencedCmdOnce writes a dispatch cmd (empty ref) unless {p}:quota's mark
// already holds episode, and sets the mark in the same script. noWake writes
// wake=no. It reports whether it wrote.
func (b *Bus) FencedCmdOnce(ctx context.Context, token, mark, episode, from, target, typ, command string, noWake bool) (bool, error) {
	fields, err := onceCmdFields(mark, episode, from, target, typ, command, noWake)
	if err != nil {
		return false, err
	}
	return b.fencedEntryOnce(ctx, token, "cmd", mark, episode, target, fields...)
}

// onceCmdFields checks a once cmd and builds its entry.
func onceCmdFields(mark, episode, from, target, typ, command string, noWake bool) ([]interface{}, error) {
	if err := ValidateText(command, CmdMaxRunes); err != nil {
		return nil, err
	}
	if !ValidName(target) {
		return nil, fmt.Errorf("invalid target %q", target)
	}
	if !validCmdTypes[typ] {
		return nil, fmt.Errorf("invalid cmd type %q", typ)
	}
	// The episode travels with the entry, so a swap can tell this episode's
	// resume from an older one still in the queue.
	fields := []interface{}{"from", from, "target", target, "type", typ, "ref", "", "command", command, "text_complete", "yes",
		"episode", mark + "=" + episode}
	if noWake {
		fields = append(fields, "wake", "no")
	}
	return fields, nil
}

// fencedEntryOnceMarks is fencedEntryOnce that also sets the extra marks
// ARGV[6..5+n] to the episode, in the same script, only when it writes:
// -1, 0, 2 and 1 as there.
var fencedEntryOnceMarks = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('EXISTS', KEYS[4]) == 1 then return 2 end
if redis.call('HGET', KEYS[2], ARGV[2]) == ARGV[3] then return 0 end
local n = tonumber(ARGV[5])
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[4], '*', unpack(ARGV, 6 + n))
redis.call('HSET', KEYS[2], ARGV[2], ARGV[3])
for i = 1, n do redis.call('HSET', KEYS[2], ARGV[5 + i], ARGV[3]) end
return 1`)

// FencedCmdOnceMarks is FencedCmdOnce that also sets extra marks to episode
// when it writes (auto-reload: the agents an update's directive already
// covers get no later hint). Nothing is marked when nothing is written.
func (b *Bus) FencedCmdOnceMarks(ctx context.Context, token, mark, episode string, extra []string, from, target, typ, command string, noWake bool) (bool, error) {
	if mark == "" || episode == "" {
		return false, fmt.Errorf("entry once: mark and episode required")
	}
	fields, err := onceCmdFields(mark, episode, from, target, typ, command, noWake)
	if err != nil {
		return false, err
	}
	args := []interface{}{token, mark, episode, streamMaxLen, len(extra)}
	for _, m := range extra {
		args = append(args, m)
	}
	args = append(args, fields...)
	n, err := fencedEntryOnceMarks.Run(ctx, b.r,
		[]string{DispatchKey(b.project), QuotaKey(b.project), StreamKey(b.project, "cmd"), SwapKey(b.project, target)}, args...).Int()
	if err != nil {
		return false, err
	}
	switch n {
	case -1:
		return false, ErrNotHolder
	case 2:
		return false, ErrSwapInProgress
	}
	return n == 1, nil
}

// FencedNotifyOnceForPerson is FencedNotifyOnce with for=person, as fenced.
func (b *Bus) FencedNotifyOnceForPerson(ctx context.Context, token, mark, episode, from, message string) (bool, error) {
	if err := ValidateText(message, CmdMaxRunes); err != nil {
		return false, err
	}
	return b.fencedEntryOnce(ctx, token, "notify", mark, episode, "", "from", from, "message", message, "for", ForPerson)
}

// FencedNotifyOnce is FencedCmdOnce for the notify stream.
func (b *Bus) FencedNotifyOnce(ctx context.Context, token, mark, episode, from, message string) (bool, error) {
	if err := ValidateText(message, CmdMaxRunes); err != nil {
		return false, err
	}
	return b.fencedEntryOnce(ctx, token, "notify", mark, episode, "", "from", from, "message", message)
}
