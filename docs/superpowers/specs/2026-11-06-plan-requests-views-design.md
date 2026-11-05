# What the Plan and Requests mock-ups bring to the real views (plan-requests-views)

Status: spec, for review (subject `plan-requests-views`). Not built. Nothing is implemented before the
person's go. v0.28 is a stabilisation release, so §4 proposes only what is small enough to fit.

## 1. The sources, and how they are read

The person received two throwaway Go programs from another assistant. Each has static demo data
and its own bubbletea v1 model. They were read, never run.

- **Plan, `agentbus-kanban/main.go` (66 lines):**
  - the tasks in three bordered columns, `WAITING`, `IN PROGRESS` and `DONE`, each with a count;
  - a task is a card: `▸ title`, then `#id · owner`;
  - `←→` change column, `↑↓` change task, and Enter toggles a detail box under the board;
  - below 96 cells, only the selected column shows.
- **Requests, `agentbus-requests/main.go` (155 lines):**
  - sections with a count and a fold marker (`▸`/`▾`), each in its own colour;
  - rows `○ from → to  subject  age`;
  - `/` opens a search over from, to, subject, body and kind. While it holds text, every section
    opens and the sections with no match hide;
  - `e` expands every section and `c` collapses them all;
  - Space or Enter on a heading folds it, and Enter on a row toggles a detail box under the list.

**The mock-ups are a source of ideas, not authority.** Their glyphs (`▸ ▾ ○ → …`), colours, key
letters and data model are theirs. The real views keep:

- the data model (`plan.Derive`'s states, `requestGroups`' sections and groups);
- the one-list pattern: `listState`, `ListItem`, the gutter `▶`/`▼` for `Expanded`, the reader
  for a row's page;
- the key tables (`keys.go`, one table per mode, the footer and help drawn from them);
- the three tiers (wide ≥ 120, medium 80–119, narrow);
- the closed glyph set (`theme.go`; `TestNoStrayNonASCIILiterals`) and the role colours.

## 2. Requests: what exists, what the mock-up adds

| In the mock-up | In busmon today |
|---|---|
| Sections | Yes: `Owed by you`, `You wait on`, `Tracked`, `Reviews gated`, `Between agents`, then the notices and the done/answered threads each folded into one line (`requestRows`) |
| A count per section | **No.** The counts are only in the panel title's summary (`requestsTitle`); the heading is a bare name |
| A section folds | **No.** Only the folds exist: `+ N older` per waited-on group, the notices line, the done/answered line (`o`) and a group's `(+N)` (Enter) |
| `/` search | **No.** `/` (`ActFilter`, every panel) opens Activity's filter, whatever has the focus. On the Requests tab it narrows Activity, a panel the person is not looking at |
| `e` / `c`: expand or collapse all | No |
| A detail box under the list | No, on purpose: Enter opens the thread, request, ask or report as a page in the reader, with `n`/`p` |
| A colour per section | No, on purpose: the panel has one hue (`panelHue`). Rows carry their meaning in the mark column (`◆` owed or gated, `●`/`○` unread/read, `↑`/`↓` priority) |

## 3. Plan: what exists, what the mock-up adds

| In the mock-up | In busmon today |
|---|---|
| Three columns: waiting, in progress, done | One list grouped by the eight states of `plan.Order`: waiting for you, answered (master to record), blocked, in review, in progress, upcoming, unknown, done. Each heading has its glyph, role and count, and done is folded to a count until Enter |
| `#id · owner` under the title | id, title, owner and details on one line; under 80 cells the owner and details fold under the title (`planOverlay`) |
| Detail box under the board | Enter opens the task's page, with its `Action:` row for the person |
| `←→` move between columns | `←→` step the lower panel's tabs while it has the focus (busmon-keys), and on a page they step to the previous or next item |
| Narrow: one column | Narrow: the same list (the narrow tier's sixth view) |
| A count per column in the heading | Yes, `state (N)` per heading. The tab's frame carries `name done/total` and the person's count (`planSeg`) |

## 4. Proposed for v0.28 (small)

### R1. Each Requests heading carries its count

`Owed by you (3)`, `Tracked (8)`.

- The figure is the one `requestsTitle` already sums: `reqView.count(s)`, with the asks counted
  in `Owed by you`, as `owedText` counts them.
- The title's summary stays. It is the panel's total at a glance, while the heading is the
  section's own count.
- **Cost:** one line in `requestRows`' `head`, golden updates, and a test that each heading's
  figure equals the title's part.

### R2. A heading is a row: Enter folds and unfolds its section

- The heading becomes a selectable `ListItem` keyed `sec:<section>`. It uses the gutter's own
  marker: `▼` open, `▶` folded (`Expanded`, `listview.go`). No new glyph.
- **Folded,** the heading reads `Tracked (8)`. Its groups are hidden, and with them its `+ N older`
  line.
- **The fold state is per busmon session,** kept in `reqOpen` beside its groups. It is not saved:
  every section is open at start, as today, so the screen does not change for someone who folds
  nothing.
- **`Owed by you` can be folded like any other section.** Its count stays on the heading and in
  the header's orange `N requests waiting`, so nothing the person owes is lost from sight.
- **The selection follows its key** across polls, as the list pattern already does. A selected row
  inside a section that gets folded moves to that heading.
- **`n`/`p` on a page skip the headings.** A page opened from the list steps through its
  selectable keys (`openTracked` records them), so a selectable heading would become a step. The
  recorded keys leave out `sec:`, the same way they would skip a fold line that is not an item.
- **Space** is not added. busmon's lists open with Enter, and Space is a binding only on the
  settings page.
- **Cost:** `requestRows` and `openRequestRow` (key `sec:`), tests for fold, unfold, the selection
  moving to the heading, and the header count unchanged, plus goldens.

### R3. `/` on the Requests tab filters Requests

- **When the Requests tab has the focus,** `/` opens its own filter line. `lineReqFilter` sits
  beside `lineFilter` and `lineHistFilter` and keeps its text in `ui.reqFilter`.
- **The syntax is Activity's,** through `parseFilter`:
  - `@name` matches the thread's sender or target;
  - a stream id matches the thread;
  - words match its subject, kind and root message, case-insensitively.
- **While it holds text,** the sections with no match are left out, and the matching threads show
  even when they sit inside a folded group, a folded section, the notices, the older line or the
  done line. A filter hides nothing it matches. The title shows `filter: <text>` and
  `N of M`. Esc clears it, as Activity's does.
- **Elsewhere `/` keeps today's meaning,** Activity's filter, and Activity's filter never narrows
  Requests. The binding becomes two rows in the shell's table (`Panels: PanelRequests` first, the
  rest after), so the footer and help say which filter `/` opens.
- **Cost:**
  - a `match` for `reqThread`;
  - `requestRows` given the filter;
  - the line kind;
  - one key row;
  - tests: the syntax per field; a thread inside a folded group, section or notices line still
    found; Esc clears; Activity's filter untouched; and `TestNoKeyCollisionPerMode` still green.
  
  R3 is the largest of the three. If v0.28 must shrink, it goes first and R1–R2 stay.

## 5. Not adopted, and why

- **The Plan as three kanban columns.**
  - **Three states lose what the Plan is read for.** Eight states fold into three. Waiting for
    you, answered (master to record), blocked and in review are exactly the distinctions the
    person opens the Plan for, and a WAITING / IN PROGRESS / DONE board merges them.
  - **The lower panel is short.** At 140×40 it holds about a dozen rows. Three-line cards in three
    bordered columns would show three or four tasks per column.
  - **It breaks the one-list pattern.** One list is what selection, wheel anchoring, click
    hit-testing, `n`/`p` on the task page (`readerStep`) and the reminder's jump all rely on. Three
    columns would need a second selection model.
  - **`←→` are taken** by the lower panel's tabs.
  - **Narrow is already one list.**
  
  If the person wants a board, the natural place is the `P` overlay at the wide tier, as a
  layout of the same rows grouped in three lanes: for you (waiting), with the team (answered,
  blocked, in review, in progress, upcoming, unknown) and done. Each card would keep its state
  glyph, so no distinction is lost. That is a release of its own, after v0.28, and only on the
  person's go.
- **A detail box under the list (both mock-ups).** The reader page already shows the whole
  thread, request or task, with `n`/`p`, copy keys and answer keys. A box would take rows from a
  short panel and duplicate the page.
- **`e` / `c`, expand or collapse all.** With five sections and R2's per-heading fold, this adds
  two letters to the shell's table for little gain. `c` sits beside `C` (settings), and `e` has no
  meaning elsewhere. It is deferred until someone asks.
- **Space as a fold key, a colour per section, and the `▸ ▾ ○ →` glyphs.** These are the
  mock-ups' own look. busmon has its markers (the gutter `▶`/`▼`, the mark column), its panel
  hue and its closed glyph set.
- **Search over the full body of every message.** R3 matches the thread's root message, the text
  a row stands for. A thread's replies are on its page, and the History page (`H`) filters the
  whole archive.

## 6. Tests and checks for R1–R3

- `go test ./...`, with the goldens regenerated for the Requests tab: wide, medium and narrow,
  both palettes.
- `TestShellAlignmentSuite` and `TestNoStrayNonASCIILiterals` stay green, with no new glyph.
- `TestTourKeysExist` stays green: the tour names `/` from the key tables.
- CLAUDE.md's Requests paragraph gets one sentence each for the counts, the section fold and the
  Requests filter.
