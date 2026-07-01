# Protocol Version Marker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Label the `subscribe` JSON contract as protocol **v1** — stamp `"v":1` on every emitted event from a single `bus.ProtocolVersion` constant, add an `agentbus version` command, and document the compatibility rule so a format change becomes an explicit signal instead of a silent break.

**Architecture:** One constant `bus.ProtocolVersion = 1` is the single source of truth. `subEvent` gains a `V` field stamped centrally in `emit()` (one chokepoint → every variant carries it). `agentbus version` is handled early in `main` (before the project check and Redis connect) so it needs no project or broker. The compatibility rule lives in the docs — the `v` field is trivial; the rule is the value.

**Tech Stack:** Go 1.26, module `github.com/netbja/agent-bus-monitor`. No new dependencies.

## Global Constraints

- Go 1.26; module `github.com/netbja/agent-bus-monitor`. The version constant goes in `bus/bus.go` (transport-neutral conventions).
- All CLI/output/doc text is **English**.
- `"v"` must appear on **every** emitted `subEvent` line — stamp it centrally in `emit()`, not per-constructor. No `omitempty` on the field.
- `agentbus version` must work with **no project and no broker** — handle it early in `main`, before the `project required` check and before `bus.Connect`. Output exactly `agentbus protocol v1` (`fmt.Printf("agentbus protocol v%d\n", bus.ProtocolVersion)`), exit 0.
- Adding `v` now is **not** a bump — it declares the current format as v1 (the baseline).
- **Do not** version individual stream entries, `agents --json`/`usage`/`verdicts` output, or add any capability handshake. **Do not** change existing parsers (`ParseEntry`, `json.Unmarshal` paths) — they are already tolerant and must stay so.
- Documented compatibility rule: `v` = the emitting binary's protocol version; **additive-only within a `v`** (consumers must ignore unknown fields); **bump `v` only on a breaking change** (field removed/renamed/repurposed or semantics changed); **recommended consumer behavior on an unknown (higher) `v` is to fail loud**, not best-effort.

## File Structure

- `bus/bus.go` — **Modify.** Add `const ProtocolVersion = 1` with a doc comment stating the bump rule.
- `cmd/agentbus/subscribe.go` — **Modify.** Add `subEvent.V` field; stamp it in `emit()`.
- `cmd/agentbus/subscribe_test.go` — **Modify.** Add two pure tests (emit stamps `v` even when `V==0`; every variant carries `v:1`).
- `cmd/agentbus/main.go` — **Modify.** Early `version` branch; usage banner + doc-comment header.
- `README.md`, `CLAUDE.md` — **Modify.** The compatibility contract + `agentbus version`.
- `docs/AGENT-BUS-GUIDE.md` — **Modify.** An `agentbus version` cheat-sheet line.

---

### Task 1: The version marker (`ProtocolVersion` + stamped `subEvent.v`)

**Files:**
- Modify: `bus/bus.go` (add `const ProtocolVersion = 1`)
- Modify: `cmd/agentbus/subscribe.go` (`subEvent.V` field + set in `emit`)
- Test: `cmd/agentbus/subscribe_test.go` (two pure tests)

**Interfaces:**
- Consumes: nothing from earlier tasks; existing `emit`, `subEvent`, `boolPtr` in `subscribe.go`.
- Produces (Task 2 relies on this): `const bus.ProtocolVersion = 1` (untyped int constant), and `subEvent.V int` (json key `v`) stamped on every emitted line.

- [ ] **Step 1: Write the failing tests**

Add to `cmd/agentbus/subscribe_test.go` (its imports already include `bytes`, `encoding/json`, `strings`, and the `bus` package — no import changes needed):

```go
func TestEmitStampsProtocolVersion(t *testing.T) {
	var buf bytes.Buffer
	emit(&buf, subEvent{Event: "cmd"}) // V intentionally left 0 — emit must stamp it
	var got subEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("emit output not JSON: %q (%v)", buf.String(), err)
	}
	if got.V != bus.ProtocolVersion {
		t.Fatalf("emit did not stamp v: got %d, want %d", got.V, bus.ProtocolVersion)
	}
}

func TestEmittedVariantsCarryVersion(t *testing.T) {
	// Every subEvent variant goes through emit, so each must carry "v":1.
	for _, ev := range []subEvent{
		{Event: "cmd"},
		{Event: "heartbeat", Rearm: boolPtr(true)},
		{Event: "error", Rearm: boolPtr(true), Msg: "boom"},
		{Event: "fatal", Rearm: boolPtr(false), Msg: "invalid agent"},
	} {
		var buf bytes.Buffer
		emit(&buf, ev)
		if !strings.Contains(buf.String(), `"v":1`) {
			t.Fatalf("%s event missing v:1: %q", ev.Event, buf.String())
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agentbus/ -run 'ProtocolVersion|VariantsCarryVersion' -count=1 -v`
Expected: FAIL — `subEvent` has no field `V` / `undefined: bus.ProtocolVersion` (compile error).

- [ ] **Step 3: Add the constant to `bus/bus.go`**

Insert after the report-kind constants block (near the top, after the `ReportNote`/`ReportAuto` `const (...)`):

```go
// ProtocolVersion is the bus wire-protocol version stamped on every subscribe
// JSON event (subEvent "v"). It labels the current subscribe contract as v1.
// Bump it ONLY on a breaking change to that contract — a field removed, renamed,
// or repurposed, or a change in an existing field's semantics. Additive fields
// within a version are non-breaking and must not bump it.
const ProtocolVersion = 1
```

- [ ] **Step 4: Add the field and stamp it in `cmd/agentbus/subscribe.go`**

Add `V` as the first field of `subEvent`:

```go
type subEvent struct {
	V      int    `json:"v"`
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
```

Stamp it in `emit` (the single chokepoint every event passes through):

```go
// emit writes one subEvent as a single JSON line, stamping the protocol version
// so every variant (cmd/heartbeat/error/fatal) carries "v".
func emit(out io.Writer, ev subEvent) {
	ev.V = bus.ProtocolVersion
	b, _ := json.Marshal(ev)
	fmt.Fprintln(out, string(b))
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/agentbus/ -run 'ProtocolVersion|VariantsCarryVersion' -count=1 -v`
Expected: PASS — `TestEmitStampsProtocolVersion`, `TestEmittedVariantsCarryVersion`.

- [ ] **Step 6: Run the existing subscribe tests (guard against a format regression)**

Run: `docker compose up -d && go test ./cmd/agentbus/ -run 'Subscribe|Watch' -count=1`
Expected: PASS — the existing subscribe tests still pass (the added `v` field is additive; `lastEvent` unmarshals into `subEvent`, which now simply has an extra field).

- [ ] **Step 7: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output (success).

- [ ] **Step 8: Commit**

```bash
git add bus/bus.go cmd/agentbus/subscribe.go cmd/agentbus/subscribe_test.go
git commit -m "feat: stamp protocol v1 on every subscribe event (bus.ProtocolVersion)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: The `agentbus version` command

**Files:**
- Modify: `cmd/agentbus/main.go` (early `version` branch ~after line 66; usage banner line ~70; doc-comment header ~line 24)

**Interfaces:**
- Consumes: `bus.ProtocolVersion` (Task 1); existing `extractFlag`, `args`.
- Produces: the user-facing `agentbus version` command. No symbols consumed by later tasks.

- [ ] **Step 1: Add the early `version` branch**

In `cmd/agentbus/main.go`, the current flow is:

```go
	args, project := extractFlag(args, "--project")
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	if project == "" {
		die("project required: pass --project <p> or set AGENT_BUS_PROJECT")
	}
```

Insert the `version` branch between the env fallback and the `project required` check, so `version` needs neither a project nor a broker:

```go
	args, project := extractFlag(args, "--project")
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	// version reads a compile-time constant — no project, no broker. Handle it
	// before the project check and before bus.Connect.
	if len(args) >= 1 && args[0] == "version" {
		fmt.Printf("agentbus protocol v%d\n", bus.ProtocolVersion)
		return
	}
	if project == "" {
		die("project required: pass --project <p> or set AGENT_BUS_PROJECT")
	}
```

- [ ] **Step 2: Update the usage banner and doc-comment header**

In the `len(args) < 1` usage `die` (line ~70), add `version` to the command list:

```go
		die("usage: agentbus --project <p> <status|report|notify|cmd|thread|challenge|reply|verdict|verdicts|version|pilot|gate|agents|pane|usage|subscribe|watch|listen> ...")
```

In the doc-comment header, add a `version` line after the `listen` line (line ~24):

```go
//	agentbus version               # print the bus protocol version (no project/broker needed)
```

- [ ] **Step 3: Build + vet + full unit tests**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: build/vet clean; all tests PASS.

- [ ] **Step 4: End-to-end smoke test (no project, no broker)**

Run:

```bash
go build -o agentbus ./cmd/agentbus
./agentbus version; echo "exit=$?"
env -u AGENT_BUS_PROJECT ./agentbus version                 # no project set
REDIS_URL=redis://127.0.0.1:1 ./agentbus version            # unreachable broker — must NOT connect
```

Expected: every invocation prints exactly `agentbus protocol v1` and `exit=0`. The `REDIS_URL` case proves `version` returns before `bus.Connect` (no "Redis connection failed").

- [ ] **Step 5: Commit**

```bash
git add cmd/agentbus/main.go
git commit -m "feat(agentbus): version command (prints the bus protocol version)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Documentation (the compatibility contract)

**Files:**
- Modify: `CLAUDE.md` (agentbus verb list + a "Things that bite" bullet with the compat rule)
- Modify: `README.md` (command reference + a short protocol-version note)
- Modify: `docs/AGENT-BUS-GUIDE.md` (an `agentbus version` cheat-sheet line)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `CLAUDE.md`**

Two edits:

1. In the **`agentbus`** bullet's verb list, add `version` (e.g. after `listen`).

2. Add a **Things that bite** bullet stating the contract verbatim:
   > - **The `subscribe` JSON output is versioned; nothing else is.** Every `subscribe` event carries `"v"` (= `bus.ProtocolVersion`, currently 1). The rule: within a `v`, changes are **additive only** and consumers **must ignore unknown fields**; **bump `v` only on a breaking change** to the subscribe contract (a field removed/renamed/repurposed or a semantics change). A consumer that sees a higher `v` than it knows should **fail loud**, not best-effort — that is what turns a format cutover (the old sentinel→JSON break) into an explicit signal. `agentbus version` prints the constant. Stream entries and `agents`/`usage`/`verdicts` output are **not** versioned (they are additive-by-key via `ParseEntry`/`json.Unmarshal`).

- [ ] **Step 2: Update `README.md`**

Add `agentbus version` to the command reference (matching the surrounding one-liner style):

```
agentbus version                                  # print the bus protocol version (no project/broker)
```

And add a short note (near the command reference or a protocol section) stating the compatibility rule: the `subscribe` output carries `"v"`; additive within a version, `v` bumps only on a breaking subscribe-contract change, and a consumer should fail loud on an unrecognized higher `v`. Match the file's existing prose style.

- [ ] **Step 3: Update `docs/AGENT-BUS-GUIDE.md`**

Add an `agentbus version` cheat-sheet line in a sensible spot (e.g. near the top utility commands), matching the aligned `#`-comment style:

```bash
agentbus version                                       # print the bus protocol version (v1)
```

- [ ] **Step 4: Verify no code drift**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: still green (docs-only; this catches an accidental code edit).

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/AGENT-BUS-GUIDE.md
git commit -m "docs: document the subscribe protocol-version contract + agentbus version

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- `const ProtocolVersion = 1` in `bus/bus.go` → Task 1 (Step 3).
- `subEvent.V` stamped centrally in `emit` (every variant carries it) → Task 1 (Step 4) + tests (Steps 1/5).
- `agentbus version` handled early (no project/broker), prints `agentbus protocol v1` → Task 2 (Steps 1, 4).
- Usage banner + doc header updated → Task 2 (Step 2).
- Compatibility contract documented (additive within v, bump on breaking, consumer fails loud) → Task 3 (Steps 1-2).
- Non-goals (no per-entry v, no other-JSON v, no handshake, parsers unchanged) → nothing in any task touches those; explicitly preserved.
- Tests (each variant carries v; emit stamps v even when V==0) → Task 1 (Steps 1, 5).

**Placeholder scan:** none — every code/doc step shows the actual content.

**Type consistency:** `bus.ProtocolVersion` (untyped int const) is defined in Task 1 and consumed identically in Task 1's `emit`, Task 1's tests, and Task 2's `version` branch. `subEvent.V int` (`json:"v"`) is defined once in Task 1 and asserted by the same task's tests. The `version` output string `agentbus protocol v%d` is identical in Task 2's branch and its smoke test.
