// Streams transport for the Agent Bus. A Bus value is bound to one project, so
// every key is namespaced {project}:{kind} and projects sharing a broker never
// collide. This file coexists with the legacy pub/sub helpers in bus.go during
// the migration; the binaries are switched over (and the old API removed) in
// later phases.
package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const streamMaxLen = 1000

// cmd entry types carried in the {project}:cmd stream "type" field.
const (
	CmdDirective = "directive" // from hermes; gated by the pilot lease
	CmdChallenge = "challenge" // from a peer; opens a 4-eyes gate on the target
	CmdReply     = "reply"     // response to a challenge, correlated by ref
	CmdVerdict   = "verdict"   // closes a challenge, correlated by ref
)

var validCmdTypes = map[string]bool{
	CmdDirective: true, CmdChallenge: true, CmdReply: true, CmdVerdict: true,
}

// nameRE bounds project slugs and agent names: lowercase, starts with a letter,
// 1–32 chars of [a-z0-9_-]. Replaces the old hardcoded ValidAgents allowlist.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidName reports whether s is an acceptable project or agent identifier.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// The entire channel convention: stream key is {project}:{kind}.
func StreamKey(project, kind string) string { return project + ":" + kind }
func PilotKey(project string) string        { return project + ":pilot" }
func GateKey(project, agent string) string  { return project + ":gate:" + agent }
func ArmedKey(project, agent string) string { return project + ":armed:" + agent }

// AgentsKey is the per-project hash of current agent state ({agent} → JSON
// AgentSnapshot), written by Status and read by `agentbus agents`.
func AgentsKey(project string) string { return project + ":agents" }

// VerdictsKey is the per-project append-only ledger of 4-eyes verdicts
// ({project}:verdicts). Unlike the activity streams it is capped at
// verdictMaxLen() (default 10000) because it is the money-path audit trail.
func VerdictsKey(project string) string { return project + ":verdicts" }

// AgentSnapshot is the cached current state of one agent.
type AgentSnapshot struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	TS      int64  `json:"ts"` // ms since epoch, from the status entry's stream id
	Pane    string `json:"pane,omitempty"` // HERDR_PANE_ID when the agent runs inside herdr
}

// Verdict is one entry in the {project}:verdicts ledger. Subject keys the review
// target (e.g. "pr:25"); Author is the agent under review and Reviewer the agent
// issuing the verdict. Ref optionally links to a challenge thread. TS is the ms
// timestamp derived from the stream id.
type Verdict struct {
	ID       string `json:"id"`
	Subject  string `json:"subject"`
	Author   string `json:"author"`
	Reviewer string `json:"reviewer"`
	Decision string `json:"decision"` // approve | reject
	Message  string `json:"message,omitempty"`
	Ref      string `json:"ref,omitempty"`
	TS       int64  `json:"ts"`
}

// UsageKey is the per-project hash of latest agent usage snapshots ({agent} →
// JSON UsageSnapshot), written by `agentbus usage` (the status-line tee) and read
// by busmon / the master. Separate from AgentsKey: a different writer and cadence.
func UsageKey(project string) string { return project + ":usage" }

// UsageSnapshot is an agent's latest budget readout — the display strings its
// status line already computed (not parsed numbers).
type UsageSnapshot struct {
	Model   string `json:"model,omitempty"`
	Ctx     string `json:"ctx,omitempty"`
	Weekly  string `json:"weekly,omitempty"`
	Session string `json:"session,omitempty"`
	Reset   string `json:"reset,omitempty"`
	TS      int64  `json:"ts"`
}

// Event is a parsed stream entry. Which fields are populated depends on Kind.
type Event struct {
	ID      string // redis stream entry id
	Project string
	Kind    string // status | report | notify | cmd
	Agent   string // status/report: the author
	From    string // notify/cmd: the sender
	Target  string // cmd: the addressed agent
	State   string // status: working|idle|blocked|done
	RKind   string // report: note|auto
	Type    string // cmd: directive|challenge|reply|verdict
	Ref     string // cmd: correlation id
	Message string // status/report/notify text, or the cmd command
}

// ParseEntry turns a raw stream entry into an Event. The kind is derived from
// the stream-key suffix ({project}:{kind}); fields are read per kind. This is
// the Streams analog of the legacy Parse in bus.go.
func ParseEntry(streamKey, id string, fields map[string]string) Event {
	project, kind := splitStreamKey(streamKey)
	e := Event{ID: id, Project: project, Kind: kind}
	switch kind {
	case "status":
		e.Agent, e.State, e.Message = fields["agent"], fields["state"], fields["message"]
	case "report":
		e.Agent, e.RKind, e.Message = fields["agent"], fields["kind"], fields["message"]
	case "notify":
		e.From, e.Message = fields["from"], fields["message"]
	case "cmd":
		e.From, e.Target, e.Type, e.Ref, e.Message =
			fields["from"], fields["target"], fields["type"], fields["ref"], fields["command"]
	}
	return e
}

// splitStreamKey splits {project}:{kind} on the last colon. Project slugs never
// contain a colon (see nameRE), so this is unambiguous.
func splitStreamKey(key string) (project, kind string) {
	if i := strings.LastIndex(key, ":"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

// Bus is a project-scoped handle over the Streams transport. Construct it with
// Open; every operation is namespaced to the project.
type Bus struct {
	r       *redis.Client
	project string
}

// Open binds a client to a project. The project is required and must be a valid
// slug — there is no global default namespace (that was the old collision bug).
func Open(r *redis.Client, project string) (*Bus, error) {
	if !ValidName(project) {
		return nil, fmt.Errorf("invalid project %q (want %s)", project, nameRE)
	}
	return &Bus{r: r, project: project}, nil
}

// Project returns the project this Bus is bound to.
func (b *Bus) Project() string { return b.project }

// add XADDs to {project}:{kind} with an approximate length cap so no stream
// grows unbounded, and returns the new entry ID.
func (b *Bus) add(ctx context.Context, kind string, values map[string]interface{}) (string, error) {
	return b.r.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamKey(b.project, kind),
		MaxLen: streamMaxLen,
		Approx: true,
		Values: values,
	}).Result()
}

// Status publishes an agent's state to the {project}:status stream. pane is the
// agent's HERDR_PANE_ID (empty outside herdr); it is stored in the {project}:agents
// snapshot only, never in the status stream.
func (b *Bus) Status(ctx context.Context, agent, state, message, pane string) (string, error) {
	if !ValidName(agent) {
		return "", fmt.Errorf("invalid agent %q", agent)
	}
	if !ValidStates[state] {
		return "", fmt.Errorf("invalid state %q (working|idle|blocked|done)", state)
	}
	id, err := b.add(ctx, "status", map[string]interface{}{
		"agent": agent, "state": state, "message": message,
	})
	if err != nil {
		return "", err
	}
	// Best-effort current-state cache for `agentbus agents` (and later slices).
	// The stream is the source of truth; a failed HSET only means a briefly
	// stale cache, so it must not fail a status publish that already landed.
	ms, _ := splitID(id)
	if snap, merr := json.Marshal(AgentSnapshot{State: state, Message: message, TS: ms, Pane: pane}); merr == nil {
		_ = b.r.HSet(ctx, AgentsKey(b.project), agent, snap).Err()
	}
	return id, nil
}

// Report publishes a curated report to the {project}:report stream. kind is
// intentionally not allowlisted here — it is free text (note/auto today) owned
// by the report protocol, mirroring the legacy Report in bus.go.
func (b *Bus) Report(ctx context.Context, agent, kind, message string) (string, error) {
	if !ValidName(agent) {
		return "", fmt.Errorf("invalid agent %q", agent)
	}
	return b.add(ctx, "report", map[string]interface{}{
		"agent": agent, "kind": kind, "message": SanitizeReportMessage(message),
	})
}

// Notify broadcasts a message on the {project}:notify stream. from is advisory
// (the announcing identity); it is not validated.
func (b *Bus) Notify(ctx context.Context, from, message string) (string, error) {
	return b.add(ctx, "notify", map[string]interface{}{"from": from, "message": message})
}

// Cmd appends an addressed entry to the shared {project}:cmd stream. typ is one
// of CmdDirective/CmdChallenge/CmdReply/CmdVerdict; ref correlates a challenge
// with its replies and verdict (empty for fire-and-forget directives).
func (b *Bus) Cmd(ctx context.Context, from, target, typ, ref, command string) (string, error) {
	if !ValidName(target) {
		return "", fmt.Errorf("invalid target %q", target)
	}
	if !validCmdTypes[typ] {
		return "", fmt.Errorf("invalid cmd type %q", typ)
	}
	return b.add(ctx, "cmd", map[string]interface{}{
		"from": from, "target": target, "type": typ, "ref": ref, "command": command,
	})
}

// AppendVerdict records one verdict in the {project}:verdicts ledger and returns
// the new entry id. It writes unconditionally — including self-approvals
// (reviewer == author) — so the audit trail is complete; the independence rule
// is enforced only at read time by the roll-up. The cap is verdictMaxLen().
func (b *Bus) AppendVerdict(ctx context.Context, v Verdict) (string, error) {
	if !ValidName(v.Author) {
		return "", fmt.Errorf("invalid author %q", v.Author)
	}
	if !ValidName(v.Reviewer) {
		return "", fmt.Errorf("invalid reviewer %q", v.Reviewer)
	}
	if v.Subject == "" {
		return "", fmt.Errorf("verdict subject required")
	}
	if v.Decision != "approve" && v.Decision != "reject" {
		return "", fmt.Errorf("verdict decision must be approve or reject")
	}
	return b.r.XAdd(ctx, &redis.XAddArgs{
		Stream: VerdictsKey(b.project),
		MaxLen: int64(verdictMaxLen()),
		Approx: true,
		Values: map[string]interface{}{
			"subject": v.Subject, "author": v.Author, "reviewer": v.Reviewer,
			"decision": v.Decision, "message": SanitizeReportMessage(v.Message), "ref": v.Ref,
		},
	}).Result()
}

// Verdicts returns ledger entries oldest→newest (XRANGE is ascending, which the
// CLI roll-up relies on). If subject != "", only entries for that subject are
// returned; subject == "" returns the whole ledger.
func (b *Bus) Verdicts(ctx context.Context, subject string) ([]Verdict, error) {
	msgs, err := b.r.XRange(ctx, VerdictsKey(b.project), "-", "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Verdict, 0, len(msgs))
	for _, m := range msgs {
		f := toStringMap(m.Values)
		if subject != "" && f["subject"] != subject {
			continue
		}
		ms, _ := splitID(m.ID)
		out = append(out, Verdict{
			ID: m.ID, Subject: f["subject"], Author: f["author"],
			Reviewer: f["reviewer"], Decision: f["decision"],
			Message: f["message"], Ref: f["ref"], TS: ms,
		})
	}
	return out, nil
}

// Tail blocks reading the given stream kinds from lastID onward (use "0" to
// replay history), invoking fn per event until ctx is cancelled. It is
// read-only: a plain XREAD never touches consumer-group cursors, so observers
// (busmon) don't compete with agents reading cmd via WatchCmd.
//
// fn is called synchronously on Tail's goroutine — it must not block (queue the
// work and return). Pass "0" or an explicit ID rather than "$" for multi-kind
// tails: with several streams, "$" can miss entries that arrive between the
// per-poll re-evaluation of "$" on each stream.
func (b *Bus) Tail(ctx context.Context, lastID string, kinds []string, fn func(Event)) error {
	if len(kinds) == 0 {
		return fmt.Errorf("tail: no stream kinds given")
	}
	start := make(map[string]string, len(kinds))
	for _, k := range kinds {
		start[StreamKey(b.project, k)] = lastID
	}
	return b.TailFrom(ctx, start, kinds, fn)
}

// TailFrom is Tail with a per-stream start cursor: start maps a full stream key
// ({project}:{kind}) to the ID to read after. A kind whose key is absent (or
// maps to "") defaults to "0" — replays nothing for an empty stream now, yet
// catches every future entry, which avoids the "$" gap (entries arriving between
// a backfill and the live subscribe). busmon pairs this with Recent: backfill
// the last N merged entries, then live-tail from the cursors Recent returned so
// nothing is replayed and nothing is missed. The fn-must-not-block contract is
// the same as Tail.
func (b *Bus) TailFrom(ctx context.Context, start map[string]string, kinds []string, fn func(Event)) error {
	if len(kinds) == 0 {
		return fmt.Errorf("tail: no stream kinds given")
	}
	keys := make([]string, len(kinds))
	ids := make(map[string]string, len(kinds))
	for i, k := range kinds {
		keys[i] = StreamKey(b.project, k)
		if id := start[keys[i]]; id != "" {
			ids[keys[i]] = id
		} else {
			ids[keys[i]] = "0"
		}
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res, err := b.r.XRead(ctx, &redis.XReadArgs{
			Streams: append(append([]string{}, keys...), idList(keys, ids)...),
			Block:   time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue // block timeout, no new entries
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, s := range res {
			for _, m := range s.Messages {
				ids[s.Stream] = m.ID
				fn(ParseEntry(s.Stream, m.ID, toStringMap(m.Values)))
			}
		}
	}
}

// Recent returns the last n entries merged across the given stream kinds, in
// chronological order (oldest→newest, ready to render top→bottom), plus a cursor
// map {streamKey: newest ID} for every non-empty stream. The cursors are meant
// to seed TailFrom so the live tail resumes exactly after the backfill with no
// replay; empty streams are intentionally absent (TailFrom defaults them to
// "0"). Entries are read with XREVRANGE COUNT n per stream — read-only, no
// consumer-group cursors touched, like Tail.
func (b *Bus) Recent(ctx context.Context, kinds []string, n int) ([]Event, map[string]string, error) {
	cursors := make(map[string]string, len(kinds))
	var all []Event
	for _, k := range kinds {
		key := StreamKey(b.project, k)
		msgs, err := b.r.XRevRangeN(ctx, key, "+", "-", int64(n)).Result()
		if err != nil {
			return nil, nil, err
		}
		if len(msgs) > 0 {
			cursors[key] = msgs[0].ID // XREVRANGE is newest-first, so [0] is this stream's latest
		}
		for _, m := range msgs {
			all = append(all, ParseEntry(key, m.ID, toStringMap(m.Values)))
		}
	}
	sort.Slice(all, func(i, j int) bool { return idLess(all[i].ID, all[j].ID) })
	if len(all) > n {
		all = all[len(all)-n:] // keep the n most recent across all streams
	}
	return all, cursors, nil
}

// Purge clears the given stream kinds with XTRIM MAXLEN 0 and returns the total
// number of entries removed. XTRIM keeps the streams' consumer groups (so cmd
// at-least-once delivery is unaffected) and never touches the project's
// armed/pilot/gate keys — it only drops the visible history. Trimming a
// nonexistent stream removes nothing and is not an error.
func (b *Bus) Purge(ctx context.Context, kinds []string) (int64, error) {
	var total int64
	for _, k := range kinds {
		removed, err := b.r.XTrimMaxLen(ctx, StreamKey(b.project, k), 0).Result()
		if err != nil {
			return total, err
		}
		total += removed
	}
	return total, nil
}

// idLess orders two Redis stream IDs ("<ms>-<seq>") numerically. A string
// compare is wrong: "10-0" sorts before "9-0" lexicographically.
func idLess(a, b string) bool {
	ams, aseq := splitID(a)
	bms, bseq := splitID(b)
	if ams != bms {
		return ams < bms
	}
	return aseq < bseq
}

// splitID parses a stream ID into its millisecond and sequence components;
// unparseable parts become 0.
func splitID(id string) (ms, seq int64) {
	s := id
	if i := strings.IndexByte(id, '-'); i >= 0 {
		s = id[:i]
		seq, _ = strconv.ParseInt(id[i+1:], 10, 64)
	}
	ms, _ = strconv.ParseInt(s, 10, 64)
	return ms, seq
}

// WatchCmd consumes the project's shared cmd stream via a per-agent consumer
// group (group name = agent), giving at-least-once delivery across one-shot
// restarts (the cursor lives server-side). fn is called only for entries whose
// target == agent AND whose ID is strictly newer than floor (pre-floor entries
// are ACKed but not delivered, so the PEL stays clean). An empty or "0" floor
// means "no floor" — every entry passes. WatchCmd returns nil when fn returns
// true (handled; used by the one-shot `agentbus watch`) or the context error
// when cancelled.
func (b *Bus) WatchCmd(ctx context.Context, agent, consumer, floor string, fn func(Event) bool) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	stream := StreamKey(b.project, "cmd")
	// A fresh group is created at the floor (so a persisted cursor catches every
	// entry after it); an empty floor keeps today's "$" = from-now. An existing
	// group yields BUSYGROUP and keeps its server-side cursor unchanged.
	createAt := "$"
	if floor == "0" {
		createAt = "0"
	} else if floor != "" {
		createAt = floor
	}
	if err := b.r.XGroupCreateMkStream(ctx, stream, agent, createAt).Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res, err := b.r.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: agent, Consumer: consumer,
			Streams: []string{stream, ">"},
			Block:   time.Second, Count: 16,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, s := range res {
			for _, m := range s.Messages {
				b.r.XAck(ctx, stream, agent, m.ID) // ACK every read entry, even skipped ones
				e := ParseEntry(stream, m.ID, toStringMap(m.Values))
				if e.Target == agent && aboveFloor(floor, e.ID) && fn(e) {
					return nil
				}
			}
		}
	}
}

// aboveFloor reports whether id is strictly newer than floor. An empty or "0"
// floor is "no floor" — every entry passes. floor must be a full stream id
// ("<ms>-<seq>") or "" / "0"; the CLI normalizes a bare "<ms>" before calling.
func aboveFloor(floor, id string) bool {
	if floor == "" || floor == "0" {
		return true
	}
	return idLess(floor, id)
}

// ServerFloor returns the Redis server's current time as a stream-id floor
// "<ms>-0". A subscriber with no explicit --since starts here, so it sees only
// commands published from now on and never replays archived backlog.
func (b *Bus) ServerFloor(ctx context.Context) (string, error) {
	t, err := b.r.Time(ctx).Result()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(t.UnixMilli(), 10) + "-0", nil
}

// Pilot sets (or renews — they are the same SET) the project's pilot lease to
// driver with a TTL. Hermes calls this on an interval while it has budget;
// stopping = letting workers fall back to autonomous mode. driver must be
// non-empty: "" is PilotDriver's "no lease / autonomous" sentinel, so an empty
// driver would be an unreadable lease.
func (b *Bus) Pilot(ctx context.Context, driver string, ttl time.Duration) error {
	if driver == "" {
		return fmt.Errorf("pilot driver must not be empty")
	}
	return b.r.Set(ctx, PilotKey(b.project), driver, ttl).Err()
}

// ReleasePilot drops the lease immediately (explicit hand-off to autonomous).
// Safe to call when no lease is held — DEL of a missing key is a no-op.
func (b *Bus) ReleasePilot(ctx context.Context) error {
	return b.r.Del(ctx, PilotKey(b.project)).Err()
}

// PilotDriver returns the current driver, or "" if no lease is held — "" means
// autonomous mode (Hermes is out of budget or down).
func (b *Bus) PilotDriver(ctx context.Context) (string, error) {
	v, err := b.r.Get(ctx, PilotKey(b.project)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// Arm records a subscribe presence lease for agent: a TTL'd key
// {project}:armed:{agent} whose value is the listening consumer/host. The TTL
// is the subscriber's idle window, so the lease self-expires if the subscriber
// crashes — busmon's "listening" badge clears with no cleanup logic. This is
// observability only; callers must not gate command delivery on it.
func (b *Bus) Arm(ctx context.Context, agent, consumer string, ttl time.Duration) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	return b.r.Set(ctx, ArmedKey(b.project, agent), consumer, ttl).Err()
}

// Disarm clears agent's presence lease (called when subscribe exits). Safe to
// call when no lease is held — DEL of a missing key is a no-op.
func (b *Bus) Disarm(ctx context.Context, agent string) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	return b.r.Del(ctx, ArmedKey(b.project, agent)).Err()
}

// ArmedAgents returns agent→consumer for every agent with a live presence
// lease. Used by busmon to render the listening badge. Keys that expire between
// the SCAN and the GET are skipped.
func (b *Bus) ArmedAgents(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string)
	prefix := b.project + ":armed:"
	var cursor uint64
	for {
		keys, next, err := b.r.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return out, err
		}
		for _, k := range keys {
			v, err := b.r.Get(ctx, k).Result()
			if err != nil {
				continue // expired between SCAN and GET
			}
			out[strings.TrimPrefix(k, prefix)] = v
		}
		if next == 0 {
			return out, nil
		}
		cursor = next
	}
}

// CmdLag returns, per consumer group on the project's cmd stream, how many
// entries the group has not yet read (XINFO GROUPS "lag"). Group name == agent
// name (see WatchCmd), so the result is agent→backlog. A non-zero backlog for an
// agent with no live armed lease is busmon's "stopped listening" signal. The
// stream not existing yet is not an error — it just means no backlog. Redis may
// report a lag of -1 when it cannot be determined (e.g. after the stream is
// trimmed at its MAXLEN cap); CmdLag passes that through unchanged, and busmon
// only renders the badge when lag > 0, so -1 is harmlessly ignored.
func (b *Bus) CmdLag(ctx context.Context) (map[string]int64, error) {
	groups, err := b.r.XInfoGroups(ctx, StreamKey(b.project, "cmd")).Result()
	out := make(map[string]int64, len(groups))
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return out, nil // stream not created yet → no groups, no lag
		}
		return out, err
	}
	for _, g := range groups {
		out[g.Name] = g.Lag
	}
	return out, nil
}

// OpenChallenge records an unresolved 4-eyes challenge gating agent: a hash
// field ref → meta ("<challenger>|<summary>"). The agent must not proceed while
// any challenge is open. There is deliberately no TTL — a safety gate is closed
// only by an explicit verdict (ResolveChallenge), never by silent expiry. Both
// ref and meta are required: ref keys the challenge, meta is its audit trail.
// agent is not re-validated here (the key is derived, not broadcast); the caller
// — typically the cmd-stream handler — is expected to have validated it upstream.
func (b *Bus) OpenChallenge(ctx context.Context, agent, ref, meta string) error {
	if ref == "" {
		return fmt.Errorf("challenge ref required")
	}
	if meta == "" {
		return fmt.Errorf("challenge meta required (audit trail)")
	}
	return b.r.HSet(ctx, GateKey(b.project, agent), ref, meta).Err()
}

// ResolveChallenge closes the challenge identified by ref (the verdict step). It
// errors if no such challenge is open, so a verdict carrying a stale or mistyped
// ref fails loudly instead of silently "resolving" a gate that was never set.
func (b *Bus) ResolveChallenge(ctx context.Context, agent, ref string) error {
	n, err := b.r.HDel(ctx, GateKey(b.project, agent), ref).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no open challenge %q for agent %q", ref, agent)
	}
	return nil
}

// OpenChallenges returns ref→meta for every unresolved challenge gating agent.
// A non-empty result means the agent is gated. An ungated agent yields an empty
// (non-nil) map, not an error.
func (b *Bus) OpenChallenges(ctx context.Context, agent string) (map[string]string, error) {
	return b.r.HGetAll(ctx, GateKey(b.project, agent)).Result()
}

// Agents returns the cached current state of every agent that has published a
// status, agent → snapshot. Unparseable fields are skipped. The cache can lag
// the stream slightly (the HSET in Status is best-effort).
func (b *Bus) Agents(ctx context.Context) (map[string]AgentSnapshot, error) {
	raw, err := b.r.HGetAll(ctx, AgentsKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]AgentSnapshot, len(raw))
	for agent, v := range raw {
		var s AgentSnapshot
		if json.Unmarshal([]byte(v), &s) == nil {
			out[agent] = s
		}
	}
	return out, nil
}

// SetUsage overwrites an agent's usage snapshot in the {project}:usage hash.
func (b *Bus) SetUsage(ctx context.Context, agent string, snap UsageSnapshot) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	v, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return b.r.HSet(ctx, UsageKey(b.project), agent, v).Err()
}

// Usage returns agent → latest usage snapshot. Unparseable fields are skipped.
func (b *Bus) Usage(ctx context.Context) (map[string]UsageSnapshot, error) {
	raw, err := b.r.HGetAll(ctx, UsageKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]UsageSnapshot, len(raw))
	for agent, v := range raw {
		var s UsageSnapshot
		if json.Unmarshal([]byte(v), &s) == nil {
			out[agent] = s
		}
	}
	return out, nil
}

// idList returns the per-key cursor IDs in the same order as keys (XREAD wants
// all keys followed by all IDs).
func idList(keys []string, ids map[string]string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = ids[k]
	}
	return out
}

// toStringMap narrows redis stream field values (interface{}) to strings.
func toStringMap(v map[string]interface{}) map[string]string {
	out := make(map[string]string, len(v))
	for k, val := range v {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}
