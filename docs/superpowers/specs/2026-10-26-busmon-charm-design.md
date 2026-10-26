# busmon on bubbletea v2 + lipgloss v2 — design

Status: draft for review (coder, 2026-10-26). Task `busmon-charm-spec`, branch
`docs/busmon-charm-spec` from `origin/main`. Spec only; no code in this change. Reviewers: foureyes,
ui-grok, ui-kimi.

Decided by Bernard (ask `1793039192455-0`): move busmon from tview to bubbletea v2 + lipgloss v2, in
three shippable steps, with the screen changes below. Inputs: ui-grok's report `1793038893110-0`,
ui-kimi's report `1793039139337-0`, master's synthesis `1793039179340-0`, Bernard's POC brief
(directive `1793035325111-0`), the local checkouts `/data/AI-Docs/{bubbletea,lipgloss,crush}`.

## The problem

busmon has the facts a person needs and shows them badly (both advisors, independently):

- **Attention is scattered.** Open asks (`askpanel.go`), waiting agents (the fade, `agents.go`),
  pending 4-eyes (a `gate N` flag) and unread marks (`unread.go`) are four signals in four places.
  The one list that answers "what do I do first" is `buildRecap`'s "Expected from you", behind `R`.
- **Rich messages are not readable on the dashboard.** Every row is one truncated `Preview` line.
  The full text exists only in one overlay `TextView` (`u.overlay`), capped at `min(100, w-4)`
  columns (`ui.go`), which hides the dashboard, replaces any other overlay, and cuts fenced code
  instead of wrapping it (`markdown.go`).
- **Overlay content is welded to tview.** `renderMarkdown` and every overlay body (`detail.go`,
  `detail_agent.go`, `tracking.go`, `recap.go`, `plan.go`, `quota.go`, `services.go`, `askpanel.go`,
  `help.go`, `reminder.go`, `health_view.go`) emit tview colour tags through `tag()`/`tg()` (177
  uses at `origin/main`). They resolve to theme roles, but only tview can draw them.
- **tview fights the model.** Keys reach the root and are relayed; `Draw` must never `SetFocus`;
  the compose `TextArea` counts bytes while bus caps count runes; the fade ticker redraws the whole
  application ten times a second while anyone waits (`main.go`, `fadeFrame`).

## Decisions this spec implements

1. **One busmon.** No `--ui next`, no second mode. Three steps, each shippable on its own:
   a) on main, move overlay and markdown content off tview tags onto `Line`/`Seg`;
   b) build the Elm shell against goldens, beside the tview loop, unused by the binary;
   c) one release swaps the main loop.
2. **Attention:** one ranked list at the top (open asks, waiting agents, pending verdicts, unread
   for you), sorted like the Agents panel.
3. **Reading:** Enter expands the selected row inline (rendered markdown, the dashboard stays
   visible); a second Enter opens a full-width reader that replaces the lower zone (no 100-column
   cap, code wrapped, never cut); `y` copies the raw text.
4. **Activity, Board and Requests are one tabbed panel.**
5. **Both inputs stay:** the one-line input and the compose window, one send path (`ui.send`).
6. **Every signal stays:** the waiting fade, the bell, the recap's auto-open, the 5-minute
   reminder.

## What does not change

- **Data:** `pollOnce`, `busReader` and the `shared` model stay as they are; the tail stays
  `Bus.Recent` + `Bus.TailFrom`. No new Redis read, no new bus write. busmon still writes as a
  person (`human=yes`), through `ui.send` only.
- **Rules:** colours and glyphs only from `theme.go` (roles, palettes dark/light/16/none, the closed
  glyph set); `TestNoStrayNonASCIILiterals` stays green; the declared state is never replaced by an
  observation (`busy`, `prompt`, `paused`, `quota` stay flags or a display look, as today); the two
  usage scopes stay apart (account budget % in the header, an agent's raw context tokens in its
  row); `keys.go` stays the one key table that drives the handler, the footer and the help.
- **Flags and CLI paths:** every `busmonFlags` entry, `--list`, `--pick`, `--reset`, `--delete`,
  their order in `main()`; the update check; the local cache files.

## Versions (pinned)

As Crush v0.98.2, Charm's own shipping TUI, pins them (`/data/AI-Docs/crush/go.mod`):

| Module | Version |
|---|---|
| `charm.land/bubbletea/v2` | v2.1.0 |
| `charm.land/lipgloss/v2` | v2.0.6 |
| `github.com/charmbracelet/ultraviolet` | the pseudo-version bubbletea v2.1.0 requires |
| `github.com/charmbracelet/x/ansi` | the version bubbletea v2.1.0 requires (Crush: v0.11.9) |

Not taken: `glamour` (it styles outside `theme.go` roles and the glyph set; our reports are a
constrained markdown that `markdown.go` already handles), `bubbles` widgets except `textarea` for
the compose window (step b decides it by a test of the rune cap, see "Compose"). Go stays 1.26,
which their `go 1.26` floor accepts. tview, tcell and uniseg leave `go.mod` in step c only if no
other package imports them (`cmd/agentbus` does not).

## Width: one method, enforced

Facts (read in the checkouts):

- busmon measures with uniseg grapheme clusters (`cells.go`: `Width`, `Fit`, `FitLine`).
- lipgloss measures with `ansi.StringWidth`, which is `GraphemeWidth` (`lipgloss/size.go`,
  `x/ansi/width.go`).
- bubbletea's renderer places cells with `WcWidth` and switches to `GraphemeWidth` only when the
  terminal answers the Unicode-core query (mode 2027) (`tea.go`, `cursed_renderer.go`). There is no
  public option to force it.

So on a terminal without mode 2027, a cluster on which `GraphemeWidth` and `WcWidth` disagree (a ZWJ
sequence, a variation selector, some flags) would be measured one way and placed another, and a
frame border would move. The rule:

- **One measuring function**, `cellWidth` in `cells.go`, used by `Fit`, `FitLine`, frames, the
  layout and every lipgloss `Width` we pass. It stays uniseg.
- **Everything printed agrees under all three methods.** The sanitisers that already clean agent
  text (`Preview`, `Detail` in `cells.go`) replace any grapheme cluster on which uniseg,
  `ansi.StringWidth` and `ansi.StringWidthWc` disagree by the glyph set's replacement character
  (one cell, ASCII `?` in the `none` palette). The glyph set itself is checked the same way.
- **Tests:** `TestWidthMethodsAgree` runs every hostile fixture of the alignment suite through the
  sanitisers and asserts the three widths are equal for every line; `TestGlyphSetIsOneCell` asserts
  each glyph is 1 under all three. The goldens are rendered twice, once per renderer width method,
  and must be identical.

## The screen

Sketches use plain ASCII; the real frames use the glyph set.

### 140 x 40 (wide tier, >= 120 columns)

```
 agent-bus-monitor  * bus  dispatch *  missions 2/1  [! 3 for you]  [* 2 unread]      15:04:05
 claude ####---- 62% 7d 41% ~25m  | grok #------- 8%  | paused 1h52                          
+ For you  3 -------------------------------++ Agents  6 ----------------------------------------+
|> ? ask   master   review lot 1?        4m || W master   waiting  12m  for you   opus     41k   |
|  W wait  coder    pane prompt           1m || * coder    working   2m            opus     88k   |
|  G gate  foureyes reviews launcher-resume  || o foureyes idle      9m            opus     12k   |
|  * unread coder report Lot 1 delivered    || o ui-grok  idle      3m            grok-4.6  4k   |
+-------------------------------------------++------------------------------------------------+
+ [Activity]  Board  Requests ----------------------------------------- auto-scroll  2 unread -+
| * 14:52 coder   report  Lot 1 delivered (+1.2k)                                             |
|   v   Lot 1 delivered                                                                        |
|       ## Done                                                                                |
|       - adapter in `d0a72be`; tests green                                                   |
|       ## What remains for you                                                               |
|       - see the open question                                                                |
|   14:51 bernard directive  review lot 1                                                     |
|   14:40 dispatch notify   pause until 16:00                                                 |
+---------------------------------------------------------------------------------------------+
 > @master _                                                                                   
 tab panel  j/k move  enter open  ! answer  u unread  I compose  1-3 tabs  R recap  ? help  q 
```

- **Header, two lines at every tier.** Line 1: project, bus, dispatch, missions, pause flag, the
  `for you` count, the unread count, the clock. Line 2: one gauge per provider (session and weekly
  windows, coloured by burn pace as today, `budgetRole`), hold or fallback, the project pause. A
  gauge is never dropped for the clock; at 80 columns the bars shrink to the percentage
  (`quotaSeg`'s existing narrow form).
- **For you** (left top) is decision 2, described below. **Agents** (right top) is today's panel,
  same sort, columns as today; the provider and model take the free space at >= 120 columns.
- **The tabbed panel** (bottom) holds Activity, Board and Requests; `1`/`2`/`3` or a click on a
  tab label switch it, as the digit tabs of the medium tier do today.
- **The input line** sits above the footer, as today; the compose window opens over the lower zone.

### 120 x 30 (wide tier, smallest)

Same zones. For you and Agents share the top row 2:3; the tabbed panel takes what is left
(at least 8 rows). The header stays two lines.

### 80 x 24 (medium tier)

```
 agent-bus-monitor  * bus  dispatch *  [! 3]  [* 2]   15:04
 claude 62% 7d 41%  | grok 8%  | paused 1h52
+ For you  3 ---------------------------------------------------+
|> ? ask  master  review lot 1?                              4m |
|  W wait coder   pane prompt                                1m |
+ Agents  6 ----------------------------------------------------+
| W master  waiting 12m for you    * coder  working  2m         |
| o foureyes idle    9m            o ui-grok idle    3m         |
+ [Activity] Board Requests ------------------------------------+
| * 14:52 coder  report Lot 1 delivered (+1.2k)                 |
|   14:51 bernard directive review lot 1                         |
+---------------------------------------------------------------+
 > _                                       enter open  ? help  q
```

For you shows at most 4 rows (then `+N more`, which focuses it); Agents goes two-up as the medium
tier does today. Under 80 columns the narrow tier keeps today's behaviour (one panel at a time,
digit tabs, input only while composing), with For you as the first panel.

### For you (decision 2)

One list, built in a pure function `forYou(snapshot, now) []attentionItem` beside today's
`attentionItems` (`reminder.go`), which it replaces as the reminder's source:

| Rank | Kind | Source today | Row |
|---|---|---|---|
| 0 | open ask (one row per ask, its open question count) | `{p}:asks`, `askpanel.go` | `? ask  <agent> <question title> <age>` |
| 1 | waiting agent with no open ask | `agents.go` (`waiting`, and an observed `prompt`) | `W wait <agent> <message> <age>` |
| 2 | pending verdict (an open 4-eyes gate) | challenges read every tick | `G gate <reviewer> reviews <subject>` |
| 3 | unread for you | `unread.go` | `* unread <sender> <preview>` |

Within a rank, oldest first: the same order the Agents panel applies inside its attention groups.
An item leaves the list when its fact goes (the ask closes, the agent declares another state, the
gate resolves, the line is read). The header count is the list's length; the reminder (5 minutes)
and the bell read the same list, so the three can never disagree. Enter on a row does what its
kind needs: an ask opens the ask in the reader (answering is the reader's ask mode, keys `1`-`9`,
`o` for a free answer in the compose window); a waiting agent opens its detail; a gate opens the
challenge thread; an unread line expands it and marks it read.

### Reading (decision 3)

- **Inline expansion.** Enter on a selected row in Activity (or For you, for an unread line)
  expands it in place: the row stays, the rendered markdown follows it, indented, at the panel's
  inner width, up to half the panel's height (then `... enter for more`). The list keeps scrolling
  around it. One row is expanded at a time; moving the selection collapses it. Esc collapses.
- **The reader.** A second Enter (or Enter on an item that is not a message: an agent, a task, a
  request) replaces the lower zone (the tabbed panel and the input) with a full-width reader: the
  header, For you and Agents stay visible. No column cap: the text width is the zone's inner width.
  Fenced code is wrapped at the width with a muted continuation mark in the gutter, never cut. `y`
  copies the raw text (OSC52, `tea.SetClipboard`), `Y` the whole thread, as today. Esc returns to
  the tabbed panel with the selection where it was. PgUp/PgDn/Home/End scroll.
- **Every overlay that shows text** (`ovMessage`, `ovAgent`, `ovTask`, `ovRequest`, `ovRequests`,
  `ovServices`, `ovHealth`, `ovPlan`, `ovPlanTask`, `ovRecap`, `ovHelp`, `ovAsk`) becomes a reader
  page; Esc returns from a page to the one that opened it (the plan's task to the plan, as today).
  The small dialogs stay dialogs over the screen: the reminder, the summary confirmation, the
  pause popup, the remove confirmation.

### Input (decision 5)

The one-line input keeps its place and behaviour (`@` completion, held directive when the agent has
an open ask, second Enter for a plain notify). `I` opens the compose window over the lower zone.
Both call `ui.send(recipient, body, directed, window)`; neither builds an `@name body` line. The
compose window's text area enforces the rune cap in `Update` (not a byte cap): step b keeps
`bubbles/textarea` only if a test shows it can hold the cap without truncating a multibyte rune;
otherwise it is our own small text area over `Line`.

### Signals (decision 6)

All kept, all driven by the snapshot and the tick, none by a draw:

- **Waiting fade:** a `fadeMsg` every `fadeFrame` (100 ms) while at least one item is waiting and
  the palette has true colour, the same `fadeLevel`; nothing is scheduled otherwise.
- **Bell:** once per new waiting declaration and once per new ask id, in `Update` after a poll
  message (today `ui.onTick`), written with `tea.Raw("\a")` or the equivalent print command.
- **Recap auto-open** after `AutoAfter` (8 h) since the last visit, as a reader page.
- **Reminder** after `PopupAfter` (5 min), snoozable, waiting until no draft or dialog is open.

## Architecture (step b's shell)

One `Model` (Crush's discipline: one model, View paints rectangles, dialogs last, no I/O in
Update, styles from one palette):

```go
type Model struct {
	snap   snapshot   // copied from shared under shared.mu by the poll Cmd, never shared.mu in View
	ui     uiState    // focus, tabs, selections, expanded row, reader stack, input, compose, dialogs
	layout Layout     // computeLayout(w, h), recomputed on tea.WindowSizeMsg only
	cache  viewCache  // rendered markdown and frames, keyed (id, width, palette)
	now    time.Time  // from the last tick or fade message; View never reads the clock
}
```

- **Messages:** `tea.WindowSizeMsg`, `tea.KeyPressMsg`, `tea.MouseMsg` (click, release, wheel),
  `pollMsg{snapshot, err}` (every second, every 5th carrying the dispatcher facts, as today),
  `tailMsg{events}` (the tail goroutine's events, batched), `fadeMsg`, `sendResultMsg`,
  `clipboardMsg`.
- **Commands:** the poll is a `tea.Cmd` that runs today's `pollOnce` against the `busReader` with
  its deadline and returns `pollMsg`, rescheduled by `tea.Tick(time.Second)`; the tail runs in its
  goroutine as today and is fed in with `Program.Send`; a send is a `tea.Cmd` around `ui.send`'s
  bus call. `Update` never blocks: every bus or file call is in a command.
- **View is pure.** It reads only the model (`now` included) and returns a `tea.View` with
  `AltScreen` and `MouseMode` set. It renders through one function `paint(Line) string` that turns
  a `Line`'s role-tagged segments into styled spans with the lipgloss styles built once per palette
  from `theme.go` (`Theme.Style(Role) lipgloss.Style`).
- **Caching.** Rendered markdown and expanded rows are cached by (message id, width, palette); a
  frame's content by (panel, data generation, width, height, focus). The data generation is bumped
  by `pollMsg`/`tailMsg` only. A `fadeMsg` changes only the waiting words: it re-styles those
  segments of the Agents and For you rows and reuses every cached body. bubbletea's renderer diffs
  cells and skips identical frames, so a fade frame writes a few cells.
- **Keys.** `keys.go` keeps its tables, modes and actions. Its matchers move off `*tcell.EventKey`
  onto a small engine-neutral `Key` (`Code`, `Rune`, `Mods`) in step a, with one adapter from tcell
  (step a) and one from `tea.KeyPressMsg` (step b). The footer and the help keep reading the same
  tables.

## The steps

Each step is one PR, reviewed, and leaves busmon working.

### Step a: content off tview (on main, tview still drives the screen)

Scope:
- `renderMarkdown(src, width) []Line` instead of a tagged string; fenced code wrapped with a
  continuation mark instead of cut.
- Every overlay body builder returns `[]Line` (`detailText`, `agentDetail`, the tracking, recap,
  plan, quota, services, health, help, ask and reminder bodies). `tag()`/`tg()` are removed; a
  single adapter `linesToTview([]Line) string` feeds the existing overlay `TextView`, so the screen
  is unchanged.
- `keys.go` matchers on the neutral `Key`, with the tcell adapter.
- The width rule: `cellWidth`, the sanitisers' disagreement replacement, `TestWidthMethodsAgree`,
  `TestGlyphSetIsOneCell`.

Files: `markdown.go`, `render.go`, `detail.go`, `detail_agent.go`, `tracking.go`, `recap.go`,
`plan.go`, `quota.go`, `services.go`, `health_view.go`, `help.go`, `askpanel.go`, `reminder.go`,
`keys.go`, `cells.go`, `theme.go` (no more `Tag`), and their tests.

Tests: every overlay's text compared before and after (a test that renders each body with the old
tagged path, captured once into `testdata/overlay/*.txt` before the change, and the new `[]Line`
path through `linesToTview`, must be equal after tag stripping); `TestNoStrayNonASCIILiterals`,
`TestAlignmentSuite` unchanged and green; a test that no file outside `theme.go` mentions a tview
colour tag.

What the person sees: nothing changes, except code blocks in overlays now wrap instead of being cut.

### Step b: the Elm shell, beside tview, not wired

Scope: the `Model`, `Update`, `View` and commands above, the new screen (For you, tabbed panel,
inline expansion, reader, dialogs), mouse handling, all in `cmd/busmon` files prefixed `tea_`
(`tea_model.go`, `tea_update.go`, `tea_view.go`, `tea_reader.go`, `tea_mouse.go`, ...). `main.go`
still starts tview; nothing runs the shell but the tests. `forYou` replaces `attentionItems` as the
reminder's source in both loops (it is a pure function of the snapshot).

Tests:
- **Goldens of `View()`** on a fixed `snapshot` (a provider on hold, a fallback agent, an agent
  waiting with an open ask, one waiting without, a pending gate, two unread lines, a report with a
  fenced code block longer than the width, a paused project): 80x24 and 140x40, dark and light,
  each in four states (dashboard, a row expanded, the reader open, the compose window open), under
  both renderer width methods. Stored in `testdata/golden/`, compared byte for byte, rewritten only
  with `-update`.
- **Alignment suite** at 80x24, 120x30 and 140x40 x every palette x the hostile fixtures: every
  frame's corners and borders, as `align_test.go` checks today, on the cell grid the View produces.
- **Purity:** View called twice on the same model returns the same string; a test model whose
  `busReader` panics on any call renders every state (View does no I/O).
- **Fade cost:** after a `fadeMsg`, the markdown renderer is not called (a counter in the cache);
  only the waiting words' cells differ between two consecutive frames.
- **Update drives everything:** key and mouse scenarios as table tests (focus cycle, tabs, expand,
  reader push and pop, ask answer, `u` to next unread, `y` copy command emitted, send result).
- **Signals:** a poll message with a new waiting declaration emits one bell; the same declaration
  twice emits one; the reminder appears after `PopupAfter` and not while compose is open.

What the person sees: nothing; the binary still runs tview.

### Step c: the swap (one release)

Scope: `main.go` starts `tea.NewProgram(model, ...)` instead of the tview application; the tail and
poll goroutines feed it; `--pick` becomes a small bubbletea model of its own (today `pick.go`,
tview); the tview files (`ui.go`, `listview.go`, `compose.go`'s tview parts, `pausepopup.go`'s
widget, `linesToTview`) are removed; tview, and tcell if unused, leave `go.mod`.

Tests: the step-b suites now cover the binary; `screen_test.go`, `capture_test.go` and
`ui_test.go` scenarios that drove tview are ported to model scenarios or removed when a step-b test
covers the same behaviour (the PR lists each one and its replacement); the picker's tests run on
its model.

What the person sees: the new screen. Same flags, same keys for the same actions (plus `1`-`3` for
the tabs at every width and Enter twice for the reader), same settings file, same cache.

## Mouse parity

Everything the mouse does today keeps working, on the new geometry:

| Today | After |
|---|---|
| Wheel over a list scrolls it 3 rows (`ListView`) | Same, over For you, Agents, the tabbed panel and the reader |
| Click selects a row; a click on the selected row opens it | Same; the open is the inline expansion, then the reader |
| Click on the scrollbar column pages towards the click | Same |
| Click on a panel's tab label focuses it (`tabSpans`) | Same for the tabbed panel's labels and each frame title |
| Click on a pause popup button presses it | Same |
| Compose: click on the recipient or the text, click outside closes | Same |
| Wheel or click on Agents stops pinning the selection to the top (`agentsTouched`) | Same |

Click-to-answer on an ask's options is not in this change (keyboard first; ui-grok's question 7
is left open).

## Risks

- **Width.** Handled by the rule above; the residual risk is a cluster that all three methods agree
  on but a given terminal draws otherwise, which no library can see. The hostile fixtures stay the
  gate.
- **View cost.** Building strings at up to 60 frames a second is new work tview did not ask for.
  The cache and the generation counter bound it; the fade test proves bodies are not rebuilt.
- **Lipgloss box widths.** `Width(n)` is the outer width including padding and borders; frames are
  built with our `DrawFrame` geometry, and lipgloss is used for styling spans, pills and gauges,
  not for layout boxes, so the goldens never wrap by one cell.
- **A big step c.** It is one release by decision; the step-b goldens and scenarios are what make
  it safe, so step c may not start until they cover every overlay and mode.

## Open questions for review

1. For you at 80 columns: 4 rows and `+N more`, or all rows with Agents pushed down?
2. The inline expansion's height cap: half the panel, or a fixed 10 rows?
3. Should an ask's options be clickable in step b (ui-grok's question 7), or keyboard only?
4. `bubbles/textarea` for compose, decided by the rune-cap test, or our own text area from the
   start?
5. The reader replaces the lower zone; should it also hide Agents at 80 columns to gain rows?
