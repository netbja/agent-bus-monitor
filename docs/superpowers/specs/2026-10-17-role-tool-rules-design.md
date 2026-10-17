# Per-role tool rules and a person's approval to pop a risky role (v0.17)

Status: rev 4, 2026-10-17, APPROVED by Bernard. rev 1 reviewed by Claude Code (abm-claude, verdict CHANGES,
`2026-10-17-role-tool-rules-review.md`); Bernard chose "bus approval + Claude prompt" (§2). rev 2
reviewed by the same (5 points: no text-matching deny, approval asks outside the one-open-ask rule,
O(1) lookup, swap spends before it closes, the manual path from master's tab); rev 3: a swap
hand-over used once and only from a swap spend, the ask scripts accept the approval index;
rev 4 APPROVED by abm-claude.

## 1. What happened

On 2026-10-17, ai-tradex-solana's master wanted to run `agent-spawn deploy ai-tradex-solana`, so
that a deploy agent would move heavy data off the VDR's `/tmp` onto `/mnt/aitradex`. Claude Code's
auto-mode classifier denied the call ("Production Deploy"). master ran nothing, did not work around
the denial, and asked Bernard to add a permission rule or type `! agent-spawn deploy …` himself.

- **master was in auto mode.** Launched with `--permission-mode acceptEdits`, then switched in the
  session (its pane reads `⏵⏵ auto mode on`). In auto mode an action no rule covers goes to the
  classifier. Masters do end up in auto mode, so the design must hold there.
- **Nothing in the launch says which commands a role may run.** `rolecfg/launch.go` passes only
  `--permission-mode`. The rest falls to Claude Code's defaults, the user's settings and the target
  project's `.claude/settings*.json`, which agent-bus-monitor does not own.
- **A denial is not a question.** When the classifier refuses, the person is never asked.

## 2. What Bernard wants

- master pops ordinary roles (architect, coder, docs, security…) **without a prompt and without
  the classifier**.
- For a **risky role** (deploy today), master still pops it, but **only after Bernard has agreed**.
  He gives his agreement; he does not type the command.
- The agreement is a **bus fact**: asked and answered in busmon like any ask, recorded, valid for
  every client. A Claude Code prompt in master's pane is a **second net**, not the guarantee.
- All of it is part of agent-bus-monitor, versioned with `roles.toml`.

## 3. Goals, non-goals, anti-goals

Goals

- A role may require a person's approval to be popped (`approval = "person"`).
- `agent-spawn` refuses such a role unless the bus holds an unused, recent approval from a person,
  and spends it.
- A role that pops others (`pops = true`, master) gets Claude Code permission rules derived from
  `roles.toml`: one `allow` per ordinary role, one `ask` per approval role.
- A role may declare extra rules of its own (`tool_rules`), validated.

Non-goals

- No change to target projects' `.claude/settings*.json`.
- No rule mechanism for Codex, Kimi or adapters (the bus approval covers them).
- No change to the dispatcher or the conductor.

Anti-goals

- **A rule that amounts to bypassing.** `Bash`, `Bash(*)`, a prefix like `Bash(agent-spawn *)`:
  the latter lets `agent-spawn "deploy" p` or `agent-spawn $ROLE p` through an allow that the
  `ask` does not match (Claude Code documents that an ask rule "isn't a security boundary").
  Spawn rules are generated per role, never written by hand (§4.3).
- **Claiming a security boundary.** See §7: this is a procedure gate.
- **Rules that leak** to every Claude Code session in the project. They live only in the session
  the launcher starts.

## 4. The manifest

### 4.1 Shape

Role properties, valid in schema 1 and schema 2 (what a role may do, not which client runs it):

```toml
[roles.master]
model = "sonnet"
permission = "acceptEdits"
tier = "boot"
pops = true
skills = ["agent-bus-master", "wayfinder", "to-tickets"]

[roles.deploy]
model = "sonnet"
permission = "bypassPermissions"
tier = "pop"
approval = "person"
skills = ["agent-bus", "diagnosing-bugs"]
```

- `pops` (bool, default false): the role pops others; the launcher derives its spawn rules (§4.3).
- `approval` (`"person"` or absent): popping the role needs a person's approval on the bus (§5).
- `tool_rules` (table, keys `allow`, `ask`, `deny`, each an array of strings): extra Claude Code
  rules, passed through unchanged. Named `tool_rules`, never `permissions`, so it is not confused
  with `permission` (the mode). None ships in this version. A `deny` rule matches command text:
  `Bash(agentbus * answer *)` would also block `agentbus report master "… answer …"`, which master
  writes daily; guards against an agent's own answers live in the CLI instead (§5.4).

Schema 2: `checkKeys` on the role table gains `pops`, `approval`, `tool_rules`.

### 4.2 Validation (`rolecfg/config.go`, at manifest load)

- `pops` a bool; `approval` exactly `"person"`; `tool_rules` only `allow`/`ask`/`deny`.
- Each rule: non-empty, ≤200 bytes, no control characters, shape `Name` or `Name(...)`, `Name`
  matching `^[A-Za-z][A-Za-z0-9_]*$` or an `mcp__…` name; at most 20 per list.
- **Rejected in `allow`:** `Bash`; any `Bash(…)` whose pattern is only `*` and spaces; **any rule
  naming `agent-spawn`** (spawn rules are derived, §4.3).
- A rule in two lists is rejected.
- `ask` in `tool_rules` on a `bypassPermissions` role is rejected (nothing prompts there).

### 4.3 Derived spawn rules (role with `pops = true`)

For every role `r` in the manifest except the popping role itself:

- `approval` absent → `allow` `Bash(agent-spawn r *)`
- `approval = "person"` → `ask` `Bash(agent-spawn r *)`

A form no allow matches (quoted role, variable, path, `bash -c`) falls to the mode: a prompt in
acceptEdits, the classifier in auto, the safe direction. A new role needs no rule edit.

### 4.4 What ships

`roles.toml`: master `pops = true`; deploy `approval = "person"`. No `tool_rules`.

## 5. The approval on the bus

### 5.1 Asking: `agentbus approval ask <role> <reason...>`

Run by the popping agent (identity `AGENT_BUS_AGENT`, master). It builds a fixed ask, so the
shape cannot drift:

- title `Pop <role>?`, one question, id `spawn-<role>`, header `Pop <role>`, question
  `Pop <role> for <project>?`, context = the reason, options `Approve` (recommended) and `Refuse`.
- through the existing ask path (`Bus.Ask`): busmon shows it with the bell, the panel and the
  waiting state; the answer is a reply that wakes master.
- the role must exist in the manifest and carry `approval = "person"`, else exit 2.
- **Outside the one-open-ask rule.** master often has an open ask (decisions waiting); an approval
  must not wait hours behind an unrelated one. An approval ask is not recorded in `open:<agent>`;
  it is indexed by the field `approval:<agent>:<role>` = ask id in `{p}:asks` (the hash already
  holds `open:<agent>`), written in the same script that creates the ask. A second
  `approval ask` for the same role while the first is open replaces it (same `replaced` state as
  today). busmon lists it like any ask.
- **The existing ask scripts accept the index.** Today `askWithdraw`, the withdrawal of one
  question and `--replace` check `open:<agent> == id` and fail with `ASK_NOT_YOURS`
  (`bus/asks.go` ~l.324, ~l.340). They also accept an id held in `approval:<agent>:<role>`; the
  index is cleared where they clear `open:<agent>` (withdraw, replace) and when the sweep drops the
  ask. Answering does not clear it (`use` needs it). The field scans that skip `open:` (listing,
  ~l.521) skip `approval:` too, so the index is never read as an ask id.

### 5.2 Spending: `agentbus approval use <role> [--check | --for-swap | --by-swap <ask-id>]`

Called by `agent-spawn` (§5.3) or by `swap` (§5.3). One Lua script, Redis `TIME`, O(1): it reads
`approval:<agent>:<role>` (never scans `{p}:asks`), then on that ask id:

- question `spawn-<role>` answered `Approve`, not withdrawn, answered within **30 minutes**
  (`AGENT_BUS_APPROVAL_TTL`);
- the answerer (`by`) is not a name in `{p}:agents`;
- `<id>|u|spawn-<role>` absent; the script writes it, atomic in the script (one approval, one
  pop), with a value that says who spent it: `spawn:<ms>` from `agent-spawn`, `swap:<ms>` from
  `swap` (`approval use --for-swap`), Redis time; returns the ask id.
- exit 0 found and spent; exit 3 none (message: run `agentbus approval ask <role> <reason>` and wait
  for the answer); exit 75 bus error. `--check` reads without spending.
- `--by-swap <ask-id>`: accepted only when `<id>|u|spawn-<role>` is `swap:<ms>` less than 3 minutes
  old **and** `<id>|s|spawn-<role>` is absent; the same script writes `<id>|s|spawn-<role>`, so the
  swap's hand-over is itself used once. A `spawn:` spend is never accepted: after a normal pop,
  `AGENT_SPAWN_APPROVAL=<id> agent-spawn …` (a variable master can set) gives no second pop.

`Refuse` is final for that ask: master reports it and does not ask again for the same reason.

### 5.3 The gate in `agent-spawn`

After its existing checks, before anything opens a tab, when the role has `approval = "person"`
(read through `agentbus roles`) **and `AGENT_BUS_AGENT` is set** (the caller is an agent): run
`agentbus --project <p> approval use <role>`; any non-zero exit stops the spawn with its message.
An approval spent on a launch that then fails is gone: master asks again (accepted).

- **Swap spends before it closes.** `agentbus swap <role>` / `swap --back` of an approval role
  spends the approval (`--for-swap`) in its "everything checked first" step, before any pane is closed, and passes
  the ask id to `agent-spawn` (`AGENT_SPAWN_APPROVAL=<id>` → `approval use --by-swap <id>`). An
  approval that expires or is spent elsewhere between the check and the spawn can no longer leave
  the role closed and not relaunched.
- **A person is not gated.** `bootstrap`, `agent-launch`, and `agent-spawn` run from a shell without
  `AGENT_BUS_AGENT` (Bernard's own terminal) pass: the person is there.

### 5.4 An agent cannot answer through the CLI

`agentbus answer` exits 2 when `AGENT_BUS_AGENT` is set (`answer: agents do not answer asks; a person
answers in busmon or from a shell without AGENT_BUS_AGENT`). The launcher sets it for every agent;
busmon and Bernard's own shell do not. Same strength as a rule (procedure), every client, no false
positive on report or notify text.

**Side effect, written down:** in master's tab, `! agent-spawn deploy …` and `! agentbus answer …`
inherit `AGENT_BUS_AGENT=master`, so the first is gated and the second refused. Bernard's manual
paths are: answer in busmon, or run `agent-spawn` from his own shell.

## 6. The launch (Claude Code)

- `build`: when the role has derived or declared rules, one flag after `--permission-mode`:
  `--settings '{"permissions":{"allow":[…],"ask":[…],"deny":[…]}}'` (empty lists omitted, built with
  `pyJSON`, deterministic). `--settings` over `--allowedTools`: it carries ask and deny, and it takes
  one value where `--allowedTools` is variadic.
- `preflight` requires `--settings` from `claude --help` when rules exist.
- `elevated` announces `allow=<n rules>` on stderr.
- dry run prints the flag.
- **Precedence (verified by the reviewer in Claude Code's docs, to be checked live, §9 step 1):**
  `deny` > `ask` > `allow`, first match wins, across every source including `--settings`; in auto
  mode rules resolve before the classifier, and a narrow allow like `Bash(agent-spawn architect *)`
  is not dropped on entering auto mode. A user or project `deny` still wins.

Other clients: rules are not applied, with one stderr note (`tool_rules not applied for <client>`).
The bus approval still gates the pop, so master may be swapped to Codex or Kimi; rev 1's swap
refusal is dropped. `bypassPermissions`: rules have no effect; the bus approval still gates.

## 7. What this guarantees, and what it does not

- **A procedure gate, not a security boundary.** It stops an honest, eager agent from popping a
  risky role on its own, records who approved what and when, and puts the decision in busmon.
- **It does not stop an agent set on getting round it.** Redis cannot tell busmon from master: an
  agent that unsets `AGENT_BUS_AGENT` can answer (`env -u AGENT_BUS_AGENT agentbus answer … --as
  bernard`) or spawn ungated, and `agent-launch` and `herdr` remain reachable. The CLI refusal
  (§5.4) and the refusal of an agent's name as answerer (§5.2) only close the obvious paths.
- **The skill says it** (`agent-bus-master`): never answer your own ask; to pop an approval role,
  `agentbus approval ask`, wait for the answer, then `agent-spawn`; call `agent-spawn` by its bare
  name.
- A real boundary would need an answer master cannot forge (e.g. signed by a key only busmon holds).
  Out of scope; noted in §10.

## 8. Tests

`rolecfg`: the manifest keys in schema 1 and 2; each §4.2 rejection with its message; derived rules
for the shipped manifest (allow per ordinary role, ask for deploy, none for master itself); `build`
unchanged without rules (byte-identical) and with the exact `--settings` JSON; `preflight`;
`elevated`; non-Claude note.

`bus` (isolated Redis, `AGENTBUS_TEST_REDIS_URL`): `approval use` spends once; refuses
unanswered, `Refuse`, withdrawn, older than the TTL, from another asker, answered by an agent name,
already used; `--check` does not spend; `--by-swap` accepts a `swap:` spend of its id within 3
minutes, once, and refuses a `spawn:` spend, a second hand-over and an older one; Redis time, not
the caller's clock. An approval ask while the agent has another open ask succeeds and leaves
`open:<agent>` unchanged; a second one for the same role replaces the first; withdrawing the
approval ask, withdrawing its question and replacing it succeed through the index and clear it;
the sweep clears it; listing asks never returns the index field.

`cmd/agentbus`: `approval ask` shape and its exits; `answer` refused with `AGENT_BUS_AGENT` set,
accepted without; swap of an approval role without approval refused before any pane closes; with
one, spent before the close and accepted by the spawn.

`tests/roles_scripts_test.sh`: `agent-spawn deploy` with `AGENT_BUS_AGENT` refused without approval
(dry run), allowed with one, the second spawn refused; without `AGENT_BUS_AGENT` not gated; master's
`agent-launch` dry run shows `--settings`.

## 9. Order of work

1. **Check Claude Code live** (2.1.302), in a throwaway session with the derived rules, in
   acceptEdits then auto: `agent-spawn architect x` runs unprompted (stub script), `agent-spawn
   deploy x` prompts, `agent-spawn "deploy" x` reaches the classifier or a prompt (never runs
   unasked), a user `deny` still wins. Record the results here.
2. Manifest keys, validation, derived rules.
3. Launch flag, preflight, announcement, dry run.
4. `approval ask` / `approval use` and the Lua scripts; `answer` refused for agents.
5. `agent-spawn` gate; swap spends before it closes.
6. `roles.toml`, master skill, CHANGELOG, CLAUDE.md.

## 10. Open questions

1. Other approval roles? Only deploy ships with it.
2. `agentbus swap`/`retire` for master without the classifier: left to the mode until needed.
3. A forge-proof approval (signed answers) as a later change.

## 11. Calls made by the author (check while reading)

- `pops` and `approval` as role keys, spawn rules derived, `agent-spawn` forbidden in hand rules.
- Approval valid 30 min, spent by one pop, spent even if the launch then fails.
- Answerer must not be a known agent name (weak, but closes the naive self-answer).
- A dedicated `approval` subcommand rather than master writing the ask JSON.
- "Agent or person" is read from `AGENT_BUS_AGENT` (gate in `agent-spawn`, refusal in `answer`).
- A second approval ask for the same role replaces the first.
- `bootstrap`/`agent-launch`, and `agent-spawn` from a shell without `AGENT_BUS_AGENT`, are not gated.
