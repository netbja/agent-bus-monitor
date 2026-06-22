# Master herdr-control skill — Design (Slice 3b)

**Date:** 2026-06-22
**Status:** Approved for implementation
**Scope:** `cmd/agentbus` (one tiny command) + `cmd/busmon` (`@agent` directed input) + a new
in-repo **Claude Code skill**. Branches off `main`. Builds on Slice 3a's pane mapping.

Second sub-slice of Slice 3:

| Sub-slice | Theme | Status |
|-----------|-------|--------|
| 3a | Pane-mapping foundation | MERGED (PR #10) |
| **3b (this doc)** | Master herdr-control skill (resync + unblock) | designing |
| 3c | Usage broadcast | later |

The master is the **pilot-lease driver** (Slice 2). It runs inside herdr and orchestrates the other
agents via the `agentbus` + `herdr` CLIs. The human's answer to a blocked agent travels over the
**bus** (the existing two-way path) — the Signal path stays one-way (alert only); building inbound
Signal is out of scope (it would mean new webhook infra on the hermes gateway).

## Goals

1. The master can **resync** an agent — inject text into its herdr pane input.
2. The master can **unblock** an agent stuck on an on-screen question — detect it, surface the
   question to a human (one-way Signal + bus), receive the human's answer over the bus, and inject it.
3. The human can answer (and direct any agent) from **busmon's input** with `@agent` autocomplete.

## Non-goals

- Inbound/two-way Signal (separate external project). Usage broadcast (3c). Auto-unblocking the
  4-eyes `gate` (a deliberate safety block — only herdr's on-screen `blocked` is in scope).

---

## Component A — `agentbus pane <agent>`

A herdr-free lookup the skill uses as the agent→pane bridge.

- New CLI command: prints the agent's registered `HERDR_PANE_ID` (from the 3a `{project}:agents`
  hash) to stdout; exits non-zero with a stderr error if the agent is unknown or has no pane.
- Backed by a pure, testable helper:
  ```go
  // agentPane returns the agent's pane and whether it is known with a non-empty pane.
  func agentPane(m map[string]bus.AgentSnapshot, agent string) (string, bool)
  ```
  The `pane` case calls `b.Agents(ctx)` then `agentPane`; prints or `die`s.

**Files:** `cmd/agentbus/agents.go` (`agentPane`), `cmd/agentbus/main.go` (`pane` case + usage),
`cmd/agentbus/agents_test.go` (`agentPane` test).

---

## Component B — busmon `@agent` directed input

busmon's INPUT becomes an agent-direction console: typing `@` autocompletes agent names; an
`@<agent> <text>` line is published as a **cmd directed to that agent**; a plain line still
broadcasts to `notify` (unchanged).

Two pure, testable helpers (in `render.go`, with the other pure helpers):

```go
// agentCompletions returns "@<name> " entries for the @-prefixed first token of currentText
// (no space yet). Returns nil once a space (message body) has started, or when not @-prefixed.
func agentCompletions(currentText string, names []string) []string

// parseDirected splits an "@<agent> <body>" line. directed is true only when the line starts
// with @, the agent token is a valid name (bus.ValidName), and a non-empty body follows.
// Otherwise returns ("", currentText, false) so the caller broadcasts the whole line to notify.
func parseDirected(text string) (target, body string, directed bool)
```

Wiring in `main.go`:
- `input.SetAutocompleteFunc(func(cur string) []string { … })` — snapshot the agent names under
  `mu`, then `return agentCompletions(cur, names)`. tview shows the dropdown; selecting an entry
  fills `@claude1 ` and the user types the body.
- The DoneFunc replaces the single `b.Notify(...)` call: `if t, body, ok := parseDirected(text); ok
  { b.Cmd(ctx, self, t, bus.CmdDirective, "", body) } else { b.Notify(ctx, self, text) }`.

A directed cmd reaches a **non-blocked** agent through its own `subscribe` (a normal directive);
a **blocked** agent's answer is injected by the master (Component C). `self` is busmon's existing
sender identity (`AGENT_BUS_AGENT`, default `hermes`).

**Files:** `cmd/busmon/render.go` (the two helpers), `cmd/busmon/main.go` (autocomplete + DoneFunc),
`cmd/busmon/render_test.go` (helper tests).

---

## Component C — the master skill (`skills/agent-bus-master/SKILL.md`)

A Claude Code skill (loadable frontmatter `name`/`description`) the master runs. Prerequisites:
`HERDR_ENV=1`, `AGENT_BUS_PROJECT`/`AGENT_BUS_AGENT` exported, the master holds the pilot lease.

**resync** — inject text into an agent's pane:
1. `pane=$(agentbus pane <agent>)` (the stored id).
2. Reconcile against live ids: confirm `pane` appears in `herdr pane list` (json); if it drifted
   (herdr's "ids aren't durable" caveat), re-resolve by matching the agent/cwd in `pane list`.
3. `herdr pane send-text "$pane" "<text>"` then `herdr pane send-keys "$pane" Enter`.

**unblock** — answer an agent stuck on a question:
1. **Detect:** `herdr pane list --json` → panes with `agent_status == "blocked"`; map each back to a
   bus agent by inverting `agentbus agents --json` (agent→pane).
2. **Read the question:** `herdr pane read "$pane" --source detection`.
3. **Alert (one-way):** the existing `hermes-notify` Signal alert **and** `agentbus notify` with the
   agent + question and the reply hint (`busmon: @<agent> <answer>`, or `agentbus cmd <agent> <answer>`).
4. **Answer over the bus:** the human replies in **busmon** (`@<agent> <answer>`, autocomplete on
   `@`) or via `agentbus cmd <agent> <answer>` — either way a **cmd to `<agent>`**.
5. **Inject:** the master watches the cmd stream read-only (`agentbus listen cmd`); for a cmd whose
   target is an agent it currently knows is **blocked**, it runs the resync injection (above),
   delivering the answer into the pane and unblocking it.

**Known edge (documented in the skill):** a cmd to a currently-blocked agent also lands in that
agent's `subscribe` consumer group, so on its next re-arm it could re-receive the answer as a
directive — mitigated by the agent persisting its `--since` cursor (Slice 1). Not a blocker.

---

## Component D — packaging + repo touchpoints

- The skill is committed at `skills/agent-bus-master/SKILL.md` (canonical source). A short note in
  the repo `README.md` points to it and says to install/symlink it where the master's Claude Code
  loads skills (mirroring how herdr ships its `SKILL.md`).
- `docs/AGENT-BUS-GUIDE.md` gets a one-line mention of `agentbus pane` and the busmon `@agent` input.

---

## Testing

- **`agentbus pane`** — `agentPane(m, agent)` returns the pane for a known agent with a pane, and
  `("", false)` for unknown or empty-pane agents.
- **busmon** — `parseDirected` (directed vs plain vs invalid-name fallback) and `agentCompletions`
  (`@`-prefix completions, none after a space, none without `@`).
- **The skill** is prose — validated by checking every `herdr`/`agentbus`/`hermes-notify` command in
  it against the real CLIs (flag/subcommand existence), not unit tests. The plan states 3b ships
  "two tested commands + a verified playbook."
- Gate (each task + final): `go build ./... && go vet ./... && go test ./... -count=1`.

---

## Summary of changed surfaces

| File | Change |
|------|--------|
| `cmd/agentbus/agents.go` | `agentPane` helper |
| `cmd/agentbus/main.go` | `pane` command + usage string |
| `cmd/agentbus/agents_test.go` | `agentPane` test |
| `cmd/busmon/render.go` | `agentCompletions`, `parseDirected` |
| `cmd/busmon/main.go` | input autocomplete + directed DoneFunc routing |
| `cmd/busmon/render_test.go` | helper tests |
| `skills/agent-bus-master/SKILL.md` | **new** — the master playbook |
| `README.md`, `docs/AGENT-BUS-GUIDE.md` | point to the skill; mention `pane` + `@agent` |

## Things to get right

- **English UI copy** (project preference) for any busmon/CLI text and the skill prose.
- **`@agent` routes to `cmd`** only when the token is a valid agent name; otherwise the whole line
  broadcasts to `notify` (no surprise drops).
- **The master injects only into agents it knows are blocked** — never into an actively-working
  agent (that would be disruptive); non-blocked agents receive directed cmds via their own subscribe.
- **herdr id reconciliation** lives in the skill (stored pane vs live `pane list`), per herdr's
  non-durable-id warning.
