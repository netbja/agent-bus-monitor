# Bus Protocol Robustness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the agent bus robust for real multi-agent use — a persistable subscribe cursor that skips stale backlog, JSON-by-default subscribe output, a queryable per-agent state hash, a configurable report cap, and a CLAUDE.md drop-in so subagents inherit bus access.

**Architecture:** Five additive/breaking changes across the `bus` package and the `agentbus` CLI. The `bus` package keeps the protocol; the CLI is a thin client. Tasks are ordered so the tree compiles and all tests pass after **every** task (Task 4 passes `""` at the subscribe call sites as a behavior-preserving stopgap until Task 5 wires the real floor).

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, Redis Streams. Tests use a live dev broker (skip when unreachable).

## Global Constraints

- Module path: `github.com/netbja/agent-bus-monitor`. Go **1.26**.
- **`cmd/busmon/` must not be modified** by any task in this plan.
- Agent/project names must match `ValidName` (`^[a-z][a-z0-9_-]{0,31}$`); `ValidStates` (`working|idle|blocked|done`) is unchanged.
- Tests require a running broker: `docker compose up -d` (redis on `localhost:6380`). Redis-touching tests `t.Skip` when it is down — never fail.
- **Breaking change accepted:** the subscribe output cutover from the `[bracket]` + `__AGENTBUS__` text format to one JSON object per fire is intentional; the deprecated `__HEARTBEAT__` line is removed.
- Verification gate (run after each task, and a full pass at the end): `go build ./... && go vet ./... && go test ./... -count=1`.
- Every git commit message ends with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Work happens on branch `feat/bus-protocol-robustness` (already created; the spec is committed there).

---

## Task 1: Configurable report cap

Replace the hardcoded `maxReportLen = 120` with a resolver defaulting to 500, overridable via `AGENT_BUS_REPORT_MAX`. Read per call so tests can set it.

**Files:**
- Modify: `bus/bus.go` (the `maxReportLen` const + `SanitizeReportMessage`)
- Test: `bus/bus_test.go` (rewrite `TestSanitizeReportMessage`, add `TestReportMaxLenEnv`)

**Interfaces:**
- Consumes: nothing.
- Produces: `SanitizeReportMessage(string) string` (signature unchanged); internal `reportMaxLen() int`.

- [ ] **Step 1: Rewrite the failing tests**

Replace the entire body of `bus/bus_test.go` with:

```go
package bus

import (
	"strings"
	"testing"
)

func TestSanitizeReportMessage(t *testing.T) {
	if got := SanitizeReportMessage("line1\nline2\r\tend"); got != "line1 line2 end" {
		t.Fatalf("control chars: got %q, want %q", got, "line1 line2 end")
	}
	if got := SanitizeReportMessage("  spaced   out  "); got != "spaced out" {
		t.Fatalf("whitespace: got %q, want %q", got, "spaced out")
	}
	// default cap is 500 runes, then an ellipsis
	got := SanitizeReportMessage(strings.Repeat("x", 600))
	if r := []rune(got); len(r) != 501 || r[len(r)-1] != '…' {
		t.Fatalf("default truncation: got %d runes (last %q), want 501 + …", len(r), string(r[len(r)-1]))
	}
}

func TestReportMaxLenEnv(t *testing.T) {
	t.Setenv("AGENT_BUS_REPORT_MAX", "10")
	got := SanitizeReportMessage(strings.Repeat("y", 50))
	if r := []rune(got); len(r) != 11 || r[len(r)-1] != '…' {
		t.Fatalf("env cap: got %d runes, want 11 + …", len(r))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./bus/ -run 'TestSanitizeReportMessage|TestReportMaxLenEnv' -v`
Expected: FAIL (default expects 501 runes but current cap truncates to 121; `AGENT_BUS_REPORT_MAX` is ignored).

- [ ] **Step 3: Implement the resolver in `bus/bus.go`**

Add `strconv` to the import block:

```go
import (
	"context"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)
```

Replace `const maxReportLen = 120` with:

```go
const defaultReportMax = 500

// reportMaxLen resolves the report rune cap: AGENT_BUS_REPORT_MAX if it parses
// to a positive int, else defaultReportMax (500). Read per call so it stays
// settable from tests and per-process env.
func reportMaxLen() int {
	if v := os.Getenv("AGENT_BUS_REPORT_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultReportMax
}
```

Replace the truncation tail of `SanitizeReportMessage` (and update its doc comment from "maxReportLen runes" to "the resolved cap"):

```go
	out := strings.Join(strings.Fields(mapped), " ")
	if max := reportMaxLen(); len([]rune(out)) > max {
		out = strings.TrimSpace(string([]rune(out)[:max])) + "…"
	}
	return out
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./bus/ -run 'TestSanitizeReportMessage|TestReportMaxLenEnv' -v`
Expected: PASS (both).

- [ ] **Step 5: Verify build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add bus/bus.go bus/bus_test.go
git commit -m "feat(bus): configurable report cap (default 500, AGENT_BUS_REPORT_MAX)"
```

---

## Task 2: Agent-state hash + `Bus.Agents` (bus layer)

`Status` additionally writes a best-effort current-state snapshot to a `{project}:agents` hash; a new `Bus.Agents` reads it back.

**Files:**
- Modify: `bus/stream.go` (`encoding/json` import; `AgentsKey`, `AgentSnapshot`, `Bus.Agents`; hash write in `Status`)
- Test: `bus/stream_test.go` (add `AgentsKey` to `dialTest` cleanup; add `TestAgentsSnapshot`)

**Interfaces:**
- Consumes: existing `Bus.Status`, `splitID`, `Bus.add`.
- Produces:
  - `func AgentsKey(project string) string`
  - `type AgentSnapshot struct { State string `json:"state"`; Message string `json:"message,omitempty"`; TS int64 `json:"ts"` }`
  - `func (b *Bus) Agents(ctx context.Context) (map[string]AgentSnapshot, error)`

- [ ] **Step 1: Write the failing test**

Add to `bus/stream_test.go`:

```go
func TestAgentsSnapshot(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "dev", "working", "plan 10"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	m, err := b.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	s, ok := m["dev"]
	if !ok {
		t.Fatalf("Agents missing dev: %+v", m)
	}
	if s.State != "working" || s.Message != "plan 10" || s.TS == 0 {
		t.Fatalf("snapshot = %+v, want state=working message=plan 10 ts>0", s)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./bus/ -run TestAgentsSnapshot -v`
Expected: FAIL — `b.Agents undefined` (compile error).

- [ ] **Step 3: Implement the hash in `bus/stream.go`**

Add `encoding/json` to the import block (keep the others):

```go
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
```

Add these declarations near the other key helpers (e.g. just after `func GateKey(...)`):

```go
// AgentsKey is the per-project hash of current agent state ({agent} → JSON
// AgentSnapshot), written by Status and read by `agentbus agents`.
func AgentsKey(project string) string { return project + ":agents" }

// AgentSnapshot is the cached current state of one agent.
type AgentSnapshot struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	TS      int64  `json:"ts"` // ms since epoch, from the status entry's stream id
}
```

Replace the body of `Bus.Status` with (validation unchanged; add the cache write):

```go
func (b *Bus) Status(ctx context.Context, agent, state, message string) (string, error) {
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
	if snap, merr := json.Marshal(AgentSnapshot{State: state, Message: message, TS: ms}); merr == nil {
		_ = b.r.HSet(ctx, AgentsKey(b.project), agent, snap).Err()
	}
	return id, nil
}
```

Add the reader near the other read helpers (e.g. after `OpenChallenges`):

```go
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
```

- [ ] **Step 4: Add the new key to `dialTest` cleanup**

In `bus/stream_test.go`, extend the `r.Del(...)` call inside `dialTest`'s `t.Cleanup` to include `AgentsKey(project)`:

```go
		r.Del(ctx, StreamKey(project, "status"), StreamKey(project, "report"),
			StreamKey(project, "notify"), StreamKey(project, "cmd"), PilotKey(project),
			AgentsKey(project))
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./bus/ -run TestAgentsSnapshot -v`
Expected: PASS (or SKIP if Redis is down — then start it with `docker compose up -d` and re-run).

- [ ] **Step 6: Build + vet + commit**

Run: `go build ./... && go vet ./...`
Expected: clean.

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): per-agent state hash + Bus.Agents"
```

---

## Task 3: `agentbus agents` command (CLI layer)

A pure table formatter (unit-tested without Redis) plus a thin `agents` case that calls `Bus.Agents`.

**Files:**
- Create: `cmd/agentbus/agents.go` (`agentsTable`, `humanAge`, age constants)
- Create: `cmd/agentbus/agents_test.go`
- Modify: `cmd/agentbus/main.go` (`encoding/json` import; `agents` case; usage strings)

**Interfaces:**
- Consumes: `bus.AgentSnapshot`, `bus.Bus.Agents` (Task 2).
- Produces: `func agentsTable(map[string]bus.AgentSnapshot, time.Time) string`, `func humanAge(time.Duration) string`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/agentbus/agents_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestAgentsTable(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	m := map[string]bus.AgentSnapshot{
		"claude1": {State: "working", Message: "plan 10", TS: now.Add(-12 * time.Second).UnixMilli()},
		"hermes":  {State: "done", TS: now.Add(-11 * time.Minute).UnixMilli()},
	}
	out := agentsTable(m, now)
	if !strings.Contains(out, "claude1") || !strings.Contains(out, "working") || !strings.Contains(out, "plan 10") {
		t.Fatalf("missing claude1 row: %q", out)
	}
	if !strings.Contains(out, "12s ago") {
		t.Fatalf("claude1 age wrong: %q", out)
	}
	if !strings.Contains(out, "offline") {
		t.Fatalf("hermes (11m) should be offline: %q", out)
	}
	if strings.Index(out, "claude1") > strings.Index(out, "hermes") {
		t.Fatalf("rows not sorted by name: %q", out)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s ago"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
	}
	for _, c := range cases {
		if got := humanAge(c.d); got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/agentbus/ -run 'TestAgentsTable|TestHumanAge' -v`
Expected: FAIL — `agentsTable`/`humanAge` undefined (compile error).

- [ ] **Step 3: Implement `cmd/agentbus/agents.go`**

```go
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const (
	agentIdleAfter  = 2 * time.Minute
	agentStaleAfter = 10 * time.Minute
)

// humanAge renders a duration as a compact "Ns/Nm/Nh ago".
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// agentsTable renders the current state of every agent, one aged line each,
// sorted by name. Entries older than agentIdleAfter/agentStaleAfter are marked
// idle/offline (never deleted — age is the staleness signal).
func agentsTable(m map[string]bus.AgentSnapshot, now time.Time) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		s := m[n]
		age := now.Sub(time.UnixMilli(s.TS))
		marker := ""
		switch {
		case age > agentStaleAfter:
			marker = "  · offline"
		case age > agentIdleAfter:
			marker = "  · idle"
		}
		msg := ""
		if s.Message != "" {
			msg = "  (" + s.Message + ")"
		}
		fmt.Fprintf(&sb, "%-12s %-8s %-9s%s%s\n", n, s.State, humanAge(age), marker, msg)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./cmd/agentbus/ -run 'TestAgentsTable|TestHumanAge' -v`
Expected: PASS (both).

- [ ] **Step 5: Wire the `agents` case in `cmd/agentbus/main.go`**

Add `encoding/json` to the import block:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)
```

Add this `case` to the command `switch` (e.g. right after the `gate` case):

```go
	case "agents":
		_, asJSON := extractBool(rest, "--json")
		m, err := b.Agents(ctx)
		if err != nil {
			die(err.Error())
		}
		if asJSON {
			out, _ := json.MarshalIndent(m, "", "  ")
			fmt.Println(string(out))
			return
		}
		fmt.Print(agentsTable(m, time.Now()))
```

Update the top-level usage `die` string to include `agents`:

```go
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|pilot|gate|agents|subscribe|watch|listen> ...")
```

Add a line to the package doc comment near the other command examples (above the `subscribe` line):

```go
//	agentbus --project P agents    [--json]      # current state of all agents (one line each)
```

- [ ] **Step 6: Verify the full package + build/vet**

Run: `go build ./... && go vet ./... && go test ./cmd/agentbus/ -count=1`
Expected: clean; package tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/agentbus/agents.go cmd/agentbus/agents_test.go cmd/agentbus/main.go
git commit -m "feat(agentbus): add `agents` command (current state of all peers)"
```

---

## Task 4: `WatchCmd` floor + `ServerFloor` (bus layer)

Add a stream-id floor to `WatchCmd` (pre-floor entries are ACKed but not delivered) and a `ServerFloor` helper. Keep the tree green by passing `""` at the existing `subscribe.go` call sites — Task 5 threads the real floor.

**Files:**
- Modify: `bus/stream.go` (`WatchCmd` signature + body; `aboveFloor`; `ServerFloor`)
- Modify: `cmd/agentbus/subscribe.go` (pass `""` at both `WatchCmd` call sites — interim)
- Test: `bus/stream_test.go` (update `TestWatchCmdDelivers` call; add `TestWatchCmdFloorSkipsBacklog`)

**Interfaces:**
- Consumes: existing `idLess`, `splitID`, `ParseEntry`, `toStringMap`.
- Produces:
  - `func (b *Bus) WatchCmd(ctx context.Context, agent, consumer, floor string, fn func(Event) bool) error`
  - `func (b *Bus) ServerFloor(ctx context.Context) (string, error)`
  - internal `func aboveFloor(floor, id string) bool`

- [ ] **Step 1: Write the failing floor test + update the existing caller**

In `bus/stream_test.go`, update the `TestWatchCmdDelivers` call site to pass `""` (preserves current behavior):

```go
		_ = b.WatchCmd(ctx, "dev", "test-consumer", "", func(e Event) bool {
```

Then add the new test:

```go
func TestWatchCmdFloorSkipsBacklog(t *testing.T) {
	b := dialTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	stream := StreamKey(b.Project(), "cmd")
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	oldID, err := b.Cmd(ctx, "hermes", "dev", CmdDirective, "", "OLD")
	if err != nil {
		t.Fatalf("Cmd OLD: %v", err)
	}
	if _, err := b.Cmd(ctx, "hermes", "dev", CmdDirective, "", "NEW"); err != nil {
		t.Fatalf("Cmd NEW: %v", err)
	}
	var got Event
	werr := b.WatchCmd(ctx, "dev", "test-consumer", oldID, func(e Event) bool {
		got = e
		return true
	})
	if werr != nil {
		t.Fatalf("WatchCmd: %v", werr)
	}
	if got.Message != "NEW" {
		t.Fatalf("delivered %q, want NEW (OLD must be skipped by floor)", got.Message)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./bus/ -run 'TestWatchCmdDelivers|TestWatchCmdFloorSkipsBacklog' -v`
Expected: FAIL — `WatchCmd` still has the 4-arg signature (compile error: too many arguments).

- [ ] **Step 3: Change `WatchCmd` and add helpers in `bus/stream.go`**

Replace the `WatchCmd` signature line and the create/deliver logic. The new function:

```go
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
```

Add `ServerFloor` near `PilotDriver`:

```go
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
```

- [ ] **Step 4: Keep `cmd/agentbus/subscribe.go` compiling (interim `""`)**

In `cmd/agentbus/subscribe.go`, add `""` as the new `floor` argument at **both** `WatchCmd` call sites. The `--loop` site:

```go
		err := b.WatchCmd(ctx, agent, consumer, "", func(e bus.Event) bool {
```

The one-shot site:

```go
	werr := b.WatchCmd(wctx, agent, consumer, "", func(e bus.Event) bool {
```

(These are temporary; Task 5 replaces them with a threaded `floor`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./bus/ -run 'TestWatchCmdDelivers|TestWatchCmdFloorSkipsBacklog' -v`
Expected: PASS (both). Then the interim CLI behavior is unchanged: `go test ./cmd/agentbus/ -count=1` still PASS.

- [ ] **Step 6: Build + vet + commit**

Run: `go build ./... && go vet ./...`
Expected: clean.

```bash
git add bus/stream.go bus/stream_test.go cmd/agentbus/subscribe.go
git commit -m "feat(bus): WatchCmd stream-id floor + ServerFloor (skip backlog)"
```

---

## Task 5: subscribe JSON output + `--since` wiring (CLI layer)

Replace the bracket+sentinel text format with one JSON `subEvent` per fire, thread the floor from `--since` (default = server "now"), and rewrite the subscribe tests.

**Files:**
- Modify: `cmd/agentbus/subscribe.go` (full rewrite — `subEvent`, `emit`, `cmdEvent`; remove `watchOutcome`/`rearmSentinel`/`printCmd`; add `floor` param)
- Modify: `cmd/agentbus/main.go` (`--since` parse + server-now floor; doc comment)
- Test: `cmd/agentbus/subscribe_test.go` (rewrite for JSON; add floor test + `lastEvent` helper)

**Interfaces:**
- Consumes: `bus.Bus.WatchCmd(…, floor, …)` and `bus.Bus.ServerFloor` (Task 4).
- Produces: `func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, floor string, loop bool, out io.Writer) int`; `type subEvent`.

- [ ] **Step 1: Rewrite `cmd/agentbus/subscribe_test.go`**

Replace the entire file with (drops `TestRearmSentinel`/`TestPrintCmd`, adds JSON + floor tests; keeps `syncBuf`/`dialMain`):

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)

// lastEvent parses the final non-empty JSON line emitted by runSubscribe.
func lastEvent(t *testing.T, out string) subEvent {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var ev subEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatalf("output last line not JSON: %q (%v)", out, err)
	}
	return ev
}

// syncBuf is a goroutine-safe io.Writer so the --loop test can read what the
// background subscriber has written without a data race.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// dialMain returns a project-scoped Bus on a throwaway project plus a raw client
// (tests need the client to pre-create consumer groups). Skips if Redis is down.
func dialMain(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	r, err := bus.Connect("")
	if err != nil {
		t.Skipf("Redis unavailable (run docker compose up -d): %v", err)
	}
	project := "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, err := bus.Open(r, project)
	if err != nil {
		t.Fatalf("Open(%q): %v", project, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		r.Del(ctx, bus.StreamKey(project, "cmd"), bus.ArmedKey(project, "dev"))
		r.Close()
	})
	return b, r
}

func TestRunSubscribeFatalOnBadAgent(t *testing.T) {
	var buf bytes.Buffer
	// b is nil on purpose: ValidName rejects the agent before any Redis call.
	code := runSubscribe(context.Background(), nil, "Bad Agent", "host-1", time.Second, "0", false, &buf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (fatal)", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "fatal" || ev.Rearm == nil || *ev.Rearm {
		t.Errorf("event = %+v, want event=fatal rearm=false", ev)
	}
}

func TestRunSubscribeDelivers(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	// Pre-create dev's group at "0" so the already-published entry is delivered
	// deterministically; floor "0" disables the skip-backlog filter.
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "review", "dev", bus.CmdChallenge, "C1", "justify X"); err != nil {
		t.Fatalf("Cmd: %v", err)
	}

	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, "0", false, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (delivered)", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "cmd" || ev.Rearm == nil || !*ev.Rearm {
		t.Fatalf("event = %+v, want cmd rearm=true", ev)
	}
	if ev.From != "review" || ev.Target != "dev" || ev.Type != "challenge" || ev.Ref != "C1" || ev.Body != "justify X" {
		t.Errorf("payload = %+v, want review/dev/challenge/C1/justify X", ev)
	}
	if ev.ID == "" {
		t.Error("cmd event missing id (cursor)")
	}
}

func TestRunSubscribeFloorSkipsBacklog(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	oldID, err := b.Cmd(ctx, "hermes", "dev", bus.CmdDirective, "", "OLD")
	if err != nil {
		t.Fatalf("Cmd OLD: %v", err)
	}
	if _, err := b.Cmd(ctx, "hermes", "dev", bus.CmdDirective, "", "NEW"); err != nil {
		t.Fatalf("Cmd NEW: %v", err)
	}
	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, oldID, false, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Body != "NEW" {
		t.Fatalf("delivered body=%q, want NEW (OLD skipped by floor)", ev.Body)
	}
}

func TestRunSubscribeHeartbeat(t *testing.T) {
	b, _ := dialMain(t)
	var buf bytes.Buffer
	// No cmd published: WatchCmd blocks, the 1s idle window elapses → heartbeat.
	code := runSubscribe(context.Background(), b, "dev", "host-1", 1*time.Second, "0", false, &buf)
	if code != 64 {
		t.Fatalf("exit code = %d, want 64 (heartbeat)", code)
	}
	ev := lastEvent(t, buf.String())
	if ev.Event != "heartbeat" || ev.Rearm == nil || !*ev.Rearm {
		t.Errorf("event = %+v, want heartbeat rearm=true", ev)
	}
}

func TestRunSubscribeLoopDeliversMany(t *testing.T) {
	b, r := dialMain(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := bus.StreamKey(b.Project(), "cmd")
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := b.Cmd(ctx, "hermes", "dev", bus.CmdDirective, "", "do "+strconv.Itoa(i)); err != nil {
			t.Fatalf("Cmd: %v", err)
		}
	}

	buf := &syncBuf{}
	done := make(chan int, 1)
	go func() { done <- runSubscribe(ctx, b, "dev", "host-1", 2*time.Second, "0", true, buf) }()

	deadline := time.After(5 * time.Second)
	for !(strings.Contains(buf.String(), "do 0") && strings.Contains(buf.String(), "do 1")) {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("loop did not deliver both cmds: %q", buf.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel() // stop the loop
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("loop exit code = %d, want 0 on ctx cancel", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not return after ctx cancel")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/agentbus/ -run TestRunSubscribe -v`
Expected: FAIL — `subEvent` undefined and `runSubscribe` still has the old 7-arg signature (compile errors).

- [ ] **Step 3: Rewrite `cmd/agentbus/subscribe.go`**

Replace the entire file with:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// subEvent is the single JSON object subscribe emits per fire. One parse gives
// the caller the payload (event=cmd), the cursor to persist (id), and whether
// to re-arm (rearm). rearm is a *bool so fatal's rearm:false survives omitempty
// while --loop entries omit the field entirely.
type subEvent struct {
	Event  string `json:"event"`
	Rearm  *bool  `json:"rearm,omitempty"`
	ID     string `json:"id,omitempty"`
	Type   string `json:"type,omitempty"`
	From   string `json:"from,omitempty"`
	Target string `json:"target,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Body   string `json:"body,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

// cmdEvent builds the subEvent for a delivered cmd entry. rearm is nil for the
// headless --loop (no wake semantics) and &true for a one-shot delivery.
func cmdEvent(e bus.Event, rearm *bool) subEvent {
	return subEvent{
		Event: "cmd", Rearm: rearm, ID: e.ID,
		Type: e.Type, From: e.From, Target: e.Target, Ref: e.Ref, Body: e.Message,
	}
}

// emit writes one subEvent as a single JSON line.
func emit(out io.Writer, ev subEvent) {
	b, _ := json.Marshal(ev)
	fmt.Fprintln(out, string(b))
}

// runSubscribe performs one subscribe tick (or a continuous --loop) and returns
// the process exit code. floor is the stream-id floor passed to WatchCmd ("" or
// "0" = no floor). It arms a presence lease around the WatchCmd block and always
// disarms on return (the caller os.Exits on the returned code, so this function
// must never os.Exit itself — that would skip the defer).
func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, floor string, loop bool, out io.Writer) int {
	if !bus.ValidName(agent) {
		emit(out, subEvent{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent " + agent})
		return 1
	}
	_ = b.Arm(ctx, agent, consumer, idle)       // best-effort observability
	defer b.Disarm(context.Background(), agent) // runs on return (never on os.Exit)

	if loop {
		// Headless continuous mode: keep the lease warm and emit every addressed
		// cmd object; never exit on delivery. rearm is omitted (no wake path).
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			tick := idle / 2
			if tick <= 0 {
				tick = time.Second
			}
			tk := time.NewTicker(tick)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					_ = b.Arm(ctx, agent, consumer, idle)
				}
			}
		}()
		err := b.WatchCmd(ctx, agent, consumer, floor, func(e bus.Event) bool {
			emit(out, cmdEvent(e, nil))
			return false // never "done" → consume continuously
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			emit(out, subEvent{Event: "error", Rearm: boolPtr(true), Msg: err.Error()})
			return 75
		}
		return 0
	}

	var last bus.Event
	wctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()
	werr := b.WatchCmd(wctx, agent, consumer, floor, func(e bus.Event) bool {
		last = e
		return true // one-shot: stop on the first addressed entry
	})
	switch {
	case werr == nil:
		emit(out, cmdEvent(last, boolPtr(true)))
		return 0
	case errors.Is(werr, context.DeadlineExceeded):
		emit(out, subEvent{Event: "heartbeat", Rearm: boolPtr(true)})
		return 64
	default:
		emit(out, subEvent{Event: "error", Rearm: boolPtr(true), Msg: werr.Error()})
		return 75
	}
}
```

- [ ] **Step 4: Thread `--since`/floor in `cmd/agentbus/main.go`**

Replace the whole `case "subscribe", "watch":` block with:

```go
	case "subscribe", "watch":
		// One subscription tick (or a headless --loop). Emits one JSON subEvent
		// per fire: the cmd payload + cursor (id) + rearm flag. --since <cursor>
		// sets the floor; omitted = skip backlog (start at the server's "now").
		rest, loop := extractBool(rest, "--loop")
		rest, since := extractFlag(rest, "--since")
		if len(rest) < 1 {
			die("usage: subscribe [--loop] [--since <cursor>] <agent> [idle_seconds]")
		}
		agent := rest[0]
		idle := heartbeat
		if len(rest) > 1 {
			idle = parseIdle(rest[1], heartbeat)
		}
		floor := since
		switch {
		case floor == "":
			f, ferr := b.ServerFloor(ctx)
			if ferr != nil {
				die("could not resolve server-time floor: " + ferr.Error())
			}
			floor = f
		case floor != "0" && !strings.Contains(floor, "-"):
			floor += "-0" // accept a bare <ms> cursor
		}
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		os.Exit(runSubscribe(ctx, b, agent, consumer, idle, floor, loop, os.Stdout))
```

Update the `subscribe` line in the package doc comment to:

```go
//	agentbus --project P subscribe [--since <cursor>] <agent> [idle_secs]  # JSON per fire; persist id, pass back as --since
```

- [ ] **Step 5: Run the subscribe tests to verify they pass**

Run: `go test ./cmd/agentbus/ -run TestRunSubscribe -v`
Expected: PASS (all five; or SKIP if Redis is down — start it and re-run).

- [ ] **Step 6: Build + vet + full package test + commit**

Run: `go build ./... && go vet ./... && go test ./cmd/agentbus/ -count=1`
Expected: clean; PASS.

```bash
git add cmd/agentbus/subscribe.go cmd/agentbus/main.go cmd/agentbus/subscribe_test.go
git commit -m "feat(agentbus): JSON-per-fire subscribe + --since cursor floor"
```

---

## Task 6: Docs — CLAUDE.md drop-in + JSON contract rewrite

Update `docs/AGENT-BUS-GUIDE.md`: add the consuming-project drop-in block, add `agents` to the cheat sheet, and rewrite the subscribe lines + sentinel table to the JSON contract (this doubles as the migration doc).

**Files:**
- Modify: `docs/AGENT-BUS-GUIDE.md`

**Interfaces:** none (documentation). Match the current text anchors below; if they have drifted, preserve intent.

- [ ] **Step 1: Add the drop-in section**

Immediately after the intro block (the first `---` near the top, before `## 0. Read this first`), insert:

````markdown
## Drop this into your project's CLAUDE.md

`CLAUDE.md` is the one file every subagent reads. Paste this block into the
consuming project's `CLAUDE.md` (fill the two names) so subagents inherit bus
access without re-asking:

```markdown
## Agent Bus (coordination over Redis Streams)
- CLI: `agentbus` (from github.com/netbja/agent-bus-monitor; `go install ./...`).
- Identity / namespace (export once):
  `export AGENT_BUS_PROJECT=<project>` and `export AGENT_BUS_AGENT=<your-agent>`.
- Receive directives — arm as a background task; its exit wakes you, then re-arm:
  `agentbus subscribe "$AGENT_BUS_AGENT" --since "$LAST_CURSOR"`  # one JSON line/fire; persist its `id`
- Publish your state (this IS your heartbeat):
  `agentbus status "$AGENT_BUS_AGENT" working "<msg>"`
- Peers' current state: `agentbus agents`. Full reference: docs/AGENT-BUS-GUIDE.md.
```

---
````

- [ ] **Step 2: Add `agents` + update `subscribe` lines in the §2 cheat sheet**

In `## 2. Cheat sheet`, add a peers block (e.g. just before the `# ── INBOUND` block):

```bash
# ── PEERS: current state of every agent (one line each) ───────────────────────
agentbus agents                                         # name · state · (message) · age; marks idle/offline
agentbus agents --json                                  # raw map for scripts
```

Replace the `# ── INBOUND` block's `subscribe` lines with:

```bash
# ── INBOUND: wait for a command addressed to you ─────────────────────────────
agentbus subscribe [--since <cursor>] <agent> [idle_secs]   # blocks for ONE cmd, emits ONE JSON object, EXITS; default idle 240s
agentbus subscribe claude1                              # no --since = skip backlog, start at "now"; arm as a background task
agentbus subscribe --since 1782053749061-3 claude1      # resume after a persisted cursor (the `id` from the last fire)
agentbus subscribe claude1 3600                         # 1h idle window before it heartbeats and exits
agentbus subscribe --loop hermes                        # HEADLESS callers only (hermes/shell): consume continuously, never exit
agentbus watch claude1                                  # legacy alias of subscribe
```

- [ ] **Step 3: Rewrite the §3 `subscribe is wake-on-exit` subsection**

Replace the subsection that begins `### `subscribe` is wake-on-exit, not a long loop` (through its sentinel table and the paragraph ending `there is no wrapper script and no watcher daemon.`) with:

````markdown
### `subscribe` is wake-on-exit, not a long loop
`agentbus subscribe <self>` **blocks until one command addressed to you arrives,
emits ONE JSON object, then exits.** Arm it as a Claude Code background task; its
exit wakes your session, and you re-arm. After the idle window (default 240s, or
`[idle_secs]`) it emits a heartbeat object and exits so you can re-arm.

Each fire is exactly one JSON line — parse it once. **Re-arm iff `rearm` is `true`:**

| You see                                                              | Meaning            | Exit | Re-arm? |
|----------------------------------------------------------------------|--------------------|------|---------|
| `{"event":"cmd","rearm":true,"id":"…","type":"…","from":"…","target":"…","ref":"…","body":"…"}` | a command arrived  | 0    | yes     |
| `{"event":"heartbeat","rearm":true}`                                 | idle window passed | 64   | yes     |
| `{"event":"error","rearm":true,"msg":"…"}`                           | transient glitch   | 75   | yes     |
| `{"event":"fatal","rearm":false,"msg":"…"}`                          | misconfigured      | 1    | **no**  |

**Persist the `id`** from each `cmd` fire and pass it back as `--since <id>` next
time you arm — that is your cursor. With no `--since`, subscribe starts at the
broker's "now" and never replays archived backlog, so a fresh session is never
flooded by stale commands. Pass `--since 0` only if you deliberately want full
at-least-once replay.

**While armed and waiting you are `idle`, never `blocked`** — `blocked` is
reserved for an open 4-eyes gate. busmon shows a `👂` badge next to armed agents.
**Do not** wrap `subscribe` in a `while` loop or a daemon — a long-lived loop
never wakes a terminal session. (The one exception is `--loop`, for **headless**
consumers like hermes; it emits one `cmd` object per entry, with no `rearm`.) The
whole loop lives in the binary; there is no wrapper script and no watcher daemon.
````

- [ ] **Step 4: Full verification gate**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: everything compiles, vet clean, all tests PASS (Redis-touching tests PASS with the broker up, else SKIP).

- [ ] **Step 5: Commit**

```bash
git add docs/AGENT-BUS-GUIDE.md
git commit -m "docs: CLAUDE.md drop-in + JSON subscribe contract for agents"
```

---

## Self-Review

**Spec coverage:**
- Friction #1 (cursor + `--since` skip-backlog) → Task 4 (`WatchCmd` floor, `ServerFloor`) + Task 5 (`--since` parse, `id` in `subEvent`). ✓
- Friction #2 (state hash + `agents`) → Task 2 (`AgentsKey`/`AgentSnapshot`/`Agents`/Status write) + Task 3 (`agents` command). ✓
- Friction #3 (configurable report cap) → Task 1. ✓
- Friction #4 (JSON-by-default) → Task 5 (`subEvent`, one object per fire). ✓
- Friction #5 (CLAUDE.md drop-in) → Task 6. ✓
- Spec "busmon untouched" → no task modifies `cmd/busmon/`. ✓
- Spec "deprecated `__HEARTBEAT__` removed" → Task 5 rewrite drops it. ✓

**Type consistency:** `WatchCmd(ctx, agent, consumer, floor string, fn)` is defined in Task 4 and called with the new arity in Tasks 4 (interim `""`) and 5 (threaded `floor`). `AgentSnapshot{State, Message, TS}` is identical in Tasks 2 and 3. `runSubscribe(…, idle, floor, loop, out)` signature matches between Task 5's impl and its tests. `subEvent.Rearm` is `*bool` everywhere; tests compare via `ev.Rearm == nil`/`*ev.Rearm`.

**Placeholder scan:** no TBD/TODO/"handle errors appropriately"; every code step shows complete code; the `<project>`/`<your-agent>`/`<cursor>` tokens are intentional doc template fields.

**Build-green ordering:** each task ends green because Task 4 supplies the interim `""` floor at the subscribe call sites before Task 5 rewrites them.
