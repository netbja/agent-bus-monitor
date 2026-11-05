# One row per report (report-one-row)

Status: for review (spec only, no code); the person's answers to the open questions are in §8. Author: coder. Asked by master on the person's
request, 2026-11-05 (directive 1793861459157-0).

## 1. The problem, as it is today

`agentbus report <a> --to <t> <msg>` writes two entries (`cmd/agentbus/main.go`, `case "report"`):

1. the report in `{p}:report` (`message` = the ≤500 one-line preview, `full` when it differs);
2. one directive per `--to` target in `{p}:cmd`, `ref` = the report's id, body
   `reportNotice(id, msg)` = `Report <id> for you (full text: agentbus reports <id>): <first line>`.

busmon shows both:

- **Activity** has one line per entry, so a report to the person is two lines: `master report ## Done - Vérif…`
  and `master → <person> directive ## Done` (the notice reads as its subject, `reportSubject`, since
  report-notice-unread). Screenshot: `~/.cache/agentbus/attachments/20261105-072957-a723e474.png`.
- **For you** lists the unread notice as `unread master ## Done` (screenshot `20261105-072804-790d43fc.png`):
  the subject is the report's first line, and the reports start with a Markdown heading, as the
  report shape note (`reportShapeNote`) suggests. When the agent waits for the person,
  `classifyWaits` also marks its newest report, so that report can get a For you row of its own
  next to the notice.
- **Read state** belongs to the person and stays in busmon's local cache
  (`~/.cache/agentbus/busmon/<p>.json`, `read`, `localcache.go`), "never on the bus" (busmon UX spec,
  item 8). Master cannot see whether the person has opened a report. `agentbus reports` prints no
  read state. The Requests view's unread/read comes from the target's consumer-group cursor
  (`reqInput.state`), and the person has no consumer group, so a notice to the person has no
  delivery state there either.

## 2. Activity: one row per report

A report and its notices are one **report row**:

- **The join is exact:** a `cmd` entry of type `directive` whose `reportSubject` parses and whose
  report id equals its `ref` is a notice of the report with that id. No other entry folds, and
  nothing is joined by its text or by its time.
- **The row** is the report's row, in today's look, with a `→` column listing the notice targets,
  as a fan-out does (`feedLine.to`, rendered like `sameFanOut`'s `(+N more)`).
- **The row's mark** is the notice's mark: unread until opened, never superseded
  (report-notice-unread is unchanged). Opening the row reads the notice ids it holds and the
  report id: the `read` cache gets every member.
- **Order.** Each stream is tailed on its own, so a notice can reach the feed before its report,
  or with other lines in between. The fold therefore looks the report up by id, not by adjacency.
  It uses two maps kept beside `feed`:
  - **`reportLine`** maps a report id to the `seq` of the line that shows it.
  - **`noticeLine`** maps a report id to the `seq` of a notice line still waiting for that report.

  Both maps hold `seq`, never a slice index. `add` re-slices `feed` to `feedCap` (`types.go`), and
  an index would go stale there. `seq` only grows, so a line is found again by its `seq`, with a
  binary search over `feed`. When `add` trims the feed, every entry whose `seq` is below the first
  kept line's is deleted from both maps. A late notice therefore never folds into a line that has
  left the feed: it becomes a line of its own.

  The two arrival orders:
  - **The notice arrives after its report:** it joins the report's line.
  - **The notice arrives before its report:** it stays a line of its own until the report comes.
    The report then takes over that line in place, keeping its `id` and `seq`, so Activity's
    selection, which `listState` anchors by line id, stays on it. The line keeps its position in the
    feed and shows the report's time. No line is added for the report.
- **The report is not at hand** (trimmed, beyond the backfill `--limit`, a stream read failed): the
  notice stays a line of its own, as today. A notice never vanishes for lack of its report.
- **Fan-out.** Each `--to` target still gets its own directive, so each agent keeps its own
  delivery and wake. Only the display folds them.
- **The `y` copy and the plain line** (`plainLine`) stay the report's, with ` → t1, t2` appended.
- **A notice whose report was written by someone else** (`deliver.go` writes it as the delivering
  agent, `self`) folds the same way: the join is by id, not by author.

The Requests view is unchanged: a notice there is a thread in the Notices section, and that
section is already one line (`ntc:all`).

## 3. For you: one row per report

- **One row per notice.** An unread notice is one For you row, keyed by the notice
  (`unread:<notice id>`), and its text is the report's headline (§4).
- **No second row for the same report.** If `classifyWaits` marks a report that has a notice
  **to the person** in the feed (`personEntry` true for that notice), it does not add the report:
  the notice's row stands for it.
  - A notice to an agent does not count. Example: master waits for the person while its newest
    report went `--to coder`. That report keeps its own For you row, because the coder notice is not
    a For you item.
  - A waiting agent's report with no notice keeps today's behaviour.
- **Several notices for the same report** (two names that are both the person's, or a re-send) make
  one row. It holds all their ids, as recap rows do (`recapRow.IDs`), and Enter reads them all.
- **The recap's For you section** uses the same rule. It already reads a notice as its report
  (`reportText`) and merges identical items, so only the headline changes there.

## 4. Preview: the headline, not the heading

`bus.ReportHeadline(text string) string` is pure, shared by the writer and busmon:

1. Read the report's lines: from `full` when the report keeps one, else from `message`. Without
   `full`, the original was a single line, because `full` is kept only when the preview differs
   from the original.
2. Skip blank lines and Markdown heading lines (`^#{1,6}\s`), remembering the first heading.
   Also skip code fences (lines starting with three backticks or `~~~`) and every line inside a
   fence, plus table lines (starting with `|`). None of these makes a headline.
3. Take the first other line, strip a list marker (`- `, `* `, `+ `, `1. `), and cut it at the
   end of its first sentence (`. `, `! `, `? ` followed by a capital letter or a digit) or at the
   line's end. Examples: `## Done\n- Vérif: …` → `Vérif: …`; `## Done\n\nmerge-queue 958b636
   approved. Next …` → `merge-queue 958b636 approved.`
4. If there is no such line, return the first heading without its `#`s: `## Done` alone → `Done`.
   With no heading either, return `""`, and the caller keeps today's text.

Where it applies:

- **The notice's subject.** `reportNotice` writes `ReportHeadline(msg)` instead of the first line,
  so `agentbus subscribe` and `--brief` show it too. If the headline is empty, it keeps the first
  line. The headline then goes through today's guards unchanged (`reports.go`):
  `SanitizeReportMessage`, then the 300-rune cut with `…`. The envelope prefix is unchanged, so
  `reportSubject` still parses old and new notices.
- **busmon's text for a report** (the Activity report row, the For you row, the recap, `i`'s
  `re:` quote): `ReportHeadline` over the report when it is at hand, else over the notice's
  subject (old notices: `## Done` → `Done`). The ` (+N)` marker stays on the Activity report row.
- **`agentbus reports`** (the table) shows the headline in place of the flattened preview.
  `reports <id>` and `--json` (`message`, `full`) are unchanged: those are the data.

The report shape note keeps its advice. Its first line already says to begin with a summary line;
the headline rule only makes a report that opens with a heading readable anyway.

## 5. Master: was a report to the person read?

What exists today:

- **busmon's local cache, `read`.** It holds the ids the person opened, but only on the host
  running busmon, which can be the laptop over the SSH tunnel. Several busmon instances each keep
  their own. It is not on the bus.
- **The Requests delivery state.** It reads consumer-group cursors; the person has no group.
- **Nothing in `agentbus reports`, `thread` or `history`.**

So master cannot know today. Two ways were weighed; the person chose **B** (§8):

- **A. Read the cache file** (`agentbus reports` reads `~/.cache/agentbus/busmon/<p>.json` on its
  own host). No bus write and no change to the decision, but it is right only when busmon runs on
  master's host, and it is silently wrong otherwise. Not recommended.
- **B. A read receipt on the bus (chosen).**
  - **Write.** When the person opens a report row or a For you row that stands for a report
    notice, busmon writes `{p}:seen` (a hash, notice id → `<at ms>|<person>`) through one script,
    as the person (`busWriter`, like `HumanCmd`). The time comes from Redis `TIME`, the first
    opening wins, and the hash is capped at 512 fields, the oldest ids pruned.
  - **Scope.** Only report notices addressed to the person: no other entry, and no unread count.
    No stream entry, no wake.
  - **Key class.** Runtime in `bus/keykinds.go`. Not in `projectKinds`, ignored by
    `HasKeysBesidesDispatch`.
  - **`agentbus reports`** marks each report that has a notice: `→ <person> read 07:31` or
    `→ <person> unread`, plus `→ <agent>` for agents, whose delivery is read from their group as the
    Requests view does. `--json` gains `notices: [{target, id, seen_at}]`.
  - **`agentbus reports --unread [--to <t>]`** lists only the reports a person has not opened
    (exit 64 when none), so master can follow up instead of re-sending.
  - **busmon stays the reader of its own cache.** `{p}:seen` is written in addition to the cache,
    never read back as the person's read state, so a second busmon's marks do not change. Whether
    marks should sync across busmons is out of scope.

B reverses "read state never on the bus" for this one fact, by the person's decision (§8). The busmon UX
spec's item 8 still holds for everything else: the unread marks themselves stay in the local cache.

## 6. Tests (when implemented)

- **`activityFeed.add`.** Each case asserts the line count, the marks and the `to` list:
  - a report then its notice: one line;
  - a notice then its report, with a status line between: one line;
  - a notice whose report is absent: one line, the notice's own;
  - a notice before its report: the report takes over that line, and its `id` and `seq` are
    unchanged (the selection follows);
  - past `feedCap`: a report trimmed from the feed leaves `reportLine`, and its late notice is a
    line of its own;
  - a report with two `--to` targets: one line;
  - a notice whose `ref` is not its report id (a forged prefix): two lines.
- **`marks`.** The report row is unread until opened, and opening it reads every member.
- **`forYou`.** A waiting agent's report that has a notice to the person gives one row. A waiting
  agent's report whose only notice went to an agent keeps its own row. Two notices for the same
  report give one row holding both ids.
- **`ReportHeadline`.** A table test: heading then list, heading then a sentence, heading alone,
  no heading, blank lines, a numbered list, a sentence with `e.g. x`, a code fence and a table before
  the first sentence.
- **Mutants:** no heading skip, no sentence cut, no list strip.
- **`reportNotice`.** Golden of old and new envelopes; `reportSubject` parses both. A control
  character in the headline is mapped to a space, and a headline over 300 runes is cut with `…`.
- **busmon goldens.** Activity and For you with a report to the person, at the wide and medium
  tiers.
- **B.**
  - The `{p}:seen` script, Redis suite: first open wins, the cap holds, the key class is set.
  - `reports` read/unread, and `--unread` exit codes.

## 7. Out of scope

- Changing what `report --to` writes (still one directive per target, waking).
- Folding other duplicate pairs: a verdict and its notice, a request and its board line.
- Syncing unread marks between several busmons.

## 8. The person's decisions (through master, 2026-11-05, directive 1793911995965-0)

1. **§5: B.** busmon writes the read receipt `{p}:seen` for report notices to the person, and
   `agentbus reports` shows read/unread per notice, with `--unread`. A is not built.
2. **§4: yes.** The `agentbus reports` table shows the headline in place of the flattened preview;
   `reports <id>` and `--json` keep the data unchanged.

No question is left open.
