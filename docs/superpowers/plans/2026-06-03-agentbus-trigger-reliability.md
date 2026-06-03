# Agentbus Trigger Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `agentbus subscribe` trigger reliable and observable — a waiting agent shows as armed (not "blocked"), re-arming is uniform across runtimes via a structured exit contract, and busmon shows who is listening plus any command backlog.

**Architecture:** All protocol logic lands in `bus/stream.go` (a TTL'd "armed" presence lease + `XINFO GROUPS` backlog read), mirroring the existing pilot-lease shape. `cmd/agentbus` gains a thin, testable `runSubscribe` that arms/disarms around the existing `WatchCmd`, prints a uniform `__AGENTBUS__` sentinel, and supports an opt-in `--loop` for headless callers. `cmd/busmon` renders two new badges (`👂` armed, `⌛N` backlog) from its existing 1s ticker — no new goroutine.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, `tview`/`tcell` (busmon TUI). Tests run against the dev broker (`docker compose up -d`; tests skip if Redis is down).

**Spec:** `docs/superpowers/specs/2026-06-03-agentbus-trigger-reliability-design.md`

**Branch:** `feat/busmon-trigger-reliability` (already checked out).

---

## File structure

| File | Responsibility | Change |
|------|----------------|--------|
| `bus/stream.go` | Presence lease (`ArmedKey`/`Arm`/`Disarm`/`ArmedAgents`) + backlog (`CmdLag`) | Modify |
| `bus/stream_test.go` | Tests for the new primitives | Modify |
| `cmd/agentbus/subscribe.go` | `watchOutcome`, `rearmSentinel`, `printCmd`, `runSubscribe` (NEW file — keeps `main.go` thin) | Create |
| `cmd/agentbus/subscribe_test.go` | Pure + integration tests for the subscribe logic | Create |
| `cmd/agentbus/main.go` | `subscribe`/`watch` case delegates to `runSubscribe`; drop now-unused `errors` import | Modify |
| `cmd/busmon/main.go` | `agentState` gains `armed`/`lag`; extract `agentLabel`; ticker reads armed + lag | Modify |
| `cmd/busmon/render_test.go` | Test `agentLabel` badge rendering | Modify |
| `docs/AGENT-BUS-GUIDE.md` | Vocabulary fix, exit contract, `--loop` | Modify |
| `README.md` | Document the `👂`/`⌛N` badges + `{p}:armed:{agent}` key | Modify |

---

## Task 1: bus presence lease (`ArmedKey`, `Arm`, `Disarm`, `ArmedAgents`)

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestArmedLease(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	t.Cleanup(func() { b.r.Del(ctx, ArmedKey(b.Project(), "dev")) })

	if got := ArmedKey("busmon", "dev"); got != "busmon:armed:dev" {
		t.Fatalf("ArmedKey = %q, want busmon:armed:dev", got)
	}
	if m, err := b.ArmedAgents(ctx); err != nil || len(m) != 0 {
		t.Fatalf("ArmedAgents before arm = (%v, %v), want (empty, nil)", m, err)
	}
	if err := b.Arm(ctx, "dev", "host-1", 30*time.Second); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	m, err := b.ArmedAgents(ctx)
	if err != nil || len(m) != 1 || m["dev"] != "host-1" {
		t.Fatalf("ArmedAgents after arm = (%v, %v), want {dev:host-1}", m, err)
	}
	if ttl := b.r.TTL(ctx, ArmedKey(b.Project(), "dev")).Val(); ttl <= 0 {
		t.Fatalf("armed key TTL = %v, want > 0 (lease must self-expire)", ttl)
	}
	if err := b.Disarm(ctx, "dev"); err != nil {
		t.Fatalf("Disarm: %v", err)
	}
	if m, err := b.ArmedAgents(ctx); err != nil || len(m) != 0 {
		t.Fatalf("ArmedAgents after disarm = (%v, %v), want (empty, nil)", m, err)
	}
	if err := b.Arm(ctx, "Bad Agent", "host-1", 30*time.Second); err == nil {
		t.Error("Arm accepted an invalid agent, want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestArmedLease`
Expected: FAIL — `undefined: ArmedKey`, `b.ArmedAgents`, `b.Arm`, `b.Disarm`.

- [ ] **Step 3: Implement the primitives**

In `bus/stream.go`, add the key helper next to the existing `PilotKey`/`GateKey` (after line 43):

```go
func ArmedKey(project, agent string) string { return project + ":armed:" + agent }
```

Then add these methods near the pilot helpers (e.g. after `PilotDriver`, ~line 283):

```go
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
```

`time`, `fmt`, and `strings` are already imported in `stream.go`. No new imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestArmedLease -count=1`
Expected: PASS (or SKIP if Redis is down — then run `docker compose up -d` first).

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): armed presence lease (Arm/Disarm/ArmedAgents)"
```

---

## Task 2: bus backlog introspection (`CmdLag`)

**Files:**
- Modify: `bus/stream.go`
- Test: `bus/stream_test.go`

- [ ] **Step 1: Write the failing test**

Append to `bus/stream_test.go`:

```go
func TestCmdLag(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	stream := StreamKey(b.Project(), "cmd")

	// No cmd stream yet → no groups → empty lag, no error.
	if m, err := b.CmdLag(ctx); err != nil || len(m) != 0 {
		t.Fatalf("CmdLag before any group = (%v, %v), want (empty, nil)", m, err)
	}

	// dev's group reads from the start ("0"), so published-but-unread entries
	// register as lag.
	if err := b.r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := b.Cmd(ctx, "hermes", "dev", CmdDirective, "", "do "+strconv.Itoa(i)); err != nil {
			t.Fatalf("Cmd: %v", err)
		}
	}
	m, err := b.CmdLag(ctx)
	if err != nil || m["dev"] != 3 {
		t.Fatalf("CmdLag after 3 unread = (%v, %v), want dev:3", m, err)
	}
}
```

`strconv` is already imported in `stream_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./bus/ -run TestCmdLag`
Expected: FAIL — `b.CmdLag undefined`.

- [ ] **Step 3: Implement `CmdLag`**

In `bus/stream.go`, add after `ArmedAgents`:

```go
// CmdLag returns, per consumer group on the project's cmd stream, how many
// entries the group has not yet read (XINFO GROUPS "lag"). Group name == agent
// name (see WatchCmd), so the result is agent→backlog. A non-zero backlog for an
// agent with no live armed lease is busmon's "stopped listening" signal. The
// stream not existing yet is not an error — it just means no backlog.
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
```

`XInfoGroup.Lag` is available on `go-redis/v9` (v9.19.0, verified) against Redis 7+ (the broker is `redis:8-alpine`). Note: `Lag` is `-1` when it cannot be determined (e.g. after the stream is trimmed at the ~1000 cap). `CmdLag` passes that through unchanged; busmon's `agentLabel` only renders the `⌛` badge when `lag > 0`, so `-1` harmlessly shows no badge — do **not** clamp it to 0 in `CmdLag` (the test asserts the real value).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./bus/ -run TestCmdLag -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): CmdLag backlog introspection via XINFO GROUPS"
```

---

## Task 3: busmon `agentLabel` extraction + badge rendering

**Files:**
- Modify: `cmd/busmon/main.go:40-45` (struct), `cmd/busmon/main.go:125-156` (renderAgents)
- Test: `cmd/busmon/render_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/busmon/render_test.go`:

```go
func TestAgentLabel(t *testing.T) {
	now := time.Now()

	base := &agentState{state: "working", lastSeen: now}
	if got := agentLabel("dev", base, now); !strings.Contains(got, "dev: working") {
		t.Fatalf("agentLabel base = %q, want it to show 'dev: working'", got)
	}
	if strings.Contains(agentLabel("dev", base, now), "👂") {
		t.Fatal("unarmed agent should not show the listening badge")
	}

	armed := &agentState{state: "idle", lastSeen: now, armed: true}
	if got := agentLabel("dev", armed, now); !strings.Contains(got, "👂") {
		t.Fatalf("armed agent label = %q, want a 👂 badge", got)
	}

	// Backlog while listening → yellow ⌛ (normal/transient).
	busy := &agentState{state: "idle", lastSeen: now, armed: true, lag: 2}
	if got := agentLabel("dev", busy, now); !strings.Contains(got, "⌛2") || !strings.Contains(got, "[yellow]") {
		t.Fatalf("armed+lag label = %q, want a yellow ⌛2", got)
	}

	// Backlog with nobody listening → the "stopped listening" tell, orange ⌛.
	dead := &agentState{state: "idle", lastSeen: now, armed: false, lag: 5}
	if got := agentLabel("dev", dead, now); !strings.Contains(got, "⌛5") || !strings.Contains(got, "[orange]") {
		t.Fatalf("unarmed+lag label = %q, want an orange ⌛5 warning", got)
	}

	gated := &agentState{state: "working", lastSeen: now, gated: 1}
	if got := agentLabel("dev", gated, now); !strings.Contains(got, "🔒1") {
		t.Fatalf("gated agent label = %q, want a 🔒1 badge", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/busmon/ -run TestAgentLabel`
Expected: FAIL — `agentLabel undefined` and `agentState` has no `armed`/`lag` fields.

- [ ] **Step 3: Add the struct fields**

In `cmd/busmon/main.go`, replace the `agentState` struct (lines 40-45):

```go
type agentState struct {
	state    string
	message  string
	lastSeen time.Time
	gated    int   // open 4-eyes challenges; >0 shows a lock badge
	armed    bool  // a live subscribe lease exists → 👂 listening badge
	lag      int64 // unconsumed {p}:cmd entries for this agent → ⌛ backlog badge
}
```

- [ ] **Step 4: Extract `agentLabel` and call it from `renderAgents`**

In `cmd/busmon/main.go`, add `agentLabel` immediately before `renderAgents` (before line 125):

```go
// agentLabel renders one agent's AGENTS-pane chip: the aged state, then badges
// for listening (👂), command backlog (⌛N — orange when nobody is listening),
// and open 4-eyes challenges (🔒N).
func agentLabel(n string, a *agentState, now time.Time) string {
	var label string
	switch age := now.Sub(a.lastSeen); {
	case age > staleAfter:
		label = tag("gray", n+": offline")
	case age > idleAfter:
		label = tag("yellow", fmt.Sprintf("%s: idle %dm", n, int(age.Minutes())))
	default:
		label = tag(stateColor(a.state), n+": "+a.state)
		if a.message != "" {
			label += " " + tview.Escape("("+clip(a.message, 48)+")")
		}
	}
	if a.armed {
		label += " [green]👂[-]"
	}
	if a.lag > 0 {
		color := "yellow" // listening but behind — transient
		if !a.armed {
			color = "orange" // backlog with no listener — the "stopped re-arming" tell
		}
		label += fmt.Sprintf(" [%s]⌛%d[-]", color, a.lag)
	}
	if a.gated > 0 {
		label += fmt.Sprintf(" [red]🔒%d[-]", a.gated)
	}
	return label
}
```

Then replace the per-agent loop body inside `renderAgents` (lines 135-154, from `parts := make(...)` through the `parts = append(...)` loop) with:

```go
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, agentLabel(n, agents[n], now))
	}
```

Leave the surrounding `renderAgents` code (the `mu.Lock()`, `SetTitle`, `names` sort, `now := time.Now()`, and final `view.SetText(strings.Join(parts, "   "))`) unchanged.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/busmon/ -run TestAgentLabel -count=1`
Expected: PASS.

- [ ] **Step 6: Compile-check the package**

Run: `go build ./cmd/busmon/`
Expected: builds clean (the inline loop body was fully replaced; no dangling references).

- [ ] **Step 7: Commit**

```bash
git add cmd/busmon/main.go cmd/busmon/render_test.go
git commit -m "feat(busmon): listening (👂) + backlog (⌛N) badges via agentLabel"
```

---

## Task 4: busmon ticker wiring (read armed + lag each second)

**Files:**
- Modify: `cmd/busmon/main.go:429-457` (the poll goroutine)

This is glue around a tview goroutine — verified by compile + vet, not a unit test (the data primitives are already covered by Tasks 1-3).

- [ ] **Step 1: Replace the poll goroutine body**

In `cmd/busmon/main.go`, replace the entire second `go func() { ... }()` block (the pilot/gate poller, lines 429-457) with:

```go
	// Poll pilot mode + per-agent gate counts + armed leases + cmd backlog off
	// the UI thread; re-render so chips age and badges update with no new traffic.
	go func() {
		for range time.Tick(time.Second) {
			driver, _ := b.PilotDriver(ctx)
			armed, _ := b.ArmedAgents(ctx)
			lag, _ := b.CmdLag(ctx)
			mu.Lock()
			names := make([]string, 0, len(agents))
			for n := range agents {
				names = append(names, n)
			}
			mu.Unlock()
			gates := make(map[string]int, len(names))
			for _, n := range names {
				if m, err := b.OpenChallenges(ctx, n); err == nil {
					gates[n] = len(m)
				}
			}
			mu.Lock()
			pilot = driver
			// Surface agents known only via a live armed lease (subscribed but no
			// status published yet). Armed keys are TTL'd, so this never leaks a
			// ghost. Lag-only groups are NOT synthesized — consumer groups persist
			// after an agent is gone, so a stale group must not conjure a chip.
			for n := range armed {
				if agents[n] == nil {
					agents[n] = &agentState{state: "active", lastSeen: time.Now()}
				}
			}
			for n, a := range agents {
				_, a.armed = armed[n]
				a.lag = lag[n]
				if c, ok := gates[n]; ok {
					a.gated = c
				}
			}
			mu.Unlock()
			app.QueueUpdateDraw(func() {
				renderAgents(agentsView, agents, &mu, &pilot)
				refreshTitle()
			})
		}
	}()
```

- [ ] **Step 2: Compile + vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: no output (clean build + vet).

- [ ] **Step 3: Manual smoke (optional but recommended)**

```bash
docker compose up -d
go build -o busmon ./cmd/busmon && go build -o agentbus ./cmd/agentbus
AGENT_BUS_PROJECT=smoke ./agentbus subscribe dev 30 &   # arm in the background
AGENT_BUS_PROJECT=smoke ./busmon                        # expect a 👂 next to "dev"
```
Expected: `dev` shows a `👂` badge while the subscriber is armed; it clears within ~30s of the subscriber exiting. (Quit busmon with `q`/`Esc`.)

- [ ] **Step 4: Commit**

```bash
git add cmd/busmon/main.go
git commit -m "feat(busmon): poll armed leases + cmd backlog in the 1s ticker"
```

---

## Task 5: agentbus pure helpers (`watchOutcome`, `rearmSentinel`, `printCmd`)

**Files:**
- Create: `cmd/agentbus/subscribe.go`
- Test: `cmd/agentbus/subscribe_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/agentbus/subscribe_test.go`:

```go
package main

import (
	"bytes"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestRearmSentinel(t *testing.T) {
	cases := []struct {
		name           string
		outcome        watchOutcome
		ref, from, msg string
		wantLine       string
		wantCode       int
	}{
		{"cmd", outcomeCmd, "C1", "hermes", "", "__AGENTBUS__ event=cmd rearm=1 ref=C1 from=hermes", 0},
		{"heartbeat", outcomeHeartbeat, "", "", "", "__AGENTBUS__ event=heartbeat rearm=1", 64},
		{"transient", outcomeTransient, "", "", "broker down", "__AGENTBUS__ event=error rearm=1 msg=broker down", 75},
		{"fatal", outcomeFatal, "", "", "invalid agent", "__AGENTBUS__ event=fatal rearm=0 msg=invalid agent", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, code := rearmSentinel(c.outcome, c.ref, c.from, c.msg)
			if line != c.wantLine {
				t.Errorf("line = %q, want %q", line, c.wantLine)
			}
			if code != c.wantCode {
				t.Errorf("code = %d, want %d", code, c.wantCode)
			}
		})
	}
}

func TestPrintCmd(t *testing.T) {
	var buf bytes.Buffer
	printCmd(&buf, bus.Event{Type: "directive", From: "hermes", Target: "dev", Message: "run it"})
	if got := buf.String(); got != "[directive hermes->dev] run it\n" {
		t.Fatalf("printCmd = %q", got)
	}
	buf.Reset()
	printCmd(&buf, bus.Event{Type: "challenge", From: "review", Target: "dev", Ref: "C1", Message: "justify"})
	if got := buf.String(); got != "[challenge review->dev ref=C1] justify\n" {
		t.Fatalf("printCmd with ref = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/agentbus/ -run 'TestRearmSentinel|TestPrintCmd'`
Expected: FAIL — `undefined: watchOutcome`, `rearmSentinel`, `printCmd`.

- [ ] **Step 3: Create the helpers**

Create `cmd/agentbus/subscribe.go`:

```go
package main

import (
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/bus"
)

// watchOutcome is why one subscribe tick ended. It maps to the exit-code +
// rearm-sentinel contract every runtime branches on.
type watchOutcome int

const (
	outcomeCmd       watchOutcome = iota // delivered an addressed cmd
	outcomeHeartbeat                     // idle window elapsed; re-arm
	outcomeTransient                     // recoverable error; re-arm
	outcomeFatal                         // misconfiguration; do NOT re-arm
)

// rearmSentinel returns the final machine-readable stdout line and the process
// exit code for a subscribe tick. The contract every runtime follows: re-arm
// iff the line carries rearm=1 (every outcome except fatal). ref/from populate
// the cmd line; msg the error/fatal line.
func rearmSentinel(o watchOutcome, ref, from, msg string) (line string, code int) {
	switch o {
	case outcomeCmd:
		return fmt.Sprintf("__AGENTBUS__ event=cmd rearm=1 ref=%s from=%s", ref, from), 0
	case outcomeHeartbeat:
		return "__AGENTBUS__ event=heartbeat rearm=1", 64
	case outcomeTransient:
		return "__AGENTBUS__ event=error rearm=1 msg=" + msg, 75
	default:
		return "__AGENTBUS__ event=fatal rearm=0 msg=" + msg, 1
	}
}

// printCmd writes one delivered cmd entry in the human-readable form agents
// already parse. Shared by the one-shot and --loop handlers.
func printCmd(out io.Writer, e bus.Event) {
	ref := ""
	if e.Ref != "" {
		ref = " ref=" + e.Ref
	}
	fmt.Fprintf(out, "[%s %s->%s%s] %s\n", e.Type, e.From, e.Target, ref, e.Message)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/agentbus/ -run 'TestRearmSentinel|TestPrintCmd' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agentbus/subscribe.go cmd/agentbus/subscribe_test.go
git commit -m "feat(agentbus): rearm sentinel + printCmd helpers"
```

---

## Task 6: agentbus `runSubscribe` (one-shot + `--loop`) and main wiring

**Files:**
- Modify: `cmd/agentbus/subscribe.go` (add `runSubscribe`)
- Modify: `cmd/agentbus/main.go:223-259` (subscribe case) and imports
- Test: `cmd/agentbus/subscribe_test.go`

- [ ] **Step 1: Write the failing fatal-path test (no Redis needed)**

First, replace the import block at the top of `cmd/agentbus/subscribe_test.go` with the full set the integration tests need:

```go
import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
)
```

Then append the tests + helpers:

```go
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
	code := runSubscribe(context.Background(), nil, "Bad Agent", "host-1", time.Second, false, &buf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (fatal)", code)
	}
	if !strings.Contains(buf.String(), "__AGENTBUS__ event=fatal rearm=0") {
		t.Errorf("missing fatal sentinel: %q", buf.String())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/agentbus/ -run TestRunSubscribeFatalOnBadAgent`
Expected: FAIL — `undefined: runSubscribe`.

- [ ] **Step 3: Implement `runSubscribe`**

In `cmd/agentbus/subscribe.go`, update the import block to:

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)
```

Then append:

```go
// runSubscribe performs one subscribe tick (or a continuous --loop) and returns
// the process exit code. It arms a presence lease around the WatchCmd block and
// always disarms on return (the caller os.Exits on the returned code, so this
// function must never os.Exit itself — that would skip the defer). Presence is
// best-effort: a failed Arm/Disarm never blocks command delivery.
func runSubscribe(ctx context.Context, b *bus.Bus, agent, consumer string, idle time.Duration, loop bool, out io.Writer) int {
	if !bus.ValidName(agent) {
		line, code := rearmSentinel(outcomeFatal, "", "", "invalid agent "+agent)
		fmt.Fprintln(out, line)
		return code
	}
	_ = b.Arm(ctx, agent, consumer, idle)       // best-effort observability
	defer b.Disarm(context.Background(), agent) // runs on return (never on os.Exit)

	if loop {
		// Headless continuous mode: keep the lease warm and print every addressed
		// cmd; never exit on delivery. Re-arm sentinels are for the terminal
		// wake path only, which --loop explicitly is NOT.
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			tk := time.NewTicker(idle / 2)
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
		err := b.WatchCmd(ctx, agent, consumer, func(e bus.Event) bool {
			printCmd(out, e)
			return false // never "done" → consume continuously
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			line, code := rearmSentinel(outcomeTransient, "", "", err.Error())
			fmt.Fprintln(out, line)
			return code
		}
		return 0
	}

	var last bus.Event
	wctx, cancel := context.WithTimeout(ctx, idle)
	defer cancel()
	werr := b.WatchCmd(wctx, agent, consumer, func(e bus.Event) bool {
		last = e
		printCmd(out, e)
		return true // one-shot: stop on the first addressed entry
	})
	var line string
	var code int
	switch {
	case werr == nil:
		line, code = rearmSentinel(outcomeCmd, last.Ref, last.From, "")
	case errors.Is(werr, context.DeadlineExceeded):
		fmt.Fprintln(out, "__HEARTBEAT__") // deprecated; kept one release for existing agent loops
		line, code = rearmSentinel(outcomeHeartbeat, "", "", "")
	default:
		line, code = rearmSentinel(outcomeTransient, "", "", werr.Error())
	}
	fmt.Fprintln(out, line)
	return code
}
```

- [ ] **Step 4: Run the fatal test to verify it passes**

Run: `go test ./cmd/agentbus/ -run TestRunSubscribeFatalOnBadAgent -count=1`
Expected: PASS.

- [ ] **Step 5: Wire `main.go` to `runSubscribe`**

In `cmd/agentbus/main.go`, replace the entire `case "subscribe", "watch":` block (lines 223-259) with:

```go
	case "subscribe", "watch":
		// One subscription tick (or a headless --loop). The wake-on-exit model
		// for terminal sessions: block on the agent's :cmd group, print the next
		// addressed entry plus a __AGENTBUS__ rearm sentinel, and EXIT — that exit
		// re-invokes the session, which re-arms iff the sentinel says rearm=1.
		// --loop is for headless consumers (hermes/shell) only — never a terminal
		// wake path, since a long-lived loop can't wake a session.
		rest, loop := extractBool(rest, "--loop")
		if len(rest) < 1 {
			die("usage: subscribe [--loop] <agent> [idle_seconds]")
		}
		agent := rest[0]
		idle := heartbeat
		if len(rest) > 1 {
			idle = parseIdle(rest[1], heartbeat)
		}
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		os.Exit(runSubscribe(ctx, b, agent, consumer, idle, loop, os.Stdout))
```

Then remove `"errors"` from `main.go`'s import block (it was only used by the old subscribe case; `runSubscribe` now owns that logic). The block becomes:

```go
import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)
```

- [ ] **Step 6: Compile-check**

Run: `go build ./cmd/agentbus/`
Expected: builds clean. If it complains `"errors" imported and not used` you missed the import removal; if `undefined: runSubscribe` the new file isn't saved.

- [ ] **Step 7: Write the delivery + heartbeat integration tests**

Append to `cmd/agentbus/subscribe_test.go`:

```go
func TestRunSubscribeDelivers(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	stream := bus.StreamKey(b.Project(), "cmd")
	// Pre-create dev's group at "0" so the already-published entry is delivered
	// deterministically (no race with WatchCmd's own MKSTREAM at "$").
	if err := r.XGroupCreateMkStream(ctx, stream, "dev", "0").Err(); err != nil {
		t.Fatalf("XGroupCreate: %v", err)
	}
	if _, err := b.Cmd(ctx, "review", "dev", bus.CmdChallenge, "C1", "justify X"); err != nil {
		t.Fatalf("Cmd: %v", err)
	}

	var buf bytes.Buffer
	code := runSubscribe(ctx, b, "dev", "host-1", 4*time.Second, false, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (delivered)", code)
	}
	out := buf.String()
	if !strings.Contains(out, "[challenge review->dev ref=C1] justify X") {
		t.Errorf("output missing cmd line: %q", out)
	}
	if !strings.Contains(out, "__AGENTBUS__ event=cmd rearm=1 ref=C1 from=review") {
		t.Errorf("output missing cmd sentinel: %q", out)
	}
}

func TestRunSubscribeHeartbeat(t *testing.T) {
	b, _ := dialMain(t)
	var buf bytes.Buffer
	// No cmd published: WatchCmd creates the group at "$", blocks, the 1s idle
	// window elapses → heartbeat.
	code := runSubscribe(context.Background(), b, "dev", "host-1", 1*time.Second, false, &buf)
	if code != 64 {
		t.Fatalf("exit code = %d, want 64 (heartbeat)", code)
	}
	out := buf.String()
	if !strings.Contains(out, "__HEARTBEAT__") {
		t.Errorf("missing deprecated __HEARTBEAT__: %q", out)
	}
	if !strings.Contains(out, "__AGENTBUS__ event=heartbeat rearm=1") {
		t.Errorf("missing heartbeat sentinel: %q", out)
	}
}
```

- [ ] **Step 8: Run delivery + heartbeat tests**

Run: `go test ./cmd/agentbus/ -run 'TestRunSubscribeDelivers|TestRunSubscribeHeartbeat' -count=1`
Expected: PASS (heartbeat test takes ~1s; skips if Redis is down).

- [ ] **Step 9: Write the `--loop` integration test**

Append to `cmd/agentbus/subscribe_test.go`:

```go
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
	go func() { done <- runSubscribe(ctx, b, "dev", "host-1", 2*time.Second, true, buf) }()

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

- [ ] **Step 10: Run the full agentbus suite**

Run: `go test ./cmd/agentbus/ -count=1`
Expected: PASS (all subscribe + parse tests).

- [ ] **Step 11: Commit**

```bash
git add cmd/agentbus/subscribe.go cmd/agentbus/subscribe_test.go cmd/agentbus/main.go
git commit -m "feat(agentbus): runSubscribe with rearm contract + --loop mode"
```

---

## Task 7: documentation (agent guide + README)

**Files:**
- Modify: `docs/AGENT-BUS-GUIDE.md`
- Modify: `README.md`

- [ ] **Step 1: Update the subscribe cheat-sheet block in the guide**

In `docs/AGENT-BUS-GUIDE.md`, replace the INBOUND block (lines 93-97) with:

```markdown
# ── INBOUND: wait for a command addressed to you ─────────────────────────────
agentbus subscribe <agent> [idle_secs]                  # blocks for ONE cmd, prints it + a rearm sentinel, EXITS; default idle 240s
agentbus subscribe claude1                              # arm as a background task; its exit wakes your session
agentbus subscribe claude1 3600                         # 1h idle window before it heartbeats and exits
agentbus subscribe --loop hermes                        # HEADLESS callers only (hermes/shell): consume continuously, never exit
agentbus watch claude1                                  # legacy alias of subscribe
```

- [ ] **Step 2: Rewrite the wake-on-exit explainer**

In `docs/AGENT-BUS-GUIDE.md`, replace the `### subscribe is wake-on-exit, not a long loop` section (lines 129-136) with:

```markdown
### `subscribe` is wake-on-exit, not a long loop
`agentbus subscribe <self>` **blocks until one command addressed to you arrives,
prints it, then prints a final machine line and exits.** Arm it as a Claude Code
background task; its exit wakes your session, and you re-arm. After the idle
window (default 240s, or `[idle_secs]`) it heartbeats and exits so you can re-arm.

The last line is always a structured sentinel — **re-arm iff it says `rearm=1`**:

| You see                                         | Meaning            | Exit code | Re-arm? |
|-------------------------------------------------|--------------------|-----------|---------|
| `__AGENTBUS__ event=cmd rearm=1 ref=… from=…`   | a command arrived  | 0         | yes     |
| `__AGENTBUS__ event=heartbeat rearm=1`          | idle window passed | 64        | yes     |
| `__AGENTBUS__ event=error rearm=1 msg=…`        | transient glitch   | 75        | yes     |
| `__AGENTBUS__ event=fatal rearm=0 msg=…`        | misconfigured      | 1         | **no**  |

**While armed and waiting you are `idle`, never `blocked`** — `blocked` is
reserved for an open 4-eyes gate. busmon shows a `👂` badge next to armed agents,
so a human can see you're listening. **Do not** wrap `subscribe` in a `while`
loop or a daemon — a long-lived loop never wakes a terminal session. (The one
exception is `--loop`, for **headless** consumers like hermes or a shell logger
that are not trying to wake a session.) The whole loop lives in the binary;
there is no wrapper script and no watcher daemon.
```

- [ ] **Step 3: Update the README AGENTS-pane bullet**

In `README.md`, replace the AGENTS bullet (lines 114-118) with:

```markdown
- **AGENTS** — one chip per agent. `{p}:status` entries set the state (color-coded);
  a `{p}:report` entry also counts as liveness, showing the agent as `active` with
  its last report if it never published a status. Badges: `👂` = the agent is armed
  and listening on `{p}:cmd` (a live `subscribe` lease); `⌛N` = N commands are queued
  for it unread (orange when no one is listening — the "stopped re-arming" tell);
  `🔒N` = open 4-eyes challenges. The pilot indicator (`[autonome]`/`[piloté par X]`)
  shows the current lease holder. Past `idleAfter` it shows `idle Nm`; past
  `staleAfter`, `offline`.
```

- [ ] **Step 4: Document the new key in the README conventions**

In `README.md`, replace the "Additional keys" line (line 151) with:

```markdown
Additional keys: `{p}:pilot` (string, pilot lease), `{p}:gate:{agent}` (hash, 4-eyes challenges),
`{p}:armed:{agent}` (string with TTL, the subscribe presence lease behind the `👂` badge).
```

- [ ] **Step 5: Sanity-check the docs render**

Run: `grep -n '__AGENTBUS__\|👂\|⌛\|--loop\|armed:' docs/AGENT-BUS-GUIDE.md README.md`
Expected: matches in both files covering the sentinel table, badges, `--loop`, and the `{p}:armed:` key.

- [ ] **Step 6: Commit**

```bash
git add docs/AGENT-BUS-GUIDE.md README.md
git commit -m "docs: rearm sentinel contract, 👂/⌛ badges, --loop, armed key"
```

---

## Task 8: full verification gate

**Files:** none

- [ ] **Step 1: Build, vet, and test the whole module**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: clean build, no vet output, all tests PASS (Redis-backed tests skip cleanly if `docker compose up -d` hasn't been run — run it to exercise them).

- [ ] **Step 2: Confirm the binaries build**

Run: `go build -o busmon ./cmd/busmon && go build -o agentbus ./cmd/agentbus && echo OK`
Expected: `OK`.

- [ ] **Step 3: Verify the branch history**

Run: `git log --oneline main..HEAD`
Expected: the spec commit plus the per-task feat/docs commits, in order.

---

## Self-review notes (author)

- **Spec coverage:** armed lease (Task 1) → observability; `CmdLag` + busmon badges (Tasks 2-4) → "stopped re-arming" visibility + correct `idle`/`👂` state; rearm sentinel + exit codes (Tasks 5-6) → reliable re-arm; `--loop` (Task 6) → headless callers; vocabulary + contract docs (Task 7) → false-"blocked" fix. All three ranked goals covered.
- **Build order** matches the spec: observability/state first (Tasks 1-4), then the exit contract (Tasks 5-6), then `--loop` (folded into Task 6 since it shares `runSubscribe`), then docs.
- **Type consistency:** `agentState.armed bool` / `lag int64`; `ArmedAgents` returns `map[string]string`; `CmdLag` returns `map[string]int64`; `runSubscribe(ctx, *bus.Bus, agent, consumer string, idle time.Duration, loop bool, out io.Writer) int`; `rearmSentinel(watchOutcome, ref, from, msg string) (string, int)` — used identically across tasks.
- **`os.Exit` vs defer:** `runSubscribe` never calls `os.Exit` (so its `defer b.Disarm` runs); `main` does `os.Exit(runSubscribe(...))`. This is deliberate and load-bearing.
- **Deferred:** the optional Claude Code `Stop` hook (Strategy 3) is out of scope per the spec.
