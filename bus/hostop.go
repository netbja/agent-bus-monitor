package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// The relay of host commands (herdr exit, spec docs/superpowers/specs/2026-11-10-herdr-exit-design.md
// §4.2, "Who may drive the holder"). The holder refuses Start, Close and
// Submit from inside an agent's terminal, so a host command run there (up,
// the pop, relaunch, reload, swap, suspend, resume, retire, holder drain)
// sends the dispatcher a host directive instead: a closed set of names on
// {p}:cmd, never argv or text. The dispatcher accepts it only within
// HostAcceptWindow of its entry id, by Redis TIME, in one fenced script
// that writes the accepted reply in the same step; it then runs the
// operation on one serial worker and ends it with one outcome reply. Each
// directive's state is kept in {p}:hostops, so an accepted operation the
// dispatcher never finished is reported at its next start, never run late.

// CmdHost is a host directive's cmd type.
const CmdHost = "host"

// HostTarget is the directive's target: the dispatch service.
const HostTarget = "dispatch"

// HostAcceptWindow: a directive is accepted only this soon after its entry
// id (Redis time), or never.
const HostAcceptWindow = 5 * time.Second

// The host operations, a closed set.
const (
	HostUp       = "up"
	HostPop      = "pop"
	HostRelaunch = "relaunch"
	HostReload   = "reload"
	HostSwap     = "swap"
	HostSuspend  = "suspend"
	HostResume   = "resume"
	HostRetire   = "retire"
	HostDrain    = "drain"
)

// hostOps: each op, and whether it names an agent (the others are the
// project's).
var hostOps = map[string]bool{
	HostUp: false, HostSuspend: false, HostResume: false, HostDrain: false,
	HostPop: true, HostRelaunch: true, HostReload: true, HostSwap: true, HostRetire: true,
}

// IsHostOp reports whether op is one of the set.
func IsHostOp(op string) bool { _, ok := hostOps[op]; return ok }

// HostOp is one host directive. ID is its entry id once written or read.
type HostOp struct {
	ID               string
	Op               string
	Agent            string // the role it acts on; "" for the project's ops
	Profile          string // swap and pop only: the profile asked for
	IgnoreBackground bool   // honoured only from master or the person (§4.3b), at run time
	From             string // who sent it: self-attested, as every bus entry
}

// Validate is the closed set: a known op, an agent exactly when the op
// takes one, a profile only for swap and pop, every name a ValidName.
func (o HostOp) Validate() error {
	takesAgent, ok := hostOps[o.Op]
	switch {
	case !ok:
		return fmt.Errorf("host: %q is not a host operation", o.Op)
	case !ValidName(o.From):
		return fmt.Errorf("host: invalid sender %q", o.From)
	case takesAgent && !ValidName(o.Agent):
		return fmt.Errorf("host: %s needs an agent, got %q", o.Op, o.Agent)
	case !takesAgent && o.Agent != "":
		return fmt.Errorf("host: %s takes no agent", o.Op)
	case o.Profile != "" && o.Op != HostSwap && o.Op != HostPop:
		return fmt.Errorf("host: only swap and pop take a profile")
	case o.Profile != "" && !ValidName(o.Profile):
		return fmt.Errorf("host: invalid profile %q", o.Profile)
	}
	return nil
}

// describe is the directive's command field: built from validated names
// only, for a person reading the stream; never parsed.
func (o HostOp) describe() string {
	s := "host " + o.Op
	if o.Agent != "" {
		s += " " + o.Agent
	}
	if o.Profile != "" {
		s += " --profile " + o.Profile
	}
	if o.IgnoreBackground {
		s += " --ignore-background"
	}
	return s
}

// SendHostOp writes a host directive (the command's side, from inside an
// agent's terminal): wake=no, the dispatch service reads it on its own.
func (b *Bus) SendHostOp(ctx context.Context, o HostOp) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	ib := ""
	if o.IgnoreBackground {
		ib = "yes"
	}
	return b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), MaxLen: streamMaxLen, Approx: true,
		Values: []interface{}{"from", o.From, "target", HostTarget, "type", CmdHost, "ref", "", "command", o.describe(),
			"op", o.Op, "agent", o.Agent, "profile", o.Profile, "ignore_background", ib, "text_complete", "yes", "wake", "no"}}).Result()
}

// HostOpsAfter reads the host directives written after id (the dispatcher's
// side, unvalidated: a directive outside the set is refused, not dropped),
// and the last entry id read, host or not.
func (b *Bus) HostOpsAfter(ctx context.Context, id string) ([]HostOp, string, error) {
	evs, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "("+id, "+").Result()
	if err != nil {
		return nil, id, err
	}
	var out []HostOp
	last := id
	for _, e := range evs {
		last = e.ID
		f := toStringMap(e.Values)
		if f["type"] != CmdHost || f["target"] != HostTarget {
			continue
		}
		out = append(out, HostOp{ID: e.ID, Op: f["op"], Agent: f["agent"], Profile: f["profile"],
			IgnoreBackground: f["ignore_background"] == "yes", From: f["from"]})
	}
	return out, last, nil
}

// HostOpsKey is {p}:hostops: each directive's state, by its entry id.
func HostOpsKey(project string) string { return project + ":hostops" }

// The states FencedHostAccept returns, and a directive's states in
// {p}:hostops.
const (
	HostAccepted = "accepted"
	HostExpired  = "expired"
	HostSeen     = "seen" // handled already: nothing written
	HostRefused  = "refused"
	HostDone     = "done"
	HostFailed   = "failed"
	HostNotRun   = "not-run"
)

// hostAccept: KEYS[1] the lease, KEYS[2] {p}:cmd, KEYS[3] {p}:hostops;
// ARGV[1] token, ARGV[2] the directive id, ARGV[3] the window ms, ARGV[4]
// its sender, ARGV[5] the stream cap, ARGV[6] the record kept when
// accepted (JSON, without its time). A directive already in {p}:hostops is
// seen; past the window by Redis TIME it is expired (reply written); else it
// is accepted, its record and its reply written in this one step.
var hostAccept = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local ms = tonumber(string.match(ARGV[2], '^(%d+)%-%d+$'))
if not ms then return redis.error_reply('host: bad directive id') end
if redis.call('HEXISTS', KEYS[3], ARGV[2]) == 1 then return 'seen' end
local state, text = 'accepted', 'accepted: queued; its outcome follows as a reply'
if now - ms > tonumber(ARGV[3]) then state, text = 'expired', 'expired' end
local rec = cjson.decode(ARGV[6])
rec['state'], rec['at'] = state, now
redis.call('HSET', KEYS[3], ARGV[2], cjson.encode(rec))
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[5], '*', 'from', 'dispatch', 'target', ARGV[4], 'type', 'reply', 'ref', ARGV[2],
  'command', text, 'text_complete', 'yes', 'wake', 'no')
return state`)

// hostRecord is a directive's entry in {p}:hostops.
type hostRecord struct {
	State string `json:"state"`
	At    int64  `json:"at"`
	Op    string `json:"op"`
	Agent string `json:"agent"`
	From  string `json:"from"`
}

// FencedHostAccept accepts o (validated by the caller) within window of its
// entry id, by Redis TIME: HostAccepted (reply written, record kept),
// HostExpired (reply written, nothing to run) or HostSeen (handled before).
// Only the lease holder writes.
func (b *Bus) FencedHostAccept(ctx context.Context, token string, o HostOp, window time.Duration) (string, error) {
	if !ValidName(o.From) {
		return "", fmt.Errorf("host: invalid sender %q", o.From)
	}
	rec, _ := json.Marshal(hostRecord{Op: o.Op, Agent: o.Agent, From: o.From})
	st, err := hostAccept.Run(ctx, b.r, []string{DispatchKey(b.project), StreamKey(b.project, "cmd"), HostOpsKey(b.project)},
		token, o.ID, window.Milliseconds(), o.From, streamMaxLen, string(rec)).Text()
	if err != nil {
		return "", notHolder(err)
	}
	return st, nil
}

// hostClose: KEYS as hostAccept; ARGV[1] token, ARGV[2] the id, ARGV[3] the
// sender, ARGV[4] the cap, ARGV[5] the new state, ARGV[6] the reply text,
// ARGV[7] "yes" when the reply wakes the sender, ARGV[8] "1" when only a
// directive never handled may be closed (a refusal before acceptance),
// ARGV[9] the record when there is none. Returns 0 when that rule left it.
var hostClose = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local raw = redis.call('HGET', KEYS[3], ARGV[2])
if ARGV[8] == '1' and raw then return 0 end
local rec = cjson.decode(raw or ARGV[9])
rec['state'], rec['at'] = ARGV[5], now
redis.call('HSET', KEYS[3], ARGV[2], cjson.encode(rec))
local f = {'from', 'dispatch', 'target', ARGV[3], 'type', 'reply', 'ref', ARGV[2], 'command', ARGV[6], 'text_complete', 'yes'}
if ARGV[7] ~= 'yes' then table.insert(f, 'wake'); table.insert(f, 'no') end
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[4], '*', unpack(f))
return 1`)

func (b *Bus) hostClose(ctx context.Context, token string, o HostOp, state, text string, wake, onlyNew bool) error {
	if !ValidName(o.From) {
		return fmt.Errorf("host: invalid sender %q", o.From)
	}
	if err := ValidateText(text, CmdMaxRunes); err != nil {
		return err
	}
	w, n := "", "0"
	if wake {
		w = "yes"
	}
	if onlyNew {
		n = "1"
	}
	rec, _ := json.Marshal(hostRecord{Op: o.Op, Agent: o.Agent, From: o.From})
	return notHolder(hostClose.Run(ctx, b.r, []string{DispatchKey(b.project), StreamKey(b.project, "cmd"), HostOpsKey(b.project)},
		token, o.ID, o.From, streamMaxLen, state, text, w, n, string(rec)).Err())
}

// FencedHostRefuse answers a directive refused before acceptance (outside
// the set, or the command's own refusal) with "refused: <why>", once.
func (b *Bus) FencedHostRefuse(ctx context.Context, token string, o HostOp, why string) error {
	return b.hostClose(ctx, token, o, HostRefused, "refused: "+why, true, true)
}

// FencedHostOutcome ends an accepted operation: HostDone (a reply that does
// not wake), HostRefused or HostFailed at run time, HostNotRun (the
// dispatcher stopped first); the last three wake the sender, who has
// something to do.
func (b *Bus) FencedHostOutcome(ctx context.Context, token string, o HostOp, state, text string) error {
	switch state {
	case HostDone, HostRefused, HostFailed, HostNotRun:
	default:
		return fmt.Errorf("host: %q is not an outcome", state)
	}
	return b.hostClose(ctx, token, o, state, state+": "+text, state != HostDone, false)
}

// UnfinishedHostOps are the accepted directives with no outcome, oldest
// first: a dispatcher starting reports each one as not run.
func (b *Bus) UnfinishedHostOps(ctx context.Context) ([]HostOp, error) {
	h, err := b.r.HGetAll(ctx, HostOpsKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []HostOp
	for id, raw := range h {
		var r hostRecord
		if json.Unmarshal([]byte(raw), &r) != nil || r.State != HostAccepted {
			continue
		}
		out = append(out, HostOp{ID: id, Op: r.Op, Agent: r.Agent, From: r.From})
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}
