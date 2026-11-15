package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

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
	Forget           bool   // retire only: --forget, the end of a done pop (foureyes on 10b)
	Episode          int64  // retire only, with Forget: the declaration the dispatcher judged (ms)
	Reason           string // retire only: one short line, recorded, never run
	From             string // who sent it: self-attested, as every bus entry
	State            string // UnfinishedHostOps only: HostAccepted or HostRunning
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
	case (o.Forget || o.Episode != 0 || o.Reason != "") && o.Op != HostRetire:
		return fmt.Errorf("host: only retire takes --forget, --episode and --reason")
	case o.Forget != (o.Episode > 0) || o.Episode < 0:
		return fmt.Errorf("host: --forget needs --episode <ms>, and --episode needs --forget")
	case o.Reason != "":
		if utf8.RuneCountInString(o.Reason) > HostReasonMax {
			return fmt.Errorf("host: a reason is at most %d characters", HostReasonMax)
		}
		if err := ValidateText(o.Reason, HostReasonMax); err != nil {
			return fmt.Errorf("host: reason: %v", err)
		}
		for _, r := range o.Reason {
			if unicode.IsControl(r) {
				return fmt.Errorf("host: a reason is one line of text, no control character")
			}
		}
	}
	return nil
}

// HostReasonMax caps a relayed retire's reason: a label for the ledger.
const HostReasonMax = 120

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
	if o.Forget {
		s += " --forget --episode " + strconv.FormatInt(o.Episode, 10)
	}
	if o.Reason != "" {
		s += " --reason " + strconv.Quote(o.Reason)
	}
	return s
}

// SendHostOp writes a host directive (the command's side, from inside an
// agent's terminal): wake=no, the dispatch service reads it on its own.
func (b *Bus) SendHostOp(ctx context.Context, o HostOp) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	ib, forget, episode := "", "", ""
	if o.IgnoreBackground {
		ib = "yes"
	}
	if o.Forget {
		forget, episode = "yes", strconv.FormatInt(o.Episode, 10)
	}
	return b.r.XAdd(ctx, &redis.XAddArgs{Stream: StreamKey(b.project, "cmd"), MaxLen: streamMaxLen, Approx: true,
		Values: []interface{}{"from", o.From, "target", HostTarget, "type", CmdHost, "ref", "", "command", o.describe(),
			"op", o.Op, "agent", o.Agent, "profile", o.Profile, "ignore_background", ib, "forget", forget, "episode", episode, "reason", o.Reason, "text_complete", "yes", "wake", "no"}}).Result()
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
		o := HostOp{ID: e.ID, Op: f["op"], Agent: f["agent"], Profile: f["profile"],
			IgnoreBackground: f["ignore_background"] == "yes", Forget: f["forget"] == "yes", Reason: f["reason"], From: f["from"]}
		if f["episode"] != "" {
			o.Episode, _ = strconv.ParseInt(f["episode"], 10, 64)
		}
		out = append(out, o)
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
	HostSeen     = "seen"    // handled already: nothing written
	HostRunning  = "running" // its worker started it (it may have acted)
	HostRefused  = "refused"
	HostDone     = "done"
	HostFailed   = "failed"
	HostNotRun   = "not-run"
	HostUnknown  = "unknown" // the dispatcher stopped while running it: it may have run
)

// hostAccept: KEYS[1] the lease, KEYS[2] {p}:cmd, KEYS[3] {p}:hostops;
// ARGV[1] token, ARGV[2] the directive id, ARGV[3] the window ms, ARGV[4]
// its sender, ARGV[5] the stream cap, ARGV[6] the record kept when
// accepted (JSON, without its time). A directive already in {p}:hostops is
// seen; one whose id is more than a second ahead of Redis TIME (an explicit
// XADD id: security S10a-1) is refused; past the window by Redis TIME it is
// expired; else it is accepted. The record and the reply are written in
// this one step.
var hostAccept = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local ms = tonumber(string.match(ARGV[2], '^(%d+)%-%d+$'))
if not ms then return redis.error_reply('host: bad directive id') end
if redis.call('HEXISTS', KEYS[3], ARGV[2]) == 1 then return 'seen' end
local state, text = 'accepted', 'accepted: queued; its outcome follows as a reply'
if ms - now > 1000 then
  state, text = 'refused', "refused: the directive's id is ahead of the server clock"
elseif now - ms > tonumber(ARGV[3]) then
  state, text = 'expired', 'expired'
end
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
// HostExpired (reply written, nothing to run), HostRefused (its id ahead of
// the server clock: reply written, nothing to run) or HostSeen (handled
// before).
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
// dispatcher stopped before it started), HostUnknown (it stopped while the
// operation ran: it may have acted); all but HostDone wake the sender, who
// has something to do.
func (b *Bus) FencedHostOutcome(ctx context.Context, token string, o HostOp, state, text string) error {
	switch state {
	case HostDone, HostRefused, HostFailed, HostNotRun, HostUnknown:
	default:
		return fmt.Errorf("host: %q is not an outcome", state)
	}
	return b.hostClose(ctx, token, o, state, state+": "+text, state != HostDone, false)
}

// hostRunning: KEYS[1] the lease, KEYS[2] {p}:hostops; ARGV[1] token,
// ARGV[2] the id. Only an accepted directive becomes running.
var hostRunning = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local raw = redis.call('HGET', KEYS[2], ARGV[2])
if not raw then return redis.error_reply('host: no such operation') end
local rec = cjson.decode(raw)
if rec['state'] ~= 'accepted' then return redis.error_reply('host: the operation is ' .. tostring(rec['state']) .. ', not accepted') end
rec['state'], rec['at'] = 'running', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('HSET', KEYS[2], ARGV[2], cjson.encode(rec))
return 1`)

// FencedHostRunning marks an accepted operation running, under the lease,
// before its worker runs it: a dispatcher without the lease runs nothing,
// and the next one can tell an operation that may have acted (running)
// from one that never started (accepted).
func (b *Bus) FencedHostRunning(ctx context.Context, token string, o HostOp) error {
	return notHolder(hostRunning.Run(ctx, b.r, []string{DispatchKey(b.project), HostOpsKey(b.project)}, token, o.ID).Err())
}

// UnfinishedHostOps are the accepted or running directives with no
// outcome, oldest first, their State set: a dispatcher starting reports an
// accepted one as not run, a running one as unknown.
func (b *Bus) UnfinishedHostOps(ctx context.Context) ([]HostOp, error) {
	h, err := b.r.HGetAll(ctx, HostOpsKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	var out []HostOp
	for id, raw := range h {
		var r hostRecord
		if json.Unmarshal([]byte(raw), &r) != nil || r.State != HostAccepted && r.State != HostRunning {
			continue
		}
		out = append(out, HostOp{ID: id, Op: r.Op, Agent: r.Agent, From: r.From, State: r.State})
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

// HostReply is the dispatcher's first reply to directive id, addressed to
// to: its text, and whether there is one.
func (b *Bus) HostReply(ctx context.Context, id, to string) (string, bool, error) {
	evs, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), id, "+").Result()
	if err != nil {
		return "", false, err
	}
	for _, e := range evs {
		f := toStringMap(e.Values)
		if f["type"] == CmdReply && f["ref"] == id && f["from"] == HostTarget && f["target"] == to {
			return f["command"], true, nil
		}
	}
	return "", false, nil
}

// hostWithdraw: KEYS[1] {p}:hostops; ARGV[1] the id, ARGV[2] its record
// (JSON, without its time). It writes the record as withdrawn only when the
// directive was never handled, and says whether it did.
var hostWithdraw = redis.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 then return 0 end
local t = redis.call('TIME')
local rec = cjson.decode(ARGV[2])
rec['state'], rec['at'] = 'withdrawn', tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(rec))
return 1`)

// HostWithdrawn is a directive its command gave up on before any reply.
const HostWithdrawn = "withdrawn"

// WithdrawHostOp is the command giving up (reviewer-codex): atomic against
// the dispatcher's accept, which finds a withdrawn directive handled (seen)
// and acts on nothing. false: the dispatcher handled it first, and its reply
// is on {p}:cmd (written in the same script). The command's own write, not
// fenced: it is not the dispatcher's.
func (b *Bus) WithdrawHostOp(ctx context.Context, o HostOp) (bool, error) {
	if !ValidName(o.From) {
		return false, fmt.Errorf("host: invalid sender %q", o.From)
	}
	rec, _ := json.Marshal(hostRecord{Op: o.Op, Agent: o.Agent, From: o.From})
	n, err := hostWithdraw.Run(ctx, b.r, []string{HostOpsKey(b.project)}, o.ID, string(rec)).Int()
	return n == 1, err
}

// HostOpsKeep is how long a finished directive's record is kept (security
// S10a-4): long past any re-read, the reader's cursor starting two windows
// back.
const HostOpsKeep = 24 * time.Hour

// hostPrune: KEYS[1] the lease, KEYS[2] {p}:hostops; ARGV[1] token, ARGV[2]
// the age ms. It deletes each finished record older than the age, and each
// unreadable one; an accepted or running record is kept, whatever its age,
// for the next sweep to report.
var hostPrune = redis.NewScript(fenceCheck + `
local t = redis.call('TIME')
local before = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000) - tonumber(ARGV[2])
local h = redis.call('HGETALL', KEYS[2])
local n = 0
for i = 1, #h, 2 do
  local ok, rec = pcall(cjson.decode, h[i + 1])
  local drop = not ok or type(rec) ~= 'table'
  if not drop and rec['state'] ~= 'accepted' and rec['state'] ~= 'running' then
    drop = tonumber(rec['at']) == nil or tonumber(rec['at']) < before
  end
  if drop then
    redis.call('HDEL', KEYS[2], h[i])
    n = n + 1
  end
end
return n`)

// FencedPruneHostOps deletes the finished records older than age (security
// S10a-4), under the lease; it returns how many went.
func (b *Bus) FencedPruneHostOps(ctx context.Context, token string, age time.Duration) (int, error) {
	n, err := hostPrune.Run(ctx, b.r, []string{DispatchKey(b.project), HostOpsKey(b.project)}, token, age.Milliseconds()).Int()
	if err != nil {
		return 0, notHolder(err)
	}
	return n, nil
}
