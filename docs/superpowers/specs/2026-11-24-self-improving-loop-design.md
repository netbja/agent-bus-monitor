# A retrospective after a task with friction (retro)

Status: spec r4, for review (subject `retro-spec`). Not built. Base: origin/dev `af527c19`.
r2: foureyes's two notes on r1 (report `1795560028942-0`): `agentbus answer` refuses through
`agentcaller.Refuse`, because the approval rests on it (§5, build step 2); busmon starts a retro
from the palette, since `R` is the recap key (§3.3).
r3: reviewer-codex's four findings on r2 (verdict `1795574033505-0`): an approval is bound to its
question by a new `qid` field on the answer's reply, cross-checked with the ask's own record
(§5). `later` is a final answer, recorded as such, never a question left open (§5). A new
skill's file may be absent, and is checked absent again at apply (§4, §6). Every retro whose
ask is fully answered goes to docs, which writes the record of all outcomes, approved or not
(§6).
r4: foureyes's reject of r3 (verdict `1795574372600-0`). An ask author holds one open ask at a
time (`askCreate`, `bus/asks.go:276`), so a retro ask in master's name would fail while master
has an ask open, and would block master's own asks while it waits. Retro asks now have their own
author, `retro`, with the answers sent to master. One is open at a time and the rest queue (§4).
A withdrawn retro ask routes like a closed one (§6). `said=yes` may be typed free text (§5).
Author: architect, from master's directive (cmd `1795559518593-0`, task `specs-from-person-docs`),
which hands on the person's note "Directive for archi: self-improving loop for agents".
Reference design: `codeursenior/product-engineer-kit`, skill `pek-auto-improve` (MIT). Its rules
are reused as text. Nothing of it is installed on an agent. No new dependency. The questions for
the person are last (§9).

## 1. The need

When an agent errs, today the output is fixed and the instruction that led to the error stays.
The next agent repeats it. The lessons that did reach the rules came by hand. Master or the person
noticed a pattern and asked docs for a skill line. The private memory files list many lessons
that never reached the repository.

The person's note asks for a loop: after a task that went badly, find the cause, and propose at
most three rule changes. The person approves each one by number. Each accepted change becomes
a commit.

## 2. What already exists, and is reused

| Piece | Where | Used for |
|---|---|---|
| Tracked requests on the board | `bus/requests.go` | The retro is one request (`retro-<task>`). The board keeps it, and `PopDone` will not end a pop that owns it (`dispatch/popdone.go:54`). |
| `typed=yes` and `said` | `bus/stream.go:297`, the answer script in `bus/asks.go` | The person's own words. `human=yes` alone is not enough (person-memory spec §4.7). |
| Asks | `bus/asks.go` | The approval question, answered in busmon's For you. |
| Verdict ledger | `Bus.VerdictsAfter`, `bus/stream.go:629` | Review rejections. |
| CI record and hand-offs | `bus/ci.go` | CI hand-offs to the branch's owner. |
| Event stream | `bus/events.go` | `blocked` and `wake-not-landed` events for an agent: the stall signals. |
| Escalations | `CmdEscalation`, `dispatch/loop.go:875` | An addressed message left unread too long. |
| `agentcaller.Refuse` | `internal/agentcaller/agentcaller.go:74` | Verbs only the person may run. |
| `health.Sensitive` | `health/health.go:212` | Refuses a secret. It refuses the write and never redacts. |
| `internal/scrub` | test suite | Flags private names in committed files. |
| Merge queue and delivery | `bus/mergequeue.go`, `agentbus deliver` | The accepted changes ship as one ordinary PR to `dev`. |

## 3. The trigger: computed, never implicit

### 3.1 Friction, from the bus

`bus.TaskFriction(task)` reads the bus and returns the friction signals of one task. Each signal
is a kind and an id. The window is the request's life on the board, from `accepted_at` to its
`done`. The owner is the request's target. The function reads only facts the bus already holds:

| Kind | Source | Counts when |
|---|---|---|
| `reject` | verdict ledger | A `reject` verdict whose author is the owner, written inside the window. |
| `ci` | cmd stream | A CI hand-off (`bus/ci.go`, `CISent`) addressed to the owner inside the window. |
| `stall` | events, escalations | A `blocked` or `wake-not-landed` event for the owner, or an escalation that names the owner, inside the window. |
| `person` | the person | A retro the person asked for (§3.3). |

Two requests of the same owner can overlap. The work-in-progress limit is 2 (`board.wip_limit`).
A signal can then appear in both retros. The agent that runs the retro judges whether it bears
on its task. "No lasting lesson" stays a valid answer (§4).

The escalation body today is text. The build adds one field, `agent=<name>`, to the
escalation entry. The field is additive and fenced like the rest of the entry. Nothing is
matched in the text.

### 3.2 When it fires

`agentbus request done <task>` runs `TaskFriction` after its transition succeeds. With no
signal, nothing more happens: **no retro on a green, uneventful task.** With one or more signals,
the command sends a tracked request `retro-<task>` from `agentbus` to the owner. Its body lists
the signals by kind and id. The command prints one line: `retro due: retro-<task> (2 reject,
1 ci)`.

The retro is a bus request, so it is never implicit. busmon and `agentbus board` show it. The
owner still holds its session, and the dispatcher's `PopDone` keeps a pop alive while it owns an
open task (condition "it owns a board task"). The pop is ended only after its retro is done.
The dispatcher gets no new condition.

A retro request never triggers a retro. `request done retro-…` skips the friction check.

### 3.3 The person's manual fix

`agentbus retro request <task>` is the person's trigger, for a fix the person made by hand. It
is refused from an agent's process tree (`agentcaller.Refuse`). In busmon, the palette's `/retro` runs it
on the selected task row of the Requests view. `R` is taken: it is the recap key
(`cmd/busmon/keys.go:280`). If the task's owner is a pop that has ended, the retro request goes to
master, who pops the role again or retires the retro (§6).

## 4. The retro itself

The agent that owns `retro-<task>` runs it. Its input is its own session and the bus history of
the task. `agentbus retro evidence <task>` prints that history: the task thread, the signals of
§3.1 with their text, and the review messages of the reject verdicts. The session is never
copied out. Only bus ids are cited.

The agent writes the retro in this order, the reference design's rules:

1. **Root cause first.** One sentence that runs evidence → trigger → mechanism → cause.
2. **At most three proposals**, as a numbered table:
   `n | lesson | evidence | destination | file | change`.
   - `evidence` is one or more bus ids (a cmd, a verdict, a CI hand-off, an event). Each one
     must exist. A proposal with no real exchange or outcome behind it is not written.
   - Approval is never inferred from silence, and one task's own choice is not made into a rule.
3. **Before each proposal,** the agent reads the target file. It checks that the rule is not
   already covered there. A conflict with an existing rule is named in the `change` column.
4. **Destination order:** an existing skill (`skills/*/SKILL.md`, `roles/*.md`) first. Next, a
   context file (`AGENTS.md`, `CLAUDE.md`, a `docs/memory/lessons/` note). A new skill only for a
   workflow that recurs, and the evidence must show it recur.
5. **Prefer editing an existing rule to adding one** (the bloat guard, §7).
6. **"No lasting lesson"** is a valid retro. The agent closes it with
   `agentbus retro none <task> "<root cause sentence>"`, and the request is done.

`agentbus retro propose <task> --file <retro.md>` files a retro with proposals. It checks:

- one to three rows, numbered 1..n, and every column filled;
- each evidence id exists (stream, ledger or history archive);
- each `file` lies under the allowed destinations of point 4 and exists on `origin/dev`. The one
  exception is a row whose destination is `new skill`: its file is `skills/<name>/SKILL.md` and
  must be **absent** on `origin/dev`;
- `health.Sensitive` matches nothing, in any field (the refusal names the field and never the
  value);
- no row repeats a rejected proposal (§6);
- the project's daily cap (§7).

It stores the retro in `{p}:retros` and records the git blob id of each target file, or
`absent` for a new skill. Then it opens the retro's ask and records the ask's id in the
retro. The ask is never replaced.

- **The ask's author is `retro`,** a reserved sender like `agentbus` (`SystemSender`,
  `bus/asks.go:46`). It is never an agent's name, and `agentbus ask` refuses it as the agent
  does today for `agentbus` (`cmd/agentbus/askcmd.go:40`). The answer script gets one rule,
  next to its `SystemSender` rule (`bus/asks.go:504`): a `retro` ask's reply goes to master and
  wakes master. Master's own ask slot (`open:master`) is never used, so a retro never blocks
  master's merge asks, and master's open ask never blocks a retro.
- **One retro ask is open at a time,** because `open:retro` is one slot. When the slot is taken,
  `propose` stores the retro as `queued` and says so. The retro's request still closes, and the
  pop may end, because its work is filed. The person sees the queue in For you through the open
  ask's title, `retro 1 of N`. The ask has one
question per proposal, with question id `p<n>`, and three options: `yes`, `no` and `later`. The ask's text is the root cause
and the table. Then the agent runs `request done retro-<task>`.

## 5. Approval: only the person's typed answer

- **An approval is the person's typed answer to that proposal's question.** Today the answer's
  reply entry does not name its question (the answer script, `bus/asks.go:327`). Without that,
  a `yes` to #1 could pass for #2. The build adds one field, `qid=<question id>`, to that reply
  entry. It is additive and written by the same script, so it is part of the same entry as
  `typed` and `said`. A proposal `n` is approved only by a reply entry with all of `typed=yes`,
  `ref` = the retro's recorded ask id, `qid=p<n>`, and `said=yes`. While the ask's record is
  kept (`<id>|a|p<n>` in `{p}:asks`, seven days after it closes), its choice must also be `yes`.
  After that, the reply entry is the record: it stays in the stream or in the history archive.
  Then `said=yes` cannot tell the option `yes` from free text typed as `yes`. Both are the
  person's own typed word, so both count.
- **A `yes` from master or any agent is not an approval.** A directive, a report and a
  `human=yes` text that agentbus composes are not approvals either.
- `no` rejects the proposal, and the rejection is stored (§6). `later` is a final answer
  too. An ask's answer cannot be changed, and the ask closes when every question is answered.
  So `later` is recorded as the outcome `later`, and the proposal is not applied. Unlike `no`, it
  is not put on the rejected list, so a later retro may propose it again with fresh evidence.
  Nothing is reopened. An unanswered proposal is never applied.
- "All", "none" or a subset are answered one question at a time. busmon already lets the person
  answer each one.

- **The answer's own guard must hold.** `typed=yes` is written on every answer, whoever runs
  `agentbus answer` (`bus/asks.go:327`). Today `runAnswer` (`cmd/agentbus/askcmd.go:198`) refuses
  an agent only when `AGENT_BUS_AGENT` or `AGENTBUS_TERM_ID` is set. An agent that unsets both
  could answer in the person's name. The build adds `agentcaller.Refuse` (the process tree) to
  `answer`, as `retro request` has, before any retro is applied. The security role reviews
  that step. On one host, the same user can still write the stream directly. That is the limit
  the person-memory spec states, until the broker ACL is on.

`bus.RetroApproved(task, n)` is the one check. It returns the approving entry's id, or a reason
for refusing. Every later step calls it. No step reads the approval any other way.

## 6. Apply, record, and the rejected list

Master is woken by each answer. The ask closes when every question is answered, or when
master withdraws it (`agentbus retro withdraw <task>`, the `retro` author's `Withdraw`), for
example when a retro is no longer wanted. Either way, master runs `agentbus retro route <task>`.
It sends `retro-apply-<task>` to docs, whatever the answers, and opens the next queued retro's
ask. Questions left unanswered are recorded as `unanswered`. Every outcome needs its
record (below). docs is the role that edits these files and delivers. It may also
be the only role still running, because the author was a pop that has since ended.

docs runs `agentbus retro show <task>`. It prints every proposal with its outcome: `accepted`
only when `RetroApproved` passes, with the approving entry's id; otherwise `rejected`, `later` or
`unanswered`, each with its entry id when there is one. docs applies only the `accepted` rows.
For each one:

1. Re-read the target. Its blob id must match the one recorded at propose time. If it differs,
   the file changed since, and docs re-reads the changed part. If the change touches the
   proposal's lines, docs reports a conflict to master (`report docs --to master`) and does not
   apply it. The person decides again on a fresh retro. For a new skill, the file must still be
   absent on `origin/dev`. If it now exists, that is a conflict too.
2. Apply the change in 1 to 3 sentences, and keep the unrelated content.
3. One commit per proposal on a branch `retro/<task>` from `origin/dev`, with the trailers
   `Retro: <task> #<n>` and `Approved-By: <entry id>`.

The branch also adds the retro's record, `docs/memory/retros/<date>-<task>.md`. It holds the root
cause, the table, and every proposal's outcome as `retro show` prints it, with its entry id.
docs writes it from `retro show` and not from memory. docs delivers the branch the usual way: a PR to `dev`,
reviews, the merge queue. docs never pushes on its own (decision 2026-11-02). Then docs runs
`agentbus retro close <task> --pr <n>`.

A retro whose proposals are all `no` or `later` also delivers its record, as a PR that contains
only the record.

**The rejected list.** `{p}:retros` keeps each rejected proposal's file and lesson, and the
repository keeps the records. `retro propose` refuses a row whose file and normalised lesson
(lower case, whitespace collapsed) equal a rejected one. Before writing, the agent reads
`agentbus retro rejected --file <f>`. That catches a reworded repeat, which an exact match
cannot.

## 7. Guards

- **Secrets and private text.** `health.Sensitive` runs on every field at `propose` and again at
  `show`. `internal/scrub` runs on the committed files. Evidence is a bus id and never a quote
  from a session. A rule names a behaviour, never a person or a private exchange.
- **Bloat.** At most 3 proposals per retro, and one open retro per agent. When a target file has
  grown past `retro.prune_lines` (default 400 lines for a `SKILL.md`), `retro propose` adds a
  note to its ask saying so. A **pruning pass** is a retro of kind `prune`. Master or the person
  starts it with `agentbus retro request --prune <file>`. It proposes merges and deletions in the
  same table, and the person approves it the same way.
- **Cap.** `retro.max_per_day`, project setting, default 3 retros that reach the person. Over the
  cap, `request done` still sends `retro-<task>`. The owner then closes it with `retro none`,
  noted `capped`, and keeps its signals in the request's done body. A lesson that recurs fires
  again on a later day.

## 8. Build, tests, and what this does not do

**Steps (in series, each its own PR to `dev`):**

1. Bus: `TaskFriction`, the `retro-<task>` request from `request done`, the `agent` field on
   escalations, and `agentbus retro request/evidence/none`.
2. Bus and CLI: `{p}:retros`, `retro propose` (with its checks), the ask, `RetroApproved`,
   `retro show/rejected/close`, and the settings in `config/keys.go`. `agentbus answer` refuses
   through `agentcaller.Refuse`, and the answer's reply entry gains `qid` (§5). The `retro` ask
   author, its reply rule and the queue, `retro route` and `retro withdraw` (§4, §6). The security
   role reviews this step.
3. Skills: the agent-bus skill's retro section (the §4 rules). The master skill's routing
   (§6). The docs skill's apply steps. busmon's palette `/retro`.

**Tests (one seam, the `bus` package, against the disposable Redis):**

- `TaskFriction`: a reject verdict, a CI hand-off and a blocked event inside the window count.
  The same signals outside it, or for another agent, do not. A green task returns none.
- `request done` with no friction sends nothing. With friction it sends exactly one
  `retro-<task>`. `request done retro-…` sends nothing.
- `RetroApproved` refuses each of: a master reply `yes`, a `human=yes` entry without `typed`, a
  typed `yes` to another ask, and `no` or `later`. It passes only the person's typed `yes`.
- **Mixed answers on a two-question ask:** `yes` to `p1` and `no` to `p2` approves #1 only.
  `yes` to `p2` and `no` to `p1` approves #2 only. A reply entry without `qid`, the shape
  before the build, approves nothing.
- `later` closes its question; `retro show` prints it as `later`. A later propose of the same
  row is not refused, but a rejected row is.
- `retro propose` accepts an absent `skills/<name>/SKILL.md` for a `new skill` row and refuses
  one that exists. `retro show` reports a conflict when it appeared since.
- An ask answered `no`, `no` still leads to `retro-apply-<task>`, and the record lists both
  outcomes. A withdrawn retro ask routes too, its questions `unanswered`.
- **The ask slot:** `retro propose` succeeds while master has an ask open, and master can open
  an ask while a retro ask is open. A second `propose` while a retro ask is open stores
  `queued`. `retro route` opens the next queued ask. The answer to a `retro` ask wakes master.
  `agentbus ask` refuses the author `retro`.
- `retro propose` refuses each of: four rows, a missing evidence id, a file outside the
  destinations, a sensitive field, a repeated rejected row, and a fifth retro on a capped day.
- `retro request` and `answer` refuse from an agent's process tree, even with `AGENT_BUS_AGENT`
  and `AGENTBUS_TERM_ID` unset (the `agentcaller` test pattern).
- A mutant run on `RetroApproved`'s `typed` check must fail a test.

**Not doing:** no retro on a green task. No automatic apply, and no apply without the person's
answer. No push outside delivery. No transcript leaves an agent's machine or enters the repo.
No LLM judge of friction: the signals are counted, not guessed. No install of the reference kit.

## 9. Questions for the person

**Q1. Who writes the commits?**
- **(A) recommended: docs.** It edits docs and skills and already delivers. It still runs after the
  author's pop has ended.
- (B) master. It saves a role, but it spends master's context on edits.
- (C) the agent that ran the retro. It knows the case best, but a pop has often ended by then.

**Q2. Where do proposals and rejections live?**
- **(A) recommended: both.** `{p}:retros` is the working state for checks and asks. A record
  under `docs/memory/retros/` is the durable list, in Git, reviewed.
- (B) the bus only (`{p}:retros`, kept by backups). Simpler, but it is not in the repository and
  is lost if the broker is lost.
- (C) the repository only. No bus key. `propose` then reads the rejected list from `origin/dev`,
  so a rejection counts only after its record is merged.

**Q3. The cap.**
- **(A) recommended: 3 retros a day for the project, one open per agent.** Your attention is the
  scarce part.
- (B) one a day per agent.
- (C) no cap, only the 3-proposal limit.

**Q4. The pruning pass.**
- **(A) recommended: on demand.** You or master start it, with a note when a file passes its size
  budget.
- (B) monthly, started by sentinel.
- (C) never: only edit-before-add.
