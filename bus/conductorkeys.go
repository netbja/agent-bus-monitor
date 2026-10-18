package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The missions conductor (spec specs/2026-10-11-missions-design.md
// §5.1) holds its own lease, {p}:conductor, and every write it makes goes
// through one of the scripts below, which first check GET {p}:conductor ==
// token. They mirror the dispatcher's fenced writes; a conductor that lost its
// lease writes nothing.

func ConductorKey(project string) string        { return project + ":conductor" }
func ChangeKey(project, change string) string   { return project + ":change:" + change }
func MissionsKey(project, change string) string { return project + ":missions:" + change }
func MissionOpsKey(project string) string       { return project + ":missionops" }

// MissionResultsKey holds the conductor's answers to operator commands. It is
// a separate stream so the conductor's consumer group never reads its own
// results; two colons keep it out of the project scanner.
func MissionResultsKey(project string) string { return project + ":missionops:results" }

// AcquireConductor takes the project's conductor lease (SET NX PX).
func (b *Bus) AcquireConductor(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("empty conductor token")
	}
	return b.r.SetNX(ctx, ConductorKey(b.project), token, ttl).Result()
}

// RenewConductor extends the lease only while token holds it; false = lost.
func (b *Bus) RenewConductor(ctx context.Context, token string, ttl time.Duration) (bool, error) {
	n, err := renewDispatch.Run(ctx, b.r, []string{ConductorKey(b.project)}, token, ttl.Milliseconds()).Int()
	return n == 1, err
}

// ReleaseConductor deletes the lease only if token holds it.
func (b *Bus) ReleaseConductor(ctx context.Context, token string) error {
	return releaseDispatch.Run(ctx, b.r, []string{ConductorKey(b.project)}, token).Err()
}

// ConductorAlive reports whether any conductor holds the project's lease.
func (b *Bus) ConductorAlive(ctx context.Context) (bool, error) {
	n, err := b.r.Exists(ctx, ConductorKey(b.project)).Result()
	return n > 0, err
}

// ServerNowMS is Redis TIME in Unix milliseconds: persisted times never come
// from the local clock.
func (b *Bus) ServerNowMS(ctx context.Context) (int64, error) {
	t, err := b.ServerTime(ctx)
	return t.UnixMilli(), err
}

const fenceCheck = `if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('NOTHOLDER') end
`

func notHolder(err error) error {
	if err != nil && strings.Contains(err.Error(), "NOTHOLDER") {
		return ErrNotHolder
	}
	return err
}

var fencedHSet = redis.NewScript(fenceCheck + `return redis.call('HSET', KEYS[2], ARGV[2], ARGV[3])`)

// FencedHashSet sets one field of a change or missions hash of this project;
// any other key is refused before Redis is touched.
func (b *Bus) FencedHashSet(ctx context.Context, token, key, field, value string) error {
	if !strings.HasPrefix(key, b.project+":change:") && !strings.HasPrefix(key, b.project+":missions:") {
		return fmt.Errorf("FencedHashSet: %q is not a change or missions key of %s", key, b.project)
	}
	return notHolder(fencedHSet.Run(ctx, b.r, []string{ConductorKey(b.project), key}, token, field, value).Err())
}

// fencedPublishRequest is publishRequest (requests.go) behind the lease check:
// KEYS = lease, board, cmd; ARGV = token, task, from, target, ref, body, maxlen.
var fencedPublishRequest = redis.NewScript(fenceCheck + `
if redis.call('EXISTS',KEYS[4])==1 then return redis.error_reply('SWAPPING') end
local raw=redis.call('HGET',KEYS[2],ARGV[2])
if raw then
 local e=cjson.decode(raw)
 local id='';if e.request then id=e.request.id end
 return redis.error_reply('board task already exists: '..ARGV[2]..' request='..id)
end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local id=redis.call('XADD',KEYS[3],'MAXLEN','~',ARGV[7],'*','from',ARGV[3],'target',ARGV[4],'type','directive','ref',ARGV[5],'command',ARGV[6],'task',ARGV[2],'expires_at',0,'text_complete','yes')
local thread=ARGV[5];if thread=='' then thread=id end
local e={owner=ARGV[4],state='requested',updated=math.floor(now/1000),request={id=id,thread=thread,from=ARGV[3],target=ARGV[4],created_at=now,expires_at=0,delivery='queued'}}
redis.call('HSET',KEYS[2],ARGV[2],cjson.encode(e))
return id`)

// FencedRequest is Bus.Request with no TTL, written only under the lease.
func (b *Bus) FencedRequest(ctx context.Context, token, task, from, target, ref, body string) (string, error) {
	if !ValidName(task) || !ValidName(from) || !ValidName(target) {
		return "", fmt.Errorf("invalid task, sender or target")
	}
	if err := ValidateText(body, CmdMaxRunes); err != nil {
		return "", err
	}
	id, err := fencedPublishRequest.Run(ctx, b.r, []string{ConductorKey(b.project), BoardKey(b.project), StreamKey(b.project, "cmd"), SwapKey(b.project, target)},
		token, task, from, target, ref, body, streamMaxLen).Text()
	return id, swapping(notHolder(err), target)
}

var fencedBoardState = redis.NewScript(fenceCheck + `
local raw=redis.call('HGET',KEYS[2],ARGV[2]);if not raw then return redis.error_reply('unknown board task '..ARGV[2]) end
local e=cjson.decode(raw);local t=redis.call('TIME')
e.state=ARGV[3];e.updated=tonumber(t[1])
redis.call('HSET',KEYS[2],ARGV[2],cjson.encode(e))
return 1`)

// FencedBoardState sets a board entry's state, leaving its request metadata.
func (b *Bus) FencedBoardState(ctx context.Context, token, task, state string) error {
	return notHolder(fencedBoardState.Run(ctx, b.r, []string{ConductorKey(b.project), BoardKey(b.project)}, token, task, state).Err())
}

// KEYS = lease, gate, cmd; ARGV = token, ref, meta, maxlen, from, target, message.
var fencedChallenge = redis.NewScript(fenceCheck + `
if redis.call('EXISTS',KEYS[4])==1 or redis.call('EXISTS',KEYS[5])==1 then return redis.error_reply('SWAPPING') end
redis.call('HSET',KEYS[2],ARGV[2],ARGV[3])
return redis.call('XADD',KEYS[3],'MAXLEN','~',ARGV[4],'*','from',ARGV[5],'target',ARGV[6],'type','challenge','ref',ARGV[2],'command',ARGV[7],'text_complete','yes')`)

// FencedChallenge opens a 4-eyes gate on reviewer (meta "<from>|<msg>") and
// publishes the challenge cmd, in one script.
func (b *Bus) FencedChallenge(ctx context.Context, token, reviewer, ref, from, msg string) error {
	if !ValidName(reviewer) || ref == "" {
		return fmt.Errorf("invalid reviewer or empty ref")
	}
	if err := ValidateText(msg, CmdMaxRunes); err != nil {
		return err
	}
	keys := []string{ConductorKey(b.project), GateKey(b.project, reviewer), StreamKey(b.project, "cmd"),
		SwapKey(b.project, reviewer), SwapKey(b.project, from)}
	return swapping(notHolder(fencedChallenge.Run(ctx, b.r, keys, token, ref, from+"|"+msg, streamMaxLen, from, reviewer, msg).Err()),
		reviewer+" or "+from)
}

var fencedHDel = redis.NewScript(fenceCheck + `return redis.call('HDEL', KEYS[2], ARGV[2])`)

// FencedResolveChallenge closes a gate; a ref already closed is not an error.
func (b *Bus) FencedResolveChallenge(ctx context.Context, token, reviewer, ref string) error {
	if !ValidName(reviewer) {
		return fmt.Errorf("invalid reviewer %q", reviewer)
	}
	return notHolder(fencedHDel.Run(ctx, b.r, []string{ConductorKey(b.project), GateKey(b.project, reviewer)}, token, ref).Err())
}

// FencedCmdRef is Bus.Cmd with a ref (and wake=no when noWake), written only
// under the conductor lease.
func (b *Bus) FencedCmdRef(ctx context.Context, token, from, target, typ, ref, command string, noWake bool) (string, error) {
	if err := ValidateText(command, CmdMaxRunes); err != nil {
		return "", err
	}
	if !ValidName(target) {
		return "", fmt.Errorf("invalid target %q", target)
	}
	if !validCmdTypes[typ] {
		return "", fmt.Errorf("invalid cmd type %q", typ)
	}
	fields := []interface{}{"from", from, "target", target, "type", typ, "ref", ref, "command", command, "text_complete", "yes"}
	if noWake {
		fields = append(fields, "wake", "no")
	}
	return b.fencedAddKey(ctx, ConductorKey(b.project), StreamKey(b.project, "cmd"), token, fields...)
}

// MissionOp is one operator command queued for the conductor.
type MissionOp struct {
	ID, From, Op string
	Args         []string
}

// SubmitMissionOp queues an operator command. It is not fenced: the CLI holds
// no lease, and only the conductor ever applies what it reads here.
func (b *Bus) SubmitMissionOp(ctx context.Context, from, op string, args []string) (string, error) {
	if !ValidName(from) {
		return "", fmt.Errorf("invalid sender %q", from)
	}
	a, _ := json.Marshal(args)
	return b.r.XAdd(ctx, &redis.XAddArgs{Stream: MissionOpsKey(b.project), MaxLen: streamMaxLen, Approx: true,
		Values: map[string]interface{}{"from": from, "op": op, "args": string(a)}}).Result()
}

// ReadMissionOps reads new operations for group (created at "0" when
// missing). block <= 0 does not wait.
func (b *Bus) ReadMissionOps(ctx context.Context, group, consumer string, block time.Duration) ([]MissionOp, error) {
	key := MissionOpsKey(b.project)
	if err := b.r.XGroupCreateMkStream(ctx, key, group, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, err
	}
	// Ops a crashed reader took but never acknowledged come first: only one
	// conductor runs per project, so whatever is pending is ours to redo.
	if msgs, _, err := b.r.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: key, Group: group, Consumer: consumer, MinIdle: 0, Start: "0-0", Count: 32}).Result(); err != nil {
		return nil, err
	} else if len(msgs) > 0 {
		return missionOps(msgs), nil
	}
	if block <= 0 {
		block = -1 // go-redis omits BLOCK for a negative duration
	}
	res, err := b.r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{key, ">"}, Count: 32, Block: block}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []MissionOp
	for _, s := range res {
		out = append(out, missionOps(s.Messages)...)
	}
	return out, nil
}

func missionOps(msgs []redis.XMessage) []MissionOp {
	out := make([]MissionOp, 0, len(msgs))
	for _, m := range msgs {
		op := MissionOp{ID: m.ID, From: fmt.Sprint(m.Values["from"]), Op: fmt.Sprint(m.Values["op"])}
		_ = json.Unmarshal([]byte(fmt.Sprint(m.Values["args"])), &op.Args)
		out = append(out, op)
	}
	return out
}

var fencedXAck = redis.NewScript(fenceCheck + `return redis.call('XACK', KEYS[2], ARGV[2], ARGV[3])`)

// AckMissionOp acknowledges an applied operation, only while token holds the
// conductor lease: a conductor that lost it must leave the op to its successor.
func (b *Bus) AckMissionOp(ctx context.Context, token, group, id string) error {
	return notHolder(fencedXAck.Run(ctx, b.r, []string{ConductorKey(b.project), MissionOpsKey(b.project)}, token, group, id).Err())
}

// MissionOpResult records the conductor's answer to operation reqID.
func (b *Bus) MissionOpResult(ctx context.Context, token, reqID string, ok bool, msg string) error {
	okv := "0"
	if ok {
		okv = "1"
	}
	_, err := b.fencedAddKey(ctx, ConductorKey(b.project), MissionResultsKey(b.project), token, "req", reqID, "ok", okv, "msg", msg)
	return err
}

// WaitMissionOpResult waits up to timeout for the answer to reqID. found is
// false on timeout: the operation is queued, not known to be applied.
func (b *Bus) WaitMissionOpResult(ctx context.Context, reqID string, timeout time.Duration) (ok bool, msg string, found bool, err error) {
	deadline := time.Now().Add(timeout)
	cursor := "0"
	// Results are written after the request, possibly in the same millisecond;
	// XREAD is exclusive, so start one millisecond earlier.
	if ms, _, cut := strings.Cut(reqID, "-"); cut {
		if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n > 0 {
			cursor = strconv.FormatInt(n-1, 10) + "-0"
		}
	}
	for {
		block, ok := resultBlock(time.Until(deadline))
		if !ok {
			return false, "", false, nil
		}
		res, err := b.r.XRead(ctx, &redis.XReadArgs{Streams: []string{MissionResultsKey(b.project), cursor}, Count: 64, Block: block}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return false, "", false, err
		}
		for _, s := range res {
			for _, m := range s.Messages {
				cursor = m.ID
				if fmt.Sprint(m.Values["req"]) == reqID {
					return fmt.Sprint(m.Values["ok"]) == "1", fmt.Sprint(m.Values["msg"]), true, nil
				}
			}
		}
	}
}

// ReadMissionHash reads a change or missions hash of this project (any other
// key is refused). Reads need no lease: busmon and the CLI use them too.
func (b *Bus) ReadMissionHash(ctx context.Context, key string) (map[string]string, error) {
	if !strings.HasPrefix(key, b.project+":change:") && !strings.HasPrefix(key, b.project+":missions:") {
		return nil, fmt.Errorf("ReadMissionHash: %q is not a change or missions key of %s", key, b.project)
	}
	return b.r.HGetAll(ctx, key).Result()
}

// ChangeNames lists the project's loaded changes, sorted. A key whose suffix
// is not a valid name is someone else's and is skipped.
func (b *Bus) ChangeNames(ctx context.Context) ([]string, error) {
	prefix := b.project + ":change:"
	var out []string
	iter := b.r.Scan(ctx, 0, prefix+"*", 200).Iterator()
	for iter.Next(ctx) {
		if name := strings.TrimPrefix(iter.Val(), prefix); ValidName(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, iter.Err()
}

// ChangeRecordField is the field of {p}:change:{c} that holds the change record.
const ChangeRecordField = "record"

// KEYS = lease, missions, change; ARGV = token, changeRecord, then mission
// field/value pairs.
var fencedMissionBatch = redis.NewScript(fenceCheck + `
for i = 3, #ARGV, 2 do redis.call('HSET', KEYS[2], ARGV[i], ARGV[i+1]) end
redis.call('HSET', KEYS[3], '` + ChangeRecordField + `', ARGV[2])
return 1`)

// FencedMissionBatch writes several mission records and the change record in
// one script: a reload or a recover is all or nothing.
func (b *Bus) FencedMissionBatch(ctx context.Context, token, change string, missions map[string]string, changeRecord string) error {
	if !ValidName(change) {
		return fmt.Errorf("invalid change %q", change)
	}
	args := []interface{}{token, changeRecord}
	ids := make([]string, 0, len(missions))
	for id := range missions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		args = append(args, id, missions[id])
	}
	return notHolder(fencedMissionBatch.Run(ctx, b.r, []string{ConductorKey(b.project), MissionsKey(b.project, change), ChangeKey(b.project, change)}, args...).Err())
}

// KEYS = lease, cmd stream, change; ARGV = token, maxlen, changeRecord, then
// the cmd field/value pairs. A record already stored means this exact signal
// was sent: a retried script (its reply lost) appends nothing and returns "".
var fencedCmdWithChange = redis.NewScript(fenceCheck + `
if redis.call('HGET', KEYS[3], '` + ChangeRecordField + `') == ARGV[3] then return '' end
local id = redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[2], '*', unpack(ARGV, 4))
redis.call('HSET', KEYS[3], '` + ChangeRecordField + `', ARGV[3])
return id`)

// FencedCmdWithChange appends one cmd entry and writes the change record in
// one script under the conductor lease: a signal sent exactly once with the
// marker that says it was sent (v0.10.1, change complete).
func (b *Bus) FencedCmdWithChange(ctx context.Context, token, change, from, target, typ, command, changeRecord string) (string, error) {
	switch {
	case !ValidName(change):
		return "", fmt.Errorf("invalid change %q", change)
	case !ValidName(target):
		return "", fmt.Errorf("invalid target %q", target)
	case !validCmdTypes[typ]:
		return "", fmt.Errorf("invalid cmd type %q", typ)
	}
	if err := ValidateText(command, CmdMaxRunes); err != nil {
		return "", err
	}
	v, err := fencedCmdWithChange.Run(ctx, b.r, []string{ConductorKey(b.project), StreamKey(b.project, "cmd"), ChangeKey(b.project, change)},
		token, streamMaxLen, changeRecord, "from", from, "target", target, "type", typ, "ref", "", "command", command, "text_complete", "yes").Result()
	if err != nil {
		return "", notHolder(err)
	}
	id, _ := v.(string)
	return id, nil
}

// resultBlock is the XREAD BLOCK for what is left of a wait. BLOCK is in whole
// milliseconds and BLOCK 0 waits forever, so under a millisecond the wait ends.
func resultBlock(left time.Duration) (time.Duration, bool) {
	if left < time.Millisecond {
		return 0, false
	}
	return min(left, time.Second).Truncate(time.Millisecond), true
}

// CmdSpec is one cmd entry of a FencedCmdsWithChange batch.
type CmdSpec struct {
	From, Target, Type, Command string
}

// KEYS = lease, cmd stream, change; ARGV = token, maxlen, changeRecord, then
// from/target/type/command for each cmd. Idempotent like fencedCmdWithChange:
// the record is the marker that the batch was sent.
var fencedCmdsWithChange = redis.NewScript(fenceCheck + `
if redis.call('HGET', KEYS[3], '` + ChangeRecordField + `') == ARGV[3] then return 0 end
for i = 4, #ARGV, 4 do
  redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[2], '*', 'from', ARGV[i], 'target', ARGV[i+1],
    'type', ARGV[i+2], 'ref', '', 'command', ARGV[i+3], 'text_complete', 'yes')
end
redis.call('HSET', KEYS[3], '` + ChangeRecordField + `', ARGV[3])
return 1`)

// FencedCmdsWithChange appends several cmd entries and writes the change
// record in one script under the conductor lease: change complete plus the
// docs agent's directive, sent together and exactly once with their marker.
func (b *Bus) FencedCmdsWithChange(ctx context.Context, token, change string, cmds []CmdSpec, changeRecord string) error {
	if !ValidName(change) {
		return fmt.Errorf("invalid change %q", change)
	}
	args := []interface{}{token, streamMaxLen, changeRecord}
	for _, c := range cmds {
		switch {
		case !ValidName(c.Target):
			return fmt.Errorf("invalid target %q", c.Target)
		case !validCmdTypes[c.Type]:
			return fmt.Errorf("invalid cmd type %q", c.Type)
		}
		if err := ValidateText(c.Command, CmdMaxRunes); err != nil {
			return err
		}
		args = append(args, c.From, c.Target, c.Type, c.Command)
	}
	return notHolder(fencedCmdsWithChange.Run(ctx, b.r, []string{ConductorKey(b.project), StreamKey(b.project, "cmd"), ChangeKey(b.project, change)}, args...).Err())
}
