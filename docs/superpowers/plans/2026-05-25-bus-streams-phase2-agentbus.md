# Bus Streams Phase 2 — agentbus CLI migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Rewrite `cmd/agentbus` onto the Phase-1 Streams API (`bus.Open` + the `Bus` handle), adding `--project`/`AGENT_BUS_PROJECT` and the full subcommand set (status/report/notify/cmd/challenge/reply/verdict/pilot/gate/watch/listen).

**Architecture:** `agentbus` stays a single `main.go` with manual arg parsing (repo style — no flag library). Three small testable pure helpers (`extractFlag`, `extractBool`, `genRef`) are unit-tested; the rest is verified by `go build`/`go vet` and a deterministic smoke test that publishes via the CLI and reads the entry back with `redis-cli` inside the broker container. The old pub/sub `bus.go` API is NOT removed here (busmon still uses it until Phase 3; removal is Phase 4).

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9`, broker on `127.0.0.1:6380` (`docker compose up -d`).

**Phase context:** Phase 2 of 4 from `docs/superpowers/specs/2026-05-25-bus-streams-unification-design.md`. Phase 1 (the `bus` Streams foundation: `Open`, `Status/Report/Notify/Cmd`, `Tail`, `WatchCmd`, `Pilot/ReleasePilot/PilotDriver`, `OpenChallenge/ResolveChallenge/OpenChallenges`, `Event`, `CmdDirective/CmdChallenge/CmdReply/CmdVerdict`) is committed and tested — read `bus/stream.go` for the exact signatures. Do NOT touch `bus/bus.go`, `bus/stream.go`, or `cmd/busmon`.

**Before you start:** `docker compose up -d`.

---

### Task 1: Arg-parsing helpers (TDD)

**Files:**
- Create: `cmd/agentbus/parse.go`
- Test: `cmd/agentbus/parse_test.go`

- [ ] **Step 1: Write the failing tests** — create `cmd/agentbus/parse_test.go`:

```go
package main

import "testing"

func TestExtractFlag(t *testing.T) {
	rest, v := extractFlag([]string{"a", "--ref", "C1", "b"}, "--ref")
	if v != "C1" {
		t.Fatalf("value = %q, want C1", v)
	}
	if len(rest) != 2 || rest[0] != "a" || rest[1] != "b" {
		t.Fatalf("rest = %v, want [a b]", rest)
	}
	if _, v := extractFlag([]string{"a", "b"}, "--ref"); v != "" {
		t.Fatalf("absent flag value = %q, want \"\"", v)
	}
	// a flag with no following value is treated as absent (not consumed)
	if rest, v := extractFlag([]string{"a", "--ref"}, "--ref"); v != "" || len(rest) != 2 {
		t.Fatalf("dangling flag = (%v, %q), want ([a --ref], \"\")", rest, v)
	}
}

func TestExtractBool(t *testing.T) {
	rest, ok := extractBool([]string{"a", "--auto", "b"}, "--auto")
	if !ok || len(rest) != 2 || rest[0] != "a" || rest[1] != "b" {
		t.Fatalf("got (%v, %v), want ([a b], true)", rest, ok)
	}
	if _, ok := extractBool([]string{"a", "b"}, "--auto"); ok {
		t.Fatal("absent bool flag reported present")
	}
}

func TestGenRefUnique(t *testing.T) {
	if a, b := genRef(), genRef(); a == b || a == "" {
		t.Fatalf("genRef not unique/non-empty: %q %q", a, b)
	}
}
```

- [ ] **Step 2: Run to verify fail** — `go test ./cmd/agentbus/ -v` → FAIL (`undefined: extractFlag`).

- [ ] **Step 3: Implement** — create `cmd/agentbus/parse.go`:

```go
package main

import (
	"strconv"
	"time"
)

// extractFlag removes the first "--name value" pair from args and returns the
// remaining args and the value ("" if the flag is absent or has no value).
func extractFlag(args []string, name string) ([]string, string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			out := append(append([]string{}, args[:i]...), args[i+2:]...)
			return out, args[i+1]
		}
	}
	return args, ""
}

// extractBool removes the first "--name" flag from args and reports presence.
func extractBool(args []string, name string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			out := append(append([]string{}, args[:i]...), args[i+1:]...)
			return out, true
		}
	}
	return args, false
}

// genRef returns a short, sortable, unique challenge id.
func genRef() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }
```

- [ ] **Step 4: Run to verify pass** — `go test ./cmd/agentbus/ -v` → PASS (the two extract tests + genRef). NOTE: `genRef` calls happen in the same nanosecond rarely; if `TestGenRefUnique` is ever flaky, that's acceptable to leave — UnixNano is monotonic on Linux so consecutive calls differ.

- [ ] **Step 5: Commit**

```bash
git add cmd/agentbus/parse.go cmd/agentbus/parse_test.go
git commit -m "feat(agentbus): arg-parsing helpers (extractFlag/extractBool/genRef)"
```

---

### Task 2: Rewrite main.go onto the Streams API

**Files:**
- Modify (full rewrite): `cmd/agentbus/main.go`

- [ ] **Step 1: Replace `cmd/agentbus/main.go` with this:**

```go
// agentbus — CLI client for the Agent Bus over Redis Streams.
//
// The project is required (--project or AGENT_BUS_PROJECT); every stream is
// namespaced {project}:{kind}. Self identity (for `from`/pilot driver) is
// AGENT_BUS_AGENT (default "hermes"). Trailing words are joined into one message.
//
// Usage:
//   agentbus --project P status    <agent> <working|idle|blocked|done> [msg...]
//   agentbus --project P report    <agent> [--auto] <msg...>
//   agentbus --project P notify    <msg...>
//   agentbus --project P cmd       <target> <command...>
//   agentbus --project P challenge <target> [--ref R] <msg...>   # opens a 4-eyes gate
//   agentbus --project P reply     --ref R <target> <msg...>
//   agentbus --project P verdict   --ref R <target> <approve|reject> [msg...]  # resolves the gate
//   agentbus --project P pilot     <claim|renew|release|status> [--ttl 90s]
//   agentbus --project P gate      <agent>      # lists open challenges; exit 1 if gated
//   agentbus --project P watch     <agent>      # one-shot: prints first addressed cmd, or __HEARTBEAT__
//   agentbus --project P listen    [status report notify cmd]    # debug tail
//   agentbus --host <host> ...
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

const (
	pilotTTL  = 90 * time.Second  // default pilot lease TTL (override --ttl)
	heartbeat = 240 * time.Second // watch prints __HEARTBEAT__ and exits after this idle window
)

func die(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	args := os.Args[1:]
	args, host := extractFlag(args, "--host")
	args, project := extractFlag(args, "--project")
	if project == "" {
		project = os.Getenv("AGENT_BUS_PROJECT")
	}
	if project == "" {
		die("project required: pass --project <p> or set AGENT_BUS_PROJECT")
	}
	if len(args) < 1 {
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|pilot|gate|watch|listen> ...")
	}

	self := envOr("AGENT_BUS_AGENT", "hermes")

	client, err := bus.Connect(host)
	if err != nil {
		die(fmt.Sprintf("Redis connection failed: %v", err))
	}
	b, err := bus.Open(client, project)
	if err != nil {
		die(err.Error())
	}
	ctx := context.Background()
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "status":
		if len(rest) < 2 {
			die("usage: status <agent> <state> [message]")
		}
		if _, err := b.Status(ctx, rest[0], rest[1], strings.Join(rest[2:], " ")); err != nil {
			die(err.Error())
		}

	case "report":
		rest, auto := extractBool(rest, "--auto")
		if len(rest) < 2 {
			die("usage: report <agent> [--auto] <message>")
		}
		kind := bus.ReportNote
		if auto {
			kind = bus.ReportAuto
		}
		if _, err := b.Report(ctx, rest[0], kind, strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}

	case "notify":
		if len(rest) < 1 {
			die("usage: notify <message>")
		}
		if _, err := b.Notify(ctx, self, strings.Join(rest, " ")); err != nil {
			die(err.Error())
		}

	case "cmd":
		if len(rest) < 2 {
			die("usage: cmd <target> <command>")
		}
		if _, err := b.Cmd(ctx, self, rest[0], bus.CmdDirective, "", strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}

	case "challenge":
		rest, ref := extractFlag(rest, "--ref")
		if len(rest) < 2 {
			die("usage: challenge <target> [--ref R] <message>")
		}
		if ref == "" {
			ref = genRef()
		}
		target, msg := rest[0], strings.Join(rest[1:], " ")
		if _, err := b.Cmd(ctx, self, target, bus.CmdChallenge, ref, msg); err != nil {
			die(err.Error())
		}
		if err := b.OpenChallenge(ctx, target, ref, self+"|"+msg); err != nil {
			die(err.Error())
		}
		fmt.Printf("challenge %s opened on %s\n", ref, target)

	case "reply":
		rest, ref := extractFlag(rest, "--ref")
		if ref == "" || len(rest) < 2 {
			die("usage: reply --ref R <target> <message>")
		}
		if _, err := b.Cmd(ctx, self, rest[0], bus.CmdReply, ref, strings.Join(rest[1:], " ")); err != nil {
			die(err.Error())
		}

	case "verdict":
		rest, ref := extractFlag(rest, "--ref")
		if ref == "" || len(rest) < 2 {
			die("usage: verdict --ref R <target> <approve|reject> [message]")
		}
		target, decision := rest[0], rest[1]
		if decision != "approve" && decision != "reject" {
			die("verdict decision must be approve or reject")
		}
		msg := decision
		if len(rest) > 2 {
			msg += ": " + strings.Join(rest[2:], " ")
		}
		// Resolve first: a verdict for a ref that isn't open must fail loudly.
		if err := b.ResolveChallenge(ctx, target, ref); err != nil {
			die(err.Error())
		}
		if _, err := b.Cmd(ctx, self, target, bus.CmdVerdict, ref, msg); err != nil {
			die(err.Error())
		}

	case "pilot":
		if len(rest) < 1 {
			die("usage: pilot <claim|renew|release|status> [--ttl 90s]")
		}
		rest, ttlStr := extractFlag(rest, "--ttl")
		ttl := pilotTTL
		if ttlStr != "" {
			if d, perr := time.ParseDuration(ttlStr); perr == nil {
				ttl = d
			} else {
				die(fmt.Sprintf("bad --ttl %q: %v", ttlStr, perr))
			}
		}
		switch rest[0] {
		case "claim", "renew":
			if err := b.Pilot(ctx, self, ttl); err != nil {
				die(err.Error())
			}
		case "release":
			if err := b.ReleasePilot(ctx); err != nil {
				die(err.Error())
			}
		case "status":
			d, err := b.PilotDriver(ctx)
			if err != nil {
				die(err.Error())
			}
			if d == "" {
				fmt.Println("autonomous")
			} else {
				fmt.Println("piloted by " + d)
			}
		default:
			die("pilot: want claim|renew|release|status")
		}

	case "gate":
		if len(rest) < 1 {
			die("usage: gate <agent>")
		}
		m, err := b.OpenChallenges(ctx, rest[0])
		if err != nil {
			die(err.Error())
		}
		if len(m) == 0 {
			fmt.Printf("%s: ungated\n", rest[0])
			return
		}
		for ref, meta := range m {
			fmt.Printf("%s\t%s\n", ref, meta)
		}
		os.Exit(1) // gated → non-zero so a script/agent can block on it

	case "watch":
		if len(rest) < 1 {
			die("usage: watch <agent>")
		}
		agent := rest[0]
		consumer, _ := os.Hostname()
		if consumer == "" {
			consumer = self
		}
		wctx, cancel := context.WithTimeout(ctx, heartbeat)
		defer cancel()
		werr := b.WatchCmd(wctx, agent, consumer, func(e bus.Event) bool {
			ref := ""
			if e.Ref != "" {
				ref = " ref=" + e.Ref
			}
			fmt.Printf("[%s %s->%s%s] %s\n", e.Type, e.From, e.Target, ref, e.Message)
			return true
		})
		if werr == nil {
			return // delivered one cmd
		}
		if errors.Is(werr, context.DeadlineExceeded) {
			fmt.Println("__HEARTBEAT__") // idle window elapsed; re-arm the watcher
			return
		}
		die(werr.Error())

	case "listen":
		kinds := rest
		if len(kinds) == 0 {
			kinds = []string{"status", "report", "notify", "cmd"}
		}
		fmt.Fprintf(os.Stderr, "Tailing %v on project %q (Ctrl+C to stop)...\n", kinds, project)
		err := b.Tail(ctx, "$", kinds, func(e bus.Event) {
			who := e.Agent
			if who == "" {
				who = e.From
			}
			fmt.Printf("[%s %s] %s\n", e.Kind, who, e.Message)
		})
		if err != nil {
			die(err.Error())
		}

	default:
		die(fmt.Sprintf("unknown command %q", cmd))
	}
}
```

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean. (`cmd/busmon` and `bus/bus.go` are untouched and still build on the old API.)

- [ ] **Step 3: Run existing tests**

Run: `go test ./... -count=1`
Expected: `bus` PASS, `cmd/agentbus` PASS (the Task-1 helper tests), `cmd/busmon` PASS.

- [ ] **Step 4: Deterministic smoke test against the broker**

```bash
go build -o /tmp/agentbus ./cmd/agentbus
/tmp/agentbus --project smoke status dev working "hello from cli"
/tmp/agentbus --project smoke challenge dev --ref C1 "justify the refactor"
docker compose exec -T redis redis-cli -a 'AgentBus2025!' --no-auth-warning XRANGE smoke:status - + 
docker compose exec -T redis redis-cli -a 'AgentBus2025!' --no-auth-warning XRANGE smoke:cmd - +
docker compose exec -T redis redis-cli -a 'AgentBus2025!' --no-auth-warning HGETALL smoke:gate:dev
/tmp/agentbus --project smoke gate dev; echo "gate exit=$?"
/tmp/agentbus --project smoke verdict --ref C1 dev approve "looks good"
/tmp/agentbus --project smoke gate dev; echo "gate exit=$?"
```

Expected:
- `smoke:status` XRANGE shows one entry with fields `agent dev state working message "hello from cli"`.
- `smoke:cmd` XRANGE shows one entry with `from hermes target dev type challenge ref C1 command "justify the refactor"`.
- `HGETALL smoke:gate:dev` shows `C1 -> "hermes|justify the refactor"`.
- first `gate dev` prints the C1 line and `gate exit=1`.
- after `verdict`, second `gate dev` prints `dev: ungated` and `gate exit=0`.

- [ ] **Step 5: Also verify the project-required guard and watch heartbeat**

```bash
/tmp/agentbus status dev working x; echo "exit=$?"   # expect: error project required, exit=1
AGENT_BUS_PROJECT=smoke /tmp/agentbus pilot status   # expect: "autonomous" (no lease)
AGENT_BUS_PROJECT=smoke /tmp/agentbus pilot claim
AGENT_BUS_PROJECT=smoke /tmp/agentbus pilot status   # expect: "piloted by hermes"
AGENT_BUS_PROJECT=smoke /tmp/agentbus pilot release
```
Expected as annotated. (Skip the long `watch` heartbeat wait; trust the code path — it is exercised by the WatchCmd unit test in Phase 1.)

- [ ] **Step 6: Clean up the smoke project + commit**

```bash
docker compose exec -T redis redis-cli -a 'AgentBus2025!' --no-auth-warning DEL smoke:status smoke:cmd smoke:gate:dev smoke:pilot >/dev/null
rm -f /tmp/agentbus
git add cmd/agentbus/main.go
git commit -m "feat(agentbus): migrate CLI to Streams (--project + cmd/challenge/reply/verdict/pilot/gate/watch/listen)"
```

---

## Notes
- `watch` output format `[type from->target ref=R] message` is the line the inbound bridge prints; Phase 4 finalizes the `bus_watch.sh` wrapper contract around it.
- `verdict` resolves the gate **before** publishing the verdict cmd, so a stale/typo ref fails loudly (leveraging Phase 1's loud `ResolveChallenge`).
- Self identity defaults to `hermes`; a worker session overrides it with `AGENT_BUS_AGENT=<role>`.
