# Project onboarding: `agentbus init` — design

- Status: draft r3, for review (foureyes, reviewer-codex).
  - r2: the check evidence is bound to the reviewed head (§9), and the load's checks run in one
    script (§8.5). Both answer reviewer-codex's r1 reject.
  - r3: answers foureyes's r1 reject: person tasks with a check (§9.4), the draft's ask rules
    (§8.1), the grade's Go reader (§11.1), and the nits (§4.1, §7.3, §8.1, §9.1, §12.2).
  - r4: a person's `passes` is bound to the exact check it answers, both for a task (§9.4) and for
    a grade item (§11.1). This answers reviewer-codex's r3 reject. r4 also takes foureyes's r3
    build points: the board-delivered person path (§9.4), `--commit` (§10.1), the 7-day record
    (§9.4), and the stray text (§12.2).
- Date: 2026-11-27
- Author: architect
- Source: the person's directive, `/data/projects/agent-bus-monitor-doc/directive-master-agentbus-init.md`
  (sent by master, task `onboarding-spec`). Section numbers written "directive §n" point there.
- Branch: `spec/onboarding`

## 1. The need

When the person starts a new project, or brings an existing repository under ABM, nothing helps them
define it. They do it by hand: a goal, a first plan, how to run and test the code. Later sessions then
lose time finding out how to run the app. They also declare victory without a real test. The
directive asks for a guided onboarding that produces these things:

- a build sheet (`BUILD.md`);
- one command that sets up the project and runs its end-to-end check (`scripts/check.sh`);
- a v0 plan of small tasks, each with a check that proves it;
- a v1/v2 direction (`NEXT-DIRECTIONS.md`).

The plan is shown in busmon as a **draft**. Only the person's typed `yes` turns it into the active
plan. When v0 is finished, an independent reviewer grades it against the definition of done, and
the architect proposes the next one or two tasks as ideas.

The constraints come from directive §0:

- It works with any LLM client. The interview runs through bus asks, never through a tool of one
  client.
- It is local. It uses Redis and Git only, and no hosted agent service.
- It works on any forge.
- Nothing is created on the broker or pushed in the person's name without their typed `yes`.

**Out of scope**

- No new progress file. The bus history, the board, the verdict ledger and `state.json` (§6) are the
  log.
- No tour pages. The tour gets one line (§12.7).
- No new review machinery. The existing roll-up and ledger decide done (§9).
- Writing the project's code. The interview defines it, and master runs the plan as usual.
- Several onboardings at once in one project. There is one at a time (§4.1).
- Enforcing that a coder leaves the check scripts alone. That stays a reviewer rule (§9.5, §13).

## 2. What exists and is reused

| Need | Already there | Used as |
|---|---|---|
| Plan file, its tasks and groups, derived states | `plan/plan.go`, `plan/derive.go`, `{p}:plan` | the v0 plan is an ordinary plan file, with two additive fields (§7) |
| Questions to the person | `bus.Ask` / `agentbus ask --file`; busmon renders them; free text is always allowed | the interview rounds (§5) and the adoption question (§8) |
| A person's answer bound to its question | the reply entry's `qid`, `typed=yes` and `said`, plus `agentcaller.Refuse` on `answer` (retro §5, merged in #813 and #822) | the only proof of adoption (§8.4) |
| One approval check | `bus.RetroApproved` (`bus/retros.go:391`) | generalised into `bus.TypedApproval` (§8.4) |
| Draft, then adopt by content hash | `draftQuestionID` and `adoptApproved` (`cmd/agentbus/draftcmd.go:65`, `:93`) | the qid of a draft plan (§8.2) |
| Missions with a machine-checked `test` | `mission/`, `docs/changes/<c>/missions.json` | a task large enough for a mission gets `test` = its check (§7.3) |
| Ideas the person triages | `agentbus idea add`; only the person runs `set` | v1/v2 proposals at wrap-up (§11) |
| Independent review | the verdict ledger and `bus.RollUp` (the reviewer is not the author) | done for a task with a check (§9), and the grade (§11) |
| Popping a role | `agentbus pop <role>`, which the person may run | `init` starts the architect when it is not running (§4.1) |

Directive §6 step 2 (the `qid` on answers, and `agentcaller.Refuse` on `answer`) is already merged.
The build starts at step 3.

## 3. Terms

- **Onboarding**: one run of this flow for one project. It is identified by the project. Its state
  lives in `docs/onboarding/state.json` on branch `onboard/<project>`.
- **Mode**: `greenfield` (nothing to read yet) or `existing` (a repository with code to explore
  first).
- **Round**: one interview ask of at most 6 questions. There are at most 3 rounds.
- **Topic**: one of nine fixed subjects that the interview must settle (§5.2).
- **DoD item**: one item of the definition of done. Each is rewritten as a **check**.
- **Check**: one line, at most 200 characters, naming a command or a scripted scenario that proves
  a task or a DoD item. It is the sprint contract. For an item only the person can verify, it is the
  word `person` and a manual step, owned by `person` (§5.4).
- **Draft plan**: a plan file at a commit, recorded in `{p}:plan:draft`, with its adoption question
  open. It is never `{p}:plan`.
- **Adoption**: the person's typed `yes` to the draft's question, checked by `bus.TypedApproval`,
  followed by master's `plan load` of the same bytes.
- **Grade**: an independent reviewer's verdict on subject `grade:<project>-<plan>`, after it has
  run every DoD check.
- **Wrap-up**: the architect's recap to the person, plus one or two `idea add` from
  `NEXT-DIRECTIONS.md`.

"archi" in the directive is the `architect` role.

## 4. The CLI: `agentbus init`

`init` is a new top-level verb. It needs `--project` like every project command, so it sits below
the project-required exit in `main()`.

### 4.1 `agentbus init [--existing] [--resume]`

`init` is the person's command. Its order is:

1. **Refuse an agent caller.** It uses `agentcaller.Refuse(ctx, getenv, agentcaller.Ancestor,
   "starting an onboarding", "agentbus init")`, the same guard as `answer`.
2. **Check that the project is set up.** The broker must answer, and the project must have keys on
   it (`hasProjectKind`). The roles manifest must define `master` and `architect`. On failure it
   names the missing piece and points to `agentbus setup` or `agentbus start <project>`. Exit 1.
3. **Refuse when a plan is loaded** (question Q3, recommended option): "a plan is active (v0.29,
   6/11 done): finish or clear it first (`agentbus plan clear`, master)". Exit 1.
4. **Refuse when an onboarding exists and `--resume` is not given.** An onboarding exists when
   branch `onboard/<project>` exists locally (`git rev-parse --verify`) or on the delivery
   remote. The remote is read with `git ls-remote --heads <remote> onboard/<project>`, which needs
   no fetch and writes no ref. A remote that cannot be read is printed as a warning, and the local
   answer stands. The message names its phase, read
   from `state.json` (§6), and says to run `agentbus init --resume` or `agentbus init status`.
5. **Detect the mode.** The repository is `existing` when its base branch holds at least one commit
   with a file other than `README*`, `LICENSE*`, `.gitignore` and `.gitattributes`. Otherwise it is
   `greenfield`. `--existing` forces `existing`, for a nearly empty repository the person wants
   read. The mode is printed, and no confirmation is asked.
6. **Start the architect if it is not running.** The test is the same as `agentbus agents`
   (verified pid). If it is not running, `init` runs the same start as `agentbus pop architect` and
   prints the line `pop` prints.
7. **Send the architect one directive.** It is `bus.HumanCmd`, so it is `human=yes` but not
   `typed=yes`. The reason: `agentbus` composes this text, and `TypedCmd` is reserved for words the
   person typed (`bus/stream.go`, `TypedCmd`'s comment). The text is fixed:

   `onboarding <project>: mode <greenfield|existing>, repo <path>, base <base>, branch
   onboard/<project>; follow the agent-bus-onboard skill<, resume from state.json>`

   The dispatcher wakes the architect as for any directive.
8. **Print what happens next**: "the architect will ask you up to 3 rounds of questions in busmon
   (For you)".

`--resume` skips step 4's refusal. It also refuses when no onboarding exists, or when the
onboarding's phase is `wrapped`. Then it runs steps 6 and 7 with ", resume from state.json". Any
architect, on any client, continues from `state.json` and the open asks (§6.3).

### 4.2 `agentbus init status [--json]`

`status` is read only, so anyone may run it. It prints:

- the phase and mode, read from `git show onboard/<p>:docs/onboarding/state.json`;
- what waits for whom: the open asks with `task=onboarding` (the person); the draft record
  (§8.1) and whether `TypedApproval` passes (the person, or master to load); the loaded plan's
  progress (`plan.Derive`, the team); the grade verdict (a reviewer);
- each topic's state (§5.2);
- **a disagreement**, when `state.json` and the live facts differ. For example: `state.json` says
  `drafted`, but the draft record is gone. Both values are printed, and the live fact is marked as
  the truth.

`--json` prints the same facts as one object. Exit codes: 0 when an onboarding exists, 1 when none
does, 2 on a read error.

### 4.3 `agentbus init grade [--json]`

`grade` is read only, so anyone may run it. It finds the newest verdict on subject
`grade:<project>-<plan>`, reads `BUILD.md` at the graded commit, and prints `onboard.Grade`'s result
per DoD item (§11.1). It exits 0 when every item passes, 1 when any item fails or is missing, and 2
on a read error.

### 4.4 `agentbus init wrap-up`

`wrap-up` is the person's command, with the same guard as `init`. It re-sends the architect the
wrap-up directive (§11.2). Normally the wrap-up starts by itself after a passing grade. This command
covers two cases: the person wants the recap again, or the grade passed while the architect was
down.

## 5. The interview

### 5.1 Rounds are bus asks

Each round is an ordinary ask, `agentbus ask architect --task onboarding --file
docs/onboarding/rounds/<n>.json`. The file is an `AskDef` exactly: `from: architect`,
`task: onboarding`, a title `Onboarding <project>: round <n>/3`, and 1 to 6 questions. So the round
file format **is** the ask format. `bus.ValidateAskDef` applies, with these limits: a header of 16
characters, a question of 300, a context of 2000, and 1 to 6 options. Each question has 2 to 4
options, exactly one of them recommended. busmon's free text is the "Other".

Busmon renders a round like any ask, and every client can open one. The interview never uses a
client's own question tool. The round file is committed before the ask is opened (§6.2), so the
person's questions are on record even after the ask expires.

The architect holds **one open ask at a time**: the `open:<from>` slot in `{p}:asks`. If the
architect has another ask open, for example on another spec, the round waits. `init status` says
"round 2 waits: architect has ask <id> open". The architect may withdraw its own other ask. It
never replaces it.

### 5.2 Topics and question ids

A question's id is its topic. A question asked again in a later round gets a `-<round>` suffix. A
DoD item sent back gets `dod-<k>`.

| Topic id | Settles |
|---|---|
| `goal` | the goal, in one sentence |
| `users` | who uses it, and their main job |
| `constraints` | licence, hosting, data, security, budget, deadline |
| `stack` | the stack, confirmed (existing mode) or chosen |
| `dod` | the definition of done: observable, checkable items |
| `out-of-scope` | what v0 does not do |
| `run-test` | how to run and test it locally |
| `risks` | risks and unknowns |
| `v0-size` | the size of v0: number of tasks, and the deadline if any |

Each topic in `state.json` is in one of four states:

- `open`;
- `answered`: the person's answer, and the reply entry's id;
- `unknown`: the person said "unknown, decide later". This is an answer, and it is recorded as
  such;
- `repo`: existing mode only. The repository answered it, and the evidence path is recorded.

The interview stops when no topic is `open`. Three rounds is the hard limit. A topic still open
after round 3 becomes `unknown`, and `BUILD.md` lists it under "Risks". Round 1 asks the hardest
topics first, following directive §3.2: "Don't ask the obvious, dig into the hard parts". `goal` and
`dod` are always in round 1, unless they are `repo`.

### 5.3 Existing mode: read first, never ask what the repository says

Before round 1, the architect explores read only: structure, build files, test commands, CI
configuration, README, and the open issues when the forge CLI works. Each finding works in one of
two ways:

- **It settles a topic.** The topic becomes `repo`, with the evidence path, and is not asked. For
  example, `go.mod` and `.github/workflows/ci.yml` settle `stack` and part of `run-test`.
- **It suggests an answer.** The question is asked. The repository's answer is the recommended
  option, and the evidence is in the question's `context`: "go.mod says Go 1.22; the CI runs `go
  test ./...`".

No exploration step writes to the repository, runs its code, or runs its tests before the person
has answered `run-test`. Running unknown code is the person's call.

### 5.4 Every DoD item becomes a check

The architect rewrites each DoD item as a check, of one of these kinds:

- a command (`go test ./store/...`);
- a test file it names (`npx playwright test notes.spec.ts`);
- a scripted scenario (`scripts/check.sh notes`);
- a manual step owned by `person` ("person: open the app on the phone, add a note, see it after a
  restart").

**An item that cannot be written as a check is not accepted.** In the next round it becomes a
`dod-<k>` question: "'fast' cannot be checked as written; which of these do you mean?". The options
are measurable rewrites, and one of them is recommended. After round 3, an uncheckable item goes to
`NEXT-DIRECTIONS.md` as "to define". It is not in the v0 definition of done.

User-facing items must exercise the running app where possible: a headless browser test, or an HTTP
scenario against the started server. A unit test alone does not prove a feature the person uses
(directive §4).

### 5.5 What the person's answers become

The person's words are not copied into the repository verbatim (question Q5, recommended option).
After each round, the architect writes the **result** into the `BUILD.md` sections it settles, in
its own words, and commits it. That commit is the round's record. `state.json` keeps each answer's
reply entry id and a SHA-256 of the answer, so a later reader can prove which answer a section came
from, as long as the stream or the archive still holds the entry. The cmd stream rolls in about a
day, and an ask record is kept for seven days. That is why the result must be in `BUILD.md`
before the next round.

## 6. Files and `state.json`

### 6.1 The files

All the files are written by the architect, on branch `onboard/<project>`, in its own worktree. The
branch is cut from the project's base (`[delivery] base`, else the forge default).

| File | Content |
|---|---|
| `docs/onboarding/state.json` | the onboarding's state (§6.3) |
| `docs/onboarding/rounds/<n>.json` | each round's `AskDef`, exactly as asked |
| `BUILD.md` | the build sheet, with fixed headings (§6.4) |
| `scripts/check.sh` | the one command that sets up, starts what is needed, and runs the end-to-end check. With an argument, it runs one task's part (`scripts/check.sh auth`). In existing mode, the repository's own equivalent when there is one (`make check`), named in `BUILD.md`. |
| `docs/plans/<plan>.json` | the v0 plan (`v0`), later `v0.1` (§11) |
| `NEXT-DIRECTIONS.md` | the v1 and v2 items, each with its acceptance criteria; not planned |

The architect never pushes. Master delivers branch `onboard/<project>` as a PR to the base after
the adoption (§8.5), through the usual two reviews. Until that merge, the plan's commit lives on the
branch. `plan load` reads Git at a commit, so a branch commit is enough.

### 6.2 Commits: one per phase step

The architect makes one commit for each of these steps:

- round `<n>` asked: the round file, plus `state.json` with the round's ask id;
- round `<n>` answered: the updated `BUILD.md` sections, plus `state.json`;
- drafted: the plan, `scripts/check.sh` and `NEXT-DIRECTIONS.md`, then `state.json` with the
  draft's qid and ask (§8.1). The draft is recorded at the commit that holds the plan and
  `BUILD.md`, so `state.json` comes in the commit after it;
- every later phase change: `state.json` alone.

A commit message names the phase: `onboarding <p>: round 2 answered`.

### 6.3 `state.json`, version 1

```json
{
  "version": 1,
  "project": "notes-app",
  "mode": "greenfield",
  "branch": "onboard/notes-app",
  "base": "dev",
  "phase": "drafted",
  "rounds": [
    {"n": 1, "file": "docs/onboarding/rounds/1.json", "ask": "1795900000000-0", "answered": true},
    {"n": 2, "file": "docs/onboarding/rounds/2.json", "ask": "1795905000000-0", "answered": true}
  ],
  "topics": {
    "goal": {"state": "answered", "reply": "1795900100000-0", "sha256": "9f2c…"},
    "stack": {"state": "repo", "evidence": "go.mod"},
    "risks": {"state": "unknown", "reply": "1795905100000-0", "sha256": "11ab…"}
  },
  "draft": {"plan": "v0", "path": "docs/plans/v0.json", "commit": "3f9c2ab…", "qid": "plan-5be0…", "ask": "1795901234567-0"},
  "loaded": {"plan": "v0", "commit": "3f9c2ab…"},
  "grade": {"subject": "grade:notes-app-v0", "verdict": "", "result": ""},
  "ideas": [],
  "history": [{"phase": "interview", "commit": "a1b2c3d…"}, {"phase": "drafted", "commit": "3f9c2ab…"}]
}
```

- It is parsed strictly, and unknown fields are refused, like a plan. `version` is checked, and a
  later version is refused by an older `init status` with "upgrade agentbus".
- `phase` is one of these values:

  | From | To | When | Who records it |
  |---|---|---|---|
  | (none) | `interview` | the branch's first commit | architect |
  | `interview` | `drafted` | `plan draft` succeeded (§8.1) | architect |
  | `drafted` | `interview` | the person answered `no` or free text: the architect revises | architect |
  | `drafted` | `approved` | `TypedApproval` passes on the draft | architect, on the reply |
  | `approved` | `v0` | master's `plan load` of the draft succeeded (`loaded` filled) | architect, on master's report |
  | `v0` | `graded` | the grade verdict is `approve` (§11.1) | architect |
  | `v0` | `drafted` | the grade is `reject`: the failures become a `v0.1` draft | architect |
  | `graded` | `wrapped` | the recap is sent and the ideas are added (§11.2) | architect |

- `state.json` is a record, not a lock. When it disagrees with the board, the asks, the draft
  record, the plan or the ledger, the live fact wins, and `init status` shows the disagreement
  (§4.2). A resumed architect first reconciles `state.json` with the live facts and commits the
  correction.
- `topics.*.sha256` is the SHA-256 of the answer's `said` value. The words themselves are not
  stored (§5.5, Q5).

### 6.4 `BUILD.md`: fixed headings

`plan draft` (§8.1) and busmon read `BUILD.md`, so its headings are fixed, in this order:

```
# <project>: build sheet (v0)
## Goal
## Users
## Constraints
## Stack
## Definition of done
- [dod-1] <item> — check: <check>
## Out of scope
## How to run
## How to verify
## Risks
## Decisions
```

`## How to verify` names the command a coder runs first (§10.1). It is `scripts/check.sh` unless
existing mode named another. `## Grade v0` is added at grade time (§11.1).

`plan draft` refuses a `BUILD.md` that lacks one of these headings, or whose `Definition of done`
has an item without `— check:`. The check is the same function the build's tests call:
`onboard.ParseBuild`, in a new small package `onboard/`.

## 7. Plan file: `check` and `checks`

### 7.1 Two additive fields

- `Task.Check string \`json:"check,omitempty"\``: the task's check. It is one line of 1 to 200
  characters, with no control character.
- `File.Checks string \`json:"checks,omitempty"\``: `""` (optional, today's behaviour) or
  `"required"`.

When `checks` is `"required"`, every agent task, whatever its owner and whether or not it is a
mission, must have a `check`. A `person` task may have one, which is then a manual step.
`Validate` refuses a missing check with "plan: task <id>: a check is required (checks: required)".
Plans without these fields parse exactly as today.

### 7.2 Ship the reader first

`Parse` refuses unknown fields. An older `agentbus` or busmon would therefore refuse a plan that
carries `check`, and the Plan tab would read "no plan". Build step (a) ships the reader. No plan
file uses these fields before (a) is released and installed. The same rule was applied in
plan-chantier §2.2.

### 7.3 Missions

A task that is mission sized may carry `mission: <change>/<id>`. That mission's `test` in
`missions.json` must then be the task's check, character for character. `plan.CheckMissions(f File, missions
func(change string) (map[string]string, error))` compares them. It reads
`docs/changes/<c>/missions.json` at the same commit, and it is called by `plan draft` **and** by
`plan load`, on any plan whose mission task has a check, whether or not `checks: required` is set.
A mismatch is refused, and a `missions.json` that cannot be read is refused too. `Validate` does
not read files, so it cannot hold this rule. The conductor already
runs `test`, so for a mission the check is enforced by the conductor.

## 8. The draft, and its adoption

### 8.1 `agentbus plan draft <path>@<commit> [--build <path>]`

**Who.** The architect or master runs it, and nobody else. It refuses any other agent's
environment, and the person's shell too: `AGENT_BUS_AGENT` must be `master` or `architect`. The
person approves drafts and never asks themselves (§8.4: the asker cannot approve). When a draft is
recorded, only its recorder (`by`) may re-draft it. Anyone else must `--drop` it first. It works in
this order:

1. **Read the plan.** It reads `<path>` at `<commit>` from Git and runs the same `plan.Parse` as
   `plan load`.
2. **Read the build sheet.** It reads `BUILD.md` (or `--build`) at the same commit. When the plan
   has `checks: required`, it is required and checked by `onboard.ParseBuild`. Otherwise it is
   optional.
3. **Check missions** (§7.3).
4. **Compute the qid**: `plan-` followed by 16 hex digits of
   `SHA-256(path ‖ 0x00 ‖ plan bytes ‖ 0x00 ‖ BUILD.md bytes)`. The `BUILD.md` bytes are the empty
   string when there is no `BUILD.md`. This is `draftQuestionID`'s scheme, so the yes binds the
   path, the plan and the build sheet the person saw.
5. **Build the adoption question** in code. The caller does not write it. The question is a
   function of the qid's inputs only, so the same qid always gives the same wording:
   - its `id` is the qid, and its `header` is "Adopt <plan>";
   - its text is `Adopt plan v0 (docs/plans/v0.json, 9 tasks) as the active plan? id plan-5be0…`;
   - its `context` holds the `BUILD.md` goal (clipped) and the commit. The commit is not part of
     the question, so `SameQuestion` is unaffected;
   - its options are `yes`, `no` and `later`, and none is recommended: adopting is the person's
     own call.
6. **Open or replace the ask** (`from` = the caller, `task: onboarding`):
   - **Replace** only when a draft is recorded, its ask is still **open**, and its `from` is the
     caller: `Ask(def, draft.ask)`. The old question can no longer be answered.
   - **Open a new ask** in every other case: no draft, or the old ask was answered (`no`, `later`,
     free text), replaced or expired. An answered ask needs nothing more. `TypedApproval` reads the
     record's `ask`, so an answer to an older ask approves nothing.
   - **The caller has another ask open** (the one-open-ask slot, `ASK_OPEN <id>`): refused before
     anything is written. The message is "you have ask <id> open: wait for its answer or withdraw
     it, then run plan draft again". The round rule of §5.1 applies to the draft too.
7. **Write `{p}:plan:draft`** in one script: `{plan, path, commit, file, build, qid, ask, by, at}`.
   `at` is Redis `TIME`, and `build` holds `BUILD.md`'s bytes, opaque like `PlanRecord.File`. The
   script refuses when the record changed since step 6 read it, for example after a concurrent
   draft. If the write fails, the ask just opened is withdrawn, and the command exits 1.
8. **Print** the qid and the ask id as JSON, as `roles draft` does.

Its other verbs:

- `plan draft show [--json]` prints the record, plus `TypedApproval`'s result.
- `plan draft --drop` deletes the record. When the caller is the ask's `from` and the ask is open,
  it also withdraws the question. Otherwise it leaves the ask alone and says so; an answer to it
  approves nothing, because no record names it. Like `plan draft`, `--drop` is master's or the
  architect's.

The key is a new kind, `plan:draft`. In `keyKinds` it is `KeyRecord`. It is **not** in
`aclMasterOnly`, because the architect writes it, so the agent ACL user can write it. That is
deliberate. The key decides nothing: §8.4 decides.

### 8.2 What the person sees

The ask is in For you like any ask. busmon's Plan tab draws the draft (§10). The person answers in
busmon or with `agentbus answer`. Both write the reply entry with `typed=yes`, `said` and `qid` in
one script (`bus/asks.go:328`). `answer` refuses an agent caller, and checks the process tree too.

### 8.3 Answers

- **`yes`** goes to §8.4.
- **`no`, or free text**: the architect reads it as a revision request. It moves the phase back to
  `interview` (§6.3), revises the files, commits, and runs `plan draft` again. The new qid differs
  from the old one, so the new draft needs a new yes. A revision needs no new round unless the
  answer raises a new topic.
- **`later`**: the draft stays recorded, and its ask is answered. The architect does nothing
  more until the person runs `agentbus init --resume`, or tells it by directive. On resume, the
  architect runs `plan draft` again on the same files. That gives the same qid and the same
  wording, in a **new** ask (§8.1 step 6), which the person answers.

Scope questions ("keep, drop or later this group?") are not asked on the adoption ask. A yes
adopts the whole file, so a per-task "drop" on the same ask would contradict it. When the plan has
more than 10 tasks (question Q2, recommended option), the architect asks **one scope round before
drafting**: one question per group, at most 5 (keep / drop / later). It revises the plan, then
drafts. A plan with more than 5 groups gets one free-text question instead: "which groups do you
drop or postpone?".

### 8.4 `bus.TypedApproval(ctx, askID, qid, choice) (entryID string, err error)`

This is the one check of an approval. It generalises `RetroApproved`'s body. It returns the
approving entry's id, or why there is none:

1. While the ask's record is kept (`HExists {p}:asks <askID>`), its answer to `qid` must exist, not
   be withdrawn, have `Choice == choice`, and have `By != ask.From`. The last condition comes from
   `adoptApproved`: the asker never approves itself.
   With §8.1's "who" rule, the asker is always master or the architect, never the person.
2. The reply entry must exist, read from the cmd stream and then from the archive when one is set:
   `type=reply`, `ref=askID`, `qid=qid`, `typed=yes`, and the last such entry. Its `said` must
   equal `choice`.
3. Anything else is refused with a reason, and a failed read is an error, never a yes.

`RetroApproved` becomes `TypedApproval(rec.AskID, "p<n>", "yes")` after its own lookups. Its tests
pass unchanged, and they stay the guard against a behaviour change. `SetRetroArchive` is renamed
`SetApprovalArchive`, and every caller is updated in the same PR. A grep of the open PRs comes
first: a rename can break a sibling PR's new caller without a conflict.

**The adoption is approved** when:

- `{p}:plan:draft` exists;
- the qid recomputed from the file and the build it stores equals its `qid`;
- `TypedApproval(draft.ask, draft.qid, "yes")` passes.

### 8.5 Loading: only the approved draft

`plan load <path>@<commit>` (master) changes as follows:

- **A draft is recorded.** The load is in two parts.
  1. **In Go.** It reads the plan and `BUILD.md` from Git at the named commit and recomputes their
     qid. It refuses when that qid is not the draft's: "this file is not the draft the person
     approved (qid plan-… ≠ plan-…)". It runs `TypedApproval(draft.ask, draft.qid, "yes")`, with
     the archive. It refuses when that fails: "a draft awaits the person's yes".
  2. **In one script** (`planLoadDraft`), which re-checks everything the broker holds before
     writing anything. Its keys are `{p}:plan`, `{p}:plan:facts`, `{p}:plan:ignore`,
     `{p}:plan:draft`, `{p}:asks` and `{p}:cmd`. Its arguments are the qid, the ask id, the plan
     bytes and the `BUILD.md` bytes read from Git, and the approving entry's id. The script
     refuses, and writes nothing, unless all of these hold:
     - the draft record exists, and its `qid`, `ask` and `path` equal the arguments;
     - its stored `file` and `build` equal the bytes read from Git, byte for byte. This is
       stronger than the hash, and needs no SHA-256 in Lua;
     - while the ask's record is kept, the field `<ask>|a|<qid>` decodes to `choice == "yes"`, not
       withdrawn, with `by` not the draft's `by` (the asker);
     - while the approving entry is still in the stream (`XRANGE {p}:cmd <id> <id>`), it has
       `type=reply`, `ref=<ask>`, `qid=<qid>`, `typed=yes` and `said=yes`.

     An entry that has only left the stream was already checked in Go, from the archive. Stream
     entries and ask answers can never be edited: the answer script refuses a second answer
     (`ASK_ANSWERED`). So the re-check closes the window in which the draft is replaced, the
     only state that can change between the two parts.

  Only then does the same script:
  - set `{p}:plan`;
  - apply the facts rule of today's load;
  - delete `{p}:plan:ignore` and `{p}:plan:draft`.

  A re-draft (`plan draft` again) or a `--drop` between part 1 and part 2 changes or removes the
  record, so the load fails with "the draft changed after the approval check: load again". The
  record keeps the approving entry's id (`PlanRecord.Approved`, additive).
- **No draft is recorded, and the plan has `checks: required`.** Refused: "a plan with checks
  required is loaded only as an approved draft (agentbus plan draft)". Master therefore cannot load
  an onboarding plan without the person's yes.
- **No draft is recorded, and the plan has no `checks: required`**: as today. Existing projects and
  plans are unchanged.

After the load, master sets priorities (`plan priority`, the person's) and dispatches as usual.
Board requests come from master's normal dispatch, never from the architect. Master reports the
load to the architect. The architect records `v0` and asks master to deliver `onboard/<project>`.

## 9. Done needs the check, run by a reviewer

### 9.1 The evidence line, bound to a head

A task with a check is done only when its review rolls up `APPROVED` (`bus.RollUp`: the reviewer
is not the author, and the last independent approval follows the last reject), **and** the deciding
verdict proves the check ran on the head that is the task's result. Two conditions make that proof:

- **The verdict names its head.** The verdict is recorded with `--head <sha>` (forge-delivery's
  `Verdict.Head`, a full SHA, `bus.ValidHead`). A verdict without a head proves nothing here.
- **Its text quotes the run at that head.** The text read is `Full` when it is set, else `Message`.
  It must hold one line of this form, followed by up to 10 lines of output or a screenshot path:

  ```
  check: <the task's check, verbatim> => exit <n> at <sha>
  ```

  The line passes only with `exit 0` and `<sha>` equal to `Verdict.Head`. The match collapses
  whitespace runs to one space on both sides.

A line with `exit <n>` other than 0 is the label `check failed`. Anything else is the label
`approved, check not run`. In both cases the task stays `InReview`. One function
decides it: `plan.CheckEvidence(v bus.Verdict, check string) (head string, ok bool)`. It is built
on the line parser `plan.EvidenceLines(text) []Evidence{Item, Check, Exit, Head}`, which the grade
(§11.1) reads too. Derive and `plan done` call `CheckEvidence`.

**The head that must have been checked** is `plan.ResultHead(t, fact, in)`:

| Task | Its result head | Read from |
|---|---|---|
| merge task, queued for the person's merge | the merge-queue item's `Head` for its review subject | `in.Merge` (already read) |
| merge task, merged | the head that was merged, recorded by `plan done` (§9.3) | `fact.Head` (new `PlanFact` field `head`) |
| `merge: false` task | the commit its board request was delivered at (`request done --commit`) | `in.Board[t.Board].Request.Commit` |

### 9.2 Derive

These changes apply in `plan/derive.go` to an **agent** task (owner not `person`, no mission) whose
`Check` is not empty. Person tasks follow §9.4, and mission tasks are unchanged. The **decider** is
the approving verdict `RollUp` picks.

- The review label is `Approved` only when `rollUp` says `Approved` **and** `CheckEvidence`
  passes on the decider. Otherwise it is `approved, check not run`, with state `InReview` and the
  reason "the approving verdict does not quote the check at its head".
- **A head that moved is not approved.** When `ResultHead` is known and differs from the
  decider's head, the label is `approved, head changed`. The state is `InReview`, with the reason
  "re-review: approved at <sha7>, now <sha7>". This covers a PR updated after review, a request
  re-delivered at another commit, and a queued head that changed. The merge task does not reach
  `Waiting` for the person's merge until the queued head is the approved head.
- **A `merge: false` task with no recorded commit** is not done: reason "delivered without
  --commit: the checked head is unknown".
- **A recorded `fact.Done`** counts only when `fact.Head` equals the decider's head. Otherwise the
  row is `InReview`, with the reason "recorded done without an approved check at the merged head". A
  fact written by any path therefore never shows done without the check at the head that landed.
- **A mission task** is unchanged: the conductor ran its `test`, which is its check (§7.3), on the
  mission's own head.

Tasks without a check derive exactly as today. The golden tests of existing plans must stay
byte-identical.

### 9.3 `plan done <task>`

For an agent task with a check, `plan done` works out the merged head itself. Master never types it:

- **A task linked to a PR**: the merged PR's head, read through the forge layer deliver already
  uses: `headRefOid` with `state MERGED` on GitHub, and the equivalent fields on GitLab and
  Bitbucket.
- **A task linked to a ref** (local delivery): the ref fact's sha.
- **A `merge: false` task**: the request's recorded commit.

A head that cannot be read refuses the command (fail closed): "cannot read PR 412's merged head:
<error>". The command then reads the verdicts of `plan.Subjects(t, fact)`, and refuses unless the
decider passes `CheckEvidence` at exactly that head: "<task>: the merged head <sha7> was not
approved with the check (approved: <sha7> or none); ask a reviewer to run the check at <sha7>". On
success, the done fact and its `head` are written in the same script (`planFactSet`, op `done`,
value the head). The ACL already makes this master's alone.

This works with deliver's existing rule, which pushes only a head whose newest independent
approval names that head (`cmd/agentbus/deliver.go:365`). That rule stops a stale head before it
is pushed. This one stops it when a head changed after the push, for example by someone pushing to
the PR branch.

### 9.4 Person tasks with a check

A `person` task has no review, so §9.1 to §9.3 do not apply to it. Its check is a manual step,
and its proof is the person's typed answer **to that exact check**.

- **The question's id binds it.** The id is `check-` followed by 16 hex digits of
  `SHA-256(plan name ‖ 0x00 ‖ task id ‖ 0x00 ‖ check text)`. This is `plan.PersonCheckQID(f File,
  t Task)`. A changed check, the same task id in another plan, or another task gives another id.
  An old `passes` therefore never answers a new check. The question's text quotes the check, and
  is a function of these inputs only.
- When the task is `Waiting`, master (or the architect) asks that one question with
  `--task <task id>`. Its options are `passes` and `fails`, plus the free text busmon always offers.
- **`plan done <task>`** on a person task with a check recomputes the id from the **active** plan,
  and finds the asks whose `task` is the task id and that hold a question with exactly that id. It
  runs `TypedApproval(<ask>, <id>, "passes")` on each, newest first, and the first that passes
  approves. It refuses when none passes: "<task>: the person has not answered passes to its current
  check (id check-…)". On success, the done fact records the approving entry's id and the id
  answered (`PlanFact.Approved`, `PlanFact.ApprovedQID`, additive).
- **An ask record is kept 7 days after it closes.** After that, `plan done` cannot find it, and
  refuses with the same message. Master then asks the question again. This is expected, not a bug:
  the record is the proof `plan done` reads.
- **Derive** counts a person task's `fact.Done` only when `fact.Approved` is set **and**
  `fact.ApprovedQID` equals `PersonCheckQID` of the task in the loaded plan. Only the guarded
  `plan done` writes them, and `plan:facts` is master-only. A plan reload that changes the check
  therefore un-does the task. Without both, the row stays `Answered`, with the reason "recorded done
  without the person's passes to the current check".
- **The board path is closed too.** Today a person task whose board entry is delivered is `Done`
  with no fact (`plan/derive.go:142`). For a person task **with a check**, that case no longer
  makes it done. It needs the same `fact.Approved` and `ApprovedQID` as above. Without a check, the
  path is unchanged.
- A `fails` or a free text is read by master as today's `Answered`: master routes it, and the task
  is not done.
- A person task **without** a check derives exactly as today.

### 9.5 The check is a contract: nobody weakens it

- The check lives in the plan file. Changing it is a new plan commit. With `checks: required`, a
  load is only an approved draft (§8.5), so every change of a check, stronger or weaker, needs the
  person's yes again. This answers directive §4's "archi decides": **no exception for
  strengthening.** A single rule (the qid is the bytes) is easier to trust than a judgement about
  which edit strengthens. The cost is one question, answered in busmon.
- A coder who finds a check wrong does not edit it. The coder raises
  `agentbus challenge architect --subject check:<task>` with the reason. The architect revises the
  plan and re-drafts.
- A check names scripts and tests in the repository, and a PR could weaken them. The reviewer
  rule (§10.2) treats a diff of `scripts/check.sh` or of a file the task's check names as a reject,
  unless the task itself is about that check. This is a review rule, not an enforcement (§13).

## 10. Prompts and skills

### 10.1 Coder (`roles/coder.md`, `roles/coder2.md`)

The role files gain one section:

> **Onboarded projects.** When the repository has `docs/onboarding/state.json` on the base branch,
> start every session by running the command under `BUILD.md`'s "How to verify" (usually
> `scripts/check.sh`). If it fails before you change anything, report it to master
> (`agentbus report coder --to master "check red on <base>@<sha>: …"`) and do not hide it in your
> task. A task's `check` is its contract: never edit, weaken or delete it, or a file it runs. If it
> is wrong, `agentbus challenge architect --subject check:<task>`. Your PR description quotes the
> check and its result. For a `merge: false` task with a check, finish the request with
> `agentbus request done <task> --commit <sha>`, where `<sha>` is the head the reviewers check. Without
> it, the checked head is unknown and the task never reaches done (§9.2).

### 10.2 Reviewers (`roles/foureyes.md`, `roles/reviewer-codex.md`)

The role files gain this section:

> **Tasks with a check.** Run the task's check yourself, on the PR's head, in your own copy. Never
> take the coder's word for it. Approve only on `exit 0`, and put this line in your verdict:
> `check: <check verbatim> => exit <n> at <head sha>`, followed by up to 10 lines of output or a
> screenshot path, and record the verdict with `--head <head sha>`. busmon and `plan done` read that
> line. A new push after your verdict needs a new run and a new verdict. For a user-facing task, the check must exercise the running
> app: a unit test alone is a reject. A diff of `scripts/check.sh`, or of a file the check runs, is
> a reject unless the task is about that check.

### 10.3 Master (`skills/agent-bus-master`)

The skill gains these rules:

- An onboarding plan is loaded only with `plan load` after the person's yes. A refusal names the
  reason, and master forwards it, never works around it.
- When an onboarding plan reaches 100% done, tell the architect "grade <plan>".
- Deliver `onboard/<project>` after the load, through the usual two reviews.

### 10.4 Architect: a new skill `skills/agent-bus-onboard/SKILL.md`

The architect's `roles.toml` skills list gains `agent-bus-onboard`. The skill is
client-agnostic: plain shell and `agentbus` only. It holds:

- the flow (§4 to §6, §8, §11) as steps, with the exact commands;
- the topic table (§5.2) and the stop rule;
- the round-file template;
- the rule "never ask what the repository says" (§5.3);
- the check-writing rules and the "send it back" rule (§5.4);
- the `BUILD.md` and `NEXT-DIRECTIONS.md` templates (§6.4);
- the `scripts/check.sh` skeleton: `set -eu`, a `case "${1:-all}"`, exit codes;
- the state transitions (§6.3);
- the resume procedure: read `state.json`, reconcile it with the asks, the draft and the plan,
  commit the correction, continue.

## 11. Grade and wrap-up

### 11.1 The grade

When master reports that the plan is 100% done, the architect commits `BUILD.md`'s DoD list
unchanged. It then raises `agentbus challenge foureyes --subject grade:<project>-<plan>` with the
list and the commit to grade. The reviewer is not the architect and not an author of the plan's
tasks: master picks `reviewer-codex` when `foureyes` authored one.

The reviewer runs every DoD check at that commit. Its verdict holds one evidence line per item,
in the §9.1 format prefixed with the item id, `dod-3 check: … => exit 0 at <sha>`, and a PASS/FAIL
table. The verdict is recorded with `--head <sha>`, where `<sha>` is the graded commit. The
threshold is hard: **approve only when every item passes.**

The grade is read by Go, not only by the skill: `onboard.Grade(build BuildSheet, v bus.Verdict,
person func(item string) (string, error)) GradeResult`. It reads the verdict's lines with
`plan.EvidenceLines` and matches them **by item id** (`dod-<k>`), so two items with the same check
are told apart. An agent item passes with `exit 0` at the verdict's head, and a manual item passes
with the person's typed `passes` (`TypedApproval`, the `person` callback). It returns PASS, FAIL or
missing per item. Two commands call it:

- `init status` prints the per-item result;
- `init wrap-up`, and the architect's `v0 → graded` step, which runs `agentbus init grade --json`,
  a read-only form of the same call. Both refuse unless every item passes. `state.json` is never
  moved to `graded` on the skill's word alone.

**Manual items** (`person`) are not run by the reviewer. The architect asks them in one ask, one
question per item ("passes / fails", plus free text). Each question's id binds it to this grade
and this item's current text. The id is `grade-` followed by 16 hex digits of
`SHA-256(project ‖ 0x00 ‖ plan ‖ 0x00 ‖ graded commit ‖ 0x00 ‖ item id ‖ 0x00 ‖ item text ‖ 0x00 ‖
check)`: `onboard.GradeQID`. A pass from an earlier grade, from another commit, or for an item whose
text or check changed has another id, and does not count. The reviewer's verdict cites the reply
entries.

`onboard.Grade`'s `person` callback receives that id, and runs `TypedApproval(<ask>, <id>,
"passes")` on the asks holding a question with that id. A typed `passes` to the current id counts
as a pass (question Q4, recommended option: every item must pass, and manual items are answered by
the person). An "accepted with a note" outcome does not exist.

The architect then copies the verdict into `BUILD.md`, under `## Grade v0`, with the verdict id,
and records the result in `state.json`:

- **approve**: the phase becomes `graded`, then the wrap-up follows (§11.2).
- **reject**: each failing item becomes a task of `docs/plans/v0.1.json`, with the same checks and
  `checks: required`. The architect drafts it (§8.1), and the person's yes is needed again. While
  v0 is loaded, `init`'s "a plan is loaded" refusal does not apply: the architect runs `plan draft`
  directly. busmon shows the active plan and the draft together (§12, the last mock-up).

### 11.2 The wrap-up

The wrap-up starts after a passing grade, or with `agentbus init wrap-up`. The architect then does
three things:

1. It sends the person one report: `agentbus report architect --to <person> "onboarding
   <project>: v0 passed (9/9, grade <verdict id>)"`. The first line is the summary, and the body
   is:
   - what was built;
   - what ABM now holds for the project: `BUILD.md`, the plan and its checks, the roles,
     `scripts/check.sh`, the CI if any;
   - how to resume work.
2. It adds one or two ideas: `agentbus idea add` from `NEXT-DIRECTIONS.md`'s first items. Each idea
   has a title, its acceptance criteria as text, and the source
   `NEXT-DIRECTIONS.md@<commit>`. Nothing more is planned until the person triages them with
   `idea set`, which is the person's only.
3. It records `ideas` and the phase `wrapped` in `state.json`, then commits.

## 12. busmon: the draft in the Plan tab

The draft is shown in the Plan tab, not in a separate view (question Q1, recommended option). The
poller reads `{p}:plan:draft` beside `{p}:plan`, on the same tick and with the same error rule. It
does not call `TypedApproval` itself. The draft's approval state comes from the ask, which busmon
already reads: open, answered, or replaced.

### 12.1 Only a draft

The draft is drawn by the board code of today (groups, rows, folds). Rows have no derived state,
because nothing runs: every row is `◇ proposed`, in the muted role.

- The **frame title** reads `Plan  DRAFT v0`. The frame's right side reads
  `<path>@<short> · <n> tasks · checks required · awaits your yes`, clipped from the left, so that
  "awaits your yes" stays.
- The **first line** is the DRAFT banner: "nothing runs until you answer yes", then
  `♙ ask <id>` and the question. Enter on it opens the ask's page. The banner is drawn in the
  waiting role, the colour For you uses.
- **Rows** show the id, the title, the owner, and at the wide tier the check (§12.4).
- The **detail strip** at the wide tier shows the selected task's check in full, its `after` list,
  its group and its review subject. A second column shows the `BUILD.md` summary: the goal, the
  DoD count with the first items, the out-of-scope list and the run line. It is parsed with
  `onboard.ParseBuild` from the record's `build`.
- The **keys** `+`, `-` and `d` do nothing on a draft. The footer says "proposed tasks cannot be
  prioritised or marked done".

### 12.2 A draft and an active plan

The Plan tab shows the active plan as today, with one extra line under its header:
`DRAFT v0.1 proposed (2 tasks) · ♙ ask <id> · E shows the draft`. `E` switches between the two
views. `E` is bound in no busmon mode at base a7093c14 (`D` is settings' "reset group",
`cmd/busmon/keys.go:440`). The key test checks that it stays free. The frame title says which plan is shown.

### 12.3 An answered or replaced ask

- After a `yes`, the banner reads "approved: master loads it". The draft disappears when master's
  load deletes the record.
- After a `no` or free text, the banner reads "the architect is revising it".
- After a `later`, the banner reads "postponed (later)".

### 12.4 Mock-ups

160 columns (wide tier: the detail strip, the check column and the `after` column):

```
╭─ Plan  DRAFT v0  ·  proposed, not active ───────────────────────────────────────── docs/plans/v0.json@3f9c2ab · 9 tasks · checks required · awaits your yes ─╮
│  DRAFT  nothing runs until you answer yes   ♙ ask 1795901234567-0  “Adopt plan v0 as the active plan?”   enter answers · E the loaded plan (none)            │
│                                                                                                                                                              │
│  Notes API          0/4                                                                                                                                      │
│  ◇ api-schema       Schema and migrations for users and notes              coder      check  go test ./store/...                                             │
│  ◇ api-auth         Login and sessions                                     coder      check  scripts/check.sh auth                after api-schema           │
│  ◇ api-notes        Create, list and delete notes                          coder      check  scripts/check.sh notes               after api-auth             │
│  ◇ api-limits       Rate limit per user                                    coder      check  go test ./api/ -run TestRateLimit    after api-auth             │
│  Web client         0/3                                                                                                                                      │
│  ◇ web-login        Login page                                             coder      check  npx playwright test login.spec.ts    after api-auth             │
│  ◇ web-notes        Notes page: list, add, delete                          coder      check  npx playwright test notes.spec.ts    after web-login, api-notes │
│  ◇ web-ci           CI runs scripts/check.sh on every PR                   coder      check  scripts/check.sh ci                                             │
│  Your steps         0/2                                                                                                                                      │
│  ◇ host-account     Create the hosting account and give its token          ♙ you                                                                             │
│  ◇ demo             Try the app on your phone                              ♙ you      after web-notes                                                        │
│                                                                                                                                                              │
│────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────  │
│  api-auth  Login and sessions                                                │ BUILD.md  Goal: a private notes app for one person, reachable from a phone    │
│  check     scripts/check.sh auth                                             │ Done when (5): dod-1 sign in · dod-2 notes survive a restart · dod-3 …        │
│  after     api-schema     group  Notes API     review  foureyes                │ Out of scope: sharing, offline mode · Run: scripts/check.sh                 │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter task  E loaded plan  ·  proposed tasks cannot be prioritised or marked done
```

120 columns (wide tier: the check column, no `after` column, the detail on two lines):

```
╭─ Plan  DRAFT v0 ─────────────────────────────────────────────────────── v0.json@3f9c2ab · 9 tasks · awaits your yes ─╮
│  DRAFT  nothing runs until you answer yes   ♙ ask 1795901234567-0   enter answers                                    │
│                                                                                                                      │
│  Notes API          0/4                                                                                              │
│  ◇ api-schema       Schema and migrations for users and notes     coder   go test ./store/...                        │
│  ◇ api-auth         Login and sessions                            coder   scripts/check.sh auth                      │
│  ◇ api-notes        Create, list and delete notes                 coder   scripts/check.sh notes                     │
│  ◇ api-limits       Rate limit per user                           coder   go test ./api/ -run TestRateLimit          │
│  Web client         0/3                                                                                              │
│  ◇ web-login        Login page                                    coder   npx playwright test login.spec.ts          │
│  ◇ web-notes        Notes page: list, add, delete                 coder   npx playwright test notes.spec.ts          │
│  ◇ web-ci           CI runs scripts/check.sh on every PR          coder   scripts/check.sh ci                        │
│  Your steps         0/2                                                                                              │
│  ◇ host-account     Create the hosting account and give its token ♙ you                                              │
│  ◇ demo             Try the app on your phone                     ♙ you                                              │
│                                                                                                                      │
│────────────────────────────────────────────────────────────────────────────────────────────────────────────────────  │
│  api-auth  Login and sessions        check  scripts/check.sh auth        after api-schema   review foureyes          │
│  BUILD.md  Goal: a private notes app for one person, reachable from a phone · Done when: 5 checks                    │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter task  E loaded plan
```

80 columns (narrow tier: no strip, no check column; Enter opens the task's page, with its check
and the `BUILD.md` summary):

```
╭─ Plan DRAFT v0 ───────────────────────────────────────── 9 tasks · your yes ─╮
│  DRAFT  inactive until you answer yes   ♙ ask 1795…567-0                     │
│  Notes API 0/4                                                               │
│  ◇ api-schema  Schema and migrations…    coder                               │
│  ◇ api-auth    Login and sessions        coder                               │
│  ◇ api-notes   Create, list and delete…  coder                               │
│  ◇ api-limits  Rate limit per user       coder                               │
│  Web client 0/3                                                              │
│  ◇ web-login   Login page                coder                               │
│  ◇ web-notes   Notes page: list, add…    coder                               │
│  ◇ web-ci      CI runs scripts/check.sh… coder                               │
│  Your steps 0/2                                                              │
│  ◇ host-account  Create the hosting…     ♙ you                               │
│  ◇ demo          Try the app on your…    ♙ you                               │
╰──────────────────────────────────────────────────────────────────────────────╯
 ↑↓  enter task (check, BUILD.md)  E loaded plan
```

An active v0 with a v0.1 draft (120 columns):

```
╭─ Plan  v0  9/9 done ──────────────────────────────────────────────────────────────────────────── v0.1 draft waiting ─╮
│  DRAFT v0.1 proposed (2 tasks, from the v0 grade)   ♙ ask 1795990000000-0   E shows the draft                        │
│                                                                                                                      │
│  DONE  9   d shows                                                                                                   │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter task  E draft v0.1
```

### 12.5 The task page (Enter)

The page of today, plus two parts:

- a `Check` line, the check in full and wrapped;
- for a draft, a `BUILD.md` section with the summary of §12.1.

For an active task with a check, the page also shows the deciding verdict's evidence line, or
"check not run yet".

### 12.6 Golden and behaviour tests

These are listed in §14.

### 12.7 The tour

Page one of the tour gets one line: "New project? Run `agentbus init` in your shell: it interviews
you and drafts a plan." `tourVersion` is bumped, because the page's meaning changes.

## 13. Trust and its limits

- **Adoption is the person's typed answer.** `TypedApproval` requires the ask's own record (while
  it is kept) **and** the reply entry. `answer` refuses an agent, by environment and by process
  tree. busmon answers as the person. A yes from master or any agent is not an approval. Neither is
  a directive, a report, nor the `human=yes` text `init` composes.
- **The limit, stated as retro §5 states it.** On one host, the same Unix user can write the
  stream and the asks hash directly. The agent ACL user can XADD to `{p}:cmd` and write
  `{p}:asks`, because those are not master-only kinds. A Redis entry does not record the ACL user
  that wrote it. With the ACL off, nothing stops a forged reply. With the ACL on, an agent can
  still write both. So the adoption is protected against mistakes and against an agent that follows
  its rules, not against a hostile local process. Putting the person's answers in a busmon-only
  key (the concierge spec's `aclBusmonOnly`) would close this. It is a larger change, out of scope
  here, and listed for the security review.
- **The check scripts can be weakened by a PR** (§9.5). This is a reviewer rule, not enforced.
  The grade (§11.1) runs every DoD check at the end, at one commit, so a weakened script that hides
  a failure is caught only if the DoD check does not use the same script. The skill tells the
  architect to write DoD checks that do not all route through `scripts/check.sh`. At least the
  user-facing items need their own scenario.
- **Nothing reaches the broker or the forge in the person's name without the yes.** `init` writes
  one `human=yes` directive and may start the architect, as the person's own command. The
  architect writes asks, a draft record and commits on its branch. Master loads only an approved
  draft and pushes only after the two reviews. These are today's delivery rules.
- The security role reviews build steps (b) and (d).

## 14. Build steps, tests, acceptance

Each step is one PR to `dev`, in this order. Steps (a) and (b) touch `bus/` or `plan/`, and
`docs/plans/*.json` uses no new field before (a) is installed.

| Step | Content | Tests |
|---|---|---|
| (a) `plan.check` + done guard | `Task.Check`, `File.Checks`, `Validate`; `plan.CheckEvidence`, `plan.ResultHead`; `PlanFact.Head`; derive (§9.2); the `plan done` guard, with the merged head read from the forge (§9.3); the task page's `Check` line | Parse: check accepted; 201 characters, a newline or `checks: required` with a missing check all refused; old plans unchanged (golden). Derive: an approved verdict with `--head H` and the evidence line `at H` is done when the result head is H. These are each `InReview` with their reason: no evidence line; `exit 1`; a check differing by more than whitespace; a verdict without a head; a line `at H2` on a verdict with head H. **Stale head**: approved with evidence at H1, then the board commit, the queued head or the merged head is H2: not done, reason "re-review". A new approval with evidence at H2 makes it done. A recorded done whose `head` is not the decider's is not done. A `merge: false` request with no commit is not done. A task with no check is byte-identical to today. `plan done` (fake forge): refused without the evidence; refused when the merged head is H2 and the approval names H1; refused when the forge read fails; passes and records the head when they match. `exit 1` labelled "check failed". **Person task** with a check: `plan done` is refused in each of these cases: without a typed `passes` to its `check-…` question; on `fails`; on an untyped reply; after the record expired. It passes on a typed `passes` to the current id, and records `approved` and `approved_qid`. **Stale check**: approved `passes` for check A, then a plan reload changes the task's check to B (same task id). `plan done` is refused, and the row recorded done under A is no longer done. A board-delivered person task with a check is not done without the fact. Derive shows it done only with `approved` and the current qid. A person task without a check is unchanged. Missions: `plan load` and `plan draft` refuse a mission task whose `test` differs from its check, and refuse an unreadable `missions.json`. Mutants: drop the `exit 0` test, drop the head comparison in each of derive and `plan done`, drop the whitespace collapse, drop the `approved` test for person tasks |
| (b) `plan draft` + `TypedApproval` | `bus.TypedApproval`; `RetroApproved` rewritten on it; `SetApprovalArchive`; key kind `plan:draft`; `plan draft`, `show`, `--drop`; `plan load`'s draft rules (§8.5); `PlanRecord.Approved`; `onboard.ParseBuild` | `TypedApproval`: yes passes; an untyped reply, another qid, another ask, `said=no`, a withdrawn answer, `By == From`, a kept record answering `no` while the entry says `yes`, and a failed archive read are each refused. The retro tests pass unchanged. qid: one byte changed in the plan or in `BUILD.md` changes it. Load: refused while unapproved; refused for another file; refused for `checks: required` with no draft; passes after the yes, deleting the draft. **Race**: a test hook runs between part 1 and part 2 of the load (§8.5) and replaces the draft (a new file and qid, and a new ask). The load fails, `{p}:plan` is unchanged, and the new draft is kept. The same holds for a `--drop` in that window. Script: each of a changed `file`, a changed `build`, a changed `ask`, a kept answer that is not `yes`, and a stream entry whose `said` is not `yes` makes the script write nothing (each a mutant of one condition). Draft ask rules (§8.1 step 6): a re-draft while the old ask is open replaces it, and the old qid no longer approves; a re-draft after `no`, free text or `later` opens a new ask (no `ASK_NOT_OPEN`); the same files at a new commit give the same qid and wording, so no `SameQuestion` refusal; a re-draft by master of the architect's draft is refused until `--drop`, and `--drop` by master leaves the architect's ask and says so; `plan draft` while the caller has another ask open is refused, and nothing is written; a qid depends on the path too. Draft refused for an agent other than master or architect, and for the person's shell. ACL: `plan:draft` writable by agent, `plan` still not |
| (c) busmon draft view | the poller reads the draft; §12.1 to §12.3; `E`; the task page | Golden at 80, 120 and 160 for a draft only, and for a plan plus a draft; the banner for answered `yes`, `no` and `later`; `+`, `-` and `d` inert on a draft; Enter on the banner opens the ask; a plan without a draft byte-identical to today |
| (d) `agentbus init` | `init`, `--existing`, `--resume`, `status [--json]`, `wrap-up`; `onboard.State` (strict parse, version) | Refused for an agent caller (env, and the fake ancestry); refused with a plan loaded; refused with a branch and no `--resume`; `--resume` refused with none or `wrapped`; mode detection on scratch repositories (empty, README only, code, `--existing`); the architect started when absent (fake host); the directive is `human=yes` and not `typed`; `status` prints the disagreement; `state.json` with an unknown field or version 2 refused |
| (e) skill + prompts | `skills/agent-bus-onboard/SKILL.md`; the architect's skills; §10.1 to §10.3 | `roles doctor` passes; the release gate's update from the previous binary (the roles.toml change) |
| (f) grade + wrap-up | `onboard.Grade`, `plan.EvidenceLines`; `agentbus init grade --json`; the grade in `init status`; `init wrap-up` refuses unless every item passes; the architect's skill steps | Grade: two items with the same check told apart by id; a missing item is "missing", not a pass; `exit 1`, or a head other than the verdict's, is FAIL; a manual item passes only on a typed `passes` to its current `grade-…` id; **stale manual pass**: a `passes` from an earlier grade commit, or given before the item's text or check changed, is not a pass; wrap-up refused on any non-PASS. Plus the acceptance test |
| (g) tour + docs | §12.7; quickstart; OPERATIONAL-GUIDE fr/en | tour golden |

**Acceptance (directive §6.4).** One end-to-end test runs on a scratch repository and a disposable
broker, with the fake client (`cmd/agentbus/fakeclient.go`) standing in for the architect, master
and a reviewer. It walks this sequence:

1. `init` → round 1 ask → answers by `agentbus answer` as the person → `BUILD.md` and `state.json`
   committed.
2. `plan draft` → the draft visible (`plan draft show`, and busmon's poller state).
3. The adoption rules:
   - an agent's `yes` written as a plain reply does **not** adopt;
   - a `yes` to an older qid after an edit does **not** adopt;
   - the person's `yes` adopts.
4. `plan load` succeeds, and the draft is gone.
5. `plan done` is refused without a verdict, and refused with a verdict lacking the evidence line.
   It is also refused when the branch moved after the approval (approved at H1, merged at H2). After
   a re-review at H2, it passes.
   An approval with the evidence makes the task done.
6. All tasks done → the grade challenge → the verdict → the wrap-up: the report, two ideas,
   `state.json` `wrapped`.

A **resume test**: kill the fake architect after round 1 is asked, run `init --resume`, and check
that it reconciles `state.json`, does not re-ask round 1, and continues.

## 15. Questions for the person

Q1 to Q4 come from directive §7. Q5 is mine: it decides what lands in the project's repository.
They are asked in one ask, in French; the recommended options are the ones this spec is written
around.

1. **Q1 Draft view**: inside the Plan tab, with a DRAFT banner (recommended), or a separate Work
   sub-view?
2. **Q2 Scope questions**: one scope round before the draft, one question per group, only when the
   plan has more than 10 tasks (recommended); always; or never (the person answers no + text).
   An ask holds at most 6 questions, so a question per task is not possible.
3. **Q3 Existing repository with an active plan**: refuse and suggest `plan clear` (recommended),
   or draft beside it?
4. **Q4 Grade threshold**: every DoD item must pass, with the manual items answered by the person
   in an ask (recommended); or "accepted with a note" allowed for manual items.
5. **Q5 The person's words in the repository**: only the architect's rewrite in `BUILD.md`, plus
   reply ids and hashes in `state.json` (recommended: a pushed repository may be public); or the
   answers verbatim in `docs/onboarding/answers.md`.
