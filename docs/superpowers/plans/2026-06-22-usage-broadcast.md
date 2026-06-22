# Usage broadcast (Slice 3c) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Each agent's status-line script tees its budget (`model/ctx/weekly/session/reset`) as structured fields to a `{project}:usage` hash; the master reads + broadcasts it, and busmon shows a compact badge — structured data, never scraped from rendered text.

**Architecture:** Mirror the 3a pattern (a hash + a command + a busmon enrichment), fed by a structured emit. A separate `{p}:usage` hash (distinct writer/cadence from `{p}:agents`). Distribution is notify+pull (no push-via-cmd).

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, `github.com/rivo/tview`.

## Global Constraints

- Module `github.com/netbja/agent-bus-monitor`, Go 1.26.
- **Structured, never scraped:** the bus stores named string fields; nothing parses a rendered status line.
- `{project}:usage` is a **separate** hash from `{project}:agents` (different writer — the status-line tee — and cadence).
- Schema is the **display strings** the script computed (no numeric parsing): `UsageSnapshot{Model, Ctx, Weekly, Session, Reset string; TS int64}`.
- Distribution is **notify + pull** — the master posts a summary on `notify`; agents read `agentbus usage`. **Never** push budget via `cmd` to each agent.
- The status-line tee must **never break the status line** — the snippet throttles (20s) and swallows errors (`|| true`).
- busmon must not synthesize ghost chips — the `Bus.Usage` poll enriches already-tracked agents only.
- **English UI/CLI/doc copy.**
- Verification gate (each task + final): `go build ./... && go vet ./... && go test ./... -count=1`. Redis-touching tests need the broker (`docker compose up -d`) or they skip.
- Every commit message ends with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Work happens on branch `feat/usage-broadcast` (already created off `main`; the spec is committed there).

---

## Task 1: bus — `UsageSnapshot` / `SetUsage` / `Usage`

**Files:**
- Modify: `bus/stream.go` (`UsageKey`, `UsageSnapshot`, `Bus.SetUsage`, `Bus.Usage`)
- Modify: `bus/stream_test.go` (`UsageKey` in `dialTest` cleanup; `TestUsageRoundTrip`)

**Interfaces:**
- Produces: `func UsageKey(project string) string`; `type UsageSnapshot struct { Model, Ctx, Weekly, Session, Reset string; TS int64 }` (json tags `model/ctx/weekly/session/reset` all `omitempty`, `ts`); `func (b *Bus) SetUsage(ctx, agent string, snap UsageSnapshot) error`; `func (b *Bus) Usage(ctx) (map[string]UsageSnapshot, error)`.

- [ ] **Step 1: Write the failing test**

Add to `bus/stream_test.go`:

```go
func TestUsageRoundTrip(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	snap := UsageSnapshot{Model: "Opus 4.8", Ctx: "194.5k", Weekly: "41.0%", Session: "99.0%", Reset: "36m", TS: 1700000000000}
	if err := b.SetUsage(ctx, "dev", snap); err != nil {
		t.Fatalf("SetUsage: %v", err)
	}
	m, err := b.Usage(ctx)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	got, ok := m["dev"]
	if !ok {
		t.Fatalf("Usage missing dev: %+v", m)
	}
	if got != snap {
		t.Fatalf("snapshot = %+v, want %+v", got, snap)
	}
	if err := b.SetUsage(ctx, "Bad Agent", snap); err == nil {
		t.Error("SetUsage accepted an invalid agent, want error")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./bus/ -run TestUsageRoundTrip -v`
Expected: FAIL to compile — `UsageSnapshot`/`SetUsage`/`Usage` undefined.

- [ ] **Step 3: Implement in `bus/stream.go`**

Add near `AgentsKey`/`AgentSnapshot`/`Agents` (the `encoding/json` import already exists):

```go
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
```

- [ ] **Step 4: Add `UsageKey` to `dialTest` cleanup**

In `bus/stream_test.go`, extend the `r.Del(...)` in `dialTest`'s `t.Cleanup` to include `UsageKey(project)`:

```go
		r.Del(ctx, StreamKey(project, "status"), StreamKey(project, "report"),
			StreamKey(project, "notify"), StreamKey(project, "cmd"), PilotKey(project),
			AgentsKey(project), UsageKey(project))
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./bus/ -run TestUsageRoundTrip -v`
Expected: PASS (or SKIP if Redis is down — `docker compose up -d` and re-run).

- [ ] **Step 6: Build + vet + commit**

Run: `go build ./... && go vet ./...`
Expected: clean.

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): {project}:usage hash — UsageSnapshot, SetUsage, Usage"
```

---

## Task 2: `agentbus usage` (write + read)

**Files:**
- Create: `cmd/agentbus/usage.go` (`usageTable`)
- Create: `cmd/agentbus/usage_test.go`
- Modify: `cmd/agentbus/main.go` (`usage` case + usage string + doc comment)

**Interfaces:**
- Consumes: `bus.UsageSnapshot`, `bus.Bus.SetUsage`/`Usage` (Task 1); the existing `humanAge` (in `agents.go`).
- Produces: `func usageTable(m map[string]bus.UsageSnapshot, now time.Time) string`.

- [ ] **Step 1: Write the failing test**

Create `cmd/agentbus/usage_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestUsageTable(t *testing.T) {
	now := time.UnixMilli(1_700_000_100_000)
	m := map[string]bus.UsageSnapshot{
		"claude1": {Model: "Opus 4.8", Session: "99.0%", Reset: "36m", TS: now.Add(-10 * time.Second).UnixMilli()},
		"claude2": {Weekly: "41.0%", TS: now.Add(-5 * time.Minute).UnixMilli()},
	}
	out := usageTable(m, now)
	if !strings.Contains(out, "claude1") || !strings.Contains(out, "Opus 4.8") || !strings.Contains(out, "99.0%") || !strings.Contains(out, "36m") {
		t.Fatalf("missing claude1 usage: %q", out)
	}
	if !strings.Contains(out, "10s ago") {
		t.Fatalf("claude1 age wrong: %q", out)
	}
	if !strings.Contains(out, "claude2") || !strings.Contains(out, "41.0%") {
		t.Fatalf("missing claude2: %q", out)
	}
	if strings.Index(out, "claude1") > strings.Index(out, "claude2") {
		t.Fatalf("rows not sorted by name: %q", out)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/agentbus/ -run TestUsageTable -v`
Expected: FAIL to compile — `usageTable` undefined.

- [ ] **Step 3: Implement `cmd/agentbus/usage.go`**

```go
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// usageTable renders each agent's latest usage, one aged line each, sorted by
// name. Empty fields are omitted (joined with " · ").
func usageTable(m map[string]bus.UsageSnapshot, now time.Time) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		s := m[n]
		parts := make([]string, 0, 5)
		for _, f := range []string{s.Model, s.Ctx, s.Weekly, s.Session, s.Reset} {
			if f != "" {
				parts = append(parts, f)
			}
		}
		age := ""
		if s.TS != 0 {
			age = humanAge(now.Sub(time.UnixMilli(s.TS)))
		}
		fmt.Fprintf(&sb, "%-12s %-44s %s\n", n, strings.Join(parts, " · "), age)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./cmd/agentbus/ -run TestUsageTable -v`
Expected: PASS.

- [ ] **Step 5: Wire the `usage` command in `cmd/agentbus/main.go`**

Add this `case` to the command switch (e.g. right after the `pane` case):

```go
	case "usage":
		rest, asJSON := extractBool(rest, "--json")
		if len(rest) == 0 {
			m, err := b.Usage(ctx)
			if err != nil {
				die(err.Error())
			}
			if asJSON {
				out, _ := json.MarshalIndent(m, "", "  ")
				fmt.Println(string(out))
				return
			}
			fmt.Print(usageTable(m, time.Now()))
			return
		}
		if len(rest) < 2 {
			die("usage: usage <agent> <json>   (or no args to read everyone's budget)")
		}
		var snap bus.UsageSnapshot
		if err := json.Unmarshal([]byte(strings.Join(rest[1:], " ")), &snap); err != nil {
			die("bad usage JSON: " + err.Error())
		}
		snap.TS = time.Now().UnixMilli()
		if err := b.SetUsage(ctx, rest[0], snap); err != nil {
			die(err.Error())
		}
```

Update the top-level usage `die` string to include `usage` (insert after `pane`):

```go
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|pilot|gate|agents|pane|usage|subscribe|watch|listen> ...")
```

Add to the package doc comment (above the `subscribe` line):

```go
//	agentbus --project P usage     [<agent> <json>]   # write a budget snapshot, or print all (status-line tee)
```

(`encoding/json`, `strings`, `time` are already imported in `main.go`.)

- [ ] **Step 6: Build + vet + package test + commit**

Run: `go build ./... && go vet ./... && go test ./cmd/agentbus/ -count=1`
Expected: clean; PASS.

```bash
git add cmd/agentbus/usage.go cmd/agentbus/usage_test.go cmd/agentbus/main.go
git commit -m "feat(agentbus): `usage` command (write a budget snapshot / read all)"
```

---

## Task 3: busmon compact usage badge

**Files:**
- Modify: `cmd/busmon/render.go` (`usageBadge`; `agentLabel` renders `[<usage>]`)
- Modify: `cmd/busmon/main.go` (`agentState.usage`; ticker `Bus.Usage` poll + enrichment)
- Modify: `cmd/busmon/render_test.go` (`TestUsageBadge`, `TestAgentLabelUsage`)

**Interfaces:**
- Consumes: `bus.UsageSnapshot`, `bus.Bus.Usage` (Task 1).
- Produces: `func usageBadge(snap bus.UsageSnapshot) string`.

- [ ] **Step 1: Write the failing tests**

Add to `cmd/busmon/render_test.go`:

```go
func TestUsageBadge(t *testing.T) {
	if got := usageBadge(bus.UsageSnapshot{Session: "99%", Reset: "36m"}); got != "99%·36m" {
		t.Fatalf("both = %q, want 99%%·36m", got)
	}
	if got := usageBadge(bus.UsageSnapshot{Session: "99%"}); got != "99%" {
		t.Fatalf("session only = %q, want 99%%", got)
	}
	if got := usageBadge(bus.UsageSnapshot{Reset: "36m"}); got != "36m" {
		t.Fatalf("reset only = %q, want 36m", got)
	}
	if got := usageBadge(bus.UsageSnapshot{Model: "Opus"}); got != "" {
		t.Fatalf("neither = %q, want empty", got)
	}
}

func TestAgentLabelUsage(t *testing.T) {
	now := time.Now()
	withUsage := &agentState{state: "working", lastSeen: now, usage: "99%·36m"}
	if got := agentLabel("dev", withUsage, now, false); !strings.Contains(got, "99%·36m") {
		t.Fatalf("agentLabel with usage = %q, want the usage badge", got)
	}
	noUsage := &agentState{state: "working", lastSeen: now}
	if strings.Contains(agentLabel("dev", noUsage, now, false), "[gray][") {
		t.Fatal("no usage badge when usage is empty")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/busmon/ -run 'TestUsageBadge|TestAgentLabelUsage' -v`
Expected: FAIL to compile — `usageBadge` undefined and `agentState` has no `usage` field.

- [ ] **Step 3: Add `usageBadge` + the badge render in `cmd/busmon/render.go`**

Add the helper (e.g. near `parseDirected`):

```go
// usageBadge joins the non-empty of session/reset with "·" for a compact chip
// badge: "99%·36m", "99%", "36m", or "" when both are empty.
func usageBadge(snap bus.UsageSnapshot) string {
	parts := make([]string, 0, 2)
	if snap.Session != "" {
		parts = append(parts, snap.Session)
	}
	if snap.Reset != "" {
		parts = append(parts, snap.Reset)
	}
	return strings.Join(parts, "·")
}
```

In `agentLabel`, add the badge between the `pane` badge and the `master` prepend:

```go
	if a.pane != "" {
		label += " [blue]⧉[-]"
	}
	if a.usage != "" {
		label += " [gray][" + a.usage + "][-]"
	}
	if master {
		label = "[fuchsia]⬢[-] " + label
	}
	return label
```

- [ ] **Step 4: Add the `usage` field to `agentState` in `cmd/busmon/main.go`**

```go
type agentState struct {
	state    string
	message  string
	lastSeen time.Time
	gated    int    // open 4-eyes challenges; >0 shows a lock badge
	armed    bool   // a live subscribe lease exists → 👂 listening badge
	lag      int64  // unconsumed {p}:cmd entries for this agent → ⌛ backlog badge
	pane     string // HERDR_PANE_ID from the agents hash → ⧉ herdr-attached badge
	usage    string // compact session·reset from the usage hash → [..] badge
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/busmon/ -run 'TestUsageBadge|TestAgentLabelUsage' -v`
Expected: PASS.

- [ ] **Step 6: Poll `Bus.Usage` in the ticker and enrich**

In `cmd/busmon/main.go`, in the 1s ticker goroutine, add the poll right after `snaps, _ := b.Agents(ctx)`:

```go
			snaps, _ := b.Agents(ctx)
			usageSnaps, _ := b.Usage(ctx)
```

and in the enrichment loop (`for n, a := range agents { … }`) add the usage copy after the pane line:

```go
			for n, a := range agents {
				_, a.armed = armed[n]
				a.lag = lag[n]
				a.pane = snaps[n].Pane
				a.usage = usageBadge(usageSnaps[n])
				if c, ok := gates[n]; ok {
					a.gated = c
				}
			}
```

(`usageSnaps[n]` for an untracked agent is the zero `UsageSnapshot`, so `usageBadge` returns `""` — no ghost chips, stale usage clears.)

- [ ] **Step 7: Full gate + commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: build clean, vet clean, all tests PASS across `bus`, `cmd/agentbus`, `cmd/busmon`.

```bash
git add cmd/busmon/render.go cmd/busmon/main.go cmd/busmon/render_test.go
git commit -m "feat(busmon): compact usage badge from the {p}:usage hash"
```

---

## Task 4: status-line tee snippet + master-skill broadcast step

Docs/skill only — no Go logic. The gate stays green; verification is checking the commands against the real CLI.

**Files:**
- Modify: `docs/AGENT-BUS-GUIDE.md` (`agentbus usage` cheat lines + the status-line tee snippet)
- Modify: `skills/agent-bus-master/SKILL.md` (a "Broadcast team budget" section)

- [ ] **Step 1: Add `agentbus usage` to the guide cheat sheet + the tee snippet**

In `docs/AGENT-BUS-GUIDE.md`, in the §2 PEERS block (next to `agentbus pane`), add:

```bash
agentbus usage <agent> '<json>'                         # write the agent's budget snapshot (status-line tee)
agentbus usage                                          # print everyone's budget; --json for raw
```

Then add a short subsection (e.g. after §2) with the tee:

````markdown
### Status-line usage tee

Your status line already computes the budget numbers — tee them to the bus (structured, never
scraped). Paste this into your `statusLine` script after you've computed the values; it throttles
(so frequent refreshes don't hammer Redis) and swallows errors (so it never breaks the line):

```bash
ts=/tmp/abus-usage-$AGENT_BUS_AGENT
if [ -z "$(find "$ts" -newermt '-20 seconds' 2>/dev/null)" ]; then
  agentbus usage "$AGENT_BUS_AGENT" \
    "{\"model\":\"$MODEL\",\"ctx\":\"$CTX\",\"weekly\":\"$WEEKLY\",\"session\":\"$SESSION\",\"reset\":\"$RESET\"}" \
    >/dev/null 2>&1 || true
  touch "$ts"
fi
```

Requires `AGENT_BUS_PROJECT` / `AGENT_BUS_AGENT` in the status-line script's env.
````

- [ ] **Step 2: Add the "Broadcast team budget" step to the master skill**

In `skills/agent-bus-master/SKILL.md`, add a section (use **real** triple backticks for the code block — do not escape them):

````markdown
## Broadcast team budget
Give the team a regular budget readout. Each agent's status-line script tees its usage to the bus
(see `docs/AGENT-BUS-GUIDE.md` → "Status-line usage tee"); read it and post a one-line summary:
```bash
agentbus usage                                  # the team budget table (or --json)
agentbus notify "budget — claude1 99%/36m · claude2 41%/2h"   # periodic one-line summary
```
Distribution is **notify + pull**: the summary lands on `{project}:notify` (visible in busmon and to
the human), and agents read `agentbus usage` themselves on demand. Never push budget via `cmd` to
each agent — that wakes every agent's `subscribe`.
````

- [ ] **Step 3: Verify the commands against the real CLI**

Run:

```bash
go build -o /tmp/agentbus ./cmd/agentbus && /tmp/agentbus --project p usage 2>&1 | head -1   # read form parses
/tmp/agentbus --project p usage dev '{"session":"99%"}' 2>&1 | head -1                        # write form parses (may error on Redis if down — syntax is what matters)
grep -c '```' skills/agent-bus-master/SKILL.md   # fences still balanced (even count)
```
Expected: the `usage` command is recognized (no "unknown command"); SKILL.md fence count is even.

- [ ] **Step 4: Full gate (docs/skill only) + commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: clean; all tests PASS.

```bash
git add docs/AGENT-BUS-GUIDE.md skills/agent-bus-master/SKILL.md
git commit -m "docs: status-line usage tee + master budget-broadcast step"
```

---

## Self-Review

**Spec coverage:**
- Component A (`{p}:usage` hash, `UsageSnapshot`/`SetUsage`/`Usage`) → Task 1. ✓
- Component B (`agentbus usage` write + read, `usageTable`) → Task 2. ✓
- Component C (busmon `usageBadge` + ticker poll, no ghost chips) → Task 3. ✓
- Component D (status-line tee snippet, throttled + error-swallowing) → Task 4 Step 1. ✓
- Component E (master-skill broadcast step, notify+pull) → Task 4 Step 2. ✓
- Testing (`SetUsage`/`Usage` round-trip; `usageTable`; `usageBadge` + agentLabel) → Tasks 1–3. ✓
- English copy; structured-not-scraped; separate hash — all honored.

**Type consistency:** `UsageSnapshot{Model, Ctx, Weekly, Session, Reset string; TS int64}` defined in Task 1 Step 3, consumed verbatim in Task 2 (`usageTable`, the `usage` case) and Task 3 (`usageBadge`, the ticker). `Bus.SetUsage(ctx, agent, snap)` / `Bus.Usage(ctx)` signatures match between Task 1 and their callers in Tasks 2/3. `usageBadge(bus.UsageSnapshot) string` and `agentState.usage` match between Task 3's helper, tests, and ticker.

**Build-green ordering:** Task 1 is independent. Tasks 2 and 3 each consume only Task 1 and are additive (new command, new badge). Task 4 is docs/skill only. Every task compiles and tests green.

**Placeholder scan:** no TBD/TODO; every code step shows complete code; the snippet/skill blocks are complete (the SKILL.md "real backticks" note is explicit, as in Slice 3b).
