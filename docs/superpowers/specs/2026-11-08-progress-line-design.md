# Requests and Plan in the person's order, a progress line per task, master's background work (progress-line)

Status: spec, for review (subject `progress-line-spec`). Not built. Request: the person, 2026-11-08,
through master (directive `progress-line-spec`). Nothing is implemented before the person's go.

The person asked for three things:

1. **Requests and Plan read in the person's order:** decisions for the person first, then tasks by
   priority, with the priority visible as a column. The Plan says `7 of 11` with its bar,
   highlights the task in hand and names the next one. Each request shows its plan task.
2. **A fine, discreet line per task in hand:** when it started, how far along it is, and when it
   should end. The end is estimated from the pace of the tasks already done, and it says
   `estimate` when there is little to go on.
3. **Master's background work, visible:** for example `2 checks running`, published by master so
   busmon shows it.

## 1. What busmon does today

Most of the order already exists. This spec adds what is missing and changes nothing else.

| Asked | Today |
|---|---|
| Decisions for the person first | Yes. Requests opens with `Owed by you` (asks, gates and requests the person owes); the Plan opens with `NOW`, whose first rows are the tasks waiting for the person (`plan.Order`) |
| Tasks by priority | Yes in `Tracked` (request-priority §6: requested ones by level, then unread, then age). No elsewhere, on purpose: a priority orders ready work only |
| A visible priority column | **Partly.** A one-cell mark: `↑` high (attention colour), `↓` low (muted), blank for normal |
| Plan header `7 of 11` with the bar | **Partly.** `v0.29  1/6 done  ━━━━━━━  16%` |
| The task in hand highlighted | **No.** It is a row of `NOW` like the others; only the selection is highlighted |
| The next task named | **Partly.** It is the first row of `NEXT`, not named in the header |
| Each request shows its plan task | **No.** The link exists (a plan task's `board` field is its request's task name) but no row shows it |
| A progress line per task | **No** |
| Master's background work | **No.** Master can only write it in its status message, and its turn hooks overwrite that message at each turn (§4.1) |

## 2. Requests and Plan

### 2.1 Requests: a priority column

At the wide width (`reqKindWidth` and up), the one-cell mark becomes a word column, `PRI`, 4 cells,
between the names and the kind:

```
  Owed by you  2
  ◆ coder → you          high  ask      merge PR 377 and 378                         4m
  ◆ foureyes → you             gate     idle-animation: dots stay static?            9m
  Tracked  3
  ● master → coder       high  request  idle-animation        plan #t4  in progress   2m
  ○ master → coder             request  progress-line-spec    plan #t5  requested    12m
  ○ master → coder       low   request  docs-typos                                  1h
```

- `high` in the attention colour, `low` muted, nothing for normal: what the mark says today, in
  words. A normal request shows no word, so the column draws the eye only when a level was set.
- Under `reqKindWidth`, the one-cell mark stays as it is: there is no room for a word.
- The order does not change: request-priority §6 already sorts `Tracked` by level. `Owed by you`
  stays first, sorted as today. A priority never moves a row out of its section.

### 2.2 Requests: each request shows its plan task

- A tracked request whose task name is a plan task's `board` gets `plan #<id>` after its subject,
  muted, and the plan state's word after it (`in progress`, `in review`, `requested`).
- No plan loaded, or a request no plan task names: nothing is shown.
- Too narrow for both, the subject keeps its room and `plan #<id>` is cut first.
- Enter on the row opens the request's page as today; the page gains a `plan` field with the
  task's id and title.

### 2.3 Plan: the header

```
 v0.29  7 of 11 done  ━━━━━━━━━━━━━━━━━━  64%  ·  now: idle-animation (coder)  ·  next: tag v0.29
```

- `7 of 11 done` replaces `7/11 done`: the person's wording. Narrow keeps `7/11 64%`.
- `now:` names the task in hand: the first task in progress or in review, else the first one
  waiting for the person, else blocked (the idle view's `next` frame already picks it this way).
  Its owner in brackets. No task in hand: `now:` is left out.
- `next:` names the first upcoming task. None: left out. Every task done: `every task is done`.
- Too narrow: `next:` goes first, then `now:`.

### 2.4 Plan: the task in hand is highlighted

- The `now:` task's row in `NOW` is drawn with its title bold and in the accent colour, and the
  row keeps its state glyph. The selection highlight still applies on top when the person selects
  it. Only one row is highlighted.
- Under `NO_COLOR`, bold alone carries it.

## 3. A progress line per task in hand

### 3.1 What it shows

Under each task in hand, one muted line:

```
  ● idle-animation  coder  in progress
      started 22:12  ━━━━━━━━━━━━━━  ~55%  ·  ends ~22:58
  ● progress-line-spec  coder  requested
      not started  ·  usual length 46m
```

- **Where:** in the Plan, under each `NOW` task in progress; in Requests, under each accepted
  tracked request row (a group of one; a folded group of several shows none). The idle view's
  `next` frame is not changed.
- **Started:** the request's `accepted_at` on the board, as a clock time (`22:12`), with the day
  when it is not today (`Sat 22:12`). A task with no linked request has no line.
- **Progress:** time used, not work done. busmon has no measure of the work itself, so the bar is
  the time since the start over the usual length of a task (below). It reads `~55%`, the `~`
  saying it is an estimate. It stops at 95% while the task is open: past the usual length, the
  line says `longer than usual (46m)` instead of a share, and the bar stays at 95% in the muted
  colour.
- **Ends:** start plus the usual length, as a clock time: `ends ~22:58`.
- **Discreet:** one line, `RoleDim`, the bar in the gauges' thin cells (`━`),
  10 cells, filled ones in the done colour and the rest muted, as `planProgress` draws its bar. No animation: it changes with the poll, like the rest.

### 3.2 The usual length, and when to say `estimate`

- **The pace:** the median of `responded_at − accepted_at` over the last 10 requests of the same
  owner that reached `done` on the board. `done` is the delivery (the work, before review), so
  the review's wait is not counted. The median keeps one long night from skewing it.
- **Thin data:** with fewer than 5 such requests, the line says `estimate ~22:58` instead of
  `ends ~22:58`, and the bar has no share (`━━━━━━━━━━  estimate`, all muted).
- **No data:** no done request for that owner: `started 22:12  ·  no estimate yet`.
- **The plan's end:** not shown. It would multiply a thin median by the tasks left, ignore that
  tasks run one at a time per agent, and look more precise than it is. Open question 2.

### 3.3 Reads

No new read: the board is already polled for both tabs (`planState.fold`, `requestGroups`), and
`accepted_at`, `responded_at` and the state are fields of its entries.

## 4. Master's background work

### 4.1 Why not the status message

Master's turn hooks publish its state at each turn (`working` at a prompt, `idle` at the turn's
end), each with its own message, so a note written into the status message is gone at the next
turn. The `{p}:agents` snapshot is rewritten at each status too (`putSnapshot`). The indicator
needs its own record, which a status leaves alone.

### 4.2 The record and the command

- **Key:** `{p}:background:<agent>`, a hash with `text`, `since` (Unix ms) and `by`. It expires on
  its own: `PEXPIREAT` at `since + for`, both from Redis `TIME`. A check that ends without master
  clearing it leaves no stale indicator past its time.
- **Command:**

  ```
  agentbus background master "2 checks running" --for 20m   # set or replace
  agentbus background master --clear                         # the work ended
  ```

  `--for` defaults to 30m, at most 24h. The text is one line, at most 60 characters, through the
  same validation as a status message (`ValidateText`). The agent name is validated (`ValidName`).
  An agent sets its own record; nothing stops another from writing it, as with a status today.
- **Not a board entry and not the snapshot:** neither `TestNoStructWriterOfTheBoard` nor
  `putSnapshot`'s rule is touched. The `subscribe` JSON does not change.

### 4.3 What busmon shows

- **Agents panel:** after the agent's status message, `… 2 checks running` muted, with its age at
  the wide width (`… 2 checks running 4m`).
- **Header:** when master has one, the master chip gains ` · 2 checks` (the text's first two words
  when it starts with a number, else `…`). The person sees master is busy without opening a panel.
- **Idle view:** the `team` frame shows master's row even when master is idle, with the text.
- **Read:** one `HGETALL` per agent of the snapshot, pipelined in the existing poll. No `SCAN`.
- A missing or expired key shows nothing. A read error shows nothing too, and the panel's read
  marker (`?`) says the read failed, as for the other per-agent reads.

### 4.4 Master's side

The `agent-bus-master` skill gains two lines: set `agentbus background master "<n> checks running"
--for <the run's bound>` when it puts a run in the background; `--clear` when notified that it
ended. Other agents may use it too; the skill says so, no role needs it.

## 5. Tests (for the implementation)

- Requests: the `PRI` column at the wide width (`high`, `low`, blank), the one-cell mark below it;
  `plan #<id>` and the state word on a linked request, nothing on an unlinked one or without a
  plan; cut first when narrow. Goldens regenerated.
- Plan header: `7 of 11 done`; `now:` and `next:` picked as §2.3, left out when absent, dropped in
  order when narrow; `every task is done`.
- Highlight: one row bold accent; none when nothing is in hand; bold under `NO_COLOR`.
- Progress line, with a fake clock and board: the median over the last 10 done of the owner; 5 or
  more says `ends`, 1 to 4 says `estimate` with no share, 0 says `no estimate yet`; capped at 95%
  and `longer than usual` past it; a task with no request has no line; a folded group has none.
- Background: the command's limits (`--for` 0, 25h, a 61-character text, an invalid agent are
  refused); `--clear`; the key's expiry is set from Redis `TIME` (disposable Redis); busmon shows
  it in the panel, the header and the idle view, and nothing once it expired.

## 6. Size

Three PRs after the go, in this order, each from `origin/main`:

1. Requests and Plan (§2): busmon only.
2. The progress line (§3): busmon only.
3. Background work (§4): `bus/` (the record), `cmd/agentbus` (the command), busmon, the master
   skill.

## 7. Questions for the person

1. **Progress as time used.** busmon cannot see how much of a task's work is done, only how long
   it has run against the usual length. Is a time-based bar what you want, with `~` and `estimate`
   saying so? (Recommended: yes. The alternative, agents declaring their own step `3/5`, needs
   every agent to report it and is easy to forget.)
2. **The plan's end date.** Leave it out (recommended, §3.2), or show `ends ~Tue` with
   `estimate`?
3. **The pace window.** The same owner's last 10 done requests, `estimate` under 5. Keep these
   numbers?
