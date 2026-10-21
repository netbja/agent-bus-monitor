# Specialist roles, the trust prompt, other LLMs as 4-eyes, skill updates — design

Status: draft for review (coder, 2026-10-21). Task `roles-spec`, branch `docs/roles-spec` from
`origin/main` `56a1699`. Spec only; no code in this change. Items A–D from Bernard.

Facts read for this spec: `roles.toml` (the live manifest, **schema 1**: `model`, `permission`
and `fallback` on each role, no profiles), `examples/roles.mixed.toml` (schema 2: `[defaults]`,
`[profiles.*]`, `fallback_profiles`), `rolecfg/config.go` (the closed key sets: a schema-2 role takes
only `profile`, `tier`, `skills`, `fallback_profiles`, `pops`, `approval`, `tool_rules`),
`roles/architect.md` (the role prompt shape), `rolecfg/skills.go` and `scripts/link-role-skills.sh`
(skills are symlinks), `setup.sh` (re-links skills on install/update), `cmd/agentbus/retire.go`,
`bus/verdicts.go` (roll-up independence by agent name), the skills installed on this machine, and
Claude Code's documentation on workspace trust.

---

## A. Specialist roles

### Shape: no Go change

Every specialist is a `pop` role. The schema already carries everything a specialist needs: its
profile (client, model, permission), its skills, `approval = "person"` for the ones that may touch a
live system, and master's `pops = true`, which already gives master one
`Bash(agent-spawn <role> *)` rule per role, or an ask rule for an `approval = "person"` role
(`rolecfg/toolrules.go`). **No schema or Go change is needed.** The trigger table that tells master
*when* to pop each role lives in the master skill, not in `roles.toml` (whose keys are closed; a
`summary` key would be a schema change for text that only master reads).

Names (all `ValidName`): `support`, `dba`, `web-design`, `backend`, `os-expert`, `cicd`.

### The roles

| Role | Profile (model · permission) | approval | Skills | Master pops it for | It never |
|---|---|---|---|---|---|
| `support` | sonnet · acceptEdits | — | agent-bus, diagnosing-bugs, implement | the person's day-to-day questions about the system, small fixes, how-to answers, triage of a reported problem | merges, deploys, changes a live host |
| `dba` | opus · manual | person | agent-bus, diagnosing-bugs, codebase-design | schema design, migrations, slow queries, index and data-model review, data repair plans | runs a write against a production database; a repair is a reviewed script the person runs or approves by ask |
| `web-design` | opus · acceptEdits | — | agent-bus, implement, tdd (+ the `frontend-design` plugin skill where installed) | pages, layout, visual design, ergonomics, accessibility, responsive behaviour | changes backend contracts; it asks `backend` through master |
| `backend` | opus · bypassPermissions | — | agent-bus, tdd, implement, codebase-design | API and service design, concurrency, performance, a second implementer on server code | merges or deploys |
| `os-expert` | sonnet · manual | person | agent-bus, diagnosing-bugs | Linux and Windows: services (systemd, Windows services), packages, permissions, PowerShell/shell scripts, boot and network issues | changes a host's configuration without an ask; it writes the commands and says what each one changes |
| `cicd` | sonnet · acceptEdits | person | agent-bus, diagnosing-bugs, tdd | pipelines, build and release automation, test matrices, deployment scripts in the repository | runs a deployment (that is `deploy`'s, already `approval = "person"`) or reads a pipeline secret |

The models are proposals: opus where design judgement dominates, sonnet for execution-heavy work.
The `permission = "manual"` roles get Claude Code's per-tool prompts, so the person sees each
command on a live system. `approval = "person"` makes master's `agent-spawn` of that role an ask.
Both are conveniences, not locks (CLAUDE.md, "The launcher's environment rules").

In schema 1 (today's `roles.toml`) an entry is:

```toml
[roles.dba]
model = "opus"
permission = "manual"
tier = "pop"
approval = "person"
skills = ["agent-bus", "diagnosing-bugs", "codebase-design"]
```

In schema 2 (needed by item C) the execution fields move into shared profiles:

```toml
[profiles.claude-opus-manual]
client = "claude-code"
provider = "anthropic"
model = "opus"
[profiles.claude-opus-manual.options]
permission = "manual"

[roles.dba]
profile = "claude-opus-manual"
tier = "pop"
approval = "person"
skills = ["agent-bus", "diagnosing-bugs", "codebase-design"]
```

### Role prompt files

One `roles/<role>.md` per role, in `roles/architect.md`'s shape: the boot steps (skills, presence,
`agentbus request` and `board` first, arm only if something is coming), then three short sections:

- **Scope**: the "pops it for" column, in the role's words.
- **Never**: the "it never" column, plus "a decision for the person goes to master, who asks".
- **Hand-back**: `agentbus request done <task> "<summary>"`, a report in the agreed shape
  (summary line, `## Done`, `## What remains for you`), then `status idle` and stop. The role does
  not retire itself; master does.

No new domain skill in this change. The domain checklist (a DBA's migration checks, web
accessibility points, OS commands to inspect before changing) lives in the prompt file, where it can
be reviewed with the role. A domain skill is extracted later only if two projects need the same one.

### When master pops and retires (master skill, new section "Specialists")

- **Pop** when a task falls in a role's "pops it for" column and is larger than a direct answer.
  Pop with a tracked request (`request send <task> <role> --ttl …`) so the work is on the board.
  An `approval = "person"` role is asked for by Claude Code's own prompt in master's pane.
- **One specialist at a time** unless the person asks for more.
- **Cost:** check `agentbus quota` before popping: no pop at ≥ 75 % of the window that the role's
  provider uses, except at the person's request.
- **Retire** with `agentbus retire <role>` once its request is `done` and any review is approved.
  Retire also checks for open challenges and running turns, so a busy specialist is not closed.
- **Idle specialists:** sentinel's daily review lists popped roles idle for more than 2 h with no
  accepted task; master retires them or asks the person.

### Tests (A)

- `roles.toml` with the six roles loads in both schemas (`rolecfg` unit tests); `agentbus roles
  validate` passes; `AGENT_LAUNCH_DRYRUN=1 agent-launch <role> demo` prints the expected command for
  each (dry-run fixtures in `tests/roles_scripts_test.sh`).
- master's derived rules: an allow rule for `support`, `web-design`, `backend`; an **ask** rule for
  `dba`, `os-expert`, `cicd` (`rolecfg/toolrules.go` tests).
- Every role in `roles.toml` has a `roles/<role>.md` (a new check beside the existing manifest
  tests), and `tests/skills_test.sh` asserts the master skill's "Specialists" section names
  `agentbus retire`.

---

## B. The trust prompt

### What Claude Code does (its documentation)

- The trust dialog appears in interactive sessions only. Trust is stored in `~/.claude.json`,
  `projects["<path>"].hasTrustDialogAccepted`.
- **In a git repository the trust is keyed on the repository root, and a worktree uses its main
  checkout's root.** So trusting a project once covers every agent and every worktree of that
  repository. Outside a repository it is keyed on the start directory and covers its
  subdirectories. In the home directory it is held for the session only.
- Observed here: `/data/projects/agent-bus-monitor` is trusted; the worktree paths have no entry of
  their own and need none.

So the dialog appearing "at each agent start in a new folder" happens because `agentbus up`
starts **all** of a new project's agents at once, before anyone has answered: every pane shows its
own dialog. After one acceptance, later starts in that repository show none.

### Options

| | Option | Verdict |
|---|---|---|
| 1 | `up` writes `hasTrustDialogAccepted: true` itself | No: it decides for the person, and `~/.claude.json` is a large file Claude Code rewrites while it runs (sessions, account, statistics); a concurrent write can lose the person's data or our key |
| 2 | `up` asks `Trust <root> for Claude Code? [y/N]` in its terminal, then writes the key | Explicit, but still writes a file we do not own, with the same race; and the person then accepts without seeing the dialog's own list of what the repository would grant (hooks, MCP servers, allow rules) |
| 3 | **Gate the start:** for a repository not yet trusted, `up` starts the **first** Claude agent (master), tells the person to answer the dialog in that pane, and starts the others only once the key reads `true` | **Recommended**: the person answers Claude's own dialog once, with its list of what is granted; we only *read* the file |

### Recommended (3)

- `up` resolves the trust key as Claude Code does: the git root of the project directory (the main
  checkout's root for a worktree), else the directory itself. It reads only
  `projects[<key>].hasTrustDialogAccepted` (and the parent rule outside a repository). No other key
  is read or printed.
- Trusted, or no Claude profile in the template → today's behaviour.
- Not trusted → `up` starts the template's first Claude agent alone, prints
  `Claude Code asks whether you trust <root>: answer it in <agent>'s pane. The other agents start once it is accepted.`,
  then polls the key read-only every 2 s for up to 10 min. Accepted → starts the rest. Declined or
  timed out → starts nothing more, and says so with the command to resume (`agentbus up <p>`). The
  workspace hook, which never starts agents, is unchanged.
- `--dry-run` shows the gate (`trust: not yet accepted for <root>; master first, others after`).
- **Risk statement in the help:** trusting a repository lets its hooks, settings `env` and MCP
  servers run in every agent there. That is the person's decision, made in Claude Code's dialog.

### Other clients

| Client | Where trust lives | Plan |
|---|---|---|
| Codex | `~/.codex/config.toml`, `[projects."<path>"] trust_level = "trusted"` (observed) | Same gate, reading that table only (TOML read, no write). Whether Codex keys on the repository root is to be verified (test plan) |
| Kimi, Grok, agy | not verified on this machine | No gate (today's behaviour) until a reader is verified; a client without a reader is never gated, never written |

### Tests (B)

- `trustKey(dir)`: repository root, worktree → main checkout root, non-repository directory, home.
- `claudeTrusted(file, key)`: true, false, absent, unreadable → not trusted; it never returns any
  other field (the reader takes a key and returns a bool). Fixtures are small hand-written JSON
  files, never the real `~/.claude.json`.
- `up` with a fake herdr and a fake trust file: untrusted → one tab started, then the rest after the
  file flips; timeout → nothing more and the resume message; trusted → all at once. Dry-run text.
- Live (Bernard): `agentbus up` of a new scratch repository, answer once in master's pane, the others
  start without a dialog.

---

## C. Other LLMs as the 4-eyes reviewer

### Why

Today `coder` and `foureyes` both run Claude Opus on the same account: the review is independent by
agent, not by model or provider. A reviewer from another provider catches a different class of
mistakes, and does not share the author's quota.

### "Advanced enough"

Judged from the machine's model table (`agentbus models`, package `models/`): a candidate is a
profile whose client is installed and whose model is in tier **frontier** in a successful catalogue
read under 7 days old. A **workhorse** model is offered only as a second reviewer, never as the only
one. `agentbus roles doctor` must show the launched model matching after the switch. A provider
without a quota reader is offered with that warning (its holds cannot be enforced).

### Where it is proposed

- master, at boot and in sentinel's daily review, compares the table with the reviewer's profile.
  When a frontier model of **another provider than coder's** is available and foureyes is on
  coder's provider, master asks the person (`agentbus ask`), once per (provider, model):
  `Codex <model> (frontier) is available. Use it for review? — as foureyes / as a second reviewer / no`.
- The answer is recorded by the ask itself; `no` is not asked again for that model.

### How it is configured

Requires **schema 2** (profiles). The migration of the live `roles.toml` to schema 2 is part of
this item: it is mechanical (one profile per distinct `model`+`permission` pair) and dry-run outputs
must stay identical, which `tests/roles_scripts_test.sh` already checks byte for byte.

- **As foureyes:** `[roles.foureyes] profile = "review"` with a Codex profile (as in
  `examples/roles.mixed.toml`), and the Claude profile kept as `fallback_profiles = ["claude-opus"]`,
  so `agentbus swap foureyes --profile claude-opus` brings Claude back when Codex is held.
- **As a second reviewer:** a pop role `foureyes2` on the other provider, popped by master for
  money-path changes. Both verdicts go to the ledger; the roll-up needs one independent approve, and
  master's skill asks for both on the money path.

### The independence rule

- Today's rule stays: a verdict counts only when reviewer ≠ author (agent names, `bus/verdicts.go`).
- New, **soft**: when a reviewer from another provider than the author's was available, master asks
  it. The verdict ledger gains two additive fields, `author_provider` and `reviewer_provider`, taken
  from `{p}:agents` when the verdict is written. `agentbus verdicts` then prints
  `same provider` beside an approve whose reviewer shares the author's provider. It does not block:
  a team with one provider must still be able to merge. **That is a code change** (a small one, in
  `cmd/agentbus` and `bus`: additive ledger fields and one output word).

### Tests (C)

- Candidate selection (pure): frontier on another provider → offered as foureyes; workhorse →
  second reviewer only; stale or failed table → nothing; client not installed → nothing.
- Schema-2 migration: the dry-run of every role is byte-identical before and after.
- Ledger: the provider fields are written from the snapshot; `verdicts` shows `same provider` and
  the exit code is unchanged.

---

## D. How skill updates reach running agents

### Current behaviour

- Role skills are **symlinks** from each client's skill directory to the repository's `skills/`
  (`rolecfg/skills.go`); `setup.sh install/update` re-runs `scripts/link-role-skills.sh`, and
  `bootstrap` runs it with `--missing-ok`. So a changed `SKILL.md` is on disk as soon as the
  checkout is updated; a **new** skill or a new role's skills need the re-link (setup does it).
- A running agent loaded its skills at boot ("On boot, once: load your configured skills"). The text
  is in its context; it does not see the new file until it invokes the skill again.

### Recommendation

- After a release that changes `skills/`, master sends each running agent whose role lists a changed
  skill (from `roles.toml`) one `wake=no` directive: `skill <name> changed in <tag>: re-invoke it
  before your next task`. `wake=no`, because nothing is urgent: it is read at the agent's next
  drain.
- The list of changed skills comes from Git (`git diff --name-only <old-tag> <new-tag> -- skills/`),
  run by master or printed by `setup.sh update`, which already knows both versions. A small setup
  addition: one line `skills changed: agent-bus, agent-bus-master`.
- A role prompt change (`roles/<role>.md`) is read only at launch: it needs the reload path of the
  client-reload spec (`/exit`, then `agentbus up <p> --resume`), proposed the same way.

### Tests (D)

- setup's changed-skills line from a fake repository with two tags (installer tests, with the
  existing fakes for docker, herdr and systemctl).
- A unit test for the mapping changed skills → roles → running agents.

---

## Review notes (foureyes, `ea5ffac`: approved)

Settled at review, to be honoured by the implementation; none changes the design above.

1. **The trust reader never exposes the file.** `~/.claude.json` can hold account data and, for
   API-key users, `primaryApiKey`. The reader parses in memory, returns only the bool, and never
   logs, caches or echoes content, including in its errors. A parse error (Claude rewriting the
   file) counts as "not trusted, keep polling".
2. **Worktree keying is documented, not yet observed here.** The absent worktree entries are
   consistent with Claude Code's documentation, not proof. Live check: start Claude Code in a fresh
   worktree of a trusted repository and see no dialog. If it is wrong, the gate times out, which
   fails safe.
3. **`upLock` is held for the whole gate** (up to 10 min), so a second `agentbus up <p>` is refused
   meanwhile; the waiting message says so.
4. **Two reviewers and the roll-up.** With `foureyes` and `foureyes2`, the roll-up reads APPROVED
   when the latest independent approve is newer than any reject, so a later approve from one
   reviewer overrides an earlier reject from the other. That is accepted as the soft rule; master's
   money-path rule reads **both** verdicts (`agentbus verdicts --json`), not the roll-up.
5. **Which skills a reviewer on another client gets.** Only the repository's own skills
   (`agent-bus`, `agent-bus-*`) ship with it. `implement`, `tdd`, `codebase-design`,
   `diagnosing-bugs` and `code-review` are user-installed under `~/.claude/skills`. On another
   machine, or for a Codex reviewer, they are missing and the launch only warns, so a Codex
   `foureyes` gets `agent-bus` and whatever is installed in its own client's skill directory. The
   switch's ask says which.
6. **The costs of a Codex `foureyes`:** it cannot be resumed through the launcher (refused for
   non-Claude clients), and it needs `bind` for the dispatcher to verify its pane (its commands run
   in Codex's shared app-server). Both are stated in the person's ask.

Open questions 1–3 were answered as proposed (the first Claude agent; the provider rule in the skill
only, with point 4; `examples/roles.specialists.toml`).

## Addendum E (Bernard, 2026-10-21): master drafts a role on demand

A task that no role covers: master drafts a specialist from the task's description, reusing the
skills that best match it, and proposes it to the person. **A drafted role is inert: it is never
added to the manifest, linked or popped without the person's yes.**

### `agentbus roles draft <role> --task <words...> [--out <dir>]`

No project, no broker (the `roles` block above the project-required exit).

- Refuses an invalid name (`ValidName`) or a role the manifest already has.
- Candidate skills are those the launcher can resolve: the roots of `skillRoots` (the repository's
  `skills/`, then the external engineering collection). Each skill's `name` and `description` are
  read from its `SKILL.md` front matter; nothing else is read.
- Score: the number of distinct words (four letters or more, minus a short stop-word list) shared by
  the task and the description. The draft keeps `agent-bus` always, plus the three best skills with a
  score of 1 or more (ties by name). No match is a valid draft with `agent-bus` only.
- It writes two files under `<out>/<role>/` (default `<out>` = `.agent-bus/drafts` in the current
  directory, as `bootstrap` keeps its markers in `.agent-bus/`):
  - `roles.toml`: the entry in the manifest's own schema: schema 1 `model = "sonnet"`,
    `permission = "manual"`, `tier = "pop"`, `approval = "person"`, `skills`; schema 2
    `profile = <defaults.profile>`, `tier`, `approval`, `skills`. Least privilege by default: the
    person widens it when they adopt it, never the draft.
  - `<role>.md`: the prompt in `roles/architect.md`'s shape (boot steps; **Scope** = the task text;
    **Never**: decisions go to master, who asks the person; nothing on a live system without an ask;
    **Hand-back**).
- It prints one JSON object (role, files, skills with score and the shared words) that master puts
  in its ask.
- It refuses to overwrite an existing draft directory unless `--force`.

### The person decides: `agentbus ask`, then `roles adopt`

- master asks with the draft's summary and three options: `adopt`, `change` (the person says what in
  a free answer; master redrafts), `discard`.
- `agentbus --project <p> roles adopt <role> --ask <id> [--from <dir>]` runs **only when** the ask
  `<id>` has a question whose answer is the option `adopt`, not withdrawn, answered by someone other
  than the asking agent. Then it checks that the manifest with the entry appended still loads, appends
  the entry to `ROLES_TOML`, and copies the prompt to `ROLES_DIR` (default `<repo>/roles`), refusing to
  overwrite an existing prompt. Skills already resolve, since the draft picked only resolvable ones.
- Adopting does not pop it: popping is master's usual step. The drafted role has
  `approval = "person"`, so Claude Code asks the person in master's pane first.
- **A convenience, not a lock.** The adopt check reads the bus; a process with bus access can write an
  answer. Its value is that master's normal path cannot skip the person's answer.

### Tests (E)

- `draftSkills(task, skills)`: scores, ties, `agent-bus` always first, at most 3 more, stop words.
- `roles draft`: refuses an existing role, an invalid name, an existing draft without `--force`; the
  two files for a schema-1 and a schema-2 manifest; the JSON; the snippet loads when appended (it is
  valid TOML for that manifest).
- `roles adopt` against the test broker: no ask, an open ask, `discard`, a withdrawn answer, an answer
  by the asking agent → refused, nothing written; `adopt` → manifest and prompt written, the manifest
  loads, an existing prompt is never overwritten.
- Master skill: a "Draft a role" section naming `roles draft`, the ask and `roles adopt`.

## Open questions for review

1. B: gate on the first Claude agent, or on master specifically? (Proposal: the template's first
   Claude agent, which is master in every shipped template.)
2. C: the soft provider rule only prints `same provider`; should the money path require a
   different provider when one is installed? (Proposal: not in code; master's skill asks for it.)
3. A: the six specialists in this repository's `roles.toml`, or only in `examples/` for projects
   to copy? (Proposal: in `examples/roles.specialists.toml`, and added to a project's manifest when
   its master first needs one: an unused role costs nothing but makes the pops rules longer.)
