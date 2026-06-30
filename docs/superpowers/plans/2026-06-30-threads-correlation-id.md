# Threads / Correlation-ID Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `:cmd` correlation queryable — `cmd` prints its entry id and accepts `--ref`, and a new `agentbus thread <T>` reader groups the `:cmd` entries sharing a thread so "directive → reply → resolution" reads as one chain.

**Architecture:** Thread-id = a message's `ref` if set, else its own stream id (flat threads, natural-id rooting). The `bus` package gets one read method `Thread` that XRANGEs `:cmd` and filters by `ref==T || id==T`; the CLI gets a pure `threadReport` renderer plus a `thread` command, and `cmd` is widened to carry/print the thread root. No new durable store, no wire-format change — the verdict ledger stays the durable audit; this is the live-coordination view.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, module `github.com/netbja/agent-bus-monitor`. Bus tests use the repo's `dialTest` helper (real dev broker, skips if Redis down).

## Global Constraints

- Go 1.26; module `github.com/netbja/agent-bus-monitor`. The read method goes in `bus/stream.go` (transport), not the binary.
- All UI/CLI text (usage, output) is **English**.
- **Do not change the `:cmd`/`subscribe` wire format.** The `ref` field already exists. The only change to an existing command is `cmd`: it now passes `ref` (instead of a hardcoded `""`) and prints the new entry id. Both are additive — armed subscribers cannot break.
- **Thread-id = `ref` if non-empty, else the entry's own `id`.** Threads are flat: replies/verdicts/follow-ups reference the root's thread-id.
- `thread <T>` belongs-test: an entry belongs iff `ref==T`, or `id==T` (the root), or `T` is all-digits (a bare `<ms>`) and `id` has prefix `T+"-"`.
- `thread` exits `0` even for an empty/unknown thread (absence is not an error). `die` (exit 1) stays reserved for usage/connection errors.
- `cmd` prints its entry id as a **bare stdout line** (so `ID=$(agentbus cmd …)` captures it). `challenge` keeps its existing sentence output, untouched.
- Live `:cmd` read only; no new durable structure.
- Bus-level tests need Redis: run `docker compose up -d` first (port 6380). `dialTest` skips them if the broker is down.

## File Structure

- `bus/stream.go` — **Modify.** Add `(*Bus).Thread` + a small `isAllDigits` helper.
- `bus/stream_test.go` — **Modify.** Add `Thread` tests (ensure `strings` is imported).
- `cmd/agentbus/thread.go` — **Create.** Pure `threadReport` renderer + a local `idMS` id→ms helper. Mirrors `verdicts.go`; reuses `humanAge` from `agents.go`.
- `cmd/agentbus/thread_test.go` — **Create.** Unit tests for `threadReport` (no Redis).
- `cmd/agentbus/main.go` — **Modify.** Widen the `cmd` case (`--ref` + print id); add the `thread` case; update the usage banner and doc-comment header.
- `README.md`, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md` — **Modify.** Document `cmd --ref`/printed id, the `thread` command, and the thread-id rule.

---

### Task 1: `bus.Thread` reader

**Files:**
- Modify: `bus/stream.go` (add `Thread` + `isAllDigits`)
- Test: `bus/stream_test.go` (add tests; ensure `strings` import)

**Interfaces:**
- Consumes: existing `bus` internals — `(*Bus).r`, `(*Bus).project`, `StreamKey`, `ParseEntry`, `toStringMap`, the `Event` type (fields `ID`, `Ref`, `Type`, `From`, `Target`, `Message`), and the cmd-type constants `CmdDirective`/`CmdChallenge`/`CmdReply`/`CmdVerdict`.
- Produces (Task 2/3 rely on this): `func (b *Bus) Thread(ctx context.Context, threadID string) ([]Event, error)` — returns the matching `:cmd` entries oldest→newest; empty (non-nil) slice when none match.

- [ ] **Step 1: Write the failing tests**

Add to `bus/stream_test.go` (and make sure the import block includes `"strings"`):

```go
func TestThread(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	root, err := b.Cmd(ctx, "hermes", "claude2", CmdDirective, "", "review the migration")
	if err != nil {
		t.Fatalf("Cmd directive: %v", err)
	}
	if _, err := b.Cmd(ctx, "claude2", "hermes", CmdReply, root, "on it"); err != nil {
		t.Fatalf("Cmd reply: %v", err)
	}
	if _, err := b.Cmd(ctx, "hermes", "dev", CmdDirective, "", "unrelated"); err != nil {
		t.Fatalf("Cmd unrelated: %v", err)
	}
	evs, err := b.Thread(ctx, root)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("Thread(%q) = %d entries, want 2 (directive+reply)", root, len(evs))
	}
	if evs[0].ID != root || evs[0].Type != CmdDirective || evs[0].Message != "review the migration" {
		t.Fatalf("entry 0 not the root directive: %+v", evs[0])
	}
	if evs[1].Type != CmdReply || evs[1].Ref != root || evs[1].Message != "on it" {
		t.Fatalf("entry 1 not the reply: %+v", evs[1])
	}
}

func TestThreadChallengeRef(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	const C = "c1abc"
	if _, err := b.Cmd(ctx, "rev", "claude2", CmdChallenge, C, "justify X"); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if _, err := b.Cmd(ctx, "claude2", "rev", CmdReply, C, "because Y"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := b.Cmd(ctx, "rev2", "claude2", CmdVerdict, C, "approve"); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	evs, err := b.Thread(ctx, C)
	if err != nil || len(evs) != 3 {
		t.Fatalf("Thread(%q) = %d (%v), want 3 (challenge+reply+verdict)", C, len(evs), err)
	}
}

func TestThreadBareMS(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	root, err := b.Cmd(ctx, "hermes", "claude2", CmdDirective, "", "x")
	if err != nil {
		t.Fatalf("Cmd: %v", err)
	}
	ms := root
	if i := strings.IndexByte(root, '-'); i >= 0 {
		ms = root[:i]
	}
	evs, err := b.Thread(ctx, ms) // bare <ms>, no -seq
	if err != nil || len(evs) != 1 || evs[0].ID != root {
		t.Fatalf("Thread(bare ms %q) = %+v (%v), want the root entry", ms, evs, err)
	}
}

func TestThreadUnknown(t *testing.T) {
	b := dialTest(t)
	evs, err := b.Thread(context.Background(), "nope-0")
	if err != nil {
		t.Fatalf("Thread(unknown) error: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("Thread(unknown) = %d entries, want 0", len(evs))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `docker compose up -d && go test ./bus/ -run 'Thread' -count=1 -v`
Expected: FAIL — `undefined: (*Bus).Thread` (compile error).

- [ ] **Step 3: Implement `Thread`**

Add to `bus/stream.go`, near the other read helpers (e.g. after `Verdicts`):

```go
// Thread returns every {project}:cmd entry belonging to thread threadID,
// oldest→newest (XRANGE is ascending). An entry belongs iff its ref == threadID,
// or its id == threadID (the root), or threadID is a bare <ms> (all digits) and
// the entry's id has the prefix threadID+"-". Scan-and-filter over the capped cmd
// stream, like Verdicts; reads with XRANGE only, so no consumer-group cursors are
// touched. An unknown thread yields an empty (non-nil) slice, not an error.
func (b *Bus) Thread(ctx context.Context, threadID string) ([]Event, error) {
	key := StreamKey(b.project, "cmd")
	msgs, err := b.r.XRange(ctx, key, "-", "+").Result()
	if err != nil {
		return nil, err
	}
	bareMS := isAllDigits(threadID)
	out := make([]Event, 0, len(msgs))
	for _, m := range msgs {
		e := ParseEntry(key, m.ID, toStringMap(m.Values))
		if e.Ref == threadID || e.ID == threadID || (bareMS && strings.HasPrefix(e.ID, threadID+"-")) {
			out = append(out, e)
		}
	}
	return out, nil
}

// isAllDigits reports whether s is non-empty and only ASCII digits (a bare <ms>
// stream-id prefix). Used by Thread to accept a millisecond cursor without its
// "-<seq>" suffix, mirroring subscribe --since.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
```

(`strings` is already imported in `stream.go`.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./bus/ -run 'Thread' -count=1 -v`
Expected: PASS — `TestThread`, `TestThreadChallengeRef`, `TestThreadBareMS`, `TestThreadUnknown`.

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
git add bus/stream.go bus/stream_test.go
git commit -m "feat(bus): Thread — group {project}:cmd entries by ref||id

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: CLI `threadReport` renderer (pure)

**Files:**
- Create: `cmd/agentbus/thread.go`
- Test: `cmd/agentbus/thread_test.go`

**Interfaces:**
- Consumes: `bus.Event` (fields `ID`, `Type`, `From`, `Target`, `Message`); `humanAge(time.Duration) string` (existing, `cmd/agentbus/agents.go` — reuse, do not redefine).
- Produces (Task 3 relies on this): `func threadReport(threadID string, evs []bus.Event, now time.Time) string`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/agentbus/thread_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestThreadReport(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	root := "1700000000000-0"
	evs := []bus.Event{
		{ID: root, Kind: "cmd", Type: "directive", From: "hermes", Target: "claude2", Message: "review the migration"},
		{ID: "1700000000005-0", Kind: "cmd", Type: "reply", From: "claude2", Target: "hermes", Message: "on it", Ref: root},
	}
	out := threadReport(root, evs, now)
	if !strings.Contains(out, "(2 entries)") {
		t.Fatalf("header missing count: %q", out)
	}
	if !strings.Contains(out, "directive") || !strings.Contains(out, "hermes→claude2") || !strings.Contains(out, `"review the migration"`) {
		t.Fatalf("root line wrong: %q", out)
	}
	if !strings.Contains(out, "(root)") {
		t.Fatalf("root not marked: %q", out)
	}
	if strings.Index(out, "review the migration") > strings.Index(out, "on it") {
		t.Fatalf("not chronological: %q", out)
	}
}

func TestThreadReportEmpty(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	if out := threadReport("x-0", nil, now); !strings.Contains(out, "(no entries)") {
		t.Fatalf("empty thread should say no entries: %q", out)
	}
	evs := []bus.Event{{ID: "1-0", Type: "directive", From: "a", Target: "b", Message: ""}}
	if out := threadReport("1-0", evs, now); strings.Contains(out, `""`) {
		t.Fatalf("empty message should not render quotes: %q", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agentbus/ -run 'ThreadReport' -count=1 -v`
Expected: FAIL — `undefined: threadReport` (compile error).

- [ ] **Step 3: Write the implementation**

Create `cmd/agentbus/thread.go`:

```go
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// threadReport renders a correlation thread: a header plus one chronological
// line per :cmd entry (oldest→newest, as bus.Thread returns them). The entry
// whose id == threadID (the root) is marked "(root)"; an empty message renders
// nothing (not `""`). Pure — no Redis.
func threadReport(threadID string, evs []bus.Event, now time.Time) string {
	var sb strings.Builder
	if len(evs) == 0 {
		fmt.Fprintf(&sb, "thread %s  (no entries)\n", threadID)
		return sb.String()
	}
	fmt.Fprintf(&sb, "thread %s  (%d entries)\n", threadID, len(evs))
	for _, e := range evs {
		fmt.Fprintf(&sb, "  %-9s %-9s %s→%s",
			humanAge(now.Sub(time.UnixMilli(idMS(e.ID)))), e.Type, e.From, e.Target)
		if e.Message != "" {
			fmt.Fprintf(&sb, "  %q", e.Message)
		}
		if e.ID == threadID {
			sb.WriteString("  (root)")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// idMS parses the millisecond component of a Redis stream id ("<ms>-<seq>").
// The bus package's splitID is unexported, so the CLI parses it locally.
func idMS(id string) int64 {
	s := id
	if i := strings.IndexByte(id, '-'); i >= 0 {
		s = id[:i]
	}
	ms, _ := strconv.ParseInt(s, 10, 64)
	return ms
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/agentbus/ -run 'ThreadReport' -count=1 -v`
Expected: PASS — `TestThreadReport`, `TestThreadReportEmpty`.

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
git add cmd/agentbus/thread.go cmd/agentbus/thread_test.go
git commit -m "feat(agentbus): threadReport — chronological thread render

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Wire `cmd --ref`/printed-id and the `thread` command

**Files:**
- Modify: `cmd/agentbus/main.go` (the `cmd` case ~lines 119-125; add a `thread` case; usage banner line 70; doc-comment header lines 12-24)

**Interfaces:**
- Consumes: `(*bus.Bus).Cmd`, `(*bus.Bus).Thread` (Task 1), `threadReport` (Task 2), existing `extractFlag`, `self`, `bus.CmdDirective`, `die`, `ctx`, `b`.
- Produces: the user-facing `cmd --ref`/printed id and `thread` command. No symbols consumed by later tasks.

- [ ] **Step 1: Replace the `cmd` case**

In `cmd/agentbus/main.go`, replace the current `case "cmd":` block:

```go
	case "cmd":
		if len(rest) < 2 {
			die("usage: cmd <target> <command>")
		}
		if _, err := b.Cmd(ctx, self, rest[0], bus.CmdDirective, "", strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}
```

with:

```go
	case "cmd":
		rest, ref := extractFlag(rest, "--ref")
		if len(rest) < 2 {
			die("usage: cmd [--ref T] <target> <command>")
		}
		id, err := b.Cmd(ctx, self, rest[0], bus.CmdDirective, ref, strings.Join(rest[1:], " "))
		if err != nil {
			die(err.Error())
		}
		fmt.Println(id) // entry id = this thread's root, capturable for replies
```

- [ ] **Step 2: Add the `thread` case**

Immediately after the `cmd` case (before `case "challenge":`), add:

```go
	case "thread":
		if len(rest) < 1 {
			die("usage: thread <thread-id>")
		}
		evs, err := b.Thread(ctx, rest[0])
		if err != nil {
			die(err.Error())
		}
		fmt.Print(threadReport(rest[0], evs, time.Now()))
```

- [ ] **Step 3: Update the usage banner and doc-comment header**

In the top-level usage `die` (line 70), add `thread` after `cmd`:

```go
		die("usage: agentbus --project <p> <status|report|notify|cmd|thread|challenge|reply|verdict|verdicts|pilot|gate|agents|pane|usage|subscribe|watch|listen> ...")
```

In the doc-comment header, replace the `cmd` line (line 12):

```go
//	agentbus --project P cmd       [--ref T] <target> <command...>   # prints the entry id (= thread root)
```

and add a `thread` line immediately after it:

```go
//	agentbus --project P thread    <thread-id>   # show the :cmd thread (entries where ref or id == thread-id), chronological
```

- [ ] **Step 4: Build + vet + full unit tests**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: build/vet clean; all tests PASS (bus tests skip if Redis is down — `docker compose up -d` to exercise them).

- [ ] **Step 5: End-to-end smoke test against the dev broker**

Run:

```bash
docker compose up -d
go build -o agentbus ./cmd/agentbus
export AGENT_BUS_PROJECT=demo-thread-$RANDOM
ID=$(AGENT_BUS_AGENT=hermes ./agentbus cmd claude2 review the migration)
echo "root id: $ID"
AGENT_BUS_AGENT=claude2 ./agentbus reply --ref "$ID" hermes on it
./agentbus thread "$ID"
```

Expected:
- `cmd` prints a bare stream id (captured into `$ID`, e.g. `1782…-0`).
- `thread "$ID"` prints `thread <ID>  (2 entries)`, the `directive hermes→claude2 "review the migration"  (root)` line first, then the `reply claude2→hermes "on it"` line.

- [ ] **Step 6: Commit**

```bash
git add cmd/agentbus/main.go
git commit -m "feat(agentbus): cmd --ref + printed id, and the thread reader command

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Documentation

**Files:**
- Modify: `README.md` (command reference)
- Modify: `CLAUDE.md` (agentbus verb list + the cmd/ref/thread description)
- Modify: `docs/AGENT-BUS-GUIDE.md` (cheat-sheet: cmd prints id, thread command)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update `CLAUDE.md`**

Two edits so the docs match the code:

1. In the **`agentbus`** bullet's verb list, add `thread` (e.g. after `cmd`): the list currently reads `status`/`report`/`notify`/`cmd`/`challenge`/`reply`/`verdict`/`verdicts`/`pilot`/`gate`/`agents`/`subscribe`/`watch`/`listen` — insert `thread` after `cmd`.

2. In the same bullet's prose, add a sentence:
   > `cmd [--ref T] <target> <command>` now prints its entry id (the thread root) and accepts `--ref` to continue an existing thread; `thread <T>` reads the `:cmd` stream and prints every entry whose `ref` or `id` equals `T`, chronologically — the thread-id of any message is its `ref` if set, else its own id, so a directive id and a challenge ref both work. Live read of `:cmd` (recent ~1000); the verdict ledger remains the durable audit.

- [ ] **Step 2: Update `README.md`**

In the command reference, add lines for the widened `cmd` and the new `thread`, matching the surrounding one-liner style:

```
agentbus cmd [--ref T] claude2 run the suite      # directive; prints the entry id (thread root)
agentbus thread 1782588072942-0                   # show the :cmd thread (ref or id == arg), chronological
```

If the `:cmd` row of the README stream table mentions correlation, leave it; otherwise no table change is needed (the wire format is unchanged).

- [ ] **Step 3: Update `docs/AGENT-BUS-GUIDE.md`**

In the directives/coordination section (around the `cmd` examples near line 94), note that `cmd` prints the entry id and that a reply threads onto it, and add a `thread` cheat-sheet line. Concretely, near the existing `agentbus cmd claude2 run the integration suite` example, add:

```bash
ID=$(agentbus cmd claude2 run the integration suite)   # prints the entry id = thread root
agentbus reply --ref "$ID" hermes on it                # thread a reply onto that directive
agentbus thread "$ID"                                  # see the whole chain (directive → reply → verdict)
```

Match the surrounding cheat-sheet formatting (aligned trailing `#` comments).

- [ ] **Step 4: Verify no code drift**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: still green (docs-only; this catches an accidental code edit).

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/AGENT-BUS-GUIDE.md
git commit -m "docs: document cmd --ref/printed id and the thread reader

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- Thread-id = `ref || id`, flat threads → Task 1 (`Thread` belongs-test) + Task 3 (`cmd` passes ref).
- Live `:cmd` read, no durable store → Task 1 (`Thread` XRANGEs `:cmd`, no new key).
- `cmd` prints id + accepts `--ref` → Task 3 (Step 1).
- `reply`/`verdict` unchanged → not touched (confirmed: only the `cmd` case changes).
- `thread <T>` reader, chronological, root marker, empty-message guard, `(no entries)`, exit 0 → Task 2 (`threadReport`) + Task 3 (Step 2).
- Bare-`<ms>` tolerance → Task 1 (`isAllDigits` + prefix match; `TestThreadBareMS`).
- Unifies directive-id and challenge-ref threads → Task 1 (`TestThreadChallengeRef`).
- Wire format untouched → only `cmd` changes (passes `ref`, prints id); `subscribe`/other commands unchanged.
- Docs (cmd/ref/thread, thread-id rule) → Task 4.
- Non-goals (report/notify threading, durable store, ack/TTL, busmon) → not implemented, as intended.

**Placeholder scan:** none — every code/doc step shows the actual content.

**Type consistency:** `Thread(ctx, threadID string) ([]Event, error)` and `threadReport(threadID string, evs []bus.Event, now time.Time) string` are used identically across Tasks 1-3. `idMS` is local to `thread.go` and used only by `threadReport`. `isAllDigits` is local to `stream.go` and used only by `Thread`. `cmd` prints the id returned by `b.Cmd` (which already returns `(string, error)`).
