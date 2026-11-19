package bus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The CI loop's record and its one writer (spec
// docs/superpowers/specs/2026-11-19-ci-loop-design.md §4 "Writes"): {p}:ci,
// one field per PR, pr:<n> = digest|count|stopped|done. An action (a
// hand-off or a stop) is written by one fenced script that records each
// part as it writes it, so a retry writes only what is missing: at least
// once, a duplicate harmless and bounded, never exactly once.

// CIKey is the loop's record, runtime (a backup does not restore it, §8 Q9).
func CIKey(project string) string { return project + ":ci" }

// What FencedCIStep did.
const (
	CISent          = "sent"           // a hand-off's parts written (or finished)
	CIStopped       = "stopped"        // the stop's parts written (or finished)
	CISame          = "same"           // the episode's action is complete: nothing written
	CIStoppedBefore = "stopped-before" // the PR was stopped: nothing written, whatever the episode
)

// ErrCIBadType: {p}:ci, the cmd stream or the notify stream holds another
// type; the script wrote nothing.
var ErrCIBadType = errors.New("ci: a key the loop writes holds another type")

// CIStep is one PR's action as the pass asks for it. The hand-off texts are
// split around the hand-off's ordinal, which the script decides.
type CIStep struct {
	PR            int
	Digest        string // the episode's digest (hex sha1)
	Cap           int    // ci.max_handoffs
	Owner, Master string
	Handoff       [2]string // the owner's directive: [0] + ordinal + [1]
	MasterCopy    [2]string // master's copy (wake=no), the same way
	StopMaster    string    // master's directive at the stop (wake=no)
	StopNotice    string    // the person's notice at the stop
}

// ciStepScript, in order (§4 Writes): the lease; the types; the field
// (stopped: finish its own stop or nothing; the same complete episode:
// nothing); the swaps of the missing parts' recipients; the decision, once
// per episode, written before any entry; each missing part, XADD then its
// done mark.
//
// KEYS: dispatch lease, {p}:ci, cmd, notify, swap:<owner>, swap:<master>.
// ARGV: token, field, digest, cap, owner, master, owner-is-master, maxlen,
// episode, handoff pre/post, copy pre/post, stop master, stop notice.
var ciStepScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 'not-holder' end
local t = redis.call('TYPE', KEYS[2]).ok
if t ~= 'hash' and t ~= 'none' then return 'bad-type' end
for i = 3, 4 do
  t = redis.call('TYPE', KEYS[i]).ok
  if t ~= 'stream' and t ~= 'none' then return 'bad-type' end
end
local dig, count, stopped, done = ARGV[3], 0, '0', ''
local new = true
local v = redis.call('HGET', KEYS[2], ARGV[2])
if v then
  local d, c, s, dn = string.match(v, '^([^|]*)|([^|]*)|([^|]*)|(.*)$')
  if d then
    count = tonumber(c) or 0
    if s == '1' then
      if d ~= dig then return 'stopped-before' end
      stopped, done, new = '1', dn, false
    elseif d == dig then
      done, new = dn, false
    end
  end
end
if new then
  if count >= tonumber(ARGV[4]) then stopped = '1' else stopped = '0'; count = count + 1 end
end
local parts
if stopped == '1' then parts = {'m', 'n'} elseif ARGV[7] == '1' then parts = {'o'} else parts = {'o', 'm'} end
local missing = {}
for _, p in ipairs(parts) do
  if not string.find(done, p, 1, true) then table.insert(missing, p) end
end
if #missing == 0 then
  if stopped == '1' then return 'stopped-before' end
  return 'same'
end
for _, p in ipairs(missing) do
  if p == 'o' and redis.call('EXISTS', KEYS[5]) == 1 then return 'swap' end
  if p == 'm' and redis.call('EXISTS', KEYS[6]) == 1 then return 'swap' end
end
local val = function() return dig .. '|' .. count .. '|' .. stopped .. '|' .. done end
if new then redis.call('HSET', KEYS[2], ARGV[2], val()) end
local ord = tostring(count)
for _, p in ipairs(missing) do
  if p == 'o' then
    redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[8], '*', 'from', 'dispatch', 'target', ARGV[5], 'type', 'directive', 'ref', '',
      'command', ARGV[10] .. ord .. ARGV[11], 'text_complete', 'yes', 'episode', ARGV[9])
  elseif p == 'm' and stopped == '1' then
    redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[8], '*', 'from', 'dispatch', 'target', ARGV[6], 'type', 'directive', 'ref', '',
      'command', ARGV[14], 'text_complete', 'yes', 'episode', ARGV[9], 'wake', 'no')
  elseif p == 'm' then
    redis.call('XADD', KEYS[3], 'MAXLEN', '~', ARGV[8], '*', 'from', 'dispatch', 'target', ARGV[6], 'type', 'directive', 'ref', '',
      'command', ARGV[12] .. ord .. ARGV[13], 'text_complete', 'yes', 'episode', ARGV[9], 'wake', 'no')
  else
    redis.call('XADD', KEYS[4], 'MAXLEN', '~', ARGV[8], '*', 'from', 'dispatch', 'message', ARGV[15], 'for', 'person')
  end
  done = done .. p
  redis.call('HSET', KEYS[2], ARGV[2], val())
end
if stopped == '1' then return 'stopped' end
return 'sent'`)

// FencedCIStep writes one PR's action, fenced by the dispatch lease. A
// hand-off to an owner that is blocked or waiting, or a PR the base holds,
// is decided by the caller, which does not call it.
func (b *Bus) FencedCIStep(ctx context.Context, token string, s CIStep) (string, error) {
	if s.PR <= 0 || len(s.Digest) != 40 || s.Cap < 0 {
		return "", fmt.Errorf("ci step: invalid PR, digest or cap")
	}
	if !ValidName(s.Owner) || !ValidName(s.Master) {
		return "", fmt.Errorf("ci step: invalid owner %q or master %q", s.Owner, s.Master)
	}
	for _, text := range []string{s.Handoff[0] + "999" + s.Handoff[1], s.MasterCopy[0] + "999" + s.MasterCopy[1], s.StopMaster, s.StopNotice} {
		if err := ValidateText(text, CmdMaxRunes); err != nil {
			return "", err
		}
	}
	ownerIsMaster := "0"
	if s.Owner == s.Master {
		ownerIsMaster = "1"
	}
	field := "pr:" + strconv.Itoa(s.PR)
	v, err := ciStepScript.Run(ctx, b.r,
		[]string{DispatchKey(b.project), CIKey(b.project), StreamKey(b.project, "cmd"), StreamKey(b.project, "notify"),
			SwapKey(b.project, s.Owner), SwapKey(b.project, s.Master)},
		token, field, s.Digest, s.Cap, s.Owner, s.Master, ownerIsMaster, streamMaxLen, "ci:"+field+"="+s.Digest,
		s.Handoff[0], s.Handoff[1], s.MasterCopy[0], s.MasterCopy[1], s.StopMaster, s.StopNotice).Text()
	if err != nil {
		return "", err
	}
	switch v {
	case "not-holder":
		return "", ErrNotHolder
	case "swap":
		return "", ErrSwapInProgress
	case "bad-type":
		return "", ErrCIBadType
	}
	return v, nil
}

// CIEntry is one PR's field of {p}:ci.
type CIEntry struct {
	Digest  string
	Count   int
	Stopped bool
	Done    string
}

// CIRecord reads {p}:ci, PR number → its field; a field that does not parse
// is left out (the script treats it as absent too).
func (b *Bus) CIRecord(ctx context.Context) (map[int]CIEntry, error) {
	h, err := b.r.HGetAll(ctx, CIKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := map[int]CIEntry{}
	for k, v := range h {
		n, err := strconv.Atoi(strings.TrimPrefix(k, "pr:"))
		parts := strings.SplitN(v, "|", 4)
		if !strings.HasPrefix(k, "pr:") || err != nil || n <= 0 || len(parts) != 4 {
			continue
		}
		c, _ := strconv.Atoi(parts[1])
		out[n] = CIEntry{Digest: parts[0], Count: c, Stopped: parts[2] == "1", Done: parts[3]}
	}
	return out, nil
}

// ciForgetScript deletes the given fields, fenced.
var ciForgetScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
for i = 2, #ARGV do redis.call('HDEL', KEYS[2], ARGV[i]) end
return 1`)

// FencedCIForget deletes the fields of PRs no longer open (merged or
// closed): a reopened PR starts at zero (§4).
func (b *Bus) FencedCIForget(ctx context.Context, token string, prs []int) error {
	if len(prs) == 0 {
		return nil
	}
	args := []interface{}{token}
	for _, n := range prs {
		args = append(args, "pr:"+strconv.Itoa(n))
	}
	n, err := ciForgetScript.Run(ctx, b.r, []string{DispatchKey(b.project), CIKey(b.project)}, args...).Int()
	if err != nil {
		return err
	}
	if n == -1 {
		return ErrNotHolder
	}
	return nil
}

// ResetCI deletes one PR's field (the person's agentbus ci reset, §5): no
// lease, as ResetRun; it reports whether there was one.
func (b *Bus) ResetCI(ctx context.Context, pr int) (bool, error) {
	if pr <= 0 {
		return false, fmt.Errorf("ci reset: invalid PR number %d", pr)
	}
	n, err := b.r.HDel(ctx, CIKey(b.project), "pr:"+strconv.Itoa(pr)).Result()
	return n == 1, err
}
