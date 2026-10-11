package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The missions conductor (spec docs/superpowers/specs/2026-10-11-missions-design.md
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
	id, err := fencedPublishRequest.Run(ctx, b.r, []string{ConductorKey(b.project), BoardKey(b.project), StreamKey(b.project, "cmd")},
		token, task, from, target, ref, body, streamMaxLen).Text()
	return id, notHolder(err)
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
	return notHolder(fencedChallenge.Run(ctx, b.r, []string{ConductorKey(b.project), GateKey(b.project, reviewer), StreamKey(b.project, "cmd")},
		token, ref, from+"|"+msg, streamMaxLen, from, reviewer, msg).Err())
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
		for _, m := range s.Messages {
			op := MissionOp{ID: m.ID, From: fmt.Sprint(m.Values["from"]), Op: fmt.Sprint(m.Values["op"])}
			_ = json.Unmarshal([]byte(fmt.Sprint(m.Values["args"])), &op.Args)
			out = append(out, op)
		}
	}
	return out, nil
}

// AckMissionOp acknowledges an applied operation.
func (b *Bus) AckMissionOp(ctx context.Context, group, id string) error {
	return b.r.XAck(ctx, MissionOpsKey(b.project), group, id).Err()
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
		left := time.Until(deadline)
		if left <= 0 {
			return false, "", false, nil
		}
		res, err := b.r.XRead(ctx, &redis.XReadArgs{Streams: []string{MissionResultsKey(b.project), cursor}, Count: 64, Block: min(left, time.Second)}).Result()
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
