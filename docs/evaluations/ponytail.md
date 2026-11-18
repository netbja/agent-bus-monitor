# Ponytail: evaluation for agent-bus-monitor's coding agents

- **Scope:** whether [Ponytail](https://github.com/DietrichGebert/ponytail) (MIT, "lazy senior dev
  mode") helps our coding agents (`coder`, `coder2`). Analysis only: nothing was installed, and no
  project or `~/.claude` file was changed.
- **Read:** Ponytail at commit `96608e0` (2026-10-29), version 5.3.3, cloned read-only to
  `/tmp/ponytail-eval`. Our side: `origin/dev` at `14ecc1b`.
- **Author:** architect, 2026-11-18, for the person's brief (thread `1794957009298-0`).

## Verdict

**Test, without the plugin.** Take Ponytail's rules (its `AGENTS.md`, about 500 words) into the
coders' role prompt and measure them against today's coders: a replayed bench of 12 merged tasks
with three arms (control, rules added, rules swapped for `karpathy-skills`), then a short
alternation in production for regressions (section 4). Do not install the
plugin: what it adds on top of the rules (Node hooks, a codebase map, a statusline, a band above the
prompt, a cost ledger) either duplicates something we have, conflicts with how we launch and resume
agents, or adds a Node dependency and a supply-chain surface the rules do not need.

The reasons, in one line each:

- The benefit is in the rules, and the rules are plain text that every one of our five clients can
  take through the role prompt we already build (`rolecfg/launch.go`).
- The plugin is global per client, not per role: scoping it to the coders needs a launcher change.
- Its SessionStart hook re-injects the rules on every `--resume`, which our self-repair does.
- Its mode flag is shared by every agent of one project folder, so one agent's `/ponytail off`
  reaches the others.
- Its published gains come from tasks with room to over-build (a hand-made date picker replaced by
  `<input type="date">`). Our coders rarely build features nobody asked for; what they do is
  re-write a helper the repo already has (section 3). That is rung 2 of Ponytail's ladder, which
  is a rule plus a search, and nothing in the plugin's machinery finds those helpers better than
  the code-search tools our agents already have.

## 1. What Ponytail is and what it does

### What it injects, and when

| What | When | Size |
|---|---|---|
| The ruleset (`skills/ponytail/SKILL.md` without its front matter, filtered to the level) | Claude Code and Codex `SessionStart` with source `startup`, `resume`, `clear` or `compact` | 2 859 characters, 494 words, about 700 tokens |
| A codebase map: the first exported names per folder, from `git ls-files` (`hooks/ponytail-map.js`) | Same hook, unless `PONYTAIL_MAP=0`; dropped if it takes more than 3 s | capped at 2 000 characters; 2 117 on our repo, about 600 tokens |
| A statusline setup "nudge" telling the model to offer to edit `~/.claude/settings.json` | Same hook, once, when that file has no `statusLine` (or one pointing at a deleted Ponytail script) | a few lines |
| The ruleset again | `SubagentStart`, for every subagent while the mode is not `off` (`PONYTAIL_SUBAGENT_MATCHER` narrows it) | about 700 tokens per subagent |
| A one-line notice ("PONYTAIL MODE CHANGED", "OFF", ...) | `UserPromptSubmit`, only for a prompt starting with `/ponytail` or exactly `stop ponytail` / `normal mode` | one line |

So a Claude Code coder pays about **1 300 tokens per session start**, resume, `/clear` and
compaction, plus 700 per subagent. Measured with Ponytail's own builder
(`getPonytailInstructions('full')`) and its map script run against our checkout.

On our repo the map is weak: it lists 14 folders and drops 32 ("32 more folders not listed"), for
large folders it keeps the first exported name per file, so `bus/` shows `Printf`, `Error`,
`String` among its 30 names, and for Go it lists exported names only. Of the nine existing helpers
our coders re-wrote in the last 15 PRs (section 3), the map shows one (`Strip`). Our agents already have structural search (the `codebase-memory` and
CodeGraph MCP servers), which answers "does this exist?" better than a 2 000-character list.

Clients without hooks (Kimi, Grok, agy, Gemini, Goose, ...) get the rules as plain text: Kimi's
plugin puts `AGENTS.md` in the system prompt every turn (`systemPromptPath`); Grok and agy install
the skills, and the rules are always on only through a project `AGENTS.md`.

### The Node hooks

All hooks are `node -e "require(<plugin root>/hooks/<file>.js)"`, 15 s timeout, and they exit after
1 s if stdin never closes.

| Hook | File | Does |
|---|---|---|
| `SessionStart` | `ponytail-activate.js` | Writes the mode flag; emits the ruleset and the map; on `compact` keeps the live level, on any other source resets to the default level; nudges the statusline once; refreshes a copy of the statusline script if one exists. |
| `UserPromptSubmit` | `ponytail-mode-tracker.js` | Parses `/ponytail [lite|full|ultra|off|default <lvl>|band on|off|import]`, `stop ponytail`, `normal mode`; writes the flag or the default; `band import` spawns a detached Node process that reads past Claude Code transcripts. |
| `SubagentStart` | `ponytail-subagent.js` | Injects the ruleset into the subagent if the flag says a level. |
| (Claude Code mod) | `hooks/band/register.tsx` | A UI band above the prompt (level, spend, reuse %, tests, buttons); wraps every `Bash` tool call to spot test runs; after every turn writes the task's row to a CSV and, if configured, posts it to a webhook. |

### What they write outside the plugin folder

| Path | Written by | Removed by `scripts/uninstall.js` |
|---|---|---|
| `~/.claude/.ponytail-active` (or `$CLAUDE_CONFIG_DIR`; Codex: `$PLUGIN_DATA`) | every mode change, every session start | yes |
| `~/.claude/ponytail-modes/<sha256 of CLAUDE_PROJECT_DIR>` | same | yes |
| `~/.claude/.ponytail-statusline-nudged` | the nudge | yes |
| `~/.claude/ponytail-statusline.sh` (or `.ps1`) | the nudge, then refreshed at each start | yes |
| `~/.claude/settings.json` `statusLine` | the **model**, if it follows the nudge | yes, only if it points at Ponytail's script |
| `~/.config/ponytail/config.json` | `/ponytail default <level>` | yes |
| `~/.config/ponytail/band/<project>.csv`, `board.html` | the band, after every turn (`ledger` defaults to **on**) | **no** |
| `~/.claude/.ponytail-band` | `/ponytail band off` | **no** |
| an HTTPS webhook | the band, if `webhookUrl` is set (default empty) | n/a |

The uninstall must run **before** the plugin is removed (the script is a plugin file), and it leaves
the band's ledgers behind. The task name it records is the first prompt; for our agents that is the
boot line or a wake line naming the project.

## 2. Compatibility with our architecture

### Injection per role

Our launcher already scopes instructions per role without any plugin: `roles.toml` gives each role
a prompt (`roles/<role>.md`) and a `skills` list, and `rolecfg/launch.go` builds one prompt from
both. It reaches each client its own way:

| Client | How the role prompt arrives (`rolecfg/launch.go`) | Survives resume / compaction |
|---|---|---|
| Claude Code | `--append-system-prompt` | yes, it is the system prompt |
| Codex | the first user turn | in history only |
| Kimi | a generated `--agent-file` (`${base_prompt}` + role) | yes, system prompt |
| Grok | `--rules` (appends to its system prompt) | yes |
| agy | `-i` (initial user turn; agy has no system-prompt flag) | in history only |

So the rules can go to the coders only, on every client, by adding them to `roles/coder.md` and
`roles/coder2.md` (or as a skill in their `skills` list). That is the role scoping the person asked
for, and it needs no code change.

The plugin cannot be scoped that way. A Claude Code plugin is enabled in `~/.claude/settings.json`
for every session of the user, master and security included. Scoping it needs the launcher to pass
`--plugin-dir <pinned checkout>` for the coder roles only (a new per-profile option in
`rolecfg/launch.go`), and a global install would have to stay off. Codex, Kimi, Grok and agy have
the same problem through their own plugin commands, which are global too.

### Clients

| Client | Ponytail ships | Hooks | What we would use |
|---|---|---|---|
| Claude Code | full plugin | yes (Node) | the rules in the role prompt |
| Codex | plugin, same hooks | yes, state in `$PLUGIN_DATA` | the rules in the role prompt; our only Codex role is a read-only reviewer anyway |
| Kimi | `.kimi-plugin` (rules as `systemPromptPath`) | no | the rules in the generated agent file |
| Grok | plugin of skills, no hooks ("passive hook output cannot inject instructions") | no | the rules in `--rules` |
| agy | `agy plugin install` of skills | no | the rules in `-i` |

The brief assumed Kimi was not covered; it is (Kimi Code 2.1.1, verified by Ponytail over ACP).
It does not matter for us: the role prompt covers it already.

### Resume (dispatcher self-repair)

Our self-repair relaunches a dead Claude Code agent with `claude --resume <id>`
(`rolecfg/clients.go`; only Claude Code resumes today). Ponytail's `SessionStart` matcher includes
`resume`, so:

- **Double injection, cumulative:** each resume appends the ruleset and the map again to a
  conversation that already holds them, about 1 300 tokens per relaunch.
- **Level reset:** on `resume` the hook takes the default level, not the live one (only `compact`
  keeps it), so a coder switched to `lite` comes back in `full`.
- After our auto-clear (`/clear` after a declared hand-off) one fresh copy is injected; that is
  correct.

The rules in the role prompt have neither problem: `--append-system-prompt` is part of the launch,
so a resumed coder has exactly one copy.

### Shared mode flag

The flag is keyed by `CLAUDE_PROJECT_DIR`, then falls back to one file per user. Every agent
launched from the same folder shares it. If one agent (or a person in its pane) runs `/ponytail off`,
every other agent of that folder reads `off` at its next compaction and its next subagent. Per-role
behaviour is not something the plugin models.

### Dispatcher, PTY and turn detection

- **Turn end** is read from our own `Stop` / `UserPromptSubmit` hooks (passed with `--settings`)
  and from the screen signatures in `agentstate/clients/claude.toml`. Neither looks at the reply's
  text, so the closing "skipped / not checked" lines cannot end or extend a turn. Ponytail's hooks
  run next to ours; Claude Code merges them.
- **Wake line:** the dispatcher types `[agentbus#<nonce>] ...`; Ponytail reacts only to a prompt
  that starts with `/ponytail` or equals `stop ponytail` / `normal mode`. No collision. The
  dispatcher never types a slash command other than `/clear` after a hand-off.
- **Screen states:** `ready` needs the rule, the empty `❯` line and the closing rule in sequence;
  the band draws in the `AbovePrompt` slot, above that box. It should not break `ready`, but no
  capture exists of a Claude Code screen with the band, so this is **not verified**; the plugin
  route would need one in `agentstate/clients/screens/` before use.
- **Blocked detection** looks for `<question>?` above `❯ 1.`; only a menu has `❯ 1.`, and the
  band's buttons are not numbered. No conflict expected.
- **Bus reports:** the closing lines apply to the agent's reply, not to `agentbus report`. If an
  agent copies them into reports, they go after the summary line (which busmon shows) and count
  against the 8 000-rune limit. Minor, and it matches our "report outcomes faithfully" rule.
- **Every Bash call** passes through the band's `tool.call` wrapper (to spot test runs). It calls
  `next(e)` and changes nothing, but it is third-party code in the path of every `agentbus` and `git`
  command an agent runs.

### Node on PATH

The hooks need `node` on the `PATH` of the client process. On this host Node comes from nvm
(`~/.nvm/versions/node/v24.10.0`), and it is on the `PATH` of the running holders and of the agents
they host (checked in `/proc/<pid>/environ` on 2026-11-18). That `PATH` is inherited from whatever
started the holder; nvm puts itself there from a shell profile, so a holder started without one (a
login service on another host, a fresh Mac) may not have it: not verified. A missing `node` makes
the hooks fail, and Claude Code carries on without the rules: a silently disabled arm. Our product is one Go binary
per platform and asks for no runtime; adding Node for the coders' instructions goes against the
target (Mac, Linux without systemd, Windows later). Ponytail is careful about Windows (BOM
stripping, `.ps1` statusline), so the issue is the dependency, not portability bugs.

### Supply chain

- The hooks run with the agent's rights at every session start and every prompt; our coders run
  with `bypassPermissions`. A plugin update is new code in that path.
- Claude Code installs a plugin from its marketplace's Git source. To pin it, vendor a reviewed
  commit and load it with `--plugin-dir` (the way Ponytail's own benchmark isolates arms), and
  review the diff of each update like a dependency bump.
- The band can post every turn's row to a webhook (default off) and keeps CSVs of every task by
  default; the `band import` command reads past transcripts.
- The rules alone have no supply chain: 500 words of MIT text, copied once with attribution, read
  in review like any role prompt change.

## 3. Relevance on our code: the last 15 coder PRs

Method: the 15 most recent merged code PRs on `origin/dev` (#580 to #599; docs and spec PRs
#581, #589, #596 and the test-only #594 skipped). For each new function, Ponytail's ladder was
applied by hand: does it need to exist, does the repo have it, does the Go standard library, does
an imported module, can it be one line. Each finding below names the new code and the existing code
it repeats; both sides were read, and the cited lines are those of `origin/dev` at `14ecc1b`.

### Per PR

| PR | Subject | Non-test Go added | Verdict | Why |
|---|---|--:|---|---|
| #599 | stuck screen rings once | +136 | over-built | `stuckNotices` repeats the `promptNotices` loop |
| #598 | busmon prompt reads blocked | +29 | clean | `promptBlocked` replaces repeated inline checks |
| #597 | ask refuses the person | +51 | minor | makes #590's ask branch redundant |
| #595 | holder behind | +143 | minor | an interface with one implementation, used as the test fake |
| #593 | `agentbus screen` | +221 | minor | copies busmon's `holderTerm`; a hand-made escape stripper |
| #592 | service starts its holder | +112 | minor | the 5-minute retry defaulted in two places |
| #590 | a person is never an agent | +76 | minor | `personGuard` parses `ask`'s flags a second time |
| #588 | paste label | +30 | minor | repeats busmon's clip with "…" |
| #587 | `quota_drain`, per project | +288 | over-built | a copied function, defaults outside the registry, fields never read |
| #586 | holder live check | +1 127 | minor | `envValue` repeats `lookupEnv`; large, but as the spec asked |
| #585 | reboot resume, holder at login | +829 | minor | the launchd holder code copies `login.go` |
| #584 | emulator contained | +666 | clean | one method only tests call |
| #583 | holder alarm | +139 | minor | `writeTermsFile` repeats `writePrivate`, weaker |
| #582 | `alt_profiles` | +80 | minor | `checkAlternates` repeats `checkFallbacks` |
| #580 | anonymous is no identity | +2 | clean | one condition |

Three clean, ten minor, two over-built. No PR adds a feature, option or flag nobody asked for
(rung 1). The pattern is rung 2: **a near-copy of a helper the repo already has**.

### The findings

- **#599**: `stuckNotices` (`dispatch/promptnotify.go:187`) repeats `promptNotices` (`:73`) step by
  step (mark, episode check, asks lookup, swap check, fenced write, notify); only the state test,
  the mark and the message differ. Its setting `dispatch.stuck_after` writes its env and file
  parsers by hand (`config/keys.go:255-266`), where `envStallDuration` (`config/keys.go:783`) does
  it and the next key, `focus_notice`, uses it. `stuckFor` (`dispatch/promptnotify.go:249`) is
  `agentstate.age` (`agentstate/background.go:174`) without the hours.
- **#587**: `quota.ResetStamp` (`quota/resetlabel.go:58`) is `ResetLabel` (`:23`) without the
  today/tomorrow case, 11 of its 13 lines copied (one function with a flag, or `ResetLabel`
  calling it, would do). `defaultWarnPct`, `defaultDrainPct`, `defaultHoldPct`, `defaultMaxAge`
  (`dispatch/quota.go:52-55`) repeat the defaults of `config/keys.go:280-283`, against the
  project's rule that settings live in the registry. `RemoveProject` (`config/write.go:395`)
  parses TOML lines itself next to `scanKeys` (`config/write.go:220`).
- **#593**: `liveTermOf` (`cmd/agentbus/screencmd.go:149`) has the same body as `holderTerm`
  (`cmd/busmon/holderterm.go:10`); the two live in different `main` packages, so the shared home
  is `termhost`. `plainLine` (`cmd/agentbus/screencmd.go:166`) repeats `agentstate.Strip`
  (`agentstate/signature.go:49`).
- **#586**: `envValue` (`cmd/agentbus/livecheckproject.go:97`) repeats `lookupEnv`
  (`cmd/agentbus/roles.go:40`) in the same package, and differs: it takes the first duplicate, where
  `lookupEnv` (and `exec`) take the last. A copy that drifts is the cost of not reusing.
- **#583**: `writeTermsFile` (`holder/server.go:533`) is an atomic write next to `writePrivate`
  (`holder/pgids.go:62`) in the same package, with a fixed `.tmp` name and no clean-up. The repo has
  about 20 hand-made atomic writes and five functions named `writeAtomic`.
- **#585**: `holderAgentPath`, `EnableHolder`, `removeHolderAgent` (`service/holderlogin.go:74-136`)
  copy `agentPath`, `writeAgent`, `removeAgent` (`service/login.go:187-264`) with another label and
  path. The same PR reuses the fenced Lua writes correctly and extracts `setInstallAt` for reuse.
- **#582**: `checkAlternates` (`rolecfg/config.go:338`) repeats `checkFallbacks` (`:413`).
- **#590 / #597**: the ask branch of `personGuard` (`cmd/agentbus/identityguard.go:140-165`) is
  covered since #597 by `askerCheck` (`cmd/agentbus/askcmd.go:116`), and repeats `runAsk`'s flag
  list. Dead weight left by a later PR, not over-building at write time.

### Is the duplication caused by rebases between the two coders?

No. Every original was merged before the copy was started: most days or weeks before
(`holderTerm` 11-15, `writePrivate` 11-14, `lookupEnv` 10-11, `checkFallbacks` 10-16, `login.go`
11-12, `envStallDuration` and `scanKeys` 11-01, `Strip` 11-12), the two closest by half an hour
(`ResetLabel`, #576, merged 22:44Z on 11-16; #587's first commit 23:13Z). The coder could see the
original each time. The one pair of PRs in flight together (#592 and #595) did not duplicate code:
#595 rebased onto #592 and plugged into its new code. None of the 15 PRs holds a merge commit, and
GitHub shows a force-push on two (#584, #595).

Rebases between our coders are real, but they come from shared append points (the settings
registry, busmon's model files, the operator notes), which the
[hot files note](../memory/open-questions/2026-11-14-hot-files.md) covers. They cause conflicts,
not copies.

### What this says about Ponytail

- The over-building to cut is **missed reuse**, mostly of unexported helpers in the same or a
  neighbouring package. Ponytail's rule 2 ("already in this codebase? use it") names it; its map
  cannot help (Go exported names only, 2 000 characters).
- Ponytail's benchmark gains are largest where a platform feature replaces a custom build, and
  close to zero on "irreducible" backend code (its own table: CRUD endpoints within a few lines of
  the baseline). Our PRs are of the second kind.
- None of these copies was stopped in 4-eyes review. A rule only in the coder's prompt leaves the
  reviewer's checklist as it is; the cheaper lever may be the review.

Not verified: which coder wrote which PR (same Git identity, the worktrees are gone), and rebases
made before a PR's first push, which leave no trace on GitHub.

## 4. A/B protocol (not run)

The person's choices (thread `1794957307782-0`, 2026-11-18): the control is today's coder,
unchanged; Ponytail is its rules only, in the role prompt; and the test is a hybrid, a replayed
bench first, then a short alternation in production for what the bench cannot see. Nothing below
has been run.

### What is tested

- **The text.** Ponytail's `AGENTS.md` at commit `96608e08` (`96608e078b2d5ea607c5c3b8b5a54d1c1fba6846`,
  version 5.3.3), copied unchanged, with a credit line (MIT). It is the `full` level: `AGENTS.md`
  carries no level table, and its rules are the `full` rules of `skills/ponytail/SKILL.md` without
  the mode header. The commit and the level are written in the arm's prompt file, so a later
  Ponytail release cannot change what was tested.
- **Where it goes.** Appended to the coder's role prompt (`roles/coder.md` for the arm), so the
  launcher delivers it the way it delivers the role on each client. No plugin, no Node, no hook,
  no launcher change for the "add" arm.
- **The arms.**

  | Arm | Coder | Global Claude plugins |
  |---|---|---|
  | **control** | today's role prompt | as today (`superpowers`, `karpathy-skills`, ...) |
  | **add** | role prompt + Ponytail text | as today |
  | **swap** | role prompt + Ponytail text | as today, minus `karpathy-skills` |

  The swap arm (the person's suggestion) answers "does Ponytail do better than what we already
  have, at the same context cost"; the add arm answers "does it add anything to it". Both are
  measured against the same control.
- **Isolation of the control.** Ponytail is installed nowhere: not as a plugin, not globally, not in
  the project. Its text exists only in the arm's prompt file. So the SessionStart contamination its
  own benchmark hit cannot happen. Each transcript is checked anyway (below).

### About adherence over a long session

The person's concern, from the review: with the plugin the rules come back in each turn, with the
role prompt they are read once. What the code shows is more precise, and it matters for the
measures:

- The Claude Code plugin injects the rules at session start, resume, `/clear` and compaction, and
  into each subagent. Not each turn. (Kimi's plugin and the OpenCode/pi adapters do inject each
  turn.)
- Our role prompt is part of the system prompt on Claude Code (`--append-system-prompt`), Kimi
  (`--agent-file`) and Grok (`--rules`): it is in **every** request and survives compaction. On
  Codex and agy it is the first user turn: it lives in the history and can be summarised away.

So "read once" holds for Codex and agy, not for Claude Code, Kimi or Grok. The fading the person
expects is still possible (a rule in a long system prompt can lose weight against a long history),
so adherence is measured per session slice in phase 2. If the effect fades on Claude Code, the
plugin is the follow-up, for Claude Code only.

### Phase 1: the replayed bench

**Tasks.** 12 merged coder PRs from `origin/dev`, chosen before any run and written down with
their sha:

- 4 small (under 50 non-test Go lines), 4 medium (50 to 300), 4 large (over 300);
- at least 4 where section 3 found a copy (#599, #587, #593, #583 are candidates) and 3 it found
  clean (#598, #580, #584), so the bench has both room to over-build and none;
- no PR whose task text cannot be rebuilt (below), and no PR that only edits docs.

**Task text.** The original directives are mostly gone from the bus (retention). Each task text
is rebuilt once, from the spec or plan step the PR cites and the PR's description, without hints
from the merged diff, and reviewed by master before the first run. All arms get the same text, and
it names the base: "work from this commit, deliver a commit in this worktree". The merged PR is a
reference, not the answer: a text rebuilt after the fact may say more or less than the original.

**Run.**

- Each run: a fresh worktree at the merged PR's parent commit
  (`git worktree add .worktrees/bench/<pr>-<arm>-<n> <merge>^1`), its own disposable Redis for the
  suite (`AGENTBUS_TEST_REDIS_URL`), and a bench project name on that Redis, so nothing reaches the
  production bus.
- Headless Claude Code: `claude -p --output-format json`, the coder's profile (model `opus`,
  `bypassPermissions`), the arm's role prompt through `--append-system-prompt-file`, the task text
  as the prompt. Bash on: our coders run the tests, so the bench lets them. The role prompt's bus
  steps (status, report) are dropped from all arms alike: there is no master to answer.
- The swap arm also passes `--settings '{"enabledPlugins":{"andrej-karpathy-skills@karpathy-skills":false}}'`.
  To verify in the pilot: the transcript's skill list no longer shows `karpathy-guidelines`.
- 2 runs per task and arm: 12 × 3 × 2 = **72 sessions**. A third run for the tasks whose two runs
  disagree most, if the budget allows.
- Order: the three arms of one task run back to back, rotating which goes first, so model or
  service drift hits them alike.
- **Pilot first:** 2 tasks × 3 arms × 1 run. It checks the isolation, the swap mechanism, the
  scripts that collect metrics, and gives the cost per session that sizes the budget before the
  other 66.

**Checks of every transcript (a run that fails one is discarded and re-run).**

- control: no "lazy senior developer" text anywhere, no `PONYTAIL` string;
- add and swap: the Ponytail text present once, in the system prompt;
- swap: no `karpathy-guidelines` skill call; control and add: count its calls (the overlap).

**Measures, per run.**

| Measure | Source |
|---|---|
| Tokens in, out, cache; cost; turns; wall time | `claude -p` JSON (`usage`, `total_cost_usd`, `num_turns`, `duration_ms`) |
| Lines added and removed, non-test Go and tests apart | `git diff --numstat <merge>^1` in the worktree |
| **Copies of existing code** | for each new function: does the parent commit already have one that does the same? Section 3's method, by a reviewer who does not know the arm |
| Unrequested options, fields, defaults outside `config/keys.go` | same review |
| Correctness | `go build ./... && go vet ./...`; the arm's own suite; then the merged PR's tests applied on top where they compile (hidden checks); `go run ./cmd/agentbus mem check` |
| Review verdict | a 4-eyes review of the diff by a reviewer who does not know the arm: approve, or the count of blocking findings |
| Security | the security role's review, for tasks that touch identity, files written with the user's rights, or the holder |
| `karpathy-guidelines` calls | transcript (control and add) |
| The closing "not checked" lines | transcript: present, and true |

**Analysis.** Per task, the median of its runs; per arm, the ratio to control per task, then the
geometric mean over tasks with a 95% bootstrap interval, and a sign test ("better on X of 12").
Small, medium and large reported apart as well as together: Ponytail's own results say the effect
is task-dependent (−94% on one task, about 0 on CRUD code).

**Decision rule, set before the runs.**

- An arm passes if, against control, it cuts copies of existing code **or** non-test lines by 15%
  or more (interval not crossing 0), with **no** rise in failed builds, failed hidden checks,
  blocking review findings or security findings.
- Cost and tokens decide between add and swap when both pass, or break a tie.
- If neither passes: the verdict becomes **ignore**, and section 5's items stand on their own.

### Phase 2: alternation in production (2 to 3 weeks)

Only if phase 1 passes, and only with the arm that won it. Its goal is regressions, not the size of
the effect: noise is accepted.

- **Who carries the arm.** One coder carries the arm's prompt in week 1, the other in week 2, then
  back (if 3 weeks), so a coder's own habits do not pass for the arm. Master assigns tasks as
  usual; each task records which arm did it and the hot files it touched, so a hot-file pattern in
  the assignment shows.
- **How the arm changes.** A PR to `dev` that edits `roles/coder.md` or `roles/coder2.md`. The new
  prompt applies at the coder's next launch (a self-repair resume relaunches with the current
  prompt), so master relaunches the coder after the merge, and records the time.
- **Swap in production**, if swap won: a per-role way to drop one global plugin from the launch
  (the launcher already builds a `--settings` JSON per role). That is a launcher change, specified
  and reviewed on its own; the add arm needs none.

**Measures.**

| Measure | Source |
|---|---|
| Adherence per session slice: the first 20 turns, after 60 turns, after a compaction or a context escalation, after a self-repair resume | per slice, the copies and unrequested options in the commits made in that slice (section 3's review), and the closing "not checked" lines in the transcript |
| Rebases and conflicts per PR | force-pushes on GitHub, and conflicts the coder reports |
| PRs rejected by security, by foureyes | `agentbus verdicts` (the ledger) |
| Tokens and cost per task | `{p}:usage` per agent, cut at task boundaries (board accept and done times) |
| Test failures after merge | CI and the next PR's report |

**Stop rule.** Back to the control prompt at once if the arm's PRs show more security rejections,
or more blocking review findings, than the control's over the same weeks, or a failure after merge
the arm caused.

**Non-Claude clients.** Our coders run on Claude Code today. A non-Claude coder has neither
`superpowers` nor `karpathy-skills`, so its control is a coder with no simplicity rules at all: a
different comparison. If one is tried (Kimi, Grok, agy or Codex as a coder), its results are kept
apart and never pooled with Claude's.

**If adherence fades** (phase 2 shows the effect in the first slice and not after compaction or
resume, on Claude Code): the follow-up is the real plugin, Claude Code only, with the preconditions
from section 2: a vendored, reviewed commit loaded with `--plugin-dir` for the coder role only, a
screen capture proving our signatures still read the pane, `node` checked on the holder's `PATH`,
the band's ledger off.

## 5. What to take without Ponytail

Each of these is a small change of its own, to propose to master as separate tasks; none is made by
this document.

1. **"Search before you write" in the coders' role prompt.** Five lines in `roles/coder.md` and
   `roles/coder2.md`, from Ponytail's ladder (MIT, credited): before adding a function, search the
   package and its neighbours for one that does the same, with the code-search tools you have
   (unexported names included); use the standard library before a helper; no option, field or
   default nobody asked for; settings defaults only in `config/keys.go`. It reaches all five
   clients through the existing launch, survives resume and compaction on Claude Code, Kimi and
   Grok, and costs about 150 tokens.
2. **The same question in review.** One line in foureyes' checklist: "for each new function,
   name the existing one you searched for". Section 3's copies all passed review.
3. **A "skipped / not checked" closing line in reports.** Ponytail's closing lines match our rule
   that outcomes are reported faithfully. A `## Not checked` part in the report template of the
   `agent-bus` skill, after the summary line, gives master the same information without new code.
4. **A debt ledger master reads.** Ponytail's `shortcut: <the limit>, <when to upgrade>` comment
   plus `/ponytail-debt` (a `git grep` of the markers). The marker and a `git grep` in master's
   periodic review are enough; a busmon page would be a later step, once the markers exist.
5. **Cost and tokens per agent.** busmon already publishes context fill per agent
   (decision [context fill](../memory/decisions/2026-10-27-context-fill.md)) and keeps `{p}:usage`.
   Ponytail's band (spend, last turn, agent time) is the same idea inside Claude Code. If the person
   wants it, it is a busmon view over data we already collect, specified on its own.
6. **A one-off clean-up of the copies in section 3**, as a coder task: `liveTermOf` into
   `termhost`, `ResetStamp` as a case of `ResetLabel`, `writeTermsFile` onto `writePrivate`,
   `envValue` onto `lookupEnv` (which also fixes the first/last difference), the quota defaults
   read from the registry, `stuckNotices` and `promptNotices` as one loop.

## Risks

| Risk | Applies to | Mitigation |
|---|---|---|
| Hooks run third-party Node at every start and prompt, with `bypassPermissions` | plugin | do not install; if ever tested, a vendored, reviewed commit through `--plugin-dir` |
| A global install reaches master, security and every project | plugin | no global install; per-role launch option only |
| Re-injection on every `--resume`, level reset to the default | plugin | rules in the role prompt instead |
| One agent's `/ponytail off` switches the others of the same folder | plugin | rules in the role prompt (no mode) |
| `node` missing from a service's `PATH`: the arm silently runs without rules | plugin | rules in the role prompt; or check the hook's output in a transcript |
| The model is told to edit `~/.claude/settings.json` (statusline nudge) on a host without a `statusLine` | plugin | `quietStartup`, or no plugin |
| Ledger CSVs of every task kept by default, not removed by the uninstall | plugin | `ledger` off, or no plugin |
| The band changes the screen above the prompt; our screen signatures were never captured with it | plugin | a capture in `agentstate/clients/screens/` before any use |
| "Write less" drops a check at a trust boundary | rules | Ponytail's own "never cut" list; its benchmark shows no loss of hidden checks (97% vs 96%); our 4-eyes and security reviews stay |
| Adherence fades in long sessions; history-only on Codex and agy | rules | measured in the protocol (adherence after N turns and after compaction) |
| Overlap with the global `karpathy-skills` plugin (similar rules), or the two rule sets pulling apart | both | the add arm measures what Ponytail adds to it, the swap arm whether it does better in its place; `karpathy-guidelines` calls are counted |
| The measured effect is noise at our volume | both | the bench pairs each task across arms: 12 tasks × 2 runs, sign test plus bootstrap interval; fewer than 12 usable tasks after the pilot: no decision. Phase 2 looks for regressions only, not for the size of the effect |
