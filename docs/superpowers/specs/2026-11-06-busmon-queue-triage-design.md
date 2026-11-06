# What waits, and the next one: busmon's queue strip, triage and row actions (busmon-queue-triage)

Status: spec, for review (subject `busmon-queue-triage`). Not built. Nothing is implemented before the
person's go. busmon only: no bus key, no stream field, no new Redis read.

The person's words (2026-11-06): "we find it hard to see what is waiting and what is left to do",
and they want navigation that is "more intuitive in its flow of use". This spec covers three
changes:

- **A**: a queue strip in the header;
- **B**: a triage walk;
- **C**: each selected row naming its own primary action.

A palette of commands, a breadcrumb on the reader stack, a "next step" column on Agents and an age
heat shared by every list were proposed in the same answer. They are out of scope here (§7).

## 1. Today: one question, seven places

"What waits?" has its answer spread over seven surfaces. Each is right on its own; none is the
place to start.

| Where | What it says | Source |
|---|---|---|
| Header, row 0 | `♙ N for you` (`decisions(forYou)`), `● N unread` (`unreadItems`), the health pill (`guardianSeg`: `▲ 2 to do`) | `header.go` `headerRows` |
| Header, row 1 | `N requests waiting · oldest X` (`owedText`), `N between agents`, `plan <name> 6/11 ♙ 1` (`planSeg`), the pause | same |
| For you panel | asks, stall rows, waits and prompts, gates, plan tasks, merges, unread lines, ranked `rankAsk`…`rankUnread` | `tea_foryou.go` `forYou` |
| Requests tab (3) | who owes whom an answer, by section | `requests_view.go` |
| Plan tab (4) / `P` | the batch's tasks by derived state | `plan.go` |
| Agents | declared state plus `idle?`, `busy`, `prompt`, `for you` flags | `agents.go` |
| F5 / `R` | health by place; the recap | `health_view.go`, `recap_model.go` |

Three effects follow:

- **The header counts but does not lead.** `♙ 3 for you` gives no split (an ask? a merge?) and
  no way in except moving the focus to For you. Clicking a header item does nothing: `click`
  (`tea_update.go:547`) hit-tests the tabs and the panels, never the header.
- **Acting on an item ends the flow.** Answering an ask's last question closes the page
  (`TestShellAskAnswersByKey` holds it: "the last answer left the page open" is its failure message). The person goes back to
  For you, finds the next row and opens it. `n`/`p` (`readerStep`, A5) walk a list, but in the
  order taken when the walk began. They skip nothing that was dealt with, and they never move on
  by themselves.
- **The footer says the same thing on every row.** `enter detail` is the hint whether the row is
  an ask to answer, a merge to make or a line to read. `footerHintsOn` already takes a `hint`
  function, but only the reader's pages fill it (`pageHint`). Panel rows always get the table's
  word.

## 2. Principles kept

- **For you stays the one list** (busmon Charm spec, decision 2). The strip (A) and the walk (B)
  are views of `forYou(s, feed)`, never a second derivation. The strip's person chips are a
  partition of the list, so the strip and the panel can never disagree.
- **No new read, no write.** Everything comes from the `snapshot` and the feed already in the
  model. Reads keep their existing rule: nothing changes on the bus but what the person sends.
- **One key table.** New keys go into `keys.go`. The footer, the help and the tour are drawn
  from it (`TestTourKeysExist`, `TestNoKeyCollisionPerMode`). Glyphs come from the closed set
  only (`TestNoStrayNonASCIILiterals`).
- **The three tiers** (wide ≥ 120, medium 80–119, narrow) and `admitHeader`'s rule: what the
  person acts on is admitted first.

## 3. A: the queue strip

### 3.1 What it shows

The strip replaces the shell's two badges `♙ N for you` and `● N unread` with one group of
chips on header row 0. The chips always appear in the same order, and a chip at 0 is not drawn.
The order is fixed so that a chip never moves because another one's count changed.

| Chip | Counts | Glyph, role | Wide | Below wide |
|---|---|---|---|---|
| **decide** | For you rows of ranks `rankAsk`, `rankHealth`, `rankWait`, `rankGate`, `rankPlan` | `♙` `GlyphHuman`, attention | `♙ 2 decide` | `♙2` |
| **merge** | rows of rank `rankMerge` other than `merge:unknown` | `◆` `GlyphGate`, dispatch | `◆ 1 merge` | `◆1` |
| **unread** | `unreadItems(items)`, as today | `●` `GlyphDotFull`, accent | `● 3 unread` | `●3` |

- **decide + merge = `decisions(items)`**, today's `forYou` badge. Adding the unread chip gives
  the whole list, the ask whose line is unread being counted once in each, as today.
- **Health** keeps its own pill (`guardianSeg`), right after the strip and unchanged: its rows
  live in F5, not in For you.
- **Row 1 is unchanged.** It keeps `owedText`, `N between agents` and `planSeg`. Whether
  `owedText` duplicates the decide chip is open question §8.1.

### 3.2 What a chip does

Every chip is a target. `click` gains a header case before the panels: the header's admitted
items keep their x span (`hItem` gets `x, w`, filled where `headerRows` lays the row out), and a
click on a chip runs its action.

| Chip | Click |
|---|---|
| decide | focus For you, select its first row of a decide rank |
| merge | focus For you, select its first merge row |
| unread | `ActNextUnread` (the `u` key) |
| health pill | `ActHealth` (F5), which the pill already advertises |

The keyboard path to the decide and merge chips is the triage walk (B, `T`), and to unread it is
`u`. No new letter is spent on the strip.

### 3.3 Room

The chips get priority 0, like today's badges, so they are admitted before everything they could
lose to. Below `wideMinWidth` they lose their word, as today (`word()`). The narrow tier, with
three chips at their widest (`♙2  ◆1  ●3`), costs 12 cells, against 9 for the two badges today.
The golden suite (`TestShellGoldens`) pins the three tiers. `TestShellAlignmentSuite` runs on
them unchanged.

### 3.4 The tour

The tour's header page (`tour.go`, `pillRow`) shows the strip in place of the two badges, with
the strip's own segment function. `tourVersion` is bumped, so a person who has seen the tour sees
that page again.

## 4. B: the triage walk

### 4.1 Entering and leaving

- **`T`** (ModeShell, free today: `T` is not bound in any mode) starts the walk. It opens the
  first For you row in the reader, as Enter would, and marks the reader as walking.
  With nothing for the person, it says so in a flash (`nothing for you`) and opens nothing.
- **Esc** leaves the walk and the reader, as today.
- The main screen's footer advertises it as `T triage`, kept like F1 and F5 when room is short,
  since it is the way into the walk.
- The reader's title gains the walk's place, `triage 2/7`, where 7 counts the rows visited plus
  the rows still in the list. It is drawn in the frame's title with `frameTitle`, so the title's
  width rule holds.

### 4.2 The order: the live list, minus what was visited

`readerStep` keeps the order taken when the walk began, because a read line leaves the list under
the page (reviewer-codex, A5). The walk needs the opposite: an item dealt with must leave, and
an item that arrives during the walk must be reached.

The walk keeps one set, `visited` (row keys, in the model, never persisted). The next item is
**the first row of the live `forYou` list whose key is not in `visited`**:

- an item dealt with drops out of the list;
- a skipped one is in `visited`;
- a new ask ranks where `forYou` puts it, so a new ask comes before an older unread line.

`p` steps back through `visited`, in the order visited, skipping keys no longer present.

### 4.3 Moving on by itself

The walk advances on its own after a **terminal** act on the item, once the act's outcome comes
back as a success:

| Item | Terminal act |
|---|---|
| ask (`rankAsk`) | the last open question answered (today: the page closes) |
| wait or prompt (`rankWait`) | a message sent to that agent from the page (`send.go` outcome ok) |
| plan task (`rankPlan`) | the Action row's message to master sent |
| gate (`rankGate`) | none: a verdict is not the person's busmon act; `n` |
| merge (`rankMerge`) | none: the merge happens outside; `y` copies, then `n` |
| health (`rankHealth`) | none; `n` |
| unread (`rankUnread`) | none: opening it is the read; `n` once read |

An item with no terminal act is advanced with `n`, which keeps its meaning: next. A failed send
keeps the page, says why (as today), and never advances. Before moving, the walk shows `sent —
next: <agent> <kind>` in the flash for one poll (1 s), so the person sees what changed under
them. This is the cinematic the person asked for: deal, see it acknowledged, land on the next one.

### 4.4 The end

When every row has been visited, the walk opens one page. It never says that nothing waits
while a skipped row still stands:

- the title `Triage done: everything was seen once`;
- the counts the walk went through (`answered 1 · sent 1 · read 1 · skipped 3`);
- what still waits, by chip (`♙ 2 decide (skipped: …) · ◆ 1 merge`);
- the next decision the empty For you already shows (`forYouEmptyRows`, foryou-no-progress).

Enter on that row opens it; Esc closes. Only when the list is empty does the title read `Nothing
waits for you`.

### 4.5 Interplay

- **The reminder and the recap's auto-open wait** while the walk runs: `busyForModal` counts the
  walk, as it counts a draft.
- **Bells ring as today.** A new ask during the walk is reached by the order rule (§4.2), not by
  interrupting the page.
- **The compose window opened from a page** (`r`, `w`, the Action row) returns to the walk when
  it is sent or closed. The send's outcome is the terminal act (§4.3).
- **The help (F1) and the tour** list `T` from the table. The tour gains one row on its For you
  page: `T` "walk what waits for you, one by one".

## 5. C: each row names its own primary action

### 5.1 The word

`footerHintsOn` already asks a `hint(Action) string` for a word. `pageHint` answers it on pages.
A new `rowHint` answers it on the shell's panels for `ActOpen`, from the selected row's key alone
(as `priorityTarget` reads the row key, request-priority §6):

| Panel, row | Word for `enter` |
|---|---|
| For you, `ask:` | `answer` |
| For you, `wait:` / `prompt:` | `reply` / `see pane` |
| For you, gate | `review` |
| For you, plan | `start` (the page's Action row) |
| For you, merge | `merge` |
| For you, health | `health` |
| For you, unread / Activity line | `read` (collapsed), `full text` (expanded) |
| Requests, an owed or waited-on thread | `thread` |
| Requests, the notices line | `notices` |
| Requests, a tracked request | `request` |
| Agents | `agent` |
| Board, Plan | `task` |

Anything else keeps `detail`. `rowHint` is pure, and one table test walks every row-key prefix
that `forYou`, `requestRows`, `planTabItems` and the Agents and Board rows produce. A new prefix
with no word fails that test, much as `TestEveryQuotaProviderHasItsColour` fails on a provider
without a colour.

### 5.2 First in the footer

The pair for `enter` moves to the front of the footer on the main screen, drawn in the row's
role (the attention colour on an ask, the dispatch colour on a merge) instead of the accent.
Every other pair keeps the table's order. On a page (the reader), the order stays as it is: the
page's own `pageHint` words already name its acts.

### 5.3 What it does not change

The key does not change: Enter still opens. Only the word does. A row's other acts (`y` on a
merge, `+`/`-` on a tracked request) keep their `OnPage` rule.

## 6. Steps, each its own subject for 4-eyes

1. **C, row actions** (`busmon-row-actions`): `rowHint`, the footer order, the prefix test. It is
   the smallest step, and the one the other two reuse: the walk's flash names the next item with
   the same words.
2. **A, the strip** (`busmon-queue-strip`): the chips, the header hit-test (`hItem` x spans,
   `click`'s header case), the goldens and the tour page.
3. **B, the walk** (`busmon-triage`): `T`, `visited`, the terminal acts, the end page, and
   `busyForModal`. Tests: a walk over a fixture with one row of each rank; an ask answered mid-walk
   advances; a new ask arriving mid-walk is reached before an older unread line; a failed send
   keeps the page; `p` skips a key gone.

Each step leaves busmon whole. `go test ./cmd/busmon -count=1` passes, with goldens updated only
where the step means them to change.

## 7. Out of scope (proposed, not here)

- A command palette (`:` or `Ctrl+K`): fuzzy over the key table's actions and over agents, tasks
  and threads.
- A breadcrumb on the reader stack (`For you › Plan › task-12 › thread`).
- A "next step" column on Agents (the accepted board task, else `bus.NextRequest`, else whom it
  waits on), and a plan progress bar.
- One age heat for every list (muted, then amber past 1 h, then red past a day).
- The colour fixes: background detection by OSC 11 (`tea.RequestBackgroundColor`) in place of
  `COLORFGBG` alone, and a 256-colour tier instead of falling to the 16-colour palette
  (`theme.go:124`). They are a separate change.

## 8. Open questions for the person

1. **`owedText` on row 1** (`N requests waiting · oldest X`) counts the Requests view's owed
   section, questions included, so it overlaps the decide chip. Keep it (it carries the oldest
   age, the chip does not), or fold the age into the decide chip (`♙ 2 decide · 3h`) and drop it?
   The spec keeps it until the person says.
2. **A "ready" chip for the team** (`▸ 4 ready`: plan tasks an agent could start). The rule
   exists in team-stall (`health/stall.go`), but busmon does not derive it. Showing it means
   moving that rule where both can read it. Wanted now, or after the walk?
3. **`T`, or Enter on the decide chip?** `T` follows the overlay letters (`P`, `U`, `H`, `R`).
   Making the walk the For you panel's Enter would change what Enter does on a row. The spec says
   `T`.
