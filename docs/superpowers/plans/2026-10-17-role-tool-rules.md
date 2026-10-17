# Per-role tool rules and pop approval (v0.17) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** master pops ordinary roles without Claude Code's classifier, and pops a role marked `approval = "person"` (deploy) only after a person approved it on the bus.

**Architecture:** `rolecfg` reads three new role keys (`pops`, `approval`, `tool_rules`), derives per-role spawn rules and passes them to Claude Code with one `--settings` JSON flag. `bus/approval.go` adds two Lua scripts on `{p}:asks` (create an approval ask indexed by `approval:<agent>:<role>`, spend it once); the existing ask scripts learn the index. `agentbus approval ask|use` exposes them, `agentbus answer` refuses agents, `scripts/agent-spawn` gates, `agentbus swap` spends before it closes.

**Tech Stack:** Go 1.26, go-redis v9 + Lua, BurntSushi TOML, bash test suite (`tests/`).

**Scope (rev 5 of the spec):** Tasks 3, 4 and 5 (bus approval, `agentbus approval`, the `answer` refusal, the `agent-spawn` gate, the swap spend) were built, reviewed and then removed at Bernard's request; Tasks 0, 1, 2 and 6 ship.

**Spec:** `docs/superpowers/specs/2026-10-17-role-tool-rules-design.md` (rev 4, approved). Read it first; section numbers below refer to it.

## Global Constraints

- Branch: work continues on `spec/v017` in `.worktrees/v017`; rename to `feat/v017` before the PR.
- Tests never touch the production broker: Redis cases use `AGENTBUS_TEST_REDIS_URL` (isolated instance, `docs/testing-redis.md`) and skip without it.
- Every write to `{p}:asks` is one Lua script; persisted times come from Redis `TIME`.
- Approval TTL 30 min, env `AGENT_BUS_APPROVAL_TTL` (Go duration); swap hand-over window 3 min.
- Question id `spawn-<role>`; options `Approve` (recommended) and `Refuse`; title `Pop <role>?`; header `Pop <role>`; question `Pop <role> for <project>?`.
- Spend markers: `<id>|u|spawn-<role>` = `spawn:<ms>` or `swap:<ms>`; hand-over marker `<id>|s|spawn-<role>`.
- Exit codes of `approval use`: 0 spent/accepted, 2 usage, 3 none, 75 bus error. `answer` with `AGENT_BUS_AGENT` set: exit 2.
- A launch with no rules produces a command line byte-identical to today's.
- All user-facing text in English.

## Review Focus

1. **An approval answered by a person whose name is also an agent's** (e.g. `--as master`): refused by `use`. Test in Task 3.
2. **The approval ask replaced, then the old id answered from a stale busmon panel**: the replacement carries nothing (`|c` empty), so the answer is refused (`ASK_REPLACED`) and the person answers the new ask; `use` reads only the index. Test in Task 3.
3. **`agent-spawn deploy` with a quoted role or by path in master's session**: no allow matches; covered by the live check (Task 0), not by Go tests.
4. **A project whose `{p}:asks` holds an index pointing at a swept ask**: `use` returns exit 3, never errors. Test in Task 3.
5. **schema 2 manifest whose master profile is Codex**: launch prints the note, starts, and the bus gate still applies. Test in Task 2.

---

### Task 0: Verify Claude Code's rule precedence live

**Files:**
- Modify: `docs/superpowers/specs/2026-10-17-role-tool-rules-design.md` (§9 step 1: record results)

- [ ] **Step 1:** In a scratch directory, create a stub `agent-spawn` on `PATH` that only echoes its arguments, and a stub `agentbus`.
- [ ] **Step 2:** Start `claude --permission-mode acceptEdits --settings '{"permissions":{"allow":["Bash(agent-spawn architect *)"],"ask":["Bash(agent-spawn deploy *)"]}}'` and ask it to run, one at a time: `agent-spawn architect x`, `agent-spawn deploy x`, `agent-spawn "deploy" x`. Expected: runs, prompts, prompts.
- [ ] **Step 3:** Repeat after Shift+Tab to auto mode. Expected: runs, prompts, classifier or prompt (never runs unasked).
- [ ] **Step 4:** Add a user-scope deny `Bash(agent-spawn architect *)` via a temporary `--settings` file layered with the user settings; expected: denied. Remove it.
- [ ] **Step 5:** Write the observed results into §9 step 1 of the spec. If any expectation fails, stop and bring it to Bernard before Task 1.
- [ ] **Step 6:** Commit: `docs(spec): v0.17 live check of Claude Code rule precedence`.

### Task 1: Manifest keys, validation and derived rules

**Files:**
- Modify: `rolecfg/config.go` (role key checks in `Load`/`resolve`; `Profile`)
- Create: `rolecfg/toolrules.go`
- Test: `rolecfg/toolrules_test.go`

**Interfaces:**
- Produces:
  - `type ToolRules struct { Allow, Ask, Deny []string }` with `func (r ToolRules) Empty() bool`
  - `Profile` gains `Pops bool`, `Approval string` (`""` or `"person"`), `ToolRules ToolRules` (declared rules only), JSON tags `pops,omitempty`, `approval,omitempty`, `tool_rules,omitempty`.
  - `func (m *Manifest) LaunchRules(role string) (ToolRules, error)`: declared rules plus, when `Pops`, derived spawn rules (§4.3) for every other role in `m.roleOrder`: `Bash(agent-spawn <r> *)` in Allow, or in Ask when that role's `approval = "person"`. Derived rules first, declared after; order = manifest role order.
  - `func (m *Manifest) Approval(role string) string`

- [ ] **Step 1: Write the failing tests** in `rolecfg/toolrules_test.go`, using manifests written to `t.TempDir()` like `config_test.go` does:
  - `TestToolRulesAcceptedInBothSchemas`: schema 1 and schema 2 manifests with `pops = true`, `approval = "person"`, `[roles.x.tool_rules] allow = ["Read"]` load; `Resolve` returns the fields.
  - `TestToolRulesRejections`: each case returns `Load` error containing the message:
    - `pops = "yes"` → `pops must be a boolean`
    - `approval = "agent"` → `approval must be "person"`
    - `tool_rules.extra` → `unknown`
    - allow `Bash` and `Bash( * )` → `tool_rules.allow: Bash would allow every shell command` (rule text in the message)
    - allow `Bash(agent-spawn coder *)` → `tool_rules.allow: agent-spawn rules are derived from the manifest`
    - a rule of 201 bytes, a rule with `\n`, a rule `9bad`, 21 rules → `invalid rule`
    - same rule in allow and ask → `in two lists`
    - ask on a `bypassPermissions` role → `tool_rules.ask has no effect with bypassPermissions`
  - `TestLaunchRulesDerivesSpawnRules`: manifest master(pops) + coder + deploy(approval) + architect → Allow `["Bash(agent-spawn coder *)","Bash(agent-spawn architect *)"]` (manifest order, master excluded), Ask `["Bash(agent-spawn deploy *)"]`; a role without `pops` → empty.
  - `TestShippedManifestRules`: `Load("../roles.toml")` (after Task 2 step edits it; write the test now, it fails until then): master Ask is exactly `["Bash(agent-spawn deploy *)"]`, Allow has one rule per other role and none for deploy.

- [ ] **Step 2:** `go test ./rolecfg -run 'ToolRules|LaunchRules|ShippedManifest' -count=1` → FAIL (undefined).

- [ ] **Step 3: Implement.** Rule shape: `^([A-Za-z][A-Za-z0-9_]*|mcp__[A-Za-z0-9_-]+)(\(.*\))?$`, ≤200 bytes, no control characters, ≤20 per list. Validate in `Load` for every role (both schemas), next to the existing tier/skills checks; add `pops`, `approval`, `tool_rules` to the schema 2 role `checkKeys` set. The bypass check uses the role's resolved Claude `permission`.

- [ ] **Step 4:** `go test ./rolecfg -count=1` → PASS except `TestShippedManifestRules`.

- [ ] **Step 5:** Commit `feat(rolecfg): pops, approval and tool_rules role keys; derived spawn rules (v0.17)`.

### Task 2: Launch flag, announcement, shipped manifest

**Files:**
- Modify: `rolecfg/launch.go` (`build`, `preflight`, `elevated`, `LaunchProfile`)
- Modify: `roles.toml` (master `pops = true`, deploy `approval = "person"`)
- Test: `rolecfg/launch_test.go`, `tests/roles_scripts_test.sh`

**Interfaces:**
- Consumes: `Manifest.LaunchRules` (Task 1).
- Produces: `func settingsJSON(r ToolRules) (string, error)` → `{"permissions":{"allow":[…],"ask":[…],"deny":[…]}}`, empty lists omitted, key order allow, ask, deny, via `pyJSON`/`ordered`. `build` gains a `rules ToolRules` parameter.

- [ ] **Step 1: Write the failing tests** in `launch_test.go`:
  - `TestLaunchSettingsFlag`: Claude profile with rules → argv contains `--settings` followed by the exact JSON, right after `--permission-mode <mode>`; without rules → argv equal to the current expectation in `TestLaunchCommandLines` (unchanged).
  - `TestPreflightNeedsSettingsOnlyWithRules`: fake `claude --help` lacking `--settings` → error only when rules exist.
  - `TestElevatedAnnouncesAllow`: `elevated` output contains `allow=2 rules` for two allow rules.
  - `TestRulesNotAppliedNote`: Codex profile on a `pops` role → stderr contains `tool_rules not applied for codex`, launch proceeds, argv has no `--settings`.
  - Dry run shows the flag (extend `TestLaunchDryRun`).
- [ ] **Step 2:** `go test ./rolecfg -run 'Settings|Preflight|Elevated|NotApplied|DryRun' -count=1` → FAIL.
- [ ] **Step 3: Implement.** `LaunchProfile` computes `m.LaunchRules(role)` once and passes it to `preflight`, `build`, `elevated`. `elevated` takes the rules as a second parameter.
- [ ] **Step 4:** Edit `roles.toml` (comment line above each key, house style). Extend `tests/roles_scripts_test.sh`: the master `agent-launch` dry run contains `--settings` and `agent-spawn deploy`.
- [ ] **Step 5:** `go test ./rolecfg -count=1 && tests/run.sh` → PASS (including `TestShippedManifestRules`).
- [ ] **Step 6:** Commit `feat(rolecfg): pass role tool rules to Claude Code with --settings (v0.17)`.

### Task 3: Approval scripts on `{p}:asks`

**Files:**
- Create: `bus/approval.go`
- Modify: `bus/asks.go` (`sweepLua`, `askCreate`, `askWithdraw`, `askWithdrawQuestion`, `Asks`)
- Test: `bus/approval_test.go` (uses `dialTest`, `cleanAsks` from `asks_test.go`)

**Interfaces:**
- Produces:
  - `const ApprovalAnswer = "Approve"`, `ApprovalRefuse = "Refuse"`, `ApprovalSwapWindow = 3 * time.Minute`, `DefaultApprovalTTL = 30 * time.Minute`
  - `func ApprovalQuestion(role string) string` → `"spawn-" + role`
  - `func (b *Bus) ApprovalAsk(ctx context.Context, from, role, reason string) (string, error)`: builds the fixed `AskDef` (Global Constraints), validates it, runs `approvalCreate`: same fields and `ask` cmd entry as `askCreate`, does **not** read or write `open:<from>`, writes `approval:<from>:<role>` = new id; a previous open ask at that index becomes `replaced` (`|r`, `|c` empty, `|e`).
  - `type ApprovalMode string` with `SpendSpawn = "spawn"`, `SpendSwap = "swap"`, `CheckOnly = "check"`, `BySwap = "by-swap"`
  - `func (b *Bus) ApprovalUse(ctx context.Context, from, role string, mode ApprovalMode, swapID string, ttl time.Duration) (string, error)`; `var ErrNoApproval = errors.New("no usable approval")`, returned wrapped with the reason (`not answered`, `refused`, `withdrawn`, `expired`, `answered by an agent`, `already used`, `not a swap hand-over`).
  - Lua helper in `sweepLua`: `approvalRole(key, from, id)` → role when `id|q` is `spawn-<role>` and `HGET approval:<from>:<role> == id`, else nil.
- Script `approvalUse`: KEYS asks, agents. Reads `approval:<from>:<role>` (absent → none); for modes spawn/swap/check: answer of `spawn-<role>` exists, not withdrawn, `choice == "Approve"`, `now - at <= ttl`, `HEXISTS agents by == 0`, `|u|` absent; spawn/swap then `HSET |u| <mode>:<now>`. by-swap: id == index value, `|u|` starts `swap:` and is ≤ 3 min old, `|s|` absent, then `HSET |s| <now>`.

- [ ] **Step 1: Write the failing tests** (`bus/approval_test.go`):
  - `TestApprovalSpendsOnce`: ask, `Answer(id, "spawn-deploy", "bernard", "Approve", "")`, use spawn → id; second use → `ErrNoApproval` "already used"; check before spend does not spend.
  - `TestApprovalRefusals`: unanswered; `Refuse`; withdrawn question; answered by `master` when `{p}:agents` has `master`; answered more than ttl ago (pass `ttl = time.Millisecond`, sleep 5 ms); index absent; other asker (`coder` uses master's) → each `ErrNoApproval` with its reason.
  - `TestApprovalBySwap`: use swap → id; by-swap with that id → ok; again → refused; a `spawn:` spend → by-swap refused; by-swap with another id → refused.
  - `TestApprovalOutsideOpenAsk`: master has an open ordinary ask; `ApprovalAsk` succeeds; `HGET open:master` unchanged; a second `ApprovalAsk` for deploy marks the first `replaced`; answering the first id → `ErrAskRefused` (`ASK_REPLACED`, nothing carried); answering the new id, then `use` → the new id.
  - `TestApprovalIndexInAskScripts`: `Withdraw(master, approvalID)` succeeds and clears the index; `WithdrawQuestion` on a new approval ask succeeds and clears it; `Ask(..., replaces=approvalID)` succeeds, keeps `open:master` untouched and moves the index to the replacement when it carries `spawn-deploy`, else clears it.
  - `TestApprovalSweepAndListing`: an approval ask past its `|e` is swept and its index field removed; `Asks()` never returns an entry whose ID starts with `approval:`; `use` after the sweep → `ErrNoApproval`.
- [ ] **Step 2:** `AGENTBUS_TEST_REDIS_URL=… go test ./bus -run Approval -count=1` → FAIL.
- [ ] **Step 3: Implement** `bus/approval.go`; in `asks.go`: `askWithdraw`/`askWithdrawQuestion` accept `open:<agent> == id` **or** `approvalRole(...)`, and when the latter, `HDEL` the index instead of `open:`; `askCreate` with `replaces` accepts the same and moves/clears the index without touching `open:`; `sweep` removes `approval:*` fields whose value is a dead id; `Asks()` skips `approval:` fields. Update the hash-layout comment at the top of `asks.go`.
- [ ] **Step 4:** `AGENTBUS_TEST_REDIS_URL=… go test ./bus -count=1` → PASS (all existing ask tests unchanged).
- [ ] **Step 5:** Commit `feat(bus): approval asks indexed per role, spent once (v0.17)`.

### Task 4: CLI — `approval` and the agent refusal in `answer`

**Files:**
- Create: `cmd/agentbus/approval.go`
- Modify: `cmd/agentbus/main.go` (dispatch `approval`), `cmd/agentbus/askcmd.go` (`runAnswer`)
- Test: `cmd/agentbus/approval_test.go`, `cmd/agentbus/askcmd_test.go`

**Interfaces:**
- Consumes: Task 3 API, `rolecfg.Manifest.Approval` (Task 1), `manifestPath(env)` (existing, `swap.go`).
- Produces: `func runApproval(ctx context.Context, b *bus.Bus, d approvalDeps, self string, args []string, out, errw io.Writer) int` with `type approvalDeps struct { Manifest func() (*rolecfg.Manifest, error); Getenv func(string) string }`.
  - `approval ask <role> <reason...>` → prints `approval ask <id> for <role>: answer it in busmon`; exit 2 when the role is unknown or has no `approval = "person"`, 1 on a bus refusal.
  - `approval use <role> [--check | --for-swap | --by-swap <id>]` → prints the id; exit codes per Global Constraints. TTL from `AGENT_BUS_APPROVAL_TTL` (invalid → exit 2).
  - `self` is the caller identity already computed in `main()` (`AGENT_BUS_AGENT`, default `hermes`).

- [ ] **Step 1: Write the failing tests:**
  - `TestApprovalAskAndUse` (Redis): ask prints the id; use without answer → 3 with the hint `agentbus approval ask deploy <reason>`; after `b.Answer(..., "Approve")` → 0, second → 3.
  - `TestApprovalArgs` (no Redis): unknown role → 2; role without approval → 2; bad TTL → 2; two mode flags → 2.
  - `TestAnswerRefusedForAgents` in `askcmd_test.go`: `runAnswer` with getenv returning `master` for `AGENT_BUS_AGENT` → 2, stderr `answer: agents do not answer asks; a person answers in busmon or from a shell without AGENT_BUS_AGENT`, nothing written.
- [ ] **Step 2:** run them → FAIL.
- [ ] **Step 3: Implement**; add `approval` to the usage text in `main.go`.
- [ ] **Step 4:** `go build ./... && go vet ./... && AGENTBUS_TEST_REDIS_URL=… go test ./cmd/agentbus -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(agentbus): approval ask/use; agents cannot answer asks (v0.17)`.

### Task 5: The gate in `agent-spawn` and the spend in `swap`

**Files:**
- Modify: `scripts/agent-spawn`, `cmd/agentbus/swap.go` (`swapDeps.Spawn` env, check step)
- Test: `tests/roles_scripts_test.sh`, `cmd/agentbus/swap_test.go`

**Interfaces:**
- Consumes: `agentbus approval use` (Task 4), `role_field <role> approval` (`scripts/lib/roles.sh`).
- Produces: `swapDeps.Spawn` gains `approvalID string`; when non-empty the child env gets `AGENT_SPAWN_APPROVAL=<id>`.

- [ ] **Step 1: Write the failing tests:**
  - `tests/roles_scripts_test.sh` with a stub `agentbus` first on `PATH` (records its args, exit code from a variable): `AGENT_BUS_AGENT=master agent-spawn deploy demo` (dry run) → exit 1 when the stub returns 3, the stub saw `approval use deploy`; returns 0 → dry run printed; `AGENT_SPAWN_APPROVAL=X` → stub saw `approval use deploy --by-swap X`; without `AGENT_BUS_AGENT` → stub not called, spawn proceeds; `agent-spawn coder demo` with `AGENT_BUS_AGENT` → stub not called.
  - `TestSwapSpendsApprovalBeforeClose` (swap_test, Redis, existing fakes): role with `approval = "person"` and no approval → refused, `ClosePane` never called; with an answered approval → spent (`|u|` = `swap:`) before `ClosePane`, `Spawn` received the id.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** In `agent-spawn`, after `roles_validate` and before the herdr command: if `[[ -n "${AGENT_BUS_AGENT:-}" && "$(role_field "$role" approval)" == person ]]`, run `"$AGENTBUS_BIN" --project "$project" approval use "$role"` plus `--by-swap "$AGENT_SPAWN_APPROVAL"` when set; non-zero → `die` with its stderr. In `swap.go`, in the checks before `BeginSwap`, when `m.Approval(role) == "person"`: `b.ApprovalUse(ctx, self, role, bus.SpendSwap, "", ttl)`; a failure is `fail(...)` with the hint.
- [ ] **Step 4:** `tests/run.sh && AGENTBUS_TEST_REDIS_URL=… go test ./cmd/agentbus -run Swap -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(spawn,swap): a person's approval gates popping an approval role (v0.17)`.

### Task 6: Skill, docs, version

**Files:**
- Modify: `skills/agent-bus-master/SKILL.md`, `CHANGELOG.md`, `CLAUDE.md` (launcher rules paragraph + asks paragraph), `README.md` (roles section, if it lists role keys)

- [ ] **Step 1:** Skill: a short section "Popping a role that needs approval": `agentbus approval ask <role> <reason>`, wait for the reply, then `agent-spawn <role> "$AGENT_BUS_PROJECT"`; never answer your own ask; call `agent-spawn` by its bare name; a `Refuse` is final for that reason.
- [ ] **Step 2:** CHANGELOG `v0.17.0` entry; CLAUDE.md: `tool_rules`/`pops`/`approval` and the `--settings` flag in the launcher bullet; `approval:<agent>:<role>` and `|u|`/`|s|` in the asks bullet; "procedure gate, not a security boundary".
- [ ] **Step 3:** `go build ./... && go vet ./... && go run ./cmd/agentbus mem check && AGENTBUS_TEST_REDIS_URL=… go test ./... -count=1 && tests/run.sh` → all PASS.
- [ ] **Step 4:** Commit `docs: v0.17 role tool rules and pop approval`.
