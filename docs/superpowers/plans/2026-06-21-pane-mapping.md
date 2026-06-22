# Pane-mapping foundation (Slice 3a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record which herdr pane each agent occupies — `agentbus status` folds `HERDR_PANE_ID` into the `{project}:agents` snapshot, and it's surfaced in `agentbus agents` and busmon — as the bus↔herdr join key for Slice 3b/3c.

**Architecture:** Add `Pane` to `AgentSnapshot`, written by `Bus.Status` (signature gains a trailing `pane`). The `agentbus status` handler reads the env; `agentbus agents` and busmon show a `⧉` badge. Hash-only metadata — the pane never enters the status stream.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, `github.com/rivo/tview`.

## Global Constraints

- Module `github.com/netbja/agent-bus-monitor`, Go **1.26**.
- The pane is **hash-only** — it goes in the `{project}:agents` snapshot, never in the `{project}:status` stream's `XADD` fields.
- **Single writer:** only `Bus.Status` writes the agent snapshot (incl. pane) — no separate merge write.
- **busmon must not synthesize ghost chips** from the agents hash: the new `Bus.Agents` poll only enriches agents busmon already tracks (via stream) with a `pane`; it never creates a chip from the hash alone.
- The `⧉` glyph denotes a herdr pane in **both** `agentbus agents` and busmon (consistent visual).
- **UI/CLI copy is English** (project preference) — the badge is a glyph; any words stay English.
- No herdr/Signal runtime dependency — 3a stores exactly what `HERDR_PANE_ID` holds.
- Verification gate (each task + a full pass at the end): `go build ./... && go vet ./... && go test ./... -count=1`. Redis-touching tests need the broker (`docker compose up -d`) or they skip.
- Every commit message ends with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Work happens on branch `feat/pane-mapping` (already created off `main`; the spec is committed there).

---

## Task 1: Register the pane (bus + CLI write path)

`AgentSnapshot` gains `Pane`; `Bus.Status` gains a trailing `pane string` and writes it in its existing snapshot `HSET`. The signature change ripples to all 9 `Status` call sites (8 bus tests pass `""`; the `agentbus status` handler passes the real `HERDR_PANE_ID`).

**Files:**
- Modify: `bus/stream.go` (`AgentSnapshot.Pane`; `Bus.Status(+pane)`)
- Modify: `bus/stream_test.go` (`TestAgentsSnapshot` pane round-trip; 4 other `Status` calls +`""`)
- Modify: `bus/recent_test.go` (3 `Status` calls +`""`)
- Modify: `cmd/agentbus/main.go` (`status` handler reads `HERDR_PANE_ID`)

**Interfaces:**
- Consumes: existing `Bus.Agents`, `AgentsKey`, `splitID`.
- Produces: `AgentSnapshot{State, Message, TS, Pane string}`; `func (b *Bus) Status(ctx context.Context, agent, state, message, pane string) (string, error)`.

- [ ] **Step 1: Rewrite `TestAgentsSnapshot` for the pane round-trip**

In `bus/stream_test.go`, replace the `TestAgentsSnapshot` function with:

```go
func TestAgentsSnapshot(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "dev", "working", "plan 10", "w1:p1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := b.Status(ctx, "ana", "idle", "", ""); err != nil {
		t.Fatalf("Status ana: %v", err)
	}
	m, err := b.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	s, ok := m["dev"]
	if !ok {
		t.Fatalf("Agents missing dev: %+v", m)
	}
	if s.State != "working" || s.Message != "plan 10" || s.TS == 0 || s.Pane != "w1:p1" {
		t.Fatalf("snapshot = %+v, want working/plan 10/ts>0/pane w1:p1", s)
	}
	if m["ana"].Pane != "" {
		t.Fatalf("ana pane = %q, want empty (no HERDR_PANE_ID)", m["ana"].Pane)
	}
}
```

- [ ] **Step 2: Run it to verify it fails (compile error)**

Run: `go test ./bus/ -run TestAgentsSnapshot -v`
Expected: FAIL to compile — `Status` takes 4 args / `AgentSnapshot` has no `Pane`.

- [ ] **Step 3: Add `Pane` and thread it through `Status` in `bus/stream.go`**

Add the `Pane` field to `AgentSnapshot`:

```go
type AgentSnapshot struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	TS      int64  `json:"ts"` // ms since epoch, from the status entry's stream id
	Pane    string `json:"pane,omitempty"` // HERDR_PANE_ID when the agent runs inside herdr
}
```

Change `Bus.Status` to take `pane` and write it (the `XADD` stays `agent/state/message`):

```go
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
```

- [ ] **Step 4: Update the other 7 `Status` call sites in the bus tests (pass `""`)**

In `bus/stream_test.go`, add a trailing `, ""` to these four calls:
- `b.Status(ctx, "dev", "flying", "x", "")`
- `b.Status(ctx, "Bad Agent", "working", "x", "")`
- `b.Status(ctx, "dev", "working", "ok", "")`
- `b.Status(ctx, "dev", "working", "hello", "")`

In `bus/recent_test.go`, add a trailing `, ""` to these three calls:
- `_, err = b.Status(ctx, "dev", "working", e[1], "")`
- `b.Status(ctx, "dev", "working", "old", "")`
- `b.Status(ctx, "dev", "idle", "new", "")`

- [ ] **Step 5: Run the bus tests to verify they pass**

Run: `go test ./bus/ -count=1`
Expected: PASS (including `TestAgentsSnapshot`; or SKIP if Redis is down — start it with `docker compose up -d` and re-run).

- [ ] **Step 6: Read `HERDR_PANE_ID` in the `agentbus status` handler**

In `cmd/agentbus/main.go`, replace the `status` case body with:

```go
	case "status":
		if len(rest) < 2 {
			die("usage: status <agent> <state> [message]")
		}
		// HERDR_PANE_ID (set inside a herdr pane) registers the agent's pane in
		// the {project}:agents hash; empty outside herdr.
		pane := os.Getenv("HERDR_PANE_ID")
		if _, err := b.Status(ctx, rest[0], rest[1], strings.Join(rest[2:], " "), pane); err != nil {
			die(err.Error())
		}
```

(`os` and `strings` are already imported in `main.go`.)

- [ ] **Step 7: Full build + vet + commit**

Run: `go build ./... && go vet ./... && go test ./cmd/agentbus/ -count=1`
Expected: clean; `cmd/agentbus` tests PASS (the `agents` table is unchanged this task, but everything compiles).

```bash
git add bus/stream.go bus/stream_test.go bus/recent_test.go cmd/agentbus/main.go
git commit -m "feat(bus): record HERDR_PANE_ID in the agent snapshot via status"
```

---

## Task 2: `agentbus agents` table shows the pane

Surface the registered pane in the human table (the `--json` form already carries it via the struct).

**Files:**
- Modify: `cmd/agentbus/agents.go` (`agentsTable` shows `⧉<pane>`)
- Modify: `cmd/agentbus/agents_test.go` (`TestAgentsTable` pane case)

**Interfaces:**
- Consumes: `bus.AgentSnapshot.Pane` (Task 1).

- [ ] **Step 1: Extend `TestAgentsTable` with a pane assertion**

In `cmd/agentbus/agents_test.go`, give `claude1` a pane and assert the badge. Change the map entry and add the check:

```go
	m := map[string]bus.AgentSnapshot{
		"claude1": {State: "working", Message: "plan 10", TS: now.Add(-12 * time.Second).UnixMilli(), Pane: "w1:p1"},
		"hermes":  {State: "done", TS: now.Add(-11 * time.Minute).UnixMilli()},
	}
	out := agentsTable(m, now)
```

and add, after the existing assertions inside `TestAgentsTable`:

```go
	if !strings.Contains(out, "⧉w1:p1") {
		t.Fatalf("claude1 pane badge missing: %q", out)
	}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/agentbus/ -run TestAgentsTable -v`
Expected: FAIL — `⧉w1:p1` is not in the output (the table doesn't render the pane yet).

- [ ] **Step 3: Render the pane in `agentsTable`**

In `cmd/agentbus/agents.go`, change the row-building tail of `agentsTable` (the `msg` block + the `Fprintf`) to:

```go
		msg := ""
		if s.Message != "" {
			msg = "  (" + s.Message + ")"
		}
		pane := ""
		if s.Pane != "" {
			pane = "  ⧉" + s.Pane
		}
		fmt.Fprintf(&sb, "%-12s %-8s %-9s%s%s%s\n", n, s.State, humanAge(age), marker, pane, msg)
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./cmd/agentbus/ -run TestAgentsTable -v`
Expected: PASS.

- [ ] **Step 5: Build + vet + commit**

Run: `go build ./... && go vet ./...`
Expected: clean.

```bash
git add cmd/agentbus/agents.go cmd/agentbus/agents_test.go
git commit -m "feat(agentbus): show the herdr pane (⧉) in the agents table"
```

---

## Task 3: busmon herdr-attached `⧉` badge

busmon enriches its tracked agents with their `pane` from the agents hash (a new poll in the existing 1s ticker) and renders a `⧉` badge.

**Files:**
- Modify: `cmd/busmon/main.go` (`agentState.pane`; ticker `Bus.Agents` poll + enrichment)
- Modify: `cmd/busmon/render.go` (`agentLabel` renders the `⧉` badge)
- Modify: `cmd/busmon/render_test.go` (`TestAgentLabel` pane case)

**Interfaces:**
- Consumes: `bus.Bus.Agents` (Slice 1) returning `AgentSnapshot.Pane` (Task 1).

- [ ] **Step 1: Add the pane case to `TestAgentLabel`**

In `cmd/busmon/render_test.go`, add to the end of `TestAgentLabel` (after the `gated` block):

```go
	attached := &agentState{state: "working", lastSeen: now, pane: "w1:p1"}
	if got := agentLabel("dev", attached, now, false); !strings.Contains(got, "⧉") {
		t.Fatalf("herdr-attached agent label = %q, want a ⧉ badge", got)
	}
	if strings.Contains(agentLabel("dev", base, now, false), "⧉") {
		t.Fatal("agent with no pane should not show the ⧉ badge")
	}
```

- [ ] **Step 2: Run it to verify it fails (compile error)**

Run: `go test ./cmd/busmon/ -run TestAgentLabel -v`
Expected: FAIL to compile — `agentState` has no `pane` field.

- [ ] **Step 3: Add `pane` to `agentState` and render the badge**

In `cmd/busmon/main.go`, add the field to `agentState`:

```go
type agentState struct {
	state    string
	message  string
	lastSeen time.Time
	gated    int    // open 4-eyes challenges; >0 shows a lock badge
	armed    bool   // a live subscribe lease exists → 👂 listening badge
	lag      int64  // unconsumed {p}:cmd entries for this agent → ⌛ backlog badge
	pane     string // HERDR_PANE_ID from the agents hash → ⧉ herdr-attached badge
}
```

In `cmd/busmon/render.go`, add the badge in `agentLabel` between the `gated` block and the `master` prepend:

```go
	if a.gated > 0 {
		label += fmt.Sprintf(" [red]🔒%d[-]", a.gated)
	}
	if a.pane != "" {
		label += " [blue]⧉[-]"
	}
	if master {
		label = "[fuchsia]⬢[-] " + label
	}
	return label
```

- [ ] **Step 4: Run `TestAgentLabel` to verify it passes**

Run: `go test ./cmd/busmon/ -run TestAgentLabel -v`
Expected: PASS.

- [ ] **Step 5: Poll `Bus.Agents` in the ticker and enrich the pane**

In `cmd/busmon/main.go`, in the 1s ticker goroutine, add the poll after `lag, _ := b.CmdLag(ctx)`:

```go
			lag, _ := b.CmdLag(ctx)
			snaps, _ := b.Agents(ctx)
```

and in the enrichment loop (`for n, a := range agents { … }`) add the pane copy:

```go
			for n, a := range agents {
				_, a.armed = armed[n]
				a.lag = lag[n]
				a.pane = snaps[n].Pane
				if c, ok := gates[n]; ok {
					a.gated = c
				}
			}
```

(`snaps[n]` for an untracked agent is the zero `AgentSnapshot`, so `a.pane` becomes `""` — no ghost chips, and a stale pane clears.)

- [ ] **Step 6: Full gate + commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: build clean, vet clean, all tests PASS across `bus`, `cmd/agentbus`, `cmd/busmon`.

```bash
git add cmd/busmon/main.go cmd/busmon/render.go cmd/busmon/render_test.go
git commit -m "feat(busmon): herdr-attached ⧉ badge from the agents hash"
```

---

## Self-Review

**Spec coverage:**
- Component A (`AgentSnapshot.Pane` + `Bus.Status(+pane)`, hash-only, single writer) → Task 1. ✓
- Component B (`agentbus status` reads `HERDR_PANE_ID`; `agentbus agents` table + json show pane) → Task 1 (env read + json via struct) + Task 2 (table). ✓
- Component C (busmon `⧉` badge via a `Bus.Agents` ticker poll, no ghost chips) → Task 3. ✓
- Testing (bus round-trip incl. empty case; agents-table pane; busmon badge) → Tasks 1–3. ✓
- `⧉` glyph consistent in `agentbus agents` (Task 2) and busmon (Task 3). ✓

**Type consistency:** `Status(ctx, agent, state, message, pane string)` is defined in Task 1 Step 3 and every one of the 9 call sites is updated (Steps 1, 4, 6). `AgentSnapshot.Pane` is read in Task 2 (`s.Pane`) and Task 3 (`snaps[n].Pane`) exactly as defined. `agentState.pane` is defined in Task 3 Step 3 and read by `agentLabel` (Step 3) and written by the ticker (Step 5).

**Build-green ordering:** Task 1 updates ALL 9 `Status` call sites in the same task, so the tree compiles at its end. Tasks 2 and 3 are additive. `agentbus agents --json` already exposes the pane after Task 1 (the struct field); Tasks 2/3 add the human surfaces.

**Placeholder scan:** no TBD/TODO/"handle errors"; every code step shows complete code; `~line`/anchor references are guidance, the code blocks are authoritative.
