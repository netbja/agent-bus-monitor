# busmon v0.9.0 UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild busmon's screen as a responsive, btop-style dashboard (layout C) on tview, with
straight frames at every size, without touching the bus protocol.

**Architecture:** Pure, unit-tested building blocks first — theme + cell-accurate text
(`theme.go`, `cells.go`), a pure layout function (`layout.go`), and one custom selectable list
primitive (`listview.go`). Row/header/detail/key-table builders are pure functions over the
existing `shared` model (`agents.go`, `activity.go`, `board.go`, `header.go`, `detail.go`,
`keys.go`). A final integration task rewires `main.go` around stable primitives and a
relayout-on-resize loop, then the alignment and application test suites lock it down.

**Tech Stack:** Go 1.26, tview v0.42.0, tcell v2.13.9, uniseg v0.4.7 (already in the module graph
as an indirect dependency of tview; importing it directly moves it to a direct `require` at the same
version — no new module).

**Spec:** `docs/superpowers/specs/2026-10-10-busmon-ui-redesign-design.md` (rev 2, 88fd90d). Every
task's implementer reads the spec sections named in its task.

## Execution model (user decision)

Tasks are distributed between **Codex** (herdr pane w2:p1), **Kimi** (w4:p1) and **Claude** (the
controller, design-heavy tasks). Each worker gets a brief file and works in **its own worktree** on
its own branch cut from `feat/v0.9-busmon-ui`; the controller merges each reviewed task into
`feat/v0.9-busmon-ui`. Each task is reviewed by the other external agent (Codex ↔ Kimi; the
controller's tasks by Codex). The owner column below is the plan's assignment.

| Task | Owner | Depends on | Parallel group |
|---|---|---|---|
| 1 Theme + cells | Codex | — | A |
| 2 Layout function | Claude | — | A |
| 3 ListView primitive | Claude | 1, 2 | B |
| 4 Agents model + rows | Kimi | 1, 2 | B |
| 5 Activity rows + pause cursor | Codex | 1, 2 | B |
| 6 Board + Requests rows | Kimi | 1, 3 | C |
| 7 Header | Kimi | 1, 2 | C |
| 8 Details | Codex | 1, 4 | C |
| 9 Key tables + help | Kimi | 2 | D |
| 10 Integration (main.go) | Claude | 1–9 | E |
| 11 Alignment + application suites, docs, release, live check | Claude (docs: Kimi) | 10 | F |

## Global Constraints

- Change only `cmd/busmon/**`, `README.md`, `CLAUDE.md`, `docs/releases/v0.9.0.md`, `go.mod`/`go.sum`
  (uniseg direct require only). Never `bus/`, `dispatch/`, `cmd/agentbus/`.
- No new module dependency. tview v0.42.0, tcell v2.13.9, uniseg v0.4.7.
- All UI text is **English**.
- UI glyph set (spec §5.1), all width 1: `● ○ ▲ ▼ ✓ · ▶ → ← ↑ ↓ … — █ ░ ▇ │ ┃ ─ ╭ ╮ ╰ ╯ ›`. No other
  non-ASCII rune in non-test string/rune literals outside `theme.go`. Never `⏰ ⌛ ✉ ⚠ ✂ 👂 🔒 ⬢ ⧉`.
- Every width computation goes through `cells.go` (grapheme clusters, uniseg). Never `len()` or
  `utf8.RuneCountInString` for layout.
- Rows are `Line` values (role-tagged segments), never tview colour-tag strings. Only overlay
  bodies (TextViews) use tags, and only through `tg(role, s)`.
- Untrusted text: `raw` for copy/filter, `Preview` for rows, `DetailText` for overlays (spec §5.3).
- No new Redis read per tick or per draw (spec §9). Redis tests only via `AGENTBUS_TEST_REDIS_URL`
  (isolated broker, e.g. `redis://127.0.0.1:6392`), never `:6380`.
- Commit messages end with `Co-Authored-By: <your model> <noreply@...>` as your client does; the
  controller's commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `go build ./... && go vet ./... && go test ./cmd/busmon/ -count=1` must pass at the end of every
  task. Tasks 1–9 add new files and tests next to the old code; **the old renderers stay until Task
  10 removes them** (so the program keeps building and running between tasks).

## Review Focus

1. A message or name whose emoji/CJK cluster falls exactly on a truncation edge — the right border
   must stay at its column (Task 1 `TestFitLineWideClusterAtEdge`, Task 11 alignment suite).
2. The broker going down after startup — header shows `▲ bus down Ns`, queue values turn `N?`,
   nothing blanks (Task 4 `TestQueueCellStates`, Task 10 `TestBrokerDownKeepsValues`).
3. 50 agents, scrolled with the wheel, while ticks reorder them — no jump, selection kept (Task 3
   `TestWheelAnchorSurvivesReorder`).
4. Light theme on a light terminal and `NO_COLOR` — selection row and the input autocomplete list
   are styled by the theme, not tview defaults (Task 1 `TestStyleInputAutocomplete`, Task 11).
5. Shrinking to too-small and back with an overlay open — no panic, overlay and focus restored
   (Task 10 `TestTooSmallRoundTrip`).

---

### Task 1: Theme and cell-accurate text (Codex)

Spec: §4, §5. **Files:** Create `cmd/busmon/theme.go`, `cmd/busmon/cells.go`,
`cmd/busmon/theme_test.go`, `cmd/busmon/cells_test.go`; modify `go.mod` (uniseg direct).

**Interfaces — Produces:**

```go
// theme.go
type Role int
const (RoleText Role = iota; RoleMuted; RoleDim; RoleAccent; RoleOK; RoleAttention; RoleError; RoleDone; RoleDispatch; RoleLine)
type Palette int
const (PaletteDark Palette = iota; PaletteLight; Palette16; PaletteNone)
type Theme struct{ /* unexported */ }
func ResolveTheme(flagVal, envVal, colorfgbg string, noColor bool, colors int) (*Theme, error) // flag > env; "auto"|"dark"|"light"; error on other values
func (t *Theme) Palette() Palette
func (t *Theme) Style(r Role) tcell.Style            // fg = role, bg = tcell.ColorDefault (PaletteNone: no colour)
func (t *Theme) Selected(r Role) tcell.Style         // role fg on selection bg (PaletteNone/16: reverse attribute)
func (t *Theme) Border(focused bool) tcell.Style     // accent when focused, line otherwise; PaletteNone: bold when focused
func (t *Theme) Tag(r Role, s string) string         // tview tag "[#rrggbb]"+tview.Escape(s)+"[-]"; Palette16: named colour; PaletteNone: escaped text only
func (t *Theme) StyleInput(f *tview.InputField)      // field, label, placeholder AND autocomplete styles from roles, default bg
func (t *Theme) StyleTextView(v *tview.TextView)     // text, background default, border via Border(false)
var th *Theme // package-level; initialised by init() to the dark truecolor theme, replaced in main()
func tg(r Role, s string) string { return th.Tag(r, s) }
// glyphs (the only non-ASCII literals allowed in non-test code)
const (GlyphWorking = "●"; GlyphIdle = "○"; GlyphBlocked = "▲"; GlyphDown = "▼"; GlyphDone = "✓"; GlyphNone = "·"; GlyphSelect = "▶"; GlyphArrowR = "→"; GlyphArrowL = "←"; GlyphUp = "↑"; GlyphDownArrow = "↓"; GlyphEllipsis = "…"; GlyphDash = "—"; GlyphBarFull = "█"; GlyphBarEmpty = "░"; GlyphGauge = "▇"; GlyphPrompt = "›")
var UIGlyphs = []string{ /* the 24 glyphs of the Global Constraints, box drawing included */ }

// cells.go
type Seg struct{ Text string; Role Role; Bold bool }
type Line []Seg
func Preview(s string) string        // spec §5.3 preview rules
func DetailText(s string) string     // spec §5.3 detail rules (\n kept, \t → next 4-cell stop)
func Width(s string) int             // sum of uniseg grapheme-cluster widths
func Truncate(s string, w int) string // ≤ w cells; appends "…" when cut (the "…" counts); whole clusters only
func Fit(s string, w int) string      // Truncate then pad with spaces to exactly w cells
func (l Line) Width() int
func FitLine(l Line, w int) Line      // across segments: exactly w cells, "…" (role of the cut segment) when cut
func DrawLine(s tcell.Screen, x, y, w int, l Line, selected bool) // writes exactly w cells using th; selected → th.Selected
type ScrollState struct{ Total, Top, Visible int } // Total ≤ Visible → no scrollbar
type Frame struct{ Title, Label Line; Focused bool; Scroll ScrollState }
func DrawFrame(s tcell.Screen, x, y, w, h int, f Frame) // rounded box via th.Border; title at x+2 as " title ", label right-aligned ending at x+w-3; label shrinks first, then title; corners never overwritten; scrollbar per spec §5.4 on column x+w-1, rows y+1..y+h-2 only when Scroll.Total > Scroll.Visible
```

- [ ] **Step 1: Write failing tests** in `cells_test.go`:
  - `TestWidthClusters`: `Width("abc")==3`, `Width("日本")==4`, `Width("é")==1`,
    `Width("👩‍💻")==2`, `Width("❤️")==2`.
  - `TestTruncateFit`: `Truncate("abcdef",4)=="abc…"`, `Fit("ab",4)=="ab  "`,
    `Fit("日本語",5)=="日本…"` (Width 5), `Fit("日本語",4)=="日… "` (the `本` would straddle: dropped, padded),
    `Width(Fit(x,n))==n` for every fixture × n in 1..12.
  - `TestFitLineWideClusterAtEdge`: `FitLine(Line{{"ab",RoleText},{"日本",RoleMuted}},4)` has
    `Width()==4` and ends with `"…"`; same for widths 1..8.
  - `TestPreview`: `"a\tb\nc\r"`→`"a b c "`; ESC (`"\x1b[31mred"`) → `"[31mred"` (ESC removed);
    `"‮abc"`→`"abc"`; `"x‍y"` unchanged; `"é"` unchanged; `"\x7f"`→`""`; `"\u0085"`→`""`.
  - `TestDetailText`: `"a\nb"` keeps `\n`; `"a\tb"`→`"a   b"`; `"ab\tc"`→`"ab  c"`; bidi removed.
  - `TestDrawFrame` on `tcell.NewSimulationScreen("")` 20×6: corners `╭ ╮ ╰ ╯` at (0,0),(19,0),(0,5),(19,5);
    with `Scroll{Total:10,Top:0,Visible:4}`: (19,1)=`▲`, (19,4)=`▼`, thumb `┃` at (19,2), corners unchanged;
    a 40-cell title on a 20-wide frame never overwrites (19,0).
- [ ] **Step 2:** `theme_test.go`:
  - `TestGlyphsAreSingleCell`: every `UIGlyphs` entry has `Width==1` and `len(UIGlyphs)==24`.
  - `TestNoStrayNonASCIILiterals`: parse every non-`_test.go` file of the package with `go/parser`
    (comments ignored), walk `*ast.BasicLit` of kind STRING/CHAR, fail on any rune > 0x7F in a file
    other than `theme.go`. **Until Task 10 removes the old renderers, skip `main.go`, `render.go`,
    `help.go`, `detail.go`, `tracking.go`, `filter.go` via an explicit `legacyFiles` set with a
    comment "emptied by Task 10".**
  - `TestResolveTheme`: (`"light","",…)`→light; (`"","dark",…)`→dark; flag beats env;
    auto + `COLORFGBG="0;15"`→light, `"15;0"`→dark, `""`→dark; `noColor=true`→PaletteNone;
    `colors=16`→Palette16; `"blue"`→error.
  - `TestStyleRoles`: dark `Style(RoleOK)` fg = `#7EE2A8`, bg = `ColorDefault`; light
    `Style(RoleError)` fg = `#CF222E`; PaletteNone `Style(RoleError)` has no fg colour;
    `Tag(RoleOK,"[x]")` dark = `"[#7ee2a8][x[][-]"` (tview-escaped), PaletteNone = `"[x[]"`.
  - `TestStyleInputAutocomplete`: after `th.StyleInput(f)`, the field's autocomplete styles use
    `Style(RoleText)` / `Selected(RoleText)` (read back via a small getter you add on Theme
    returning the three styles you applied, if tview has no getter).
- [ ] **Step 3:** Run `go test ./cmd/busmon/ -run 'Width|Truncate|Fit|Preview|DetailText|DrawFrame|Glyph|NoStray|ResolveTheme|StyleRoles|StyleInput' -count=1` → FAIL (undefined).
- [ ] **Step 4:** Implement `theme.go` and `cells.go`. Width/Truncate iterate
  `uniseg.NewGraphemes`/`uniseg.StringWidth` per cluster. Palette hex values exactly as spec §4.
  `tview.Borders` is set in Task 10, not here.
- [ ] **Step 5:** Run the Step-3 command → PASS; then `go build ./... && go vet ./... && go test ./cmd/busmon/ -count=1` → PASS.
- [ ] **Step 6:** Commit `feat(busmon): theme roles and cell-accurate text helpers`.

### Task 2: Layout function (Claude)

Spec: §3.1. **Files:** Create `cmd/busmon/layout.go`, `cmd/busmon/layout_test.go`.

**Interfaces — Produces:**

```go
type Tier int
const (TierTooSmall Tier = iota; TierNarrow; TierMedium; TierWide)
type PanelID int
const (PanelAgents PanelID = iota; PanelActivity; PanelBoard; PanelRequests)
type Rect struct{ X, Y, W, H int }
type Counts struct{ Agents, Board int; Composing bool } // Composing: narrow shows the input row only while composing
type Layout struct {
	Tier                      Tier
	Header                    Rect // 1 or 2 rows
	Tabs                      Rect // narrow only (H=1), zero otherwise
	Agents, Activity, Board, Requests Rect // frames incl. borders; zero = not shown on this tier/tab
	Lower                     Rect // medium: the tabbed frame (holds Activity|Board|Requests)
	Input, Footer             Rect
	AgentRows                 int  // data rows of the Agents table
	Visible                   []PanelID // panels drawn, in Tab order
}
func computeLayout(w, h int, c Counts, active PanelID) Layout // active = tab shown in medium (lower) / narrow (whole view)
```

- [ ] **Step 1:** Failing tests `TestComputeLayoutTiers` (table over the spec §8.1 sizes, expected
  tier: 160×45 wide, 120×40 wide, 120×12 narrow, 119×40 medium, 119×12 narrow, 160×13 narrow,
  100×30 medium, 80×24 medium, 80×11 narrow, 79×24 narrow, 64×22 narrow, 40×12 narrow, 30×6 narrow,
  30×5/1×40/10×40 too small) and `TestComputeLayoutInvariants` (every non-zero rect inside w×h,
  pairwise disjoint, Agents data rows ≥ 3 when Agents shown and `c.Agents ≥ 3`, active view ≥ 1 data
  row, wide right column width = clamp(w*3/10,40,56), Board ≥ 4 rows, Requests ≥ 3; `Visible`
  contains only drawn panels; `AgentRows == min(Agents, max(3,(h-fixed)/3-borders))` per spec).
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement per spec §3.1 (vertical budget first, tier
  fallback when minima fail). **Step 4:** Run → PASS. **Step 5:** Commit
  `feat(busmon): pure responsive layout function`.

### Task 3: ListView primitive (Claude)

Spec: §3.3.4, §3.4 (pause), §5.4, §6 (mouse). **Files:** Create `cmd/busmon/listview.go`,
`cmd/busmon/listview_test.go`.

**Interfaces — Consumes:** Task 1 `Line`, `DrawLine`, `DrawFrame`, `ScrollState`, `th`. **Produces:**

```go
type ListItem struct{ Key string; Line Line; Selectable bool }
type ListView struct{ *tview.Box /* … */ }
func NewListView() *ListView
func (l *ListView) SetHeader(h Line) *ListView               // optional column header row (not selectable)
func (l *ListView) SetFrame(title, label Line)
func (l *ListView) SetItems(items []ListItem)                // keeps selection by Key; departed key → item now at its former index (clamped, selectable)
func (l *ListView) Selected() string
func (l *ListView) Select(key string)                         // follow-selection mode
func (l *ListView) SetTail(on bool)                           // tail mode: viewport pinned to the end while live (Activity)
func (l *ListView) Live() bool
func (l *ListView) SetOnChange(func(key string, live bool))   // selection or live changed
func (l *ListView) SetOnOpen(func(key string))                // Enter/v or click on the selected row
func (l *ListView) Scroll() ScrollState
func (l *ListView) Move(delta int); First(); Last(); Open(); Resume(); ClearSelection(); ScrollBy(delta int) // the key table (Task 10) calls these
const listGutter = 2 // every row reserves 2 cells for the selection marker: row builders get width = panel W − 4
// Draw, InputHandler (↑↓ j k PgUp PgDn Home g End G Enter v), MouseHandler (click, wheel, track click), PasteHandler (ignore)
```

- [ ] **Step 1:** Failing tests: `TestSelectionByKeySurvivesReorder`, `TestDepartedSelection`,
  `TestFollowSelectionScrollsMinimal`, `TestWheelAnchorSurvivesReorder` (wheel down 3, then
  `SetItems` reordered: top row key unchanged, selection unchanged), `TestTailModeAndPause` (tail on:
  new items keep the end visible; ↑ leaves live and `OnChange(key,false)`; `End` returns live),
  `TestClickSelectsThenOpens`, `TestTrackClickPages`, `TestDrawStaysInRect` (recording screen wrapper
  fails on any write outside `GetRect()`), `TestNonSelectableSkipped` (separators skipped by ↑↓).
- [ ] **Step 2–5:** FAIL → implement → PASS → commit `feat(busmon): selectable scrolling list primitive`.

### Task 4: Agents model and rows (Kimi)

Spec: §3.3 (all subsections), §3.6 agent row sources. **Files:** Create `cmd/busmon/agents.go`,
`cmd/busmon/agents_test.go`; modify `cmd/busmon/main.go` **only** the `agentState` struct and the
tick-loop block that fills it (add fields, set them; keep every existing field).

**Interfaces — Consumes:** Task 1 (`Line`, `Seg`, roles, glyphs, `Fit`, `Preview`), Task 2 (`Tier`).
**Produces:**

```go
// new agentState fields (main.go)
pid int; pidStart uint64      // from AgentSnapshot.PID / .PIDStart (snapsErr == nil)
queueKnown bool               // an Obligation read has succeeded at least once
queueFailed bool              // the latest attempted Obligation read failed
queueAt time.Time             // time of the last successful Obligation read
ctxRaw string                 // UsageSnapshot.Ctx exactly as read (usage keeps working until Task 10)
// agents.go
func parseCtx(raw string) (tokens int64, ok bool)
func formatTokens(n int64) string
type agentFlag struct{ Word string; Role Role; Prio int; Group int } // Group 0 = no sort effect
func agentFlags(a *agentState, pilot bool) []agentFlag            // sorted by Prio, spec §3.3.2 table
func stateLook(a *agentState) (glyph, word string, role Role, group int) // spec §3.3.1 table
func agentGroup(a *agentState, pilot bool) int                    // spec §3.3.4 comparator groups 1..6
func sortedAgentNames(m map[string]*agentState, pilot string) []string
func queueCell(a *agentState, tier Tier) Seg                      // "N msg"/"N", "·", "—", "N?" (muted)
func agentHeader(tier Tier, width int) Line                       // wide only; nil otherwise
func agentRow(name string, a *agentState, pilot bool, tier Tier, width int, now time.Time) Line // exactly width cells
```

- [ ] **Step 1:** Failing tests:
  - `TestParseCtx`: `"608k"`→608000, `"608k ctx"`→608000, `"194.5k"`→194500, `"1.2M"`→1200000,
    `"42000"`→42000, `"0"`→(0,true), `"25%"`/`""`/`"5h12m"`→(_,false).
  - `TestFormatTokens`: 999→`"999"`, 194500→`"195k"`, 608000→`"608k"`, 1200000→`"1.2M"`.
  - `TestStateLook` covers each row of spec §3.3.1 incl. `review`, `"unknown-future"`, empty state.
  - `TestAgentFlagsTable` covers each row of spec §3.3.2, incl. `Escalated:"pending"` →
    `escalating`, `State:"prompted"` → `woken`, `State:"clear"` → no flag, `State:"weird"` →
    `wake:weird`, two flags sorted by Prio.
  - `TestAgentOrder`: groups 1..6 plus name tie-break; an agent with `queueKnown=false` and
    `waiting=5` stays in its state group; `queueFailed=true` with an old value also stays.
  - `TestQueueCellStates`: unknown→`—`; known 0→`·`; known 2 wide→`2 msg`; failed after 2→`2?` muted.
  - `TestAgentRowWidth`: for tiers wide/medium/narrow and widths 30..200, `Width(agentRow(...))==width`,
    with a 32-char name and a message containing `"日本語👩‍💻\t"`.
  - `TestUpdatedBar`: declaration 0s→10 full cells, 5 min→5, ≥10 min→0, no declaration→`··········`
    and age `—`, future→full and `0s`.
- [ ] **Step 2–5:** FAIL → implement → PASS (plus full package build/vet/test) → commit
  `feat(busmon): agent table model, flags and attention sort`.

### Task 5: Activity rows and pause cursor (Codex)

Spec: §3.4. **Files:** Create `cmd/busmon/activity.go`, `cmd/busmon/activity_test.go`.

**Interfaces — Consumes:** Task 1, Task 2 (`Tier`), existing `feedLine`, `feedFilter`,
`reportMarker`. **Produces:**

```go
type eventLook struct{ From, To, Type string; Role Role; Text string } // Text = raw message text before Preview
func lookEvent(e bus.Event) eventLook                       // spec §3.4 mapping table
func activityHeader(tier Tier, width int) Line              // wide/medium; nil in narrow
func activityRow(fl feedLine, tier Tier, width int) Line     // event or day-separator row, exactly width cells
type pauseCursor struct{ /* unexported */ }
func (p *pauseCursor) Pause(lastSeq int)
func (p *pauseCursor) Arrive(seq int, admitted bool)        // counts only when paused, admitted and not a separator
func (p *pauseCursor) Resume()
func (p *pauseCursor) Paused() bool
func (p *pauseCursor) New() int
func activityFrame(p *pauseCursor, shown, total int, f feedFilter) (title, label Line) // "Activity  live" / "Activity  paused · N new"; "N events · filter: <describe()|none>"
```

`feedLine` gains `sep bool` (true for day separators) and `seq int`; existing callers keep working.

- [ ] **Step 1:** Failing tests: `TestLookEventMapping` (one case per table row incl. directive with
  `Task` → `request`, report auto → `auto`, report with full → ` (+N)` in Type or Text as spec,
  unknown cmd type → raw), `TestActivityRowWidth` (all tiers, widths 30..200, hostile text),
  `TestNarrowFoldsTarget` (narrow row contains `→ codex` inside the message),
  `TestPauseCursor` (pause at 10; arrive 11 admitted, 12 not admitted, 13 separator → New()==1;
  Resume → 0; Arrive while live → 0), `TestActivityFrameKeepsFilter`.
- [ ] **Step 2–5:** FAIL → implement → PASS → commit `feat(busmon): activity rows and pause cursor`.

### Task 6: Board and Requests rows (Kimi)

Spec: §3.5. **Files:** Create `cmd/busmon/board.go`, `cmd/busmon/board_test.go`.

**Interfaces — Consumes:** Task 1, existing `request`, `openThread`, `trackedOnly`, `humanAge`,
`deadlineWords`. **Produces:**

```go
func boardHeader(width int) Line
func boardItems(m map[string]bus.BoardEntry, now time.Time, width int) []ListItem // Key = task; sorted as today's boardPanel
func boardFrame(m map[string]bus.BoardEntry) (title, label Line)               // "Board", "D/N done"
func boardStateRole(state string) Role                                         // spec §3.5 board states
func requestItems(reqs []request, threads []openThread, now time.Time, width int) []ListItem // two labelled sections; section headers Selectable=false; Key "req:"+Task / "thr:"+ID; 2 lines per entry → 2 items, the second non-selectable with Key ""
func waitingCount(reqs []request, threads []openThread) int                    // == today's waitingLine count
```

(`ListItem` is defined by Task 3; until it merges, define nothing — Task 6 starts after Task 3 is
merged, or temporarily uses a local copy removed at merge. Prefer waiting: group C.)

- [ ] **Step 1:** Failing tests: `TestBoardItemsWidthAndRoles`, `TestBoardEmpty` (one non-selectable
  item `no task on the board`), `TestRequestSections` (tracked waiting + untracked threads, headers
  non-selectable, count equals `waitingCount`, blocked reason in RoleError, `no deadline` when
  `Expires` zero, untracked never says accepted), `TestWaitingCountMatchesWaitingLine` (same fixtures
  as the existing `waitingLine` tests).
- [ ] **Step 2–5:** FAIL → implement → PASS → commit `feat(busmon): board and requests rows`.

### Task 7: Header (Kimi)

Spec: §3.2. **Files:** Create `cmd/busmon/header.go`, `cmd/busmon/header_test.go`.

**Interfaces — Consumes:** Task 1, Task 2, existing `health`, `humanAge`. **Produces:**

```go
type headerData struct {
	project  string
	health   health
	dispatch string // "", "up", "down"
	pilot    string
	waiting  int
	budgets  map[string]bus.BudgetSnapshot
	now      time.Time
}
func headerLines(d headerData, width int, tier Tier) []Line // len 1 (wide) or 2 (medium/narrow); each exactly width cells
```

- [ ] **Step 1:** Failing tests: `TestHeaderAllocationOrder` (widths 30..200: health and dispatch
  text always present; at 30 the compact forms `● bus`/`● disp`; project truncated with `…` but ≥ 6
  cells or dropped only below that; budgets dropped whole from the right before pilot),
  `TestHeaderBusDown` (`▲ bus down 42s` in RoleError), `TestHeaderDispatchUnknown` (`? dispatch`),
  `TestHeaderBudgetUsedLabel` (contains `used`, gauge role ok/attention/error at 74/75/90 %),
  `TestHeaderNoBudget` (no provider text), `TestHeaderLineWidths`.
- [ ] **Step 2–5:** FAIL → implement → PASS → commit `feat(busmon): header allocation`.

### Task 8: Details (Codex)

Spec: §3.6. **Files:** Modify `cmd/busmon/detail.go`, `cmd/busmon/detail_test.go`; create
`cmd/busmon/detail_agent.go`, `cmd/busmon/detail_agent_test.go`.

**Interfaces — Consumes:** Task 1 (`tg`, `DetailText`), Task 4 (`agentFlags`, `parseCtx`,
`formatTokens`, `stateLook`, new agentState fields). **Produces:**

```go
func agentDetail(name string, a *agentState, pilot bool, board map[string]bus.BoardEntry, recent []feedLine, now time.Time) (title, body string) // body uses tg(); field order and absent texts = spec §3.6 table
func agentDetailPlain(a *agentState) string            // the status message as retained (y)
func taskDetail(e bus.BoardEntry, task string, now time.Time) (title, body, plain string)
func requestDetail(r request, now time.Time) (title, body, plain string) // body = today's requestBlock content, restyled with tg
// detailText keeps its signature; its order becomes: body + fidelity warning, then from/to/date, then id/thread
```

- [ ] **Step 1:** Failing tests: `TestAgentDetailFieldsAndAbsent` (each row of the §3.6 table present
  with value, and each absent text when missing; `woken` explained as "nonce echoed — not read or
  accepted"; gap worded "possible, not proven"; lag caveat present), `TestAgentDetailRecentFromWindow`
  (≤ 5 lines, label "in the retained window", none → `none in the retained window`),
  `TestAgentDetailPlainIsRaw` (message with tabs/newlines returned byte-for-byte),
  `TestMessageDetailOrder` (body index < `from` index < `id` index), `TestDetailEscapesTags`
  (message `"[red]x[-]"` renders literally), existing detail tests updated for the new order only.
- [ ] **Step 2–5:** FAIL → implement → PASS → commit `feat(busmon): agent, task and request details`.

### Task 9: Key tables and help (Kimi)

Spec: §6. **Files:** Create `cmd/busmon/keys.go`, `cmd/busmon/keys_test.go`; modify
`cmd/busmon/help.go` and its tests in `render_test.go` (`helpCoversEveryBadge` → new vocabulary).

**Interfaces — Consumes:** Task 1 (`Line`), Task 2 (`Tier`, `PanelID`). **Produces:**

```go
type Mode int
const (ModePanel Mode = iota; ModeInput; ModeFilter; ModeOverlay)
type Action int
const (ActNone Action = iota; ActQuit; ActNextPanel; ActPrevPanel; ActTab1; ActTab2; ActTab3; ActTab4; ActUp; ActDown; ActPageUp; ActPageDown; ActFirst; ActLast; ActLive; ActOpen; ActCopy; ActCopyThread; ActFilter; ActCompose; ActComposeAt; ActHelp; ActRequests; ActEscape; ActSend; ActClose)
type binding struct{ Keys string /* display, e.g. "↑↓ j k" */; Hint string; Act Action; Panels []PanelID /* nil = all */; Tiers []Tier /* nil = all */; match func(*tcell.EventKey) bool }
func bindings(m Mode) []binding
func lookup(m Mode, p PanelID, t Tier, ev *tcell.EventKey) Action // ActNone when unbound (text modes: printable keys are ActNone so the field gets them)
func footerHints(m Mode, p PanelID, t Tier, width int) Line       // from bindings with a Hint, in table order, truncated by whole hints
func helpText() string                                           // sections KEYS (generated from bindings), STATES, DELIVERY
```

- [ ] **Step 1:** Failing tests: `TestLookupMatrix` (one case per spec §6 row: e.g. Activity `End` →
  ActLive, Agents `End` → ActLast, ModeInput `q` → ActNone, ModeInput `Tab` → ActNone, ModePanel `q`
  → ActQuit, ModeOverlay `1` → ActNone, medium `1` → ActTab1, wide `1` → ActNone, `Ctrl-C` → ActQuit
  in every mode), `TestFooterMatchesBindings` (every hint shown maps back to a binding active in that
  mode/panel/tier), `TestHelpCoversVocabulary` (every flag word of spec §3.3.2, every state word, every
  activity type label, `used`, `possible, not proven`, and every binding's Keys appear in `helpText()`).
- [ ] **Step 2–5:** FAIL → implement → PASS → commit `feat(busmon): per-mode key tables, footer hints, help`.

### Task 10: Integration (Claude)

Spec: §3, §4 (style constructors), §6, §7, §9. **Files:** Modify `cmd/busmon/main.go`,
`render.go`, `tracking.go`, `filter.go`, `screen_test.go`, `render_test.go`, `main_test.go`;
create `cmd/busmon/ui.go`, `cmd/busmon/ui_test.go`.

**Interfaces — Consumes:** Tasks 1–9. **Produces:** `type ui struct` (all stable primitives, mode,
logical focus, drafts, pause cursor, layout) with `newUI(app *tview.Application, st *shared, project string, self string, b sender) *ui`, `(*ui).relayout()`, `(*ui).render()`, `(*ui).handleKey(*tcell.EventKey) *tcell.EventKey`; `--theme` flag + `AGENT_BUS_BUSMON_THEME`; `tview.Borders` rounded set once in `main`.

- [ ] **Step 1:** Failing tests in `ui_test.go` (tview `Application` + `SimulationScreen`, injected
  fake data, every test with a 5 s timeout): `TestResizeDuringDetailLoadNoDeadlock`,
  `TestTooSmallRoundTrip`, `TestFocusSurvivesTierChange`, `TestTabNeverHiddenPanel`,
  `TestEscLadder`, `TestDraftsSeparate`, `TestModalBlocksOutsideClick`, `TestBrokerDownKeepsValues`,
  `TestNoExtraReadsPerTick` (fake source counts calls per tick: 1 s set and 5-tick set as spec §9).
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement: stable primitives created once; `SetBeforeDrawFunc` only detects size
  change and starts `go app.QueueUpdateDraw(u.relayout)`; tick loop fills the Task 4 fields; remove
  `agentLabel`, `packChips`, `chip`, `wakeBadges`, `usageBadge` fallback, `statusBar`,
  `renderAgents/renderBoard/renderStatus`, old chip/badge tests; empty `legacyFiles` in
  `theme_test.go`; port `screen_test.go` to the new screen.
- [ ] **Step 4:** `go build ./... && go vet ./... && AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./... -count=1 -race` → PASS.
- [ ] **Step 5:** Commit `feat(busmon): integrate the v0.9 screen`.

### Task 11: Alignment and application suites, docs, release, live check (Claude; docs Kimi)

Spec: §8, §11. **Files:** Create `cmd/busmon/align_test.go`; modify `README.md`, `CLAUDE.md`
(busmon paragraph and "Things that bite" glyph note); create `docs/releases/v0.9.0.md`.

- [ ] **Step 1:** `align_test.go`: for every size of spec §8.1 (except too small) × palettes dark and
  none × fixture sets (20 agents hostile text; empty board; Redis down; filter + selection; each
  overlay open): draw the real `ui` on a recording `SimulationScreen`; assert corners, left border,
  right border (`│` or scrollbar set on data rows), no out-of-rect writes, header contains health and
  dispatch, row widths ≤ screen. Run → PASS (fix the owning task's code if not).
- [ ] **Step 2:** Docs (Kimi, from a brief): README busmon section (tiers, keys table, `--theme`,
  ambiguous-width note), CLAUDE.md busmon paragraph (files, keys, initial focus, words replacing
  badges), `docs/releases/v0.9.0.md` (what changes, use it, validation, live check pending).
- [ ] **Step 3:** Live check (controller): build busmon from the branch, seed project `uidemo` on
  `redis://127.0.0.1:6392`, run in a herdr tab (not w1:p1) and in a terminal outside herdr at 160,
  100 and 64 columns, dark and light, `NO_COLOR`; capture with `herdr pane read`; add captures to
  the release notes. OSC52 and wheel checked by hand → ask the user.
- [ ] **Step 4:** Full verification: `go build ./... && go vet ./... && AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./... -count=1 -race && bash tests/run.sh && python3 scripts/check-memory.py`.
- [ ] **Step 5:** Commit `docs: busmon v0.9.0 release notes, README and CLAUDE.md`.
