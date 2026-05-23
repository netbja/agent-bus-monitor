# Curated Bus Notifications — Phase 1 (Bus Foundation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the `hermes:report:{agent}` bus convention and an `agentbus report` command so agents can publish curated, sanitized reports that show up in `busmon` — the foundation the hermes-side relay (a companion plan) consumes.

**Architecture:** Extend the shared `bus` package (single source of truth) with a `ReportChannel`, a `Report` publisher, a `SanitizeReportMessage` helper, and a `Parse` case. Wire a `report` subcommand into the `agentbus` CLI and a render case into the `busmon` TUI. Payload stays `kind|message` (`kind ∈ {note, auto}`), pub/sub, consistent with the existing `status`/`cmd` conventions.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, `github.com/rivo/tview` / `github.com/gdamore/tcell/v2`. Tests are standard `go test` (these are the repo's first tests).

**Spec:** `docs/superpowers/specs/2026-05-23-curated-bus-notifications-design.md` (Phase 1, §Architecture 1).

**Out of scope (companion plan):** `bus_watch_hdl.sh` report consumer, throttle/dedup/log, the `Stop`-hook repoint, and `adv-trading-ai` `CLAUDE.md` guidance — all live in other repos.

---

## File Structure

- `bus/bus.go` (modify) — report channel/kind constants, `ReportChannel`, `SanitizeReportMessage`, `reportPayload`, `Report`, and a `Parse` case. The one place bus conventions live.
- `bus/bus_test.go` (create) — first unit tests in the repo: `Parse` of report payloads, `ReportChannel`, sanitization, payload round-trip.
- `cmd/agentbus/main.go` (modify) — `report` subcommand.
- `cmd/busmon/main.go` (modify) — render `report` lines in ACTIVITY.
- `README.md`, `CLAUDE.md` (modify) — document the channel + command.

---

### Task 1: Report channel + Parse case

**Files:**
- Modify: `bus/bus.go`
- Test: `bus/bus_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `bus/bus_test.go`:

```go
package bus

import "testing"

func TestReportChannel(t *testing.T) {
	if got := ReportChannel("claude1"); got != "hermes:report:claude1" {
		t.Fatalf("ReportChannel = %q, want hermes:report:claude1", got)
	}
}

func TestParseReport(t *testing.T) {
	cases := []struct {
		name, channel, data                string
		agent, kind, state, message        string
	}{
		{"note", "hermes:report:claude1", "note|bug fixed", "claude1", "report", "note", "bug fixed"},
		{"auto", "hermes:report:claude2", "auto|deploy done", "claude2", "report", "auto", "deploy done"},
		{"pipe in message", "hermes:report:claude1", "note|a|b", "claude1", "report", "note", "a|b"},
		{"no message", "hermes:report:claude1", "note", "claude1", "report", "note", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agent, kind, state, message := Parse(c.channel, c.data)
			if agent != c.agent || kind != c.kind || state != c.state || message != c.message {
				t.Fatalf("Parse(%q, %q) = (%q,%q,%q,%q), want (%q,%q,%q,%q)",
					c.channel, c.data, agent, kind, state, message,
					c.agent, c.kind, c.state, c.message)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./bus/ -run 'TestReportChannel|TestParseReport' -v`
Expected: FAIL — build error `undefined: ReportChannel` (and `Parse` returns `"?"` for the report channel).

- [ ] **Step 3: Implement the channel, kind constants, and Parse case**

In `bus/bus.go`, add the kind constants right after the `NotifyChannel` const:

```go
const NotifyChannel = "hermes:notify"

// Report kinds carried in the hermes:report:{agent} payload (kind|message).
const (
	ReportNote = "note" // intentional, agent-authored report → relayed verbatim
	ReportAuto = "auto" // Stop-hook safety-net summary → LLM-gated (phase 2)
)
```

Add `ReportChannel` next to the other channel helpers:

```go
func StatusChannel(agent string) string { return "status:" + agent }
func CmdChannel(agent string) string    { return "hermes:cmd:" + agent }
func ReportChannel(agent string) string { return "hermes:report:" + agent }
```

Update the `Parse` doc comment and add the report case before the final `return`:

```go
// Parse turns a (channel, payload) pair into its logical fields. kind is one of
// "status", "notify", "cmd", "report", or "?" for anything outside the convention.
func Parse(channel, data string) (agent, kind, state, message string) {
	switch {
	case strings.HasPrefix(channel, "status:"):
		parts := strings.SplitN(data, "|", 2)
		state = parts[0]
		if len(parts) > 1 {
			message = parts[1]
		}
		return strings.TrimPrefix(channel, "status:"), "status", state, message
	case channel == NotifyChannel:
		return "hermes", "notify", "", data
	case strings.HasPrefix(channel, "hermes:cmd:"):
		return strings.TrimPrefix(channel, "hermes:cmd:"), "cmd", "", data
	case strings.HasPrefix(channel, "hermes:report:"):
		parts := strings.SplitN(data, "|", 2)
		state = parts[0]
		if len(parts) > 1 {
			message = parts[1]
		}
		return strings.TrimPrefix(channel, "hermes:report:"), "report", state, message
	}
	return "?", "?", "", data
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./bus/ -run 'TestReportChannel|TestParseReport' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add bus/bus.go bus/bus_test.go
git commit -m "feat(bus): add hermes:report channel + Parse case"
```

---

### Task 2: Message sanitization + Report publisher

**Files:**
- Modify: `bus/bus.go`
- Test: `bus/bus_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `bus/bus_test.go` and add `"strings"` to its imports (make the import block):

```go
import (
	"strings"
	"testing"
)
```

```go
func TestSanitizeReportMessage(t *testing.T) {
	if got := SanitizeReportMessage("line1\nline2\r\tend"); got != "line1 line2 end" {
		t.Fatalf("control chars: got %q, want %q", got, "line1 line2 end")
	}
	if got := SanitizeReportMessage("  spaced   out  "); got != "spaced out" {
		t.Fatalf("whitespace: got %q, want %q", got, "spaced out")
	}
	got := SanitizeReportMessage(strings.Repeat("x", 200))
	if r := []rune(got); len(r) != maxReportLen+1 || r[len(r)-1] != '…' {
		t.Fatalf("truncation: got %d runes (last %q), want %d + …",
			len([]rune(got)), string([]rune(got)[len([]rune(got))-1]), maxReportLen)
	}
}

func TestReportPayloadRoundTrip(t *testing.T) {
	agent, kind, state, message := Parse(ReportChannel("claude1"), reportPayload(ReportNote, "bug\nX|fixed"))
	if agent != "claude1" || kind != "report" || state != "note" || message != "bug X|fixed" {
		t.Fatalf("round-trip = (%q,%q,%q,%q), want (claude1,report,note,bug X|fixed)",
			agent, kind, state, message)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./bus/ -run 'TestSanitizeReportMessage|TestReportPayloadRoundTrip' -v`
Expected: FAIL — build error `undefined: SanitizeReportMessage`, `maxReportLen`, `reportPayload`.

- [ ] **Step 3: Implement sanitize, payload, and Report**

In `bus/bus.go`, add `"unicode"` to the import block:

```go
import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)
```

Add the length cap near the report constants:

```go
const maxReportLen = 120
```

Add the helpers (place them next to the `Status` publisher):

```go
// SanitizeReportMessage strips control characters — the line-based `agentbus
// listen` consumer breaks on embedded newlines — collapses runs of whitespace,
// and truncates to maxReportLen runes so a report stays one bounded line.
func SanitizeReportMessage(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	out := strings.Join(strings.Fields(mapped), " ")
	if r := []rune(out); len(r) > maxReportLen {
		out = strings.TrimSpace(string(r[:maxReportLen])) + "…"
	}
	return out
}

func reportPayload(kind, message string) string {
	return kind + "|" + SanitizeReportMessage(message)
}

// Report publishes an agent's report on hermes:report:{agent}. kind is
// ReportNote (intentional) or ReportAuto (Stop-hook safety net).
func Report(ctx context.Context, r *redis.Client, agent, kind, message string) error {
	return r.Publish(ctx, ReportChannel(agent), reportPayload(kind, message)).Err()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./bus/ -v`
Expected: PASS (all four test functions).

- [ ] **Step 5: Commit**

```bash
git add bus/bus.go bus/bus_test.go
git commit -m "feat(bus): sanitize + truncate report messages, add Report publisher"
```

---

### Task 3: `agentbus report` subcommand

**Files:**
- Modify: `cmd/agentbus/main.go`

Note: `report <agent>` validates `<agent>` against `bus.ValidAgents`, which is a strict allowlist (`claude1`, `claude2`, `hermes_laptop`, `hermes_vdr`) — stricter than a regex, so it already prevents arbitrary identities from publishing. No extra `AGENT_BUS_AGENT` regex is needed here.

- [ ] **Step 1: Add the subcommand and update usage text**

In `cmd/agentbus/main.go`, extend the top-of-file usage doc comment with:

```go
//   agentbus report <agent> [--auto] <message...>
```

Update the top-level usage in `die(...)`:

```go
	if len(args) < 1 {
		die("usage: agentbus <status|cmd|notify|listen|report> ...  [--host <host>]")
	}
```

Add this case to the `switch args[0]` block (e.g. after the `notify` case):

```go
	case "report":
		if len(args) < 3 {
			die("usage: agentbus report <agent> [--auto] <message>")
		}
		agent := args[1]
		if !bus.ValidAgents[agent] {
			die(fmt.Sprintf("invalid agent %q", agent))
		}
		rest := args[2:]
		kind := bus.ReportNote
		if rest[0] == "--auto" {
			kind = bus.ReportAuto
			rest = rest[1:]
		}
		if len(rest) == 0 {
			die("usage: agentbus report <agent> [--auto] <message>")
		}
		if err := bus.Report(ctx, client, agent, kind, strings.Join(rest, " ")); err != nil {
			die(err.Error())
		}
```

- [ ] **Step 2: Build**

Run: `go build ./cmd/agentbus && go build ./cmd/busmon`
Expected: no output (success).

- [ ] **Step 3: Manually verify the round-trip against the live loopback broker**

The broker runs on `127.0.0.1:6380` (docker `agent-bus-redis`). In terminal A:

Run: `go run ./cmd/agentbus listen "hermes:report:*"`

In terminal B, run each and check terminal A's output:

```bash
go run ./cmd/agentbus report claude1 "bug X corrigé"
# A: [hermes:report:claude1] note|bug X corrigé

go run ./cmd/agentbus report claude1 --auto "deploy done"
# A: [hermes:report:claude1] auto|deploy done

printf 'go run ./cmd/agentbus report claude1 "%s"\n' 'line1
line2'   # multi-line message → must arrive as ONE sanitized line
go run ./cmd/agentbus report claude1 "$(printf 'line1\nline2')"
# A: [hermes:report:claude1] note|line1 line2   (single line, no break)

go run ./cmd/agentbus report nobody "x"
# stderr: error: invalid agent "nobody"  (exit 1)
```

Stop terminal A with Ctrl-C.

- [ ] **Step 4: Commit**

```bash
git add cmd/agentbus/main.go
git commit -m "feat(agentbus): add report subcommand"
```

---

### Task 4: Render reports in busmon ACTIVITY

**Files:**
- Modify: `cmd/busmon/main.go`

busmon already `PSubscribe`s `"status:*", "hermes:*"`, so `hermes:report:{agent}` messages already arrive — only the render switch needs a case.

- [ ] **Step 1: Add the render case**

In `cmd/busmon/main.go`, inside the `switch kind {` block, add a `case "report":` before `default:` (the report kind — `note`/`auto` — is in `state`):

```go
			case "report":
				line = tag("gray", ts) + " " + tag("teal", "[report:"+state+"->"+agent+"]") + " " + tview.Escape(message)
```

- [ ] **Step 2: Build**

Run: `go build ./cmd/busmon`
Expected: no output (success).

- [ ] **Step 3: Manually verify in the TUI**

Run (terminal A): `go run ./cmd/busmon`
Then (terminal B): `go run ./cmd/agentbus report claude1 "render test"`
Expected: a line appears in the ACTIVITY pane like `HH:MM:SS [report:note->claude1] render test`.
Quit busmon with Esc or Ctrl-C.

- [ ] **Step 4: Commit**

```bash
git add cmd/busmon/main.go
git commit -m "feat(busmon): render hermes:report lines in ACTIVITY"
```

---

### Task 5: Document the report channel + command

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update README**

In `README.md`, add a row to the Bus conventions table (after the `hermes:cmd:{agent}` row):

```
| `hermes:report:{agent}` | `note\|message` or `auto\|message` | ACTIVITY (consumed by hermes) |
```

And add to the "Use it" examples:

```bash
agentbus report claude1 "bug corrigé"             # curated report → hermes relays to Signal
agentbus report claude1 --auto "soak 24h done"    # auto = Stop-hook safety net
```

- [ ] **Step 2: Update CLAUDE.md**

In `CLAUDE.md`, under the `agentbus` description, note the new subcommand and channel:

```
- **`agentbus`** — fire-and-forget CLI: `status`/`cmd`/`notify`/`listen`/`report`. `report <agent>
  [--auto] <msg>` publishes on `hermes:report:{agent}` (`kind|message`, kind `note`/`auto`),
  sanitized + truncated in `bus.go`. Consumed by hermes_laptop, not by the other agents.
```

- [ ] **Step 3: Verify the module still builds and vets, then commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build/vet clean; `ok github.com/netbja/agent-bus-monitor/bus`.

```bash
git add README.md CLAUDE.md
git commit -m "docs: document hermes:report channel and agentbus report"
```

---

## Self-Review

**Spec coverage (Phase 1 §Architecture 1 — Bus convention):**
- `ReportChannel` → Task 1. ✓
- `Report` publisher → Task 2. ✓
- `Parse` report case → Task 1. ✓
- sanitize control chars + truncate ~120 → Task 2. ✓
- `agentbus report <agent> [--auto] <message...>` + ValidAgents check → Task 3. ✓
- `AGENT_BUS_AGENT` validation → covered by the stricter `ValidAgents` allowlist (noted in Task 3). ✓
- busmon render → Task 4. ✓
- docs → Task 5. ✓
- Out of scope here (companion plan): throttle/dedup, decision log, `Stop`-hook repoint, `bus_watch_hdl.sh` consumer, adv-trading-ai guidance, `AGENT_BUS_AGENT` wiring. Tracked, not dropped.

**Placeholder scan:** none — every code/test/command step is concrete.

**Type consistency:** `ReportChannel`, `Report(ctx,r,agent,kind,message)`, `SanitizeReportMessage`, `reportPayload(kind,message)`, `ReportNote`/`ReportAuto`, `maxReportLen` used consistently across Tasks 1–4; `Parse` returns the report kind in the `state` slot, and busmon/tests read it from `state`. ✓
