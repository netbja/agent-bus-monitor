# What waits, and the next one: busmon's queue strip, triage and row actions (busmon-queue-triage)

Status: spec, for review (subject `busmon-queue-triage`), amended after foureyes' review of
f0e1486 (B1, B2 and the minor points; §8 answered as the review reads the code). Not built. Nothing is implemented before the
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
  For you, finds the next row and opens it. `n`/`p` (`readerStep`, A5) walk a list in the order
  taken when the walk began. They do skip a key gone from the list, but they keep an `unread:`
  line walkable once it is read (`tea_reader.go:1115-1121`). They never reach a row that arrived
  after the walk began, and they never move on by themselves.
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
| **unread** | `unreadItems(items)`, as today | `●` `GlyphDotFull`, attention (as today) | `● 3 unread` | `●3` |

- **The roles.** Both of today's badges are `RoleAttention` (`header.go:110-113`). The decide and
  unread chips keep it. Only the merge chip takes the dispatch role, as the merge rows do.
- **decide + merge = `decisions(items)`**, today's `forYou` badge. Adding the unread chip gives
  the whole list, the ask whose line is unread being counted once in each, as today.
- **Health: two different things.**
  - The health pill (`guardianSeg`) counts the checks. It stays right after the strip,
    unchanged, and those checks' rows live in F5, not in For you.
  - The stall guard is another matter. It is a For you row (`rankHealth`), so the decide chip
    counts it, and Enter on it opens F5 (§4.1).
- **Row 1 is unchanged.** It keeps `owedText`, `N between agents` and `planSeg`. It does not
  duplicate the decide chip: §8.1.

### 3.2 What a chip does

Every chip is a target. `click` gains a header case before the panels, after the step that
closes an open reader: a click outside the reader closes it first (`tea_update.go:566-570`), and
the chip's action then runs on the main screen. A chip clicked during a walk therefore ends the
walk, as any click outside the page does today. The header's admitted
items keep their x span (`hItem` gets `x, w`, filled where `headerRows` lays the row out), and a
click on a chip runs its action.

| Chip | Click |
|---|---|
| decide | focus For you, select its first row of a decide rank |
| merge | focus For you, select its first merge row that is a PR (never `merge:unknown`) |
| unread | `ActNextUnread` (the `u` key) |
| health pill | `ActHealth` (F5), which the pill already advertises |

The keyboard path to the decide and merge chips is the triage walk (B, `T`), and to unread it is
`u`. No new letter is spent on the strip.

### 3.3 Room

The chips get priority 0, like today's badges, so they are admitted before everything they could
lose to. Below `wideMinWidth` they lose their word, as today (`word()`). The narrow tier, with
three chips at one-digit counts (`♙2  ◆1  ●3`, the header's gap of 2 between items), costs 10
cells, against 6 for the two badges today; each further digit costs one cell.
The golden suite (`TestShellGoldens`) pins the three tiers. `TestShellAlignmentSuite` runs on
them unchanged.

### 3.4 The tour

The tour's header page (`tour.go`, `pillRow`) shows the strip in place of the two badges, with
the strip's own segment function. `tourVersion` is bumped, so a person who has seen the tour sees
that page again.

## 4. B: the triage walk

### 4.1 Entering and leaving

- **`T`** (ModeShell, free today: `T` is not bound in any mode) starts the walk. It opens the
  first walkable For you row in the reader, with the page the table below names, and marks the
  reader as walking. With nothing walkable, it opens the end page (§4.4) at once.
- **What the walk opens, by rank.** Enter does not open every row (`tea_update.go:234-266`). An
  unread row expands in place on the first Enter, and `merge:unknown` returns nil. So the walk
  does not "do what Enter does". It opens, for each rank:

  | Rank | The walk opens |
  |---|---|
  | `rankAsk` | the ask page (`pushAsk`) |
  | `rankHealth` (the stall guard) | F5 (`ovHealth`) |
  | `rankWait` (wait or prompt) | the agent page (`pushAgent`) |
  | `rankGate` | the challenge thread (`pushMessage` of the challenge, as Enter) |
  | `rankPlan` | the task page (`ovPlanTask`, with its Action row) |
  | `rankMerge`, a PR | the PR page (`ovMerge`) |
  | `rankMerge`, `merge:unknown` | nothing: the row is skipped, never opened, never counted in the walk's `N/M` |
  | `rankUnread` | the line's message page (`pushMessage`), straight away, not the in-place expansion; opening it reads it (`tea_reader.go:234`) |

  - **`merge:unknown`** says only that the queue cannot be read. It is not a decision
    (`decisions()` drops it), so the walk passes it. The end page names it (§4.4).
  - **The empty state's `empty:plan:<task>` row** is not a `forYou` item. It is drawn only when
    the list is empty (`forYouEmptyRows`), so the walk never reaches it as a row. It is on the
    end page, and Enter there opens its task (`ovPlanTask`), as on the panel.
- **Esc** leaves the walk and the reader, as today.
- The main screen's footer advertises it as `T triage`, kept when room is short, since it is the
  way into the walk. The keep rule covers F-keys only today (`keys.go:485-487`: `kept` reads
  `fkeyNumber`). It is widened to name `ActTriage` as well. With `enter` moved to the front
  (§5.2), a narrow footer then holds F1, F5, `T` and `enter` before any other key: the ordinary
  keys give way first.
- The reader's title gains the walk's place, `triage 2/7`, where 7 counts the rows visited plus
  the walkable rows still in the list (`merge:unknown` is not one). It is drawn in the frame's title with `frameTitle`, so the title's
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

`p` steps back through `visited`, in the order visited. It skips a key no longer present, except
an `unread:` line, which stays reachable once read: the A5 rule `readerStep` keeps on purpose
(reviewer-codex). Otherwise a line just read under the walk could never be seen again with `p`.

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
keeps the page, says why (as today), and never advances. On a success, the walk moves to the next
page at once and sets the flash to `sent — next: <agent> <kind>`. The flash stays until the next
key action clears it (`tea_update.go:83`), as every flash does, so the person sees what changed
under them. There is no timer and no pause before the move. This is the cinematic the person asked for: deal, see it acknowledged, land on the next one.

### 4.4 The end

When every row has been visited, the walk opens one page. It never says that nothing waits
while a skipped row still stands:

- the title `Triage done: everything was seen once`;
- the counts the walk went through (`answered 1 · sent 1 · read 1 · skipped 3`);
- what still waits, by chip (`♙ 2 decide (skipped: …) · ◆ 1 merge`);
- `merge:unknown`, when the list holds it: `merge queue unreadable` in muted text, never a
  count;
- the next decision the empty For you already shows (`forYouEmptyRows`, foryou-no-progress): its
  `empty:plan:<task>` row opens that task (`ovPlanTask`).

Enter on that row opens it; Esc closes. Only when the list is empty does the title read `Nothing
waits for you`.

### 4.5 Interplay

- **The reminder and the recap's auto-open wait** while the walk runs. `busyForModal` already
  counts `m.rd.open()`, which is true while any walk page is open. The walk's own flag is added
  for the gap between pages only: a page closed by its terminal act before the next one is
  pushed, or a compose window's outcome still on its way.
- **Bells ring as today.** A new ask during the walk is reached by the order rule (§4.2), not by
  interrupting the page.
- **The compose window opened from a page** (`r`, `w`, the Action row) returns to the walk when
  it is sent or closed. The send's outcome is the terminal act (§4.3).
- **The help (F1) and the tour** list `T` from the table. The tour gains one row on its For you
  page: `T` "walk what waits for you, one by one". F1 lists `T` (triage) beside `t` (the tour),
  since the two are easy to confuse (foureyes, §8.3).

## 5. C: each row names its own primary action

### 5.1 The word

`footerHintsOn` already asks a `hint(Action) string` for a word. `pageHint` answers it on pages.
A new `rowHint` answers it on the shell's panels for `ActOpen`, from the selected row's key alone
(as `priorityTarget` reads the row key, request-priority §6):

| Panel, row | Word for `enter` |
|---|---|
| For you, `ask:` | `answer` |
| For you, `wait:` / `prompt:` | `agent` (both open the agent page, `pushAgent`) |
| For you, gate | `thread` (the challenge thread; the person does not record the verdict) |
| For you, plan | `task` (the task page, `ovPlanTask`, with its Action row) |
| For you, merge (a PR) | `PR` (the PR page, `ovMerge`) |
| For you, `merge:unknown` | none: Enter does nothing there, so the `enter` pair is left out |
| For you, health | `health` (F5) |
| For you, `empty:plan:` | `task` |
| For you, unread / Activity line | `read` (collapsed), `full text` (expanded) |
| Requests, an owed or waited-on thread | `thread` |
| Requests, the notices line | `notices` |
| Requests, a tracked request | `request` |
| Agents | `agent` |
| Board, Plan | `task` |

**Each word names the page Enter opens, never an act the person then does there.** An ask's
`answer` is the one act word left: its page is the answer form. Anything else keeps `detail`.
`rowHint` returns either a word or "no pair". On `merge:unknown` the footer draws no `enter`
pair, so it never offers what Enter does not do. A test holds each word to the page Enter
pushes, by pressing Enter on a fixture row of each prefix and reading the page kind. `rowHint` is pure, and one table test walks every row-key prefix
that `forYou`, `requestRows`, `planTabItems` and the Agents and Board rows produce. A new prefix
with no word fails that test, much as `TestEveryQuotaProviderHasItsColour` fails on a provider
without a colour.

### 5.2 First in the footer

The pair for `enter` moves to the front of the footer on the main screen. Its key stays in the
accent: `pair()` hard-codes `RoleAccent` (`keys.go:478`), and the hint callback returns only a
word. A colour per row would need the callback to return a role too, for little gain, so it is
left out. Every other pair keeps the table's order. **Every main-screen footer golden changes,
in every tier**, since the first pair moves. The step (§6.1) regenerates them, and that is its
only intended golden change. On a page (the reader), the order stays as it is: the
page's own `pageHint` words already name its acts.

### 5.3 What it does not change

The key does not change: Enter still opens, and opens the same page. Only the word does, and it
names that page (§5.1). A row's other acts (`y` on a
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
   keeps the page; `p` skips a key gone but reaches an `unread:` line read under the walk;
   `merge:unknown` is passed and named on the end page; an unread row opens its message page
   (read), not the expansion; an empty list opens the end page, whose `empty:plan:` row opens the
   task; the flash after a send stays until the next key.

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

## 8. Recommended answers (foureyes' review, from the code)

These are the reviewers' recommendations, not the person's decisions. The person's go on this
spec still decides them.

1. **`owedText` on row 1 is kept.** It counts the Requests view's owed section (`titleCounts`),
   not the For you rows, so it is not the decide chip's number. A chip that grew an age would
   move its neighbours, against §3.1's fixed-place rule. It is priority 5 on row 1, already the
   first to go when room is short.
2. **The "ready" chip comes after the walk.** busmon cannot reuse the rule as it stands. It is
   the `switch` over `plan.Derive` rows in `cmd/agentbus/stallchecks.go:253-266`: answered
   tasks go to master; upcoming tasks not the person's, with nothing in `WaitingOn`, go to their
   owner. That switch fills `health.TeamStallInput.AgentReady`, an input field of `[]ReadyTask`,
   not a function. Showing the chip means moving that rule where both binaries can read it (for
   instance into `plan`), in the chip's own spec.
3. **`T`, not Enter.** B2 and §5.3 depend on Enter keeping "open this row". `T` sits beside `t`
   (the tour), so F1 lists both (§4.5).
