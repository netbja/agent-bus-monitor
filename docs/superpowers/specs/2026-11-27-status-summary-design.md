# Where are we: `agentbus status`, and master's replies (status-summary)

- Status: spec r1, for review (subject `status-summary-spec`). Not built.
- Date: 2026-11-27. Author: architect. Base: origin/dev `18a1ec16`.
- Source: the person's typed directive to master of 2026-11-27, relayed as task
  `status-summary-spec` ("New feature before concierge"). It comes before the concierge build.
  The concierge spec stays, and is built later as a read-only MVP that reuses this summary across
  projects (concierge §5.2).
- MVP, spec only, no LLM in the summary. The person's open questions are in §8, asked in one ask
  in French.

## 1. What the person asked

1. **`agentbus status [--project p]`** prints a short human summary from broker state, with no
   LLM. It covers what waits on the person, the merge queue, CI red, blocked tasks and plan
   progress. It reuses what `status`, `board` and `health` already give, and duplicates
   nothing. Master answers "where are we" from it first, briefly.
2. **Master's UX takes the concierge's ideas, with no new agent:**
   - **The compose window:**
     - `Ctrl+T` attaches the selected row as a context chip (its id, not its text);
     - a mode selector (directive, question or note) at the top right;
     - the line count replaces `0 / 65536`, and the limit shows only near it;
     - `Enter` stays newline, and `Ctrl+S` stays send.
   - **Master's replies:**
     - the answer or decision first, short bullets, no preamble;
     - ids rendered as links in busmon;
     - a sources line (`↳ …`) when reporting state;
     - up to three suggested actions, as number keys handled by busmon, with concierge §5.3's
       press and typed rules;
     - the dialogue style rules (concierge §4.5) and `person.language`.
   - Goldens at 80x24 and 140x40.

## 2. What exists, and what is reused

| Section of the summary | Read today by | Reused as |
|---|---|---|
| Waiting on the person | busmon's For you, `forYou` (`cmd/busmon/tea_foryou.go:34`): asks, waiting agents and blocked prompts, the stall guard, open gates, merges, reports for the person, unread lines | its broker-only ranks move to a new package `attention` (§3.2); busmon and `status` both call it |
| Merge queue | `agentbus merge-queue`; `bus.MergeQueue` (`MergeItem`: number, head, approvers) | the same record |
| CI red | `MergeItem.CIRed`, the failing checks of a PR's head, read only while `ci.loop` is on | the same field; "not read" when the loop is off |
| Blocked tasks | `agentbus board`; `bus.Board` (state `blocked`, `Request.BlockedReason`) | the same entries |
| Plan progress | `agentbus plan show`; `plan.Load`, `plan.Read`, `plan.Derive`, `plan.Count` | the same calls, plus the plan board's `planInHand`, moved to `plan/` (§3.2) |
| Team | `bus.Agents` (`{p}:agents`: state, message, verified identity) | counts per state |
| Health | `agentbus health`, the health records | failing and warning checks, by name |
| Projects (`--all`) | `bus.Projects` (`bus/projects.go:51`) | the list of projects to summarise |

`agentbus status <agent> <state> [message]` already exists: it is an agent publishing its state,
which every agent runs (`commands.go:25`). §3.1 keeps it and adds the no-argument form (Q1).

## 3. `agentbus status`

### 3.1 The form

- `agentbus status [--project p] [--all] [--json]`, **with no positional argument**, prints the
  summary. With `<agent> <state> [message]`, it publishes an agent's state, as today. The two
  forms are told apart by the positional count: 0 means the summary, 2 or more means presence.
  One positional is a usage error naming both forms. No agent script calls `status` with no
  argument today, and the fake client always passes three (`fakeclient.go:185`). Q1 offers a
  separate verb instead.
- **Read only.** It writes nothing, takes no guard, and works under the observer ACL user. Every
  number comes from the broker, and nothing is inferred.
- `--all` summarises every project of `bus.Projects`, one block each, in the order the list gives
  (Q5).
- **Exit codes:** 0 when every read succeeded; 1 when a read failed, in which case that section
  prints `?` and the reason, and the others still print; 2 on a usage error.

### 3.2 No second definition: `attention` and `plan.InHand`

"Waiting on the person" must be one definition. If `status` and busmon counted differently, the
person would see two answers. The build moves the broker-only part of `forYou` into a package
`attention`:

- `attention.Items(in Inputs, now time.Time) []Item` returns the asks, waiting agents, prompts
  and stuck screens, the stall guard (health), open gates for the person, reports for the person,
  and merges. The ranks are today's, from `rankAsk` to `rankMerge`.
- `Inputs` holds what the broker gives: asks, agents with their observations, gates, the health
  records, the report triage marks, and the merge queue.
- busmon's `forYou` becomes `attention.Items(...)`, plus its local `rankUnread` rows, which come
  from busmon's own read marks and stay in busmon.
- `status` calls `attention.Items` alone. It shows no unread lines: those are one busmon's local
  marks, not the project's state.
- A golden on one fixture checks that busmon's For you, minus unread, and `status`'s "For you"
  line count the same items.

The plan board's `planInHand` (`cmd/busmon/planboard.go:200`, "in progress" and "waits for you")
moves to `plan.InHandRows(rows)`. busmon and `status` call it. This is not onboarding's
`plan.InHand(Row)` predicate (onboarding §8.1). If both land, `InHandRows` uses `InHand`.

### 3.3 The output

```
agent-bus-monitor · 22:31
For you   2 asks · 1 merge (#841) · 1 gate (foureyes on concierge-amend)
Merge     #841 onboarding-answers · approved by 2 · CI not read (ci.loop off)
Blocked   1 · resource-limits-3e (coder2): waits for the holder fix
Plan      v0.34 12/20 · in progress: sibling-enforce-2 (coder2) · waits for you: #841
Team      2 working · 3 idle · 1 waiting (architect: ask 1795808071208-0)
Health    2 warn: cmd-delivery, agent-identity
↳ asks, merge-queue, board, plan v0.34@3f9c2ab, agents, health · read 22:31:04
```

- **One line per section**, at most 7 lines, plus the sources line. A section with nothing to
  report is `none`. A section that cannot be read is `?`, with the reason.
- **For you** gives counts per kind, named only when there is one item of that kind (`1 merge
  (#841)`). It is the line master reads first.
- **Merge** lists each approved head waiting for the person: number, subject, approvers, CI.
- **CI red** has no line of its own. It is part of Merge: `CI red: test (go 1.26)` on the PR it
  concerns. With `ci.loop` off, it reads "CI not read". It never reads "green".
- **Blocked** gives the count, then the first two tasks with their owner and reason, then
  `+N more`.
- **Plan** shows done/total (`plan.Count`), the task in progress and the first task waiting for
  the person. With no plan loaded, it reads `no plan loaded`.
- **Team** counts agents per state, and names the waiting ones with their ask.
- **Health** names the checks in `fail`, then those in `warn`.
- **The sources line** names each reader and the plan's commit, with the read time. It uses the
  same `↳` as the concierge.
- **Width.** Lines are clipped to the terminal width, or 100 columns when not a terminal, with
  `…`.
- **`--json`** gives the same sections, as typed fields (counts, ids, reasons), version `"v": 1`,
  additive changes only (the `subscribe` JSON rule). Master reads this form.

### 3.4 Master answers "where are we" from it

- The `agent-bus-master` skill gains one rule. When the person asks where things stand, in any
  words or language ("where are we", "on en est où", "status?"), master runs
  `agentbus status --json` first. It answers from that output, briefly (§5), before reading
  anything else. It reads further only for what the person asked beyond the summary.
- The answer ends with the summary's sources line (`↳ …`).

## 4. The compose window

The compose window (`cmd/busmon/compose.go`, `tea_compose.go`) keeps `Enter` for newline and
`Ctrl+S` for send.

### 4.1 Context chip: `Ctrl+T`

- `Ctrl+T` attaches the row selected on the page under the window as a chip in the text's first
  line: `[PR #841]`, `[board: concierge-amend]`, `[ask 1795808071208-0]`. Only the id is sent.
  The message gains the field `context=<kind>:<id>`, an additive cmd field, so master reads the
  entry itself and never a pasted copy.
- `Backspace` at the start of the text removes the chip. There is at most one chip.
- **`Ctrl+T` is bound today in the compose window:** "after the open-question hint: answer in its
  question panel" (`ActComposeAsk`, `keys.go:349`). Q2 asks the person. The recommended answer
  keeps `Ctrl+T` for the chip, as the person asked, and moves the question-panel switch to
  `Ctrl+O`. `Ctrl+O` is bound only in the input line (`keys.go:319`), not in compose.

### 4.2 Mode selector: directive, question, note

The mode is shown at the top right of the window's frame: `directive ▾`. **`Ctrl+N`** cycles it.
`Ctrl+N` is not bound in the compose window, and the key test checks that it stays free. Each mode
sends this:

| Mode | busmon sends | Master |
|---|---|---|
| **directive** | today's `TypedCmd`: a directive, `typed=yes` | acts on it, as today |
| **question** | the same directive plus the additive field `intent=question` | answers, and starts no work: no request, no pop, no delivery. The skill says so. |
| **note** | Q3 decides: a `TypedNotify` that nobody reads (recommended), or a directive to master with `intent=note` and `wake=no` | reads it at its next wake (the second option only) |

The mode resets to `directive` when the window closes. A mode other than `directive` is drawn in
the window's frame colour of an answer, so a question is never mistaken for an order.

### 4.3 The counter

`0 / 65536` becomes the line count, `3 lines`. The rune limit (`bus.CmdMaxRunes`) shows only from
90 % of it: `58 912 / 65 536`, in the warning role. The existing check that refuses an
over-limit text is unchanged.

### 4.4 Mock-ups (goldens at 80x24 and 140x40)

80 columns:

```
┌ to: master ────────────────────────────────────────────────────── question ▾ ┐
│ [PR #841] is anything left before I merge it?                                │
│                                                                              │
│                                                                              │
└ ^S send · ^T context · ^N mode · esc keep draft ───────────────────── 1 line ┘
```

At the limit (80 columns, the last line only):

```
└ ^S send · ^T ctx · ^N mode ─────────────────── 3 120 lines · 59 004 / 65 536 ┘
```

The 140x40 golden draws the same frame at its width, with the hint row unshortened.

## 5. Master's replies

### 5.1 Style

Master's skill gains the dialogue style of concierge §4.5. The rules are stated once, in
`skills/shared/dialogue-style.md`, and both skills point to it:

- the answer or decision first;
- short bullets only if needed;
- no preamble, no staged opener or closer, no filler;
- uncertainty given with its source;
- answers in `person.language`;
- verbatim quotes, typed text, ids and sources are never rewritten.

A report's first line stays its summary (the report rule).

### 5.2 Ids as links

busmon renders ids in a report's text as links: `#841`, a board task, an ask id and a stream
entry id. This is done by one function, `linkIDs(text) []Seg`, which the concierge window reuses.
`Enter` on a link opens its page. Master writes ids plainly.

### 5.3 The sources line

When master reports state, its report ends with one line starting `↳ `, naming what it read:
`↳ status 22:31, board, #841`. busmon draws that line dim and folds it like concierge §4.2.

### 5.4 Suggested actions

- `agentbus report master --to <person> … --action <kind>:<arg>`, repeated up to 3 times. It
  writes the report entry with an additive field `actions`, a JSON list of
  `{label, kind, arg}`. There are two kinds:
  - **`open:<id>`** opens a page in busmon: a PR, a board task, an ask or an entry.
  - **`reply:<text>`** is a reply to master that master drafted, for example "yes, merge #841".
- busmon shows the actions as a bar under the report, on its page and in For you: `[1] open #841
  [2] reply "yes, merge #841"`. **Only the number keys run them.** `Enter` never does.
- **The press rules are concierge §5.3's:**
  - `open` just opens.
  - `reply` shows the full text and its target (master) once more, and sends on `y`. It goes as a
    directive with `human=yes` and `draft_by=master`, never `typed=yes`. So master treats it as
    the person's press of master's own words, and never as the person's typed approval.
  - Anything that needs a typed approval (an adoption, a retro, a merge yes recorded by
    `TypedApproval`) still needs the person's own typed words. A `reply` action cannot give it.
- Each press of a `reply` leaves one audit line (`action=reply`, the report id, `draft`), as
  concierge §5.3's relay does.
- **Q4** asks whether `reply` is in the MVP, or only `open`.

## 6. Build steps (in series), tests

1. **`attention` and `plan.InHandRows`** (§3.2): the move, with busmon calling them.
   - Tests: busmon's For you goldens are unchanged, byte for byte; the fixture golden of
     busmon's count against `status`'s count; `go vet`.
2. **`agentbus status`** (§3.1, §3.3): the no-argument form, `--project`, `--all` and `--json`.
   - Tests:
     - one positional is a usage error, and the presence form is unchanged;
     - each section against a fixture broker, including `none`, `?` with its reason (a failed
       read gives exit 1), and `CI not read` with `ci.loop` off;
     - the clip at 80 and at 100;
     - the JSON's `v` field;
     - the observer ACL user can run it;
     - `--all` over two projects.
3. **Master's skill** (§3.4, §5.1, §5.3) and `skills/shared/dialogue-style.md`.
   - Tests: the skill files lint (`roles doctor`).
4. **The compose window** (§4): the chip, `Ctrl+T` (and `Ctrl+O` per Q2), `context=`, the
   modes, `Ctrl+N`, `intent=`, and the counter.
   - Tests:
     - goldens at 80x24 and 140x40 for each mode, with and without a chip, and at the limit;
     - `Backspace` removes the chip;
     - `question` sends `intent=question` and `typed=yes`;
     - `note` sends as Q3 says;
     - the key test (`Ctrl+N`, `Ctrl+T`, `Ctrl+O` in compose).
5. **Master's replies in busmon** (§5.2 to §5.4): `linkIDs`, the folded sources line, the
   `--action` flag and the `actions` field, the bar, and the press rules.
   - Tests:
     - goldens at 80x24 and 140x40 of a report with links, a sources line and three actions;
     - `Enter` on the bar runs nothing;
     - a `reply` press sends `human=yes`, `draft_by=master` and never `typed`, and only after
       `y`;
     - one audit line per press;
     - a `reply` never passes `TypedApproval` (a test with the retro's check).

## 7. Not doing

- No LLM in `status`, and no new agent.
- No unread lines in `status`: they are one busmon's local marks.
- No cross-project answers by master: that is the concierge's, later. `--all` only prints.
- No change to what needs the person's typed words.

## 8. Questions for the person (one ask, in French)

1. **Q1 The command's name:** `agentbus status` with no argument, beside the agents' presence
   form `status <agent> <state>`, as asked (recommended); or a separate verb, `agentbus summary`.
2. **Q2 `Ctrl+T` is already taken** in the compose window ("answer in the question panel"):
   `Ctrl+T` for the chip, with the question panel moved to `Ctrl+O` (recommended); or the chip on
   `Ctrl+O`.
3. **Q3 The `note` mode:** a broadcast that nobody reads and that wakes nobody, as busmon's
   notes are today (recommended); or a note to master, read at its next wake, waking nothing.
4. **Q4 Actions in master's replies:** `open` and `reply` (a reply master drafted, sent on your
   press as `human=yes`, never as your typed words) (recommended); or `open` only for the MVP.
5. **Q5 Scope:** the current project by default, with `--all` for every project, on which the
   concierge builds later (recommended); or the current project only.
6. **Q6 The Team line:** counts per state, and the waiting agents named (recommended); or no Team
   line, since For you already shows what waits on you.
