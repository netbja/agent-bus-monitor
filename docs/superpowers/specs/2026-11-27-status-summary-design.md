# Where are we: `agentbus summary`, and master's replies (status-summary)

- Status: spec r2, for review (subject `status-summary-spec`). Not built.
  - r2 takes the person's answers to all six questions (§8):
    - the verb `agentbus summary`;
    - the chip on `Ctrl+O`;
    - the note sent to a recipient;
    - the actions `open` and `reply`;
    - `--all` with the projects that wait on the person first;
    - a Team line of anomalies.
  - r3 answers the two r2 rejects (§4.3):
    - foureyes: `intent`, `context` and `draft_by` are carried by `bus.Event` and the subscribe
      JSON;
    - reviewer-codex: code guards, so a question or a note never grants authority and never
      starts tracked work.
  - r2 also takes foureyes's r1 findings: report rows stay in busmon (§3.2), `plan.NowNext`
    (§3.2), the `reply` action's sender (§5.4), and the nits (§3.1, §7).
- Date: 2026-11-27. Author: architect. Base: origin/dev `18a1ec16`.
- Source: the person's typed directive to master of 2026-11-27, relayed as task
  `status-summary-spec` ("New feature before concierge"). It comes before the concierge build.
  The concierge spec stays, and is built later as a read-only MVP that reuses this summary across
  projects (concierge §5.2).
- MVP, spec only, no LLM in the summary. The person's open questions are in §8, asked in one ask
  in French.

## 1. What the person asked

1. **`agentbus status [--project p]`** (the person's words; the verb became `agentbus summary`,
   the person's Q1) prints a short human summary from broker state, with no
   LLM. It covers what waits on the person, the merge queue, CI red, blocked tasks and plan
   progress. It reuses what `status`, `board` and `health` already give, and duplicates
   nothing. Master answers "where are we" from it first, briefly.
2. **Master's UX takes the concierge's ideas, with no new agent:**
   - **The compose window:**
     - a key attaches the selected row as a context chip, its id and not its text (asked as
       `Ctrl+T`; `Ctrl+O` after Q2);
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
| Waiting on the person | busmon's For you, `forYou` (`cmd/busmon/tea_foryou.go:34`): asks, waiting agents and blocked prompts, the stall guard, open gates, merges, reports for the person, unread lines | its broker-only ranks move to a new package `attention` (§3.2); busmon and `summary` both call it (reports and unread stay in busmon) |
| Merge queue | `agentbus merge-queue`; `bus.MergeQueue` (`MergeItem`: number, head, approvers) | the same record |
| CI red | `MergeItem.CIRed`, the failing checks of a PR's head, read only while `ci.loop` is on | the same field; "not read" when the loop is off |
| Blocked tasks | `agentbus board`; `bus.Board` (state `blocked`, `Request.BlockedReason`) | the same entries |
| Plan progress | `agentbus plan show`; `plan.Load`, `plan.Read`, `plan.Derive`, `plan.Count` | the same calls, plus the plan board's `planInHand`, moved to `plan/` (§3.2) |
| Team | `bus.Agents` (`{p}:agents`: state, message, verified identity) | counts per state |
| Health | `agentbus health`, the health records | failing and warning checks, by name |
| Projects (`--all`) | `bus.Projects` (`bus/projects.go:51`) | the list of projects to summarise |

`agentbus status <agent> <state> [message]` already exists: it is an agent publishing its state,
which every agent runs (`commands.go:25`). §3.1 keeps it and adds the no-argument form (Q1).

## 3. `agentbus summary`

### 3.1 The form

- **`agentbus summary [--project p] [--all] [--json]`** prints the summary. It is a new verb, the
  person's answer to Q1.
- `agentbus status <agent> <state> [message]` stays the agents' presence write, unchanged.
  `agentbus status` with no positional argument is a usage error (exit 2, the exit every
  `agentbus` usage error gives): `usage: status <agent> <state> [message...] (the project's
  summary is agentbus summary)`. The build checks that the generic usage check in
  `commands.go` prints that line, and does not print its own first.
- **Read only.** It writes nothing, takes no guard, and works under the observer ACL user. Every
  number comes from the broker, and nothing is inferred.
- `--all` (the person, Q5) prints one compact block per project of `bus.Projects`, with the same
  sections and no LLM. Projects with something waiting on the person (a non-empty For you) come
  first, then the others in the list's order. `--json` works with both forms.
- **Exit codes:** 0 when every read succeeded; 1 when a read failed, in which case that section
  prints `?` and the reason, and the others still print; 2 on a usage error.

### 3.2 No second definition: `attention` and `plan.NowNext`

"Waiting on the person" must be one definition. If `summary` and busmon counted differently, the
person would see two answers. Part of For you depends on one busmon's **local** marks, though,
and the broker cannot see them. So the line between the two is drawn here, by foureyes's option
(a):

- **`attention.Items(in Inputs, now time.Time) []Item`**, a new package, holds the broker-only
  ranks: asks (`rankAsk`), the stall guard (`rankHealth`), waiting agents, prompts and stuck
  screens (`rankWait`), open gates for the person (`rankGate`), and merges (`rankMerge`).
  - `Inputs` lists every read they need: the asks (`{p}:asks`), the agents with their
    observations (`{p}:agents` and the observation keys busmon reads), the gates, the health
    records, the dispatch record (for the merge queue's "unknown" row), and the merge queue.
  - Nothing else is read. No feed and no local mark goes in.
- **Two ranks stay in busmon.** Both end on busmon's local marks (`unreadState`, `unread.go:24`,
  "kept in the local cache"):
  - `rankReport`: a report that needs the person, ended by `fl.mark` or `fl.answered`
    (`tea_foryou.go:96-199`);
  - `rankUnread`.

  The lines `foldLines` attaches to asks, waits and merges (`Held`, `Unread`) are presentation,
  and stay in busmon too.
- busmon's `forYou` becomes `attention.Items(...)`, then its own report and unread rows, then
  the folds, as today.
- **`summary` shows reports differently.** It counts the reports that ask for the person
  (`--needs read` or `--needs answer`) from the last 24 hours of the report stream, as `3 reports
  (24 h)`, and never calls them unread. Which of them the person has read is one busmon's
  knowledge, not the project's state.
- **The golden names exactly what it compares:** on one fixture, the For you items of busmon whose
  rank is in `attention` equal `summary`'s "For you" counts, minus reports and unread.

**`plan.NowNext(rows)`** is the plan board's `planInHand` (`cmd/busmon/planboard.go:200`),
moved to `plan/` with its name changed to what it returns: the row in progress now and the next
one. Its rules are unchanged. `Answered` is not in hand there ("master's to record"), and
`Waiting` comes before `Blocked`. busmon and `summary` call it. It is **not** onboarding's
`plan.InHand(Row)` (onboarding §8.1 step 4), which counts `Answered` in on purpose: a draft must
name the work it stops. They are two functions, for two questions, and neither calls the other.

### 3.3 The output (`agentbus summary`)

```
agent-bus-monitor · 22:31
For you   2 asks · 1 merge (#841) · 1 gate (foureyes on concierge-amend) · 3 reports (24 h)
Merge     #841 onboarding-answers · approved by 2 · CI not read (ci.loop off)
Blocked   1 · resource-limits-3e (coder2): waits for the holder fix
Plan      v0.34 12/20 · in progress: sibling-enforce-2 (coder2) · waits for you: #841
Team      2 working · 3 idle · coder2 idle 40 min with resource-limits-3e accepted
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
- **Team** (the person, Q6): one line of counts by state. A zero count is hidden, except
  `failed`. Agents waiting on the person are **not** repeated here: they are in For you. Only
  anomalies are named, each with its duration:
  - **failed:** lost, or its launch failed (`{p}:agents`' verified state, and the self-repair
    record);
  - **stalled:** idle while it holds an accepted request. This is the health check `idle
    implementer` (`health/idleimplementer.go`), read from its record, never recomputed;
  - **quota-paused:** the quota hold on its provider.

  `--all` prints one Team line per project.
- **Health** names the checks in `fail`, then those in `warn`.
- **The sources line** names each reader and the plan's commit, with the read time. It uses the
  same `↳` as the concierge.
- **Width.** Lines are clipped to the terminal width, or 100 columns when not a terminal, with
  `…`.
- **`--json`** gives the same sections, as typed fields (counts, ids, reasons), version `"v": 1`,
  additive changes only (the `subscribe` JSON rule). Master reads this form.

### 3.4 Master answers "where are we" from `agentbus summary`

- The `agent-bus-master` skill gains one rule. When the person asks where things stand, in any
  words or language ("where are we", "on en est où", "status?"), master runs
  `agentbus summary --json` first. It answers from that output, briefly (§5), before reading
  anything else. It reads further only for what the person asked beyond the summary.
- The answer ends with the summary's sources line (`↳ …`).

## 4. The compose window

The compose window (`cmd/busmon/compose.go`, `tea_compose.go`) keeps `Enter` for newline and
`Ctrl+S` for send.

### 4.1 Context chip: `Ctrl+O` (the person, Q2)

- **`Ctrl+O`** attaches the row selected on the page under the window as a chip in the text's
  first line: `[PR #841]`, `[board: concierge-amend]`, `[ask 1795808071208-0]`. Only the id is
  sent. The message gains the field `context=<kind>:<id>`, an additive cmd field, so master reads
  the entry itself and never a pasted copy.
- `Backspace` at the start of the text removes the chip. There is at most one chip.
- **`Ctrl+T` keeps its role**, "answer in the question panel" (`ActComposeAsk`, `keys.go:349`).
  `Ctrl+O` is bound only in the input line (`keys.go:319`, open the compose window), not in
  compose. The two modes do not overlap.
- **The binding is a setting**, `compose.context_key` (default `ctrl+o`, in `config/keys.go`).
  busmon's help and its goldens show the active key.
- **Ctrl+O must reach busmon.** It is 0x0F. On macOS and BSD, the terminal driver's `VDISCARD`
  takes it in cooked mode. busmon runs in raw mode (bubbletea clears `IEXTEN`), so it passes
  through. The build checks this with a test that reads 0x0F through the raw-mode input path.
  It also checks by hand, on macOS Terminal and iTerm2, and over SSH from macOS to Linux. The
  results are recorded in the PR. A terminal that swallows it is named in the help, which points
  to the setting.

### 4.2 Mode selector: directive, question, note

The mode is shown at the top right of the window's frame: `directive ▾`. **`Ctrl+N`** cycles it.
`Ctrl+N` is not bound in the compose window, and the key test checks that it stays free. Each mode
sends this:

| Mode | busmon sends | Master |
|---|---|---|
| **directive** | today's `TypedCmd`: a directive, `typed=yes` | acts on it, as today |
| **question** | the same directive plus the additive field `intent=question` | answers, and starts no work: no request, no pop, no delivery. The skill says so. |
| **note** (the person, Q3) | a message to the window's recipient (`to:`, master by default) with `intent=note` and `wake=no`. It is `typed=yes`, because the person typed it, but marked as a note | reads it at its next wake, as a note: not a directive, and no task. A note alone never counts as the person's typed instruction or approval. The receiving skill and master's checks read `intent=note` as "not actionable" |

busmon's existing broadcast notes (`TypedNotify`, a line nobody reads) stay as they are. The
note mode is a separate thing, addressed to someone. The mode resets to `directive` when the
window closes. A mode other than `directive` is drawn in
the window's frame colour of an answer, so a question is never mistaken for an order.

### 4.3 Transport and guards: the receiver sees the intent, and code enforces it

**Transport** (foureyes r2). An agent reads its commands through `subscribe --drain`. That JSON
is a fixed struct (`cmd/agentbus/subscribe.go:30-52`, `cmdEvent`) built from `bus.Event`, whose
parser (`bus/stream.go:318`) reads only the fields it knows. A new stream field would be dropped
before the receiver saw it. So the build adds them end to end:

- **`bus.Event` gains three fields:**
  - `Intent`, from the field `intent`. Only `note` and `question` are read; any other value reads
    as `""`, as `Needs` does with its values;
  - `Context`, from `context`: `<kind>:<id>`, validated like an id;
  - `DraftBy`, from `draft_by`: a valid name, or `""`.
- **The subscribe JSON** gains `intent`, `context` and `draft_by`. They are additive `v1` fields,
  beside `human`, `typed` and `needs` (the versioned-JSON rule).
- **The `--brief` line**, which a woken agent may read alone, prefixes the text with `[note]`,
  `[question: answer only]` or `[draft by <name>]`, and appends `[context <kind>:<id>]`.
- `agentbus thread` and `history` show the same tags.
- **The dispatcher's answer guard** (`internal/dispatchsvc/answerguard.go:42`) copies
  `Intent` into `health.AnswerCmd`. A `note` is never counted as a person's directive left
  unanswered; it asks for no answer. A `question` is counted, because it asks for one.

**Guards in code** (reviewer-codex r2). A question or a note is `typed=yes`, because the person
typed it. The rule is that such an entry never grants authority, and never starts tracked
work. Master's skill alone is not enough to enforce that, so:

- **One predicate:** `bus.Event.Actionable() bool` is `Typed && Intent == ""`. Every place where
  code grants authority from `typed=yes` calls it, and no other test exists:
  - `TypedApproval` (onboarding §8.4) refuses an approving entry with an intent. Its entries are
    replies, and busmon never writes `intent` on one, so this is defence in depth;
  - the concierge's `typed_ref` check (concierge §5.3) refuses a referenced entry whose relay
    carried an intent;
  - the retro's approval goes through `TypedApproval`, so it is covered too.

  A test greps the repository: no authority check reads `.Typed` except through `Actionable`.
  Reading `Typed` for display or for person-memory's words stays allowed: a question is still the
  person's words.
- **No tracked work from it.** Tracked work starts with `agentbus request send <task> <target>
  [--ref T]`. The `--ref` is the thread it answers, often the person's directive. `request send`
  reads the entry named by `--ref` (or the root of that thread), and refuses when it has an
  intent: "<id> is a question (or a note) from the person: it starts no tracked work; ask the
  person for a directive". The same check runs in `plan` writes that take an entry id, and in
  the retro's request.
- **What code cannot stop.** Master can still do work by hand on a question, without a
  request: it is an agent. Nothing in code links that work to the entry. The skill rule covers
  it, the `[question: answer only]` tag makes the intent impossible to miss, and the rule is that
  no tracked work, approval or delivery can cite the question.
- **Tests (step 4):**
  - a note sent by busmon arrives in `subscribe --drain --json` with `"intent":"note"` and
    `wake=no`, and a question with `"intent":"question"`;
  - the `--brief` tags;
  - `Actionable` is false for both, and true for a plain typed directive;
  - `request send --ref <question id>` is refused, and the same request with a directive's id
    passes;
  - `TypedApproval` refuses an entry with an intent;
  - the answer guard skips a note and counts a question;
  - the grep test for `.Typed` outside `Actionable` in authority checks.

  Step 5 adds: `context` and `draft_by` reach the JSON, and a `reply` press arrives with
  `"draft_by":"master"` and no `typed`.

### 4.4 The counter

`0 / 65536` becomes the line count, `3 lines`. The rune limit (`bus.CmdMaxRunes`) shows only from
90 % of it: `58 912 / 65 536`, in the warning role. The existing check that refuses an
over-limit text is unchanged.

### 4.5 Mock-ups (goldens at 80x24 and 140x40)

80 columns:

```
┌ to: master ────────────────────────────────────────────────────── question ▾ ┐
│ [PR #841] is anything left before I merge it?                                │
│                                                                              │
│                                                                              │
└ ^S send · ^O context · ^N mode · esc keep draft ───────────────────── 1 line ┘
```

At the limit (80 columns, the last line only):

```
└ ^S send · ^O ctx · ^N mode ─────────────────── 3 120 lines · 59 004 / 65 536 ┘
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
- **The press rules are concierge §5.3's** (the person, Q4):
  - `open` just opens.
  - `reply` shows the full text, its sender and its target once more, and sends only on `y`. It
    goes as a directive with `human=yes` and `draft_by=<sender>`, never `typed=yes`. So the
    receiver treats it as the person's press of the sender's own words, never as the person's
    typed approval.
  - **A drafted reply never satisfies a typed=yes gate.** For anything that needs the person's
    typed words (an adoption, a retro, a merge yes recorded by `TypedApproval`), master offers
    no `reply` action. It asks the person to type the answer. Master's skill says so, and a test of
    `TypedApproval` shows a `reply` press never passes.
  - At most 3 actions. Never the `Enter` default. Each press is audited: one line with
    `action=reply` or `action=open`, the report id, and `draft` for a reply.
- **The sender comes from the entry, not from a label** (foureyes r1). The report's sender is its
  `from` field, the positional `report <self>`. Any agent can write one, because agents share an
  ACL user. So busmon:
  - takes the reply's target and `draft_by` from that `from` field;
  - shows it in the confirmation ("draft by coder → coder");
  - offers `reply` only on reports whose `from` is `master`. Reports from other agents show
    `open` actions only.

  A forged `from=master` gets at most a `human=yes` draft sent to master in master's name, which
  master reads as a draft, never as typed. The security role reviews this boundary.

## 6. Build steps (in series), tests

1. **`attention` and `plan.NowNext`** (§3.2): the move, with busmon calling them.
   - Tests: busmon's For you goldens are unchanged, byte for byte; the fixture golden of
     busmon's items in the `attention` ranks against `summary`'s For you counts, minus reports and unread; a read report still counts in busmon's reports and not in `attention`; `go vet`.
2. **`agentbus summary`** (§3.1, §3.3): the verb, `--project`, `--all` and `--json`; `status` with no argument gives the usage error pointing to `summary`.
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
4. **The compose window** (§4): the chip on `Ctrl+O` (the setting `compose.context_key`, and the raw-mode 0x0F test), `context=`, the
   modes, `Ctrl+N`, `intent=`, and the counter.
   - Tests:
     - goldens at 80x24 and 140x40 for each mode, with and without a chip, and at the limit;
     - `Backspace` removes the chip;
     - `question` sends `intent=question` and `typed=yes`;
     - `note` sends as Q3 says;
     - `note` goes to the recipient with `intent=note` and `wake=no`;
     - the key test (`Ctrl+N`, `Ctrl+O` free in compose, and `Ctrl+T` unchanged).
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

- No LLM in `summary`, and no new agent.
- No unread lines and no read state of reports in `summary`: they are one busmon's local marks.
- No code link between a question and work master does by hand without a request. Every coded
  path is guarded (§4.3), and the skill covers the rest.
- No cross-project answers by master: that is the concierge's, later. `--all` only prints.
- No change to what needs the person's typed words.

## 8. The person's answers (ask 1795815392807-0, typed, 2026-11-27)

1. **Q1, option 2:** a new verb, `agentbus summary [--project p] [--json]`, read only, with no
   LLM. `status <agent> <state>` is unchanged. `status` with no argument is a usage error that
   points to `summary`. Master's "where are we" uses `summary` (§3.1, §3.4).
2. **Q2, option 2:** `Ctrl+T` keeps "reply in the question panel". The chip takes `Ctrl+O`,
   which must reach busmon in raw mode on macOS and over SSH. The binding is configurable, and
   the help and goldens are updated (§4.1).
3. **Q3, option 2, generalised:** a note goes to the selected recipient (master by default) with
   `wake=no`, and is read at its next wake, marked as a note. It is not a directive and creates no
   task. A note alone is never actionable as `typed=yes`. busmon's broadcast notes stay as they
   are (§4.2).
4. **Q4, option 1:** `open` and `reply`. A reply is shown in full, sent on a press and `y`,
   as `human=yes` and `draft_by`, never `typed=yes`. A drafted reply never satisfies a typed=yes
   gate, so master then offers no reply and asks the person to type. At most 3, never the
   `Enter` default, each press audited (§5.4).
5. **Q5, option 1:** the current project, and `--all` prints one compact block per project,
   those waiting on the person first. The data is the same, with no LLM, and `--json` works with
   both (§3.1).
6. **Q6, option 1, without duplicating For you:** one line of counts by state, naming only
   anomalies (failed, stalled, quota-paused) with their duration. Agents waiting on the person
   appear in For you only. Zero counts are hidden, except failed. `--all` prints one Team line
   per project (§3.3).
