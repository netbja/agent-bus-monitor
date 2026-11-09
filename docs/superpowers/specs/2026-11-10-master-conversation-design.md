# A conversation with master in busmon (master-conversation)

Status: **spec for the person's review, not built.** Task `master-conversation-spec` (master,
2026-11-10). The person chose a pane in busmon that holds their conversation with master: their
directives, master's reports to them with the full text, a line to write the next one, unread
marks, and each answer's thread. Part 8 holds the questions, each with a recommendation.

## 1. What exists

- **The person's messages to master** are `cmd` entries on `{p}:cmd`. Their `from` is the
  person, their `target` is `master`, their `type` is `directive`, and their field `human=yes`
  (`Bus.HumanCmd`) marks a person, not an agent. A person's `reply` exists only to close a thread
  (`Bus.HumanClose`: `wake=no`, it wakes nobody).
- **Master's reports to the person** are two entries. `agentbus report master --to <person>`
  writes the report on `{p}:report` (its full text in `full` when it differs from the preview)
  and a `cmd` from master to the person, whose `ref` is the report's id: `Report <id> for you
  (full text: agentbus reports <id>): <headline>` (`reportNotice`). Master's plain directives to
  the person, if any, are `cmd` entries too.
- **Threads** link an entry to the one it answers through `ref` (`Bus.Thread`). A report notice's
  `ref` is the **report's** id, not the person's message: today nothing links master's report to
  the directive it answers.
- **Unread marks** live in busmon's local read cache (`feed.unread`: the entries meant for the
  person, the ones they opened). Activity already shows them.
- **Retention:** each stream keeps its last 1000 entries (`streamMaxLen`). Older entries are in
  the history archive (`agentbus history`).
- **Compose** (the window, `^O`, and the one-line input `i`) already writes a human directive,
  with the recipient and a draft.

## 2. What the pane reads (no new storage)

The conversation is a **filter** over what busmon already reads. Nothing new is stored in Redis.

1. **From `{p}:cmd`:** every entry from the person to master (`human=yes`), and every entry from
   master to the person.
2. **For a report notice** (a `cmd` from master to the person whose `ref` is a `{p}:report` id):
   that report's full text, read by id (`Bus.ReportByID`) when the row is first drawn, and kept
   for the session. The pane shows the report, not the notice: its headline in the row, the full
   text on `enter`.
3. **Order:** by stream id, which is time order across both streams. A day header
   (`Tue 11-10`) is drawn when the day changes.
4. **Threads:** a master entry that answers one of the person's entries gets a second line,
   `answers your 09:12 '<first words>'`. `enter` on any row opens the existing reader on its
   thread (`Bus.Thread`), as Activity does. **This needs one addition** (part 8, Q7), since
   `report --to` names only its report today: `agentbus report master --to <person> --re <entry>`
   records the entry it answers, as an extra field `re` on the notice (additive, `subscribe` v1
   unchanged), and master's skill says to pass it. Without `--re`, the row has no thread line;
   it is never guessed from time order.
5. **Unread:** the same read cache as Activity. A master entry the person has not opened is marked
   (`●`); opening it in the pane marks it read everywhere, and the frame counts what is left
   (`2 unread`).
6. **History:** the live window first, then on scrolling past its top, older entries from the
   history archive, read in a command (part 8, Q5).

## 3. The key and the layout

**`m` opens it** (master; `M` is the Projects page) and closes it. `esc` closes it too. It is a
**floating pane** over the dashboard: at the wide tier (160 columns and over) it takes the right
part of the screen and leaves For you and Agents visible on the left, dimmed; below that it takes
the whole screen, as the reader does.

At 160 columns:

```
 agent-bus-monitor   ● bus ok   ● dispatch up               ╭─ Master  conversation ───────────────────────────────────────────────────── 2 unread · m closes ─╮
 ♙ Charlie   pilot master                                   │  Mon 11-09                                                                                       │
╭─ For you ────────────────────────────────────────────────╮│  14:02  you       Rebase the step 3 branch and kimi-family on main, report the shas.             │
│  ● ask  master  review lot 1? (2 questions)     4m       ││  14:31  master  ● rebase-step3-and-kimi done: feat/progress-background-2 @ 5fe72a2,              │
╰──────────────────────────────────────────────────────────╯│                   feat/kimi-family-3 @ a99694a, both approved on the same heads.                 │
╭─ Agents ─────────────────────────────────────────────────╮│                   (full report · enter)                                                          │
│  ● coder     working  compose-at-file step 2             ││  Tue 11-10                                                                                       │
│  ○ foureyes  idle     reviewed projects-admin-6          ││  09:12  you       What is left before the v0.29 tag?                                             │
│  ● master    working  merging PR 392                     ││  09:20  master  ● v0.29: 3 tasks left (t7 compose-at-file, t8 requests-plan, t9 docs);           │
╰──────────────────────────────────────────────────────────╯│                   → answers your 09:12 'What is left before the v0.29 tag?'                      │
                                                            │  09:40  master  ● ask: review lot 1? (2 questions)  → For you                                    │
                                                            │                                                                                                  │
                                                            │  ── 2 unread ──                                                                                  │
                                                            │› @master  _                                                                                      │
                                                            ╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ scroll  enter full report or thread  i write  r reply to the selected  esc · m close                                                                        
```

At 80 columns:

```
╭─ Master  conversation ──────────────────────────────────────────── 1 unread ─╮
│  Tue 11-10                                                                   │
│  09:12  you     What is left before the v0.29                                │
│                 tag?                                                         │
│  09:20  master  ● v0.29: 3 tasks left (t7                                    │
│                 compose-at-file, t8 requests-                                │
│                 plan, t9 docs)                                               │
│                 → answers your 09:12                                         │
│                 (full report · enter)                                        │
│  09:40  master  ● ask: review lot 1? → For you                               │
│  ── 1 unread ──                                                              │
│› @master  _                                                                  │
╰──────────────────────────────────────────────────────────────────────────────╯
 ↑↓ scroll  enter open  i write  r reply  esc close                             
```

- **Rows:** time, author (`you` or `master`), the text, wrapped. A report's row is its headline;
  its full text opens on `enter` (the reader, with copy). An answer carries its thread line.
- **An ask** from master appears as a row that points to For you (`ask: … → For you`), where it is
  answered. The pane does not duplicate the ask's form.
- **The compose line** at the bottom is the existing one-line input, its recipient fixed to
  `@master`. `enter` sends a human directive; `i` (or `^O`) opens the compose window for a long
  one, with the same recipient. `r` on a selected master row answers it in its thread. That
  needs a new `Bus.HumanReply` (a `reply` with `human=yes` and `ref` = that entry, which **wakes**
  master, unlike `HumanClose`): an additive change in `bus/` (step 3).
- **Keys** (one table, `conversationBindings`, read by the handler, the footer and the help):
  `↑↓` move, `pgup pgdn home end`, `enter` open, `i` write, `r` reply to the selected row,
  `y` copy its text, `esc` or `m` close.

## 4. How it differs from the other views

- **For you** lists what waits on the person (asks, merges, gates); the conversation is the
  history of what they and master said to each other, waiting or not.
- **Requests** groups tracked requests and reviews by who owes what between everyone; the
  conversation holds only the person's exchange with master, in time order.
- **Activity** is the raw bus, every stream and every agent; the conversation is the person's
  thread with master, with full report texts and nothing else.

## 5. What it does not show

- **What master says only in its own pane.** Its turns, its reasoning and its tool output never
  reach the bus. The conversation holds only what master **publishes** to the person:
  `report --to <person>`, a directive or reply to them, an ask. This is why master's skill already
  says to publish every answer with `report master --to <person>`.
- **Master's reports without `--to <person>`** (notes for the team), and the traffic between
  agents.
- **Other agents' messages to the person** (part 8, Q3).
- **Status lines** (`working`, `idle`): the Agents panel shows them.

## 6. What it changes elsewhere

- The pane reads the streams busmon already reads; nothing new is stored. Two **additive**
  changes support it: `Bus.HumanReply` (step 3) and `report --re` with its `re` field (step 4,
  Q7). The dispatcher and the `subscribe` JSON version do not change.
- Activity keeps showing the same entries. The read marks are shared, so opening an entry in
  either view marks it read in both.
- The idle view's "you" count and the header's unread chip count the same entries as before.

## 7. Steps (each its own PR, after the person's answers)

1. **The filter and the rows**, pure: which entries belong, their order, the day headers, the
   thread line, the report's headline and full text by id; table tests.
2. **The pane:** the floating layout at the three tiers, the key table, the unread count and the
   shared read marks, `enter` to the reader; goldens at 80, 120 and 160 columns.
3. **Writing from it:** the compose line fixed to `@master`, `i` and `^O`; `Bus.HumanReply` and
   `r` as a thread reply.
4. **The thread line:** `report --re <entry>`, its `re` field, master's skill updated to pass it,
   and the pane's `answers your …` line (Q7).
5. **History** past the live window, if the person wants it (Q5).

## 8. Questions for the person

1. **The key:** `m` (recommended; `M` stays the Projects page), or another letter?
2. **Floating or full screen** at the wide tier: floating, with For you and Agents visible beside
   it (recommended), or always full screen like the reader?
3. **Only master**, or also the other agents' messages to you (`report coder --to <you>`)? The
   recommendation is master only: the others reach you through For you and Requests, and the pane
   stays one conversation.
4. **Reports in full:** the headline in the row and the full text on `enter` (recommended), or the
   whole report inline in the conversation?
5. **History:** only what the streams keep (their last 1000 entries), or older entries from the
   history archive when you scroll past the top (recommended)?
6. **Read marks shared with Activity** (recommended: one read is one read), or separate?
7. **Which report answers which message:** add `report --re <entry>`, so master says which of your
   messages a report answers and the pane shows it (recommended), or keep plain time order with
   no thread line?
