# Bus Streams Phase 1 — bus.go foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Redis Streams transport API (project-scoped `Bus` handle: publish, tail, consumer-group cmd delivery, pilot lease, 4-eyes gate) to the `bus` package, fully tested, without touching the binaries — so every commit stays green.

**Architecture:** A new file `bus/stream.go` introduces a `Bus` value bound to one project (`Open(client, project)`), with methods that wrap Redis Streams + a pilot lease key + per-agent gate hashes. The old pub/sub functions in `bus/bus.go` are left untouched in this phase so `cmd/agentbus` and `cmd/busmon` keep compiling; they get migrated (and the old API removed) in Phases 2–4. The whole convention is one rule: stream key = `{project}:{kind}`.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, `redis:8-alpine` on `127.0.0.1:6380` (via `docker compose up -d`). TDD: pure helpers as table unit tests; Redis-touching behavior as integration tests that `t.Skip` when the broker is unreachable.

**Phase context (for the reader):** This is Phase 1 of 4 from `docs/superpowers/specs/2026-05-25-bus-streams-unification-design.md`. Later phases: **2** migrate `agentbus` (`--project`, `cmd/challenge/reply/verdict/pilot/gate/watch`), **3** migrate `busmon` (XREAD tail, project selector, pilot+gate rendering), **4** finalize `agentbus watch` + the `adv-trading-ai` wrapper and remove the old pub/sub API. Do **not** edit the binaries or `CLAUDE.md` in this phase.

**Before you start:** `docker compose up -d` so the broker is live; the integration tests skip without it (but you want them running, not skipping).

---

### Task 1: Naming & validation helpers

**Files:**
- Create: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing tests**

Create `bus/stream_test.go`:

```go
package bus

import "testing"

func TestStreamKeyNaming(t *testing.T) {
	if got := StreamKey("busmon", "status"); got != "busmon:status" {
		t.Fatalf("StreamKey = %q, want busmon:status", got)
	}
	if got := PilotKey("busmon"); got != "busmon:pilot" {
		t.Fatalf("PilotKey = %q, want busmon:pilot", got)
	}
	if got := GateKey("busmon", "dev"); got != "busmon:gate:dev" {
		t.Fatalf("GateKey = %q, want busmon:gate:dev", got)
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"dev", "busmon", "dev-1", "hermes", "x", "a_b-2"}
	invalid := []string{"", "Dev", "1dev", "a:b", "dev ", "trading/dev",
		"this-name-is-way-too-long-to-be-accepted-okay"}
	for _, s := range valid {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./bus/ -run 'TestStreamKeyNaming|TestValidName' -v`
Expected: FAIL — `undefined: StreamKey` (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `bus/stream.go`:

```go
// Streams transport for the Agent Bus. A Bus value is bound to one project, so
// every key is namespaced {project}:{kind} and projects sharing a broker never
// collide. This file coexists with the legacy pub/sub helpers in bus.go during
// the migration; the binaries are switched over (and the old API removed) in
// later phases.
package bus

import "regexp"

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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./bus/ -run 'TestStreamKeyNaming|TestValidName' -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): stream key naming + name validation helpers"
```

---

### Task 2: Event struct & ParseEntry

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestParseEntry(t *testing.T) {
	cases := []struct {
		name, stream string
		fields       map[string]string
		want         Event
	}{
		{"status", "busmon:status",
			map[string]string{"agent": "dev", "state": "working", "message": "hi"},
			Event{ID: "1-0", Project: "busmon", Kind: "status", Agent: "dev", State: "working", Message: "hi"}},
		{"report", "busmon:report",
			map[string]string{"agent": "dev", "kind": "note", "message": "bug fixed"},
			Event{ID: "1-0", Project: "busmon", Kind: "report", Agent: "dev", RKind: "note", Message: "bug fixed"}},
		{"notify", "trading:notify",
			map[string]string{"from": "hermes", "message": "soak running"},
			Event{ID: "1-0", Project: "trading", Kind: "notify", From: "hermes", Message: "soak running"}},
		{"cmd", "busmon:cmd",
			map[string]string{"from": "review", "target": "dev", "type": "challenge", "ref": "C1", "command": "justify X"},
			Event{ID: "1-0", Project: "busmon", Kind: "cmd", From: "review", Target: "dev", Type: "challenge", Ref: "C1", Message: "justify X"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseEntry(c.stream, "1-0", c.fields); got != c.want {
				t.Fatalf("ParseEntry = %+v, want %+v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestParseEntry -v`
Expected: FAIL — `undefined: Event` / `undefined: ParseEntry`.

- [ ] **Step 3: Write minimal implementation**

Add to `bus/stream.go` (and add `"strings"` to the import block, making it `import ( "regexp"; "strings" )`):

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestParseEntry -v`
Expected: PASS (4 sub-tests).

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): Event struct + ParseEntry for stream entries"
```

---

### Task 3: Bus handle + publishers (Status/Report/Notify/Cmd)

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go` (adds the shared integration helper `dialTest` used by all later tasks):

```go
import (
	"context"
	"strconv"
	"testing"
	"time"
)

// dialTest connects to the dev broker and returns a Bus on a unique throwaway
// project; it skips the test if Redis is down. All four streams + the pilot key
// are deleted on cleanup (gate keys are per-agent — tests clean their own).
func dialTest(t *testing.T) *Bus {
	t.Helper()
	r, err := Connect("")
	if err != nil {
		t.Skipf("Redis unavailable (run docker compose up -d): %v", err)
	}
	project := "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := Open(r, project)
	if err != nil {
		t.Fatalf("Open(%q): %v", project, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		r.Del(ctx, StreamKey(project, "status"), StreamKey(project, "report"),
			StreamKey(project, "notify"), StreamKey(project, "cmd"), PilotKey(project))
		r.Close()
	})
	return b
}

func TestOpenRejectsBadProject(t *testing.T) {
	if _, err := Open(nil, "Bad:Project"); err == nil {
		t.Fatal("Open accepted an invalid project, want error")
	}
}

func TestPublishValidation(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "dev", "flying", "x"); err == nil {
		t.Error("Status accepted invalid state, want error")
	}
	if _, err := b.Status(ctx, "Bad Agent", "working", "x"); err == nil {
		t.Error("Status accepted invalid agent, want error")
	}
	if _, err := b.Cmd(ctx, "hermes", "dev", "shout", "", "x"); err == nil {
		t.Error("Cmd accepted invalid type, want error")
	}
	if _, err := b.Status(ctx, "dev", "working", "ok"); err != nil {
		t.Errorf("valid Status failed: %v", err)
	}
}
```

(Replace the existing `import "testing"` lines at the top of the file — there are now two test files; keep each file's imports correct. If `bus/stream_test.go` already has a single-line `import "testing"`, remove it in favor of this block.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run 'TestOpen|TestPublishValidation' -v`
Expected: FAIL — `undefined: Bus` / `b.Status undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `bus/stream.go`; expand the import block to:

```go
import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/redis/go-redis/v9"
)
```

Then add:

```go
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

func (b *Bus) Report(ctx context.Context, agent, kind, message string) (string, error) {
	if !ValidName(agent) {
		return "", fmt.Errorf("invalid agent %q", agent)
	}
	return b.add(ctx, "report", map[string]interface{}{
		"agent": agent, "kind": kind, "message": SanitizeReportMessage(message),
	})
}

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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run 'TestOpen|TestPublishValidation' -v`
Expected: PASS. (`TestPublishValidation` runs against Redis; if it SKIPs, run `docker compose up -d` and re-run — you want it green, not skipped.)

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): project-scoped Bus handle + XADD publishers"
```

---

### Task 4: Tail (read-only XREAD observer)

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestTailRoundTrip(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := b.Status(ctx, "dev", "working", "hello"); err != nil {
		t.Fatalf("Status: %v", err)
	}

	got := make(chan Event, 1)
	go func() {
		_ = b.Tail(ctx, "0", []string{"status"}, func(e Event) {
			select {
			case got <- e:
			default:
			}
		})
	}()

	select {
	case e := <-got:
		if e.Kind != "status" || e.Agent != "dev" || e.State != "working" || e.Message != "hello" {
			t.Fatalf("Tail event = %+v, want status/dev/working/hello", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Tail produced no event within 3s")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestTailRoundTrip -v`
Expected: FAIL — `b.Tail undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `bus/stream.go`; expand the import block to add `"errors"` and `"time"`:

```go
import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)
```

Then add:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestTailRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): Tail — read-only XREAD observer over project streams"
```

---

### Task 5: WatchCmd (per-agent consumer group delivery)

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestWatchCmdDelivers(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream := StreamKey(b.Project(), "cmd")
	// Pre-create dev's group at "0" so the test deterministically replays the
	// entries published next (WatchCmd's own MKSTREAM at "$" is then a no-op).
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "hermes", "other", CmdDirective, "", "not for dev"); err != nil {
		t.Fatalf("Cmd other: %v", err)
	}
	if _, err := b.Cmd(ctx, "review", "dev", CmdChallenge, "C1", "justify X"); err != nil {
		t.Fatalf("Cmd dev: %v", err)
	}

	got := make(chan Event, 1)
	go func() {
		_ = b.WatchCmd(ctx, "dev", "test-consumer", func(e Event) bool {
			got <- e
			return true // one-shot: stop on first entry addressed to dev
		})
	}()

	select {
	case e := <-got:
		if e.Target != "dev" || e.Type != CmdChallenge || e.Ref != "C1" || e.Message != "justify X" {
			t.Fatalf("WatchCmd delivered %+v, want the dev/challenge/C1 entry", e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("WatchCmd delivered nothing for dev within 4s")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestWatchCmdDelivers -v`
Expected: FAIL — `b.WatchCmd undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `bus/stream.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestWatchCmdDelivers -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): WatchCmd — per-agent consumer-group cmd delivery"
```

---

### Task 6: Pilot lease

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestPilotLease(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if d, err := b.PilotDriver(ctx); err != nil || d != "" {
		t.Fatalf("PilotDriver before claim = (%q, %v), want (\"\", nil)", d, err)
	}
	if err := b.Pilot(ctx, "hermes", 90*time.Second); err != nil {
		t.Fatalf("Pilot: %v", err)
	}
	if d, err := b.PilotDriver(ctx); err != nil || d != "hermes" {
		t.Fatalf("PilotDriver after claim = (%q, %v), want (\"hermes\", nil)", d, err)
	}
	if ttl := b.r.TTL(ctx, PilotKey(b.Project())).Val(); ttl <= 0 {
		t.Fatalf("pilot key TTL = %v, want > 0 (lease must expire)", ttl)
	}
	if err := b.ReleasePilot(ctx); err != nil {
		t.Fatalf("ReleasePilot: %v", err)
	}
	if d, err := b.PilotDriver(ctx); err != nil || d != "" {
		t.Fatalf("PilotDriver after release = (%q, %v), want (\"\", nil)", d, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestPilotLease -v`
Expected: FAIL — `b.PilotDriver undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `bus/stream.go`:

```go
// Pilot sets (or renews — they are the same SET) the project's pilot lease to
// driver with a TTL. Hermes calls this on an interval while it has budget;
// stopping = letting workers fall back to autonomous mode.
func (b *Bus) Pilot(ctx context.Context, driver string, ttl time.Duration) error {
	return b.r.Set(ctx, PilotKey(b.project), driver, ttl).Err()
}

// ReleasePilot drops the lease immediately (explicit hand-off to autonomous).
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestPilotLease -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): pilot lease (Pilot/ReleasePilot/PilotDriver)"
```

---

### Task 7: 4-eyes gate

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestChallengeGate(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	defer b.r.Del(ctx, GateKey(b.Project(), "dev"))

	if m, err := b.OpenChallenges(ctx, "dev"); err != nil || len(m) != 0 {
		t.Fatalf("OpenChallenges before = (%v, %v), want (empty, nil)", m, err)
	}
	if err := b.OpenChallenge(ctx, "dev", "C1", "review|justify X"); err != nil {
		t.Fatalf("OpenChallenge: %v", err)
	}
	m, err := b.OpenChallenges(ctx, "dev")
	if err != nil || len(m) != 1 || m["C1"] != "review|justify X" {
		t.Fatalf("OpenChallenges after open = (%v, %v), want {C1: review|justify X}", m, err)
	}
	if err := b.ResolveChallenge(ctx, "dev", "C1"); err != nil {
		t.Fatalf("ResolveChallenge: %v", err)
	}
	if m, err := b.OpenChallenges(ctx, "dev"); err != nil || len(m) != 0 {
		t.Fatalf("OpenChallenges after resolve = (%v, %v), want (empty, nil)", m, err)
	}

	if err := b.OpenChallenge(ctx, "dev", "", "no ref"); err == nil {
		t.Fatal("OpenChallenge accepted an empty ref, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestChallengeGate -v`
Expected: FAIL — `b.OpenChallenges undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `bus/stream.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestChallengeGate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): 4-eyes gate (OpenChallenge/ResolveChallenge/OpenChallenges)"
```

---

### Task 8: Full-package verification

**Files:** none (verification only)

- [ ] **Step 1: Build and vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: no output (binaries still compile — this phase added to `bus` without touching them).

- [ ] **Step 2: Run the full test suite**

Run: `go test ./... -v`
Expected: PASS for all `bus` tests (legacy `bus.go` tests + the new `stream_test.go` tests). The Redis-touching tests must PASS, not SKIP — if they SKIP, run `docker compose up -d` and re-run.

- [ ] **Step 3: Confirm no stray keys leak**

Run: `redis-cli -p 6380 -a 'AgentBus2025!' --no-auth-warning keys 't*'`
Expected: empty (each integration test deletes its throwaway `t<nano>` project on cleanup). If keys remain, a test's `t.Cleanup` is missing a key — fix before moving on.

- [ ] **Step 4: Phase-complete commit (no-op if everything was already committed)**

```bash
git status --short   # expect clean
```

If `bus/stream.go` or `bus/stream_test.go` have uncommitted formatting changes from `gofmt`, run `gofmt -w bus/stream.go bus/stream_test.go` then:

```bash
git add bus/stream.go bus/stream_test.go && git commit -m "chore(bus): gofmt streams foundation"
```

---

## Notes for the next phases (do NOT do here)

- **Phase 2 (`agentbus`)** wires `--project`/`AGENT_BUS_PROJECT`, switches `status`/`cmd`/`notify`/`report` onto the `Bus` methods, and adds `challenge`/`reply`/`verdict` (these call `Cmd` + `OpenChallenge`/`ResolveChallenge`), `pilot claim|renew|release|status`, `gate <agent>` (exit ≠ 0 when gated), and `watch <agent>` (wraps `WatchCmd` with a heartbeat-on-timeout and one-shot exit).
- **Phase 4** removes the legacy pub/sub API from `bus.go` (`Status`/`Cmd`/`Notify`/`Report`/`Listen`/`Parse`/`*Channel`/`ValidAgents`) once no binary references it, and updates `CLAUDE.md`.
- The `Bus` method API intentionally supersedes the package-level function signatures sketched in the spec's §6 — binding the project to a handle makes "project required" unforgeable. Keep the spec's behavior; the handle is the implementation shape.
