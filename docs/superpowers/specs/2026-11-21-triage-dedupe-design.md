# Triage without duplicates: one item per action (triage-dedupe)

Status: spec r2, for review (subject `triage-dedupe`; r2: §1's measure named as architect's, with its window and command, reviewer-codex). Not built. Base: origin/dev `2408c6dc`.
Author: architect, from the person's brief relayed by master (cmd `1795252093773-0`, task
`triage-dedupe-spec`). Point 5 of the brief (master's skill) is master's, not here; §3.1 says what
that skill must tell master.

## 1. Today

busmon's triage (`T`, `cmd/busmon/triage.go`) walks the For you list, `forYou()` in
`cmd/busmon/tea_foryou.go`, one row at a time. The rows are:

| Rank | Row | Key |
|---|---|---|
| `rankAsk` | an open ask | `ask:<id>` |
| `rankHealth` | the stall guard (the team waits on the person) | `health:<check>:<since>` |
| `rankWait` | a waiting agent, a stuck screen, a client prompt | `wait:` / `stuck:` / `prompt:<agent>:<since>` |
| `rankGate` | a 4-eyes gate that concerns the person | `gate:<gated>:<ref>` |
| `rankMerge` | a PR in the merge queue | `merge:<head>` |
| `rankUnread` | **every** unread Activity line for the person, a report to the person included | `unread:<id>` |

The duplicates come from the last row and one key:

- **Every report to the person is a row.** `report --to <person>` writes a notice to the person
  (`personNotices`, `cmd/busmon/types.go:162`), and the report becomes a `rankUnread` row
  whatever it says. Architect's measure, on the live bus, not re-run by the reviewers: master's
  reports from `1795115620213-0` to `1795252098915-0` (38 h), read with `agentbus history
  --agent master --kind report --since 2d --limit 0 --json`, a PR counted as `#<n>` in the text.
  Master sent 108 reports to the person, so 108 triage rows. 74 of them name a
  PR as `#<n>`. They name 59 distinct PRs 143 times: 84 mentions repeat a PR an earlier report already
  named. A PR waiting for the merge also has its own `rankMerge` row. So "merge #694" is one
  row, and each report that lists it again is one more.
- **A waiting agent counts twice.** `classifyWaits` (`cmd/busmon/unread.go`) marks a waiting
  agent's newest report unread for the person, beside the agent's `rankWait` row.
- **A merge row's key is the head.** After a rebase the same PR is a new row: new key, so
  `visited` and snoozes keyed on the old head no longer apply.

Asks already dedupe: an ask's own Activity line folds into its row (`Unread`). Master opened no
ask in those 38 h (`agentbus asks`: the newest is `1794957307782-0`); it asks in reports.

## 2. The rule: one item per target

An **item** is one thing the person acts on, its **target**:

| Target | Item key | Kind (§4) | Built from |
|---|---|---|---|
| a PR | `merge:pr:<number>`, or `merge:branch:<branch>` with no number | merge | the merge queue |
| an ask | `ask:<id>` (all its questions) | decision | the ask |
| a gate on the person | `gate:<gated>:<ref>` | decision | the gate |
| a waiting agent (waiting, stuck, prompt) | as today, per episode | answer | the agent's state |
| a stall | as today, per episode | answer | the stall guard |
| a report that needs the person (§3) and names no live target | `report:<id>` | answer or read | the report |

Every message about a target **attaches to that target's item** and makes no item of its own.
The list (`forYou()`) is built once from these targets, so the header badge, the queue strip,
the reminder and the triage walk all count items, not messages; they stay subsets of one list.

**Which target a message names (derived, no flag).** In a report's or a line's full text:

- `#<n>`, `PR <n>` or `PR #<n>` where `<n>` is the number of a live merge item;
- a branch name of a live merge item;
- the stream id of a live ask;
- a waiting agent's own newest report names that agent's wait item (today's `classifyWaits`).

A number that matches no live item is ignored, so "#1 priority" attaches nothing.

## 3. Which reports are items

**A report is an item only when it says it needs the person.** One new field on the report,
set by the sender:

```sh
agentbus report master --to <person> --needs answer "Merge order for 694-696? …"
agentbus report master --to <person> --needs read   "Release notes are ready: …"
```

- `--needs answer`: the person must reply. The item leaves the list when the person sends a
  message to that sender from the item's page (the send that closes a waiting agent's row today,
  `walkDone("sent")` in `cmd/busmon/tea_input.go`), or when the
  sender writes a newer `--needs` report naming the same targets (it replaces the item).
- `--needs read`: the person must read it. Opening it is the read, as for an unread row today.
- No `--needs`: the report goes to Activity only. It stays unread there, with its mark, and
  attaches to the items it names (§5). It is never a triage item.

A `--needs` report that names exactly **one** live target folds into that target's item: no
new row, the item shows "needs your answer" or "to read", and the report is first in its
context. With no live target, or several, it is its own `report:<id>` item, and it is also
shown in the context of each target it names.

**Why a flag, not derived.** Master writes to the person in the person's language. Only 2 of
the 108 said "nothing to do" in words a pattern could catch. A question mark or a phrase is not
a reliable signal, and a missed ask is worse than a duplicate. The sender knows; one flag says
it. The field goes in `bus/` (`Bus.ReportTo`, `bus/stream.go:447`) as `needs`, additive in the
report entry and in the `subscribe` JSON (same `"v"`). Any other value is refused.

### 3.1 What master's skill must say (point 5, master's)

Use `--needs` only for a new ask: a question for the person, or a document they must read.
A status report, a "still waiting" list or "nothing new" carries no `--needs`. To ask about a
PR already in the merge queue, name it (`#694`): the question lands on its merge item.

Other lines for the person that are not reports (a directive to the person, a notify for the
person) follow the same attachment: one naming a live target attaches to it; one naming none
stays a `read` item, and a newer one from the same sender supersedes it, as today.

## 4. The first page: a summary of the actions

`T` opens a summary page, not the first item:

```
Triage: 4 actions                                     answered 0 · sent 0 · read 0
▸ merge     PR 694  busmon triage dedupe               approved   2h
  decision  ask     Broker questions (3 open)          master    40m
  answer    coder   waiting: "which base for #696?"    coder     12m
  read      master  Release notes v0.33 are ready      master     5m

  next plan task: ▸ triage-dedupe build (Enter)
```

- One line per item: kind (merge, decision, answer, read), target, title, who, age. Sorted by
  today's rank, then oldest first.
- ↑↓ move, Enter opens the item's page (today's page for that row), and that is the walk at
  that item: `n`/`p` move through the items as today.
- **Esc on an item's page returns to the summary**; Esc on the summary ends triage.
- After a terminal act (answered, sent, read) the walk moves to the next item by itself, as
  today. After the last one it returns to the summary, which replaces today's end page. The
  summary's counts line and its plan-next line are the end page's.
- An item gone while the walk is on it (an ask answered elsewhere): back to the summary.

## 5. Context on the item's page

Each item's page gets a **Context** section under its own fields: the messages attached to it
(§2), newest first, at most 3. Each is one line: sender, age, the report's headline. Enter on
one opens it in full. With more: "n older in Activity". A `--needs` report folded into the item
comes first and says so.

So master's explanation of PR #694 shows on #694's merge page, and nowhere as a page of its own.

## 6. Steps (in series; each its own PR and review)

1. **bus and CLI:** `report --needs answer|read` writes `needs`; `subscribe` JSON and
   `agentbus history` show it; any other value refused. Tests: the field round-trips; a report
   without the flag has no field; a bad value is refused. Master's skill (point 5) can land with
   or after this step.
2. **busmon list:** `forYou()` builds items by target (§2): reports only with `needs`; the
   attachment by number, branch, ask id and waiting agent; the merge key by PR number;
   `classifyWaits`' report folded into the wait row. Tests: three reports naming #694 and a
   merge item for #694 make one row; a `--needs answer` report naming #694 folds into it; one
   naming nothing is its own row; a rebased PR keeps its key; "#1" with no PR 1 attaches nothing.
3. **busmon pages:** the Context section (§5) on merge, ask, gate, wait and report pages.
4. **busmon triage:** the summary page (§4); Esc on an item returns to it; the end page goes.
   Tests: `T` opens the summary; Enter then Esc returns to it; the last terminal act returns
   to it; the summary's counts.

Step 2 needs step 1 merged and master using `--needs`: before that, every report would leave
triage at once.

## 7. Questions for the person

1. **The flag: `--needs answer|read`, or one boolean `--ask`?** With one boolean, every ask is
   "answer" and the kind "read" goes. Recommended: `--needs answer|read`.
2. **Reports sent before step 2, with no `--needs`:** Activity only, like any new one?
   Recommended: yes; the open ones are in master's next report.
3. **The header's unread badge.** Today it counts unread For you rows, so every report. After
   step 2 a plain report is no item. Should the badge count Activity's unread lines (plain
   reports included), or only items? Recommended: two numbers as today, items for the For you
   badge and Activity's unread marks for the unread badge.
4. **A stall row's kind.** The stall guard says the team waits on the person. Recommended:
   "answer". The alternative is a fifth word, "unblock".
5. **A `--needs` report naming several PRs** ("merge order for 694-696?"): its own item, shown
   in the context of each PR. Recommended: yes. Folding it into one PR would hide it from the
   others.
