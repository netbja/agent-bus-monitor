// Streams transport for the Agent Bus. A Bus value is bound to one project, so
// every key is namespaced {project}:{kind} and projects sharing a broker never
// collide. This file coexists with the legacy pub/sub helpers in bus.go during
// the migration; the binaries are switched over (and the old API removed) in
// later phases.
package bus

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// Status publishes an agent's state to the {project}:status stream.
func (b *Bus) Status(ctx context.Context, agent, state, message string) (string, error) {
	if !ValidName(agent) {
		return "", fmt.Errorf("invalid agent %q", agent)
	}
	if !ValidStates[state] {
		return "", fmt.Errorf("invalid state %q (working|idle|blocked|done)", state)
	}
	return b.add(ctx, "status", map[string]interface{}{
		"agent": agent, "state": state, "message": message,
	})
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

// Tail blocks reading the given stream kinds from lastID onward (use "$" for
// only-new, "0" to replay history), invoking fn per event until ctx is
// cancelled. It is read-only: a plain XREAD never touches consumer-group
// cursors, so observers (busmon) don't compete with agents reading cmd via
// WatchCmd.
func (b *Bus) Tail(ctx context.Context, lastID string, kinds []string, fn func(Event)) error {
	keys := make([]string, len(kinds))
	ids := make(map[string]string, len(kinds))
	for i, k := range kinds {
		keys[i] = StreamKey(b.project, k)
		ids[keys[i]] = lastID
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

// WatchCmd consumes the project's shared cmd stream via a per-agent consumer
// group (group name = agent), giving at-least-once delivery across one-shot
// restarts (the cursor lives server-side). fn is called only for entries whose
// target == agent; every read entry is XACKed — including ones for other agents
// — so the pending-entries list stays clean. WatchCmd returns nil when fn
// returns true (handled; used by the one-shot `agentbus watch`) or the context
// error when cancelled.
func (b *Bus) WatchCmd(ctx context.Context, agent, consumer string, fn func(Event) bool) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	stream := StreamKey(b.project, "cmd")
	// MKSTREAM creates the stream if absent; an existing group yields BUSYGROUP,
	// which we ignore so the group keeps its current cursor.
	if err := b.r.XGroupCreateMkStream(ctx, stream, agent, "$").Err(); err != nil &&
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
				b.r.XAck(ctx, stream, agent, m.ID) // best-effort; clears the PEL
				if e := ParseEntry(stream, m.ID, toStringMap(m.Values)); e.Target == agent && fn(e) {
					return nil
				}
			}
		}
	}
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

// OpenChallenge records an unresolved 4-eyes challenge gating agent: a hash
// field ref → meta ("<challenger>|<summary>"). The agent must not proceed while
// any challenge is open. There is deliberately no TTL — a safety gate is closed
// only by an explicit verdict (ResolveChallenge), never by silent expiry.
func (b *Bus) OpenChallenge(ctx context.Context, agent, ref, meta string) error {
	if ref == "" {
		return fmt.Errorf("challenge ref required")
	}
	return b.r.HSet(ctx, GateKey(b.project, agent), ref, meta).Err()
}

// ResolveChallenge closes the challenge identified by ref (the verdict step).
func (b *Bus) ResolveChallenge(ctx context.Context, agent, ref string) error {
	return b.r.HDel(ctx, GateKey(b.project, agent), ref).Err()
}

// OpenChallenges returns ref→meta for every unresolved challenge gating agent.
// A non-empty result means the agent is gated.
func (b *Bus) OpenChallenges(ctx context.Context, agent string) (map[string]string, error) {
	return b.r.HGetAll(ctx, GateKey(b.project, agent)).Result()
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
