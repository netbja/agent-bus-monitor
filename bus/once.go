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
// in this episode); 1 XADDed and marked.
var fencedEntryOnce = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if redis.call('HGET', KEYS[2], ARGV[2]) == ARGV[3] then return 0 end
redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[4], '*', unpack(ARGV, 5))
redis.call('HSET', KEYS[2], ARGV[2], ARGV[3])
return 1`)

func (b *Bus) fencedEntryOnce(ctx context.Context, token, kind, mark, episode string, fields ...interface{}) (bool, error) {
	if mark == "" || episode == "" {
		return false, fmt.Errorf("entry once: mark and episode required")
	}
	args := append([]interface{}{token, mark, episode, streamMaxLen}, fields...)
	n, err := fencedEntryOnce.Run(ctx, b.r,
		[]string{DispatchKey(b.project), QuotaKey(b.project), StreamKey(b.project, kind)}, args...).Int()
	if err != nil {
		return false, err
	}
	if n == -1 {
		return false, ErrNotHolder
	}
	return n == 1, nil
}

// FencedCmdOnce writes a dispatch cmd (empty ref) unless {p}:quota's mark
// already holds episode, and sets the mark in the same script. noWake writes
// wake=no. It reports whether it wrote.
func (b *Bus) FencedCmdOnce(ctx context.Context, token, mark, episode, from, target, typ, command string, noWake bool) (bool, error) {
	if err := ValidateText(command, CmdMaxRunes); err != nil {
		return false, err
	}
	if !ValidName(target) {
		return false, fmt.Errorf("invalid target %q", target)
	}
	if !validCmdTypes[typ] {
		return false, fmt.Errorf("invalid cmd type %q", typ)
	}
	fields := []interface{}{"from", from, "target", target, "type", typ, "ref", "", "command", command, "text_complete", "yes"}
	if noWake {
		fields = append(fields, "wake", "no")
	}
	return b.fencedEntryOnce(ctx, token, "cmd", mark, episode, fields...)
}

// FencedNotifyOnce is FencedCmdOnce for the notify stream.
func (b *Bus) FencedNotifyOnce(ctx context.Context, token, mark, episode, from, message string) (bool, error) {
	if err := ValidateText(message, CmdMaxRunes); err != nil {
		return false, err
	}
	return b.fencedEntryOnce(ctx, token, "notify", mark, episode, "from", from, "message", message)
}
