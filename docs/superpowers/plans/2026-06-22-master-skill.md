# Master herdr-control skill (Slice 3b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the master (pilot-lease driver) a way to drive peer agents' herdr panes — an `agentbus pane` lookup, a busmon `@agent` directed input with autocomplete, and a master skill that resyncs/unblocks agents (the human's answer arrives over the bus).

**Architecture:** Two small Go additions (`agentbus pane`, busmon `@agent` routing) plus a prose Claude Code skill that orchestrates the `agentbus` + `herdr` CLIs. `agentbus`/`busmon` stay herdr-agnostic; herdr lives only in the skill.

**Tech Stack:** Go 1.26, `github.com/rivo/tview` (`InputField.SetAutocompleteFunc`), the `herdr` 0.7.0 CLI (in the skill only).

## Global Constraints

- `cmd/agentbus` + `cmd/busmon` + `skills/` + docs. `bus/` and `cmd/busmon`/`cmd/agentbus` stay **herdr-agnostic** (no shelling to `herdr` from Go — herdr lives only in `skills/agent-bus-master/SKILL.md`).
- **English UI/CLI/skill copy** (project preference) — no French.
- busmon `@<agent> <text>` routes to a **cmd directed to that agent** ONLY when the token is a valid agent name (`bus.ValidName`) and a non-empty body follows; otherwise the whole line broadcasts to `notify` (no surprise drops).
- The master skill injects an answer **only into agents it has confirmed are currently `blocked`** (from `herdr pane list`) — never into an actively-working agent.
- herdr pane-id reconciliation (stored id vs live `herdr pane list`) lives in the **skill** (herdr's "ids aren't durable" warning).
- Verification gate (each task + final): `go build ./... && go vet ./... && go test ./... -count=1`. Redis-touching tests need the broker (`docker compose up -d`) or they skip.
- Every commit message ends with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Work happens on branch `feat/master-skill` (already created off `main`; the spec is committed there).

---

## Task 1: `agentbus pane <agent>`

A herdr-free lookup: print the agent's registered `HERDR_PANE_ID` from the 3a `{project}:agents` hash; non-zero exit if unknown or no pane.

**Files:**
- Modify: `cmd/agentbus/agents.go` (`agentPane` helper)
- Modify: `cmd/agentbus/main.go` (`pane` case + usage string + doc comment)
- Modify: `cmd/agentbus/agents_test.go` (`TestAgentPane`)

**Interfaces:**
- Consumes: `bus.AgentSnapshot.Pane`, `bus.Bus.Agents` (Slice 3a).
- Produces: `func agentPane(m map[string]bus.AgentSnapshot, agent string) (string, bool)`.

- [ ] **Step 1: Write the failing test**

Add to `cmd/agentbus/agents_test.go`:

```go
func TestAgentPane(t *testing.T) {
	m := map[string]bus.AgentSnapshot{
		"claude1": {State: "working", Pane: "w1:p1"},
		"claude2": {State: "idle"}, // no pane
	}
	if p, ok := agentPane(m, "claude1"); !ok || p != "w1:p1" {
		t.Fatalf("agentPane(claude1) = (%q,%v), want (w1:p1,true)", p, ok)
	}
	if _, ok := agentPane(m, "claude2"); ok {
		t.Fatal("agentPane(claude2) should be false (no pane)")
	}
	if _, ok := agentPane(m, "ghost"); ok {
		t.Fatal("agentPane(ghost) should be false (unknown)")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/agentbus/ -run TestAgentPane -v`
Expected: FAIL to compile — `agentPane` undefined.

- [ ] **Step 3: Implement `agentPane` in `cmd/agentbus/agents.go`**

Add (e.g. just below `agentsTable`):

```go
// agentPane returns the agent's registered herdr pane and whether the agent is
// known with a non-empty pane.
func agentPane(m map[string]bus.AgentSnapshot, agent string) (string, bool) {
	s, ok := m[agent]
	if !ok || s.Pane == "" {
		return "", false
	}
	return s.Pane, true
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./cmd/agentbus/ -run TestAgentPane -v`
Expected: PASS.

- [ ] **Step 5: Wire the `pane` command in `cmd/agentbus/main.go`**

Add this `case` to the command switch (e.g. right after the `agents` case):

```go
	case "pane":
		if len(rest) < 1 {
			die("usage: pane <agent>")
		}
		m, err := b.Agents(ctx)
		if err != nil {
			die(err.Error())
		}
		p, ok := agentPane(m, rest[0])
		if !ok {
			die(fmt.Sprintf("no herdr pane registered for %q", rest[0]))
		}
		fmt.Println(p)
```

Update the top-level usage `die` string to include `pane` (insert after `agents`):

```go
		die("usage: agentbus --project <p> <status|report|notify|cmd|challenge|reply|verdict|pilot|gate|agents|pane|subscribe|watch|listen> ...")
```

Add a line to the package doc comment near the other command examples (above the `subscribe` line):

```go
//	agentbus --project P pane      <agent>       # print the agent's herdr pane (HERDR_PANE_ID); non-zero if none
```

- [ ] **Step 6: Build + vet + full package test + commit**

Run: `go build ./... && go vet ./... && go test ./cmd/agentbus/ -count=1`
Expected: clean; PASS.

```bash
git add cmd/agentbus/agents.go cmd/agentbus/main.go cmd/agentbus/agents_test.go
git commit -m "feat(agentbus): add `pane <agent>` to resolve the registered herdr pane"
```

---

## Task 2: busmon `@agent` directed input

Agent autocomplete on `@`; an `@<agent> <text>` line publishes a cmd directed to that agent, a plain line still broadcasts to notify. Also fixes the stray French `copié` → `copied` in the same file.

**Files:**
- Modify: `cmd/busmon/render.go` (`agentCompletions`, `parseDirected`; add `sort` + `bus` imports)
- Modify: `cmd/busmon/main.go` (input autocomplete + directed DoneFunc; `copié`→`copied`)
- Modify: `cmd/busmon/render_test.go` (`TestParseDirected`, `TestAgentCompletions`)

**Interfaces:**
- Consumes: `bus.ValidName`, `bus.CmdDirective`, `bus.Bus.Cmd`/`Notify`.
- Produces: `func parseDirected(text string) (target, body string, directed bool)`; `func agentCompletions(currentText string, names []string) []string`.

- [ ] **Step 1: Write the failing tests**

Add to `cmd/busmon/render_test.go`:

```go
func TestParseDirected(t *testing.T) {
	if tgt, body, ok := parseDirected("@claude1 do the thing"); !ok || tgt != "claude1" || body != "do the thing" {
		t.Fatalf("parseDirected directed = (%q,%q,%v), want claude1/do the thing/true", tgt, body, ok)
	}
	if _, body, ok := parseDirected("hello world"); ok || body != "hello world" {
		t.Fatalf("parseDirected plain = (_,%q,%v), want (hello world,false)", body, ok)
	}
	if _, _, ok := parseDirected("@claude1"); ok {
		t.Fatal("parseDirected with no body should not be directed")
	}
	if _, _, ok := parseDirected("@Bad foo"); ok {
		t.Fatal("parseDirected with an invalid agent name should fall back (not directed)")
	}
}

func TestAgentCompletions(t *testing.T) {
	names := []string{"claude2", "claude1", "hermes"}
	got := agentCompletions("@cl", names)
	if len(got) != 2 || got[0] != "@claude1 " || got[1] != "@claude2 " {
		t.Fatalf("agentCompletions(@cl) = %v, want [@claude1 , @claude2 ]", got)
	}
	if agentCompletions("@claude1 do", names) != nil {
		t.Fatal("no completions once the body has started (space present)")
	}
	if agentCompletions("hello", names) != nil {
		t.Fatal("no completions when not @-prefixed")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/busmon/ -run 'TestParseDirected|TestAgentCompletions' -v`
Expected: FAIL to compile — `parseDirected`/`agentCompletions` undefined.

- [ ] **Step 3: Implement the helpers in `cmd/busmon/render.go`**

Add `sort` and the `bus` package to the import block:

```go
import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
)
```

Add the two helpers:

```go
// parseDirected splits an "@<agent> <body>" line. directed is true only when the
// line starts with '@', the agent token is a valid name, and a non-empty body
// follows. Otherwise it returns ("", text, false) so the caller broadcasts the
// whole line to notify.
func parseDirected(text string) (target, body string, directed bool) {
	if !strings.HasPrefix(text, "@") {
		return "", text, false
	}
	rest := text[1:]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return "", text, false // "@agent" with no body
	}
	agent := rest[:sp]
	body = strings.TrimSpace(rest[sp+1:])
	if !bus.ValidName(agent) || body == "" {
		return "", text, false
	}
	return agent, body, true
}

// agentCompletions returns "@<name> " entries for the @-prefixed first token of
// currentText (no space yet) whose name matches the partial, sorted. Returns nil
// once a space (the body) has started, or when currentText is not @-prefixed.
func agentCompletions(currentText string, names []string) []string {
	if !strings.HasPrefix(currentText, "@") || strings.ContainsRune(currentText, ' ') {
		return nil
	}
	prefix := currentText[1:]
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, "@"+n+" ")
		}
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/busmon/ -run 'TestParseDirected|TestAgentCompletions' -v`
Expected: PASS.

- [ ] **Step 5: Wire the input in `cmd/busmon/main.go`**

(a) Just before `input.SetDoneFunc(...)`, add the autocomplete:

```go
	input.SetAutocompleteFunc(func(currentText string) []string {
		mu.Lock()
		names := make([]string, 0, len(agents))
		for n := range agents {
			names = append(names, n)
		}
		mu.Unlock()
		return agentCompletions(currentText, names)
	})
```

(b) Replace the `SetDoneFunc` body so a directed line routes to `Cmd`:

```go
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			if text := strings.TrimSpace(input.GetText()); text != "" {
				if tgt, body, directed := parseDirected(text); directed {
					b.Cmd(ctx, self, tgt, bus.CmdDirective, "", body)
				} else {
					b.Notify(ctx, self, text)
				}
			}
			input.SetText("")
		}
	})
```

(c) Fix the stray French in `copySelect` (the only non-English busmon string left):

```go
		input.SetTitle(" INPUT  [green][✓ copied][-] ")
```

- [ ] **Step 6: Full gate + commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: build clean, vet clean, all tests PASS across `bus`, `cmd/agentbus`, `cmd/busmon`.

```bash
git add cmd/busmon/render.go cmd/busmon/main.go cmd/busmon/render_test.go
git commit -m "feat(busmon): @agent directed input with autocomplete (cmd routing)"
```

---

## Task 3: The master skill + doc touchpoints

A prose Claude Code skill the master runs, plus pointers in the repo docs. No Go logic — the gate stays green; verification is checking the skill's commands against the real CLIs.

**Files:**
- Create: `skills/agent-bus-master/SKILL.md`
- Modify: `README.md` (point to the skill; mention `pane` + busmon `@agent`)
- Modify: `docs/AGENT-BUS-GUIDE.md` (`agentbus pane` in the cheat sheet; busmon `@agent` note)

**Interfaces:** none (prose). Consumes `agentbus pane` (Task 1), the busmon `@agent` input (Task 2), and the `herdr` 0.7.0 CLI.

- [ ] **Step 1: Create `skills/agent-bus-master/SKILL.md`**

```markdown
---
name: agent-bus-master
description: "Run from the MASTER agent (the pilot-lease driver) inside herdr to coordinate peer agents over the Agent Bus: resync an agent by injecting text into its herdr pane, and unblock an agent stuck on an on-screen question (detect it, alert a human, then inject the human's answer). Use when you hold the pilot lease and need to drive other agents' panes."
---

# Agent Bus — Master Skill

You are the **master** (you hold the pilot lease), running inside herdr. This skill drives peer
agents' herdr panes over the Agent Bus.

## Check first
- `HERDR_ENV=1` — you must be inside a herdr pane (you control panes via the `herdr` CLI). If unset, stop.
- `AGENT_BUS_PROJECT` and `AGENT_BUS_AGENT` exported (see `docs/AGENT-BUS-GUIDE.md`).
- You hold the lease: `agentbus pilot status` prints `piloted by <you>`. If not, claim it
  (`agentbus pilot claim`) or stop — only the master drives panes.

## Agent → pane bridge
Each peer registers its pane (`HERDR_PANE_ID`) via its `agentbus status` heartbeat:
\`\`\`bash
agentbus agents --json          # {"claude1":{"state":...,"pane":"w1:p1"},...}
agentbus pane claude1           # just the pane id; non-zero exit if none
\`\`\`
herdr pane ids are NOT durable. Before acting, confirm the stored id is still live with
`herdr pane list`; if it's gone, re-resolve by matching the agent/cwd in that output.

## Resync — inject text into an agent's pane
\`\`\`bash
pane=$(agentbus pane claude1) || { echo "claude1 has no registered pane"; exit 1; }
herdr pane send-text "$pane" "<text / context to inject>"
herdr pane send-keys "$pane" Enter
\`\`\`

## Unblock — answer an agent stuck on a question
1. **Detect** blocked peers:
   \`\`\`bash
   herdr pane list --json | jq -r '.result.panes[] | select(.agent_status=="blocked") | .pane_id'
   \`\`\`
   Map each blocked pane back to its bus agent by inverting `agentbus agents --json`.
2. **Read the question:** `herdr pane read "$pane" --source detection`.
3. **Alert a human** (one-way Signal + the bus — they may be away):
   \`\`\`bash
   hermes-notify "claude1 is BLOCKED: <question>"
   agentbus notify "claude1 BLOCKED: <question> — reply in busmon: @claude1 <answer>"
   \`\`\`
4. **Receive the answer over the bus.** The human answers in busmon (`@claude1 <answer>`, type `@`
   for agent autocomplete) or `agentbus cmd claude1 <answer>` — either way a cmd to `claude1`.
   Watch directed cmds read-only: `agentbus listen cmd`.
5. **Inject** the answer (the Resync step) into the blocked pane to unblock it. **Only inject into
   agents you have confirmed are currently `blocked`** — never interrupt an actively-working agent.

### Known edge
A cmd to a currently-blocked agent also lands in that agent's `subscribe` consumer group, so on its
next re-arm it could re-receive the answer as a directive — mitigated by the agent persisting its
`--since` cursor (see the bus guide). Don't re-inject an answer you've already delivered.
```

(Note: in the file, the fenced `bash` blocks use real triple backticks — the `\`\`\`` above is only escaped for this plan.)

- [ ] **Step 2: Add a pointer in `README.md`**

Add a short subsection (e.g. after the busmon panes section) — adjust the heading level to match the file:

```markdown
## Master skill

`skills/agent-bus-master/SKILL.md` is a Claude Code skill the **master** (the pilot-lease driver,
running inside herdr) uses to drive peer agents' panes: **resync** (inject text into an agent's
herdr pane) and **unblock** (detect a herdr-`blocked` agent, alert a human one-way via Signal + the
bus, then inject the human's answer — typed in busmon as `@<agent> <answer>` or via
`agentbus cmd`). Install/symlink it where the master's Claude Code loads skills. The bridge is
`agentbus pane <agent>` (the agent's `HERDR_PANE_ID`, from `agentbus status`).
```

- [ ] **Step 3: Update `docs/AGENT-BUS-GUIDE.md`**

In the §2 cheat sheet PEERS block (next to `agentbus agents`), add:

```bash
agentbus pane <agent>                                   # print the agent's herdr pane (HERDR_PANE_ID); non-zero if none
```

In the busmon section, add a line about the directed input:

```markdown
- busmon **INPUT**: type `@` for agent autocomplete; an `@<agent> <text>` line sends a **directed
  cmd** to that agent (a plain line broadcasts to `notify`).
```

- [ ] **Step 4: Verify the skill's commands against the real CLIs**

Run (confirm each subcommand/flag the skill uses actually exists):

```bash
go build -o /tmp/agentbus ./cmd/agentbus && /tmp/agentbus --project p pane 2>&1 | head -1   # the new command parses
herdr pane --help 2>&1 | grep -E 'list|read|send-text|send-keys'                            # herdr subcommands exist
herdr pane read --help 2>&1 | grep -E 'source'                                              # --source flag exists
command -v jq >/dev/null && echo "jq present" || echo "jq MISSING — note in report"
command -v hermes-notify >/dev/null && echo "hermes-notify present" || echo "hermes-notify not on PATH here (external Signal alert) — note in report"
```
Expected: `herdr pane` shows `list/read/send-text/send-keys`; `--source` exists; note jq/hermes-notify availability. (`hermes-notify` is the external VDR Signal path; absence here is expected — just note it.)

- [ ] **Step 5: Full gate (docs/skill only — unaffected) + commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: clean; all tests PASS.

```bash
git add skills/agent-bus-master/SKILL.md README.md docs/AGENT-BUS-GUIDE.md
git commit -m "docs: add the agent-bus-master skill (resync + unblock via herdr)"
```

---

## Self-Review

**Spec coverage:**
- Component A (`agentbus pane` + `agentPane` helper) → Task 1. ✓
- Component B (busmon `@agent` autocomplete + cmd routing; `agentCompletions`/`parseDirected`) → Task 2. ✓
- Component C (the master skill: resync + unblock workflows, the documented edge) → Task 3 (SKILL.md). ✓
- Component D (skill packaging in `skills/`, README + guide touchpoints) → Task 3. ✓
- Testing (`agentPane`, `parseDirected`, `agentCompletions` unit-tested; skill commands verified against CLIs) → Tasks 1–3. ✓
- English-copy constraint, incl. fixing the stray `copié` → Task 2. ✓

**Type consistency:** `agentPane(m, agent) (string, bool)` defined in Task 1 Step 3, used in the Task 1 `pane` case and `TestAgentPane`. `parseDirected(text) (target, body string, directed bool)` and `agentCompletions(currentText, names) []string` defined in Task 2 Step 3, used in Task 2's DoneFunc/autocomplete (Step 5) and tests (Step 1). `b.Cmd(ctx, self, tgt, bus.CmdDirective, "", body)` matches the existing `Bus.Cmd` signature.

**Build-green ordering:** Tasks 1 and 2 are additive (new command, new input behavior) — each compiles and tests green. Task 3 is docs/skill only.

**Placeholder scan:** no TBD/TODO; every code step shows complete code; the SKILL.md content is complete (the `\`\`\`` escaping note is explicit). The skill is prose, validated by Step 4's CLI checks, not unit tests — stated in Global Constraints and the spec.
