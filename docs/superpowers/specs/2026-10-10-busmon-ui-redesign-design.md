# busmon v0.9.0 — UI redesign (btop-style, responsive)

- **Status:** rev 3 — brainstorm sections 1–4 approved by the user 2026-10-10; Codex counter-audit
  C1–C22 (`.superpowers/brainstorm/codex-spec-review.md`) integrated; rev 3 records what
  implementation settled (§12).
- **Base:** `main` @ 0baba13 (v0.8.0)
- **Inputs:** user brainstorm; Codex UI audit (`.superpowers/brainstorm/codex-busmon-ui.md`);
  browser mockups (`.superpowers/brainstorm/2815610-1791649884/content/`).

## 1. Goal

busmon looks like a 1980s block terminal: double frames everywhere, chips that read as one
sentence of state + badges, a status line that puts the broker's health last, and a palette where
a normal `idle` looks like a warning. v0.9.0 makes it a **modern, readable dashboard** in the
style of btop, without touching the bus protocol.

What the user decided:

- Style: **btop-like dashboard** (rounded panels, inline bars, dense but legible).
- Usage: **mostly read-only** — a dashboard left open in a herdr tab; typing is rare.
- Sizes: **responsive** by column count; busmon also runs in a terminal outside herdr.
- Layout: **C — a btop process-table** of agents, then Activity + Board + Requests.
- Many agents: the Agents panel is **height-capped with a scrollbar**; **Enter or click** on an
  agent opens its detail (full message).
- Sort: **attention first**.
- Context: shown in **raw tokens**, never as a percentage.
- Theme: **inherit the terminal background** by default, **light theme** available.
- **Frames must never be misaligned** — a hard requirement with its own tests.

Success: at a glance, the user sees whether the bus and the dispatcher are up, which agents
need attention and why, and what just happened; every frame is straight, in cells, at every
supported size, under the width assumption of §5.5.

## 2. Scope

In scope: everything under `cmd/busmon/` that draws or handles input; `README.md` and
`CLAUDE.md` busmon paragraphs; `docs/releases/v0.9.0.md`.

Out of scope: `bus/`, `dispatch/`, `cmd/agentbus/`, the stream layout and every protocol
contract (including the bus-side `GapPossible` computation); new dependencies (tview v0.42.0 +
tcell v2.13.9 + their transitive `uniseg` stay the only UI libraries). The ACP/CLI drivers move
from v0.9 to v0.10.

Unchanged behaviour (parity, §10): `--limit` / `AGENT_BUS_BUSMON_LIMIT`, `--reset`, `--list`,
`--delete`, the read inventory and cadence of §9, `Recent` + `TailFrom` cursors, the filter
grammar, message/thread detail with the `detailGen` guard, OSC52 copy, directive vs notify
composition with `@agent` autocomplete, mouse support.

## 3. Screen layout

### 3.1 Tiers and the layout function

A **pure function** `computeLayout(w, h int, counts) Layout` returns the tier and one rectangle per
visible zone. Nothing else decides geometry; it is unit-tested directly (§8.1).

| Tier | Chosen when | Composition (top to bottom) |
|---|---|---|
| wide | `w ≥ 120` and the wide minima fit | header 1 · Agents · lower row: Activity (left) + right column Board / Requests · input 1 · footer 1 |
| medium | `80 ≤ w < 120`, or wide minima don't fit, and medium minima fit | header 2 · Agents · tab bar inside the lower frame: `1 Activity · 2 Board · 3 Requests` · input 1 · footer 1 |
| narrow | otherwise, while `w ≥ 30` and `h ≥ 6` | header 1–2 · tab bar `1 Agents · 2 Activity · 3 Board · 4 Req` · one full-height view · footer 1 (input 1 only while composing) |
| too small | `w < 30` or `h < 6` | only `terminal too small (WxH)` centred; no frame is drawn |

Vertical budget (wide and medium). Fixed rows are subtracted first: header (1 wide, 2 medium),
input 1, footer 1. Every framed zone costs 2 border rows; the Agents table in wide costs 1 more for
its column header. Minima, in **data rows**: Agents ≥ 3, lower area ≥ 3 (wide: Activity ≥ 3,
Board ≥ 1, Requests ≥ 1). Agents data rows =
`min(agentCount, max(3, (h − fixed) / 3 − borders))`, never below 3 when agents exist; an empty
agent list shows one data row `no agent has published yet`. If the minima do not fit at that
height, the next tier down is chosen — so 120×12 renders the narrow tier, never overlapping frames.

Horizontal budget (wide): right column width = `clamp(w * 3 / 10, 40, 56)`; Activity takes the
rest. Right column heights: Board = `min(boardRows + 3, (lowerH + 1) / 2)`, at least 4; Requests
gets the remainder (≥ 3). Empty states: Board `no task on the board`, Requests `nothing waiting`.

Wide tier, 160×40 (demo data; colour omitted):

```text
  busmon uidemo   ● bus ok   ● dispatch up   pilot master   1 request waiting      anthropic used  session ██░░░░░░░░ 25%  week ████░░░░░░ 44%     18:26:20

 ╭─ Agents ────────────────────────────────────────────────────────────────────────────────────────────────── 14 agents · 4 working · 2 blocked · 1-8 of 14 ──╮
 │    agent     state     updated             queue       ctx     flags             message                                                                   ▲
 │▶ ▲ grok      blocked   ████░░░░░░ 6m       ·           —       gate 1            needs review of PR #46                                                    ┃
 │  ▲ ops       blocked   ████████░░ 2m       ·           —       gate 1            waiting for deploy approval                                               ┃
 │  ○ agy       idle      ░░░░░░░░░░ 18m      2 msg       —       woken             standing by                                                               ┃
 │  ● coder     working   ██████████ 8s       ·           140k    armed             implementing task 4: theme tokens                                         ┃
 │  ○ codex     idle      ███████░░░ 3m       1 msg       64k     deferred          waiting for review request                                                │
 │  ○ foureyes  idle      ██████░░░░ 4m       1 msg       33k     deferred          waiting for PR #47                                                        │
 │  ● kimi      working   █████████░ 40s      ·           —       armed             running go test ./... -race                                               │
 │  ● master    working   ██████████ 12s      ·           182k    pilot · armed     coordinating v0.9 UI work                                                 ▼
 ╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ╭─ Activity  live ────────────────────────────────────────────────── 10 events · filter: none ─╮ ╭─ Board ───────────────────── 1/4 done ────╮
 │ time      from      to         type       message                                            │ │ task             owner    state      age  │
 │ 18:23     master    → codex    directive  review the busmon render.go refactor               │ │ ui-theme         codex    working    2m   │
 │ ...                                                                                          │ ╰───────────────────────────────────────────╯
 ╰──────────────────────────────────────────────────────────────────────────────────────────────╯ ╭─ Requests  2 ────────────────────────────╮
  › @agent message sends a directive · plain text notifies                                         ...
  tab  focus   ↑↓  select   enter  detail   y  copy   /  filter   end  live   F2  requests   F1  help   q  quit
```

The scrollbar arrows sit on the first and last **data-row** cells of the right border, never on a
corner (§5.4).

### 3.2 Header

Space is allocated in this order, and only then the rest is laid out left to right: **bus health**
then **dispatch** (reserved first, never truncated), then the project name (truncated with `…` to
what is left, minimum 6 cells), then pilot, requests waiting, budget gauges, clock — each dropped
whole, from the right, when it does not fit.

- Health: `● bus ok` (ok) / `▲ bus down 42s` (error, with the duration since the last success).
  Compact form below 80 columns: `● bus` / `▲ bus 42s`.
- Dispatch: `● dispatch up` (ok) / `○ dispatch down` (muted) / `? dispatch` (muted, not yet
  measured). Compact: `● disp` / `○ disp` / `? disp`.
- Pilot: `pilot <name>` / `autonomous`. Requests: `N waiting` (attention when > 0), the same count
  as today's `waitingLine` (tracked requests waiting + untracked open threads).
- Budgets: per provider `<provider> used  session ▇▇░░ 25%  week ▇▇▇▇ 44%` — **used**, never
  "left"; gauge colour ok < 75 % ≤ attention < 90 % ≤ error. No budget published → nothing drawn.
  Narrow: `anthropic 25%/44%` text.
- Medium and narrow move pilot, requests and budgets to the second line.

### 3.3 Agents panel

One row per agent, a btop-style table.

| Column | wide | medium | narrow | Content |
|---|---|---|---|---|
| selection | ✓ | ✓ | ✓ | `▶` on the selected row (plus row tint) |
| glyph | ✓ | ✓ | ✓ | from the state table below |
| agent | ✓ | ✓ | ✓ | name, truncated by cell width |
| state | ✓ | ✓ | ✓ | the **declared** state word, or `—` when none was ever declared |
| updated | bar + age | age | age | 10-cell bar that empties linearly over `staleAfter` (10 min) from the **declaration** time, then the age. No declaration → bar of `·` and age `—`. A declaration time in the future (clock skew) → full bar and age `0s`. Never a liveness signal |
| queue | `N msg` | `N` | `N` | **wake-eligible obligations** (`Obligation.Size()`: addressed, not `wake=no`, within the horizon). `·` = measured zero; `—` = not yet measured; a value whose last read failed is shown muted with `?` (e.g. `2?`) |
| ctx | ✓ | — | — | own context in raw tokens (§3.3.3), `—` when unknown. **Never a percentage** |
| flags | up to 2 | 1 | 1 | highest-priority flags (§3.3.2); all of them in the detail |
| message | ✓ | ✓ | ✓ | status-message **preview** (§5.3), truncated with `…` |

#### 3.3.1 States

| Declared state | glyph | word | colour role | sort group |
|---|---|---|---|---|
| `blocked` | `▲` | blocked | error | 1 |
| `working` | `●` | working | ok | 3 |
| `idle` | `○` | idle | muted | 4 |
| `done` | `✓` | done | done | 5 |
| any other recorded value (legacy `review`, future states) | `○` | the value itself | muted | 4 |
| none ever declared (report-only, armed-only) | `·` | `—` | muted | 6 |

A report proves presence, not a state: a report-only agent is **not** labelled `active`; its last
report time appears in the detail as "last seen".

#### 3.3.2 Flags

| Source (stored value) | flag word | role | priority | sort effect |
|---|---|---|---|---|
| `WakeRecord.Escalated == "true"` | `escalated` | error | 1 | group 1 |
| `WakeRecord.Escalated == "pending"` | `escalating` | error | 2 | group 1 |
| open 4-eyes challenges `N > 0` | `gate N` | error | 3 | group 1 |
| `WakeRecord.State == "deferred"` | `deferred` | attention | 4 | group 2 |
| `WakeRecord.State == "unobserved"` | `unobserved` | attention | 5 | group 2 |
| `WakeRecord.State == "prompting"` | `waking` | dispatch | 6 | group 2 |
| `WakeRecord.State == "prompted"` | `woken` | dispatch | 7 | group 2 |
| pilot lease holder | `pilot` | accent | 8 | — |
| live receiver lease | `armed` | muted | 9 | — |
| `Obligation.Stale > 0` | `stale N` | muted | 10 | — |
| any other non-empty `WakeRecord.State` | `wake:<value>` | muted | 11 | — |
| `""` / `clear` | — | — | — | — |

`woken` means the dispatcher saw its own nonce echoed on the agent's screen — **not** that the
agent read or accepted anything; the help and the detail say so. `armed` is a lease, `gate N` is a
count of challenges; neither changes the declared state. **GapPossible is shown only in the agent
detail**, worded "entries may have been trimmed before this agent read them — possible, not
proven"; it is no longer on the main screen. Shared-stream lag and the pane id also move to the
detail.

#### 3.3.3 Context tokens

`UsageSnapshot.Ctx` is a string. A UI parser accepts an optional trailing ` ctx`, then an integer
or decimal with an optional `k`/`K` or `M` suffix (`608k`, `194.5k`, `1.2M`, `42000`). Display is
compact: `< 1000` as is, `< 1M` as `Nk` rounded to the nearest integer k (`194.5k` → `195k`),
otherwise `N.NM`. Anything else — a percentage, an empty string, legacy `Session`/`Weekly`/`Reset`
fields — renders `—`, and the detail shows the raw value and "not a token count". The legacy
fallback to `Session`/`Reset` in today's `usageBadge` is removed.

#### 3.3.4 Sort, selection and viewport

The comparator is a **pure total order**: (1) escalated, escalating, gated or blocked; (2) a wake
deferred, unobserved, waking or woken, **or** a measured queue > 0; (3) working; (4) idle and other
recorded states; (5) done; (6) no state declared; name ascending inside each group. An unmeasured
or failed queue value never moves an agent between groups. Silence never changes the group (no
decision is taken on age).

- Selection is held by **agent name**; it survives re-sorting, arrival and departure of agents. A
  departed selected agent moves the selection to the agent now at its former index (clamped).
- Viewport: keyboard moves follow the selection (scroll just enough to keep it visible). Wheel and
  scrollbar scrolling move the viewport only, and **anchor it to the agent name at its top row**;
  a tick that reorders keeps that anchor at the top. The viewport returns to follow-selection on the
  next keyboard move.
- Clicking a row selects that agent by name (the row under the mouse at click time); a second
  click on the selected row, or Enter, opens the detail.
- Title: `N agents · W working · B blocked`, plus `first-last of N` when scrolled content exists.

### 3.4 Activity

One line per event: `time · from · to · type · message`. Columns: wide `HH:MM` 9, from 9, to 10,
type 10; medium compacts widths; narrow folds `to` into the message as `→ x`. The message is the
**preview** (§5.3). Mapping from `bus.Event`:

| Kind / Type | from | to | type label | role |
|---|---|---|---|---|
| status | Agent | — | `status` (message shows `state · text`) | muted |
| report (RKind note) | Agent | — | `report` (+ ` (+N)` when `full` holds more) | text |
| report (RKind auto) | Agent | — | `auto` (+ ` (+N)`) | muted |
| notify | From | — | `notify` | attention |
| cmd directive with `Task` set (request metadata) | From | Target | `request` (derived label; the wire type stays directive) | accent |
| cmd directive | From | Target | `directive` | accent |
| cmd reply | From | Target | `reply` | accent |
| cmd challenge / verdict | From | Target | `challenge` / `verdict` | attention |
| cmd escalation | From | Target | `escalation` | dispatch |
| cmd blocked | From | Target | `blocked` | error |
| cmd any other type | From | Target | the raw type, truncated | muted |

Day separators (v0.5.1) stay, as a muted full-width rule with the date; they are not events (not
selectable, not counted). `y` on a line copies **exactly what it copies today** (the existing plain
line format); the detail's `y`/`Y` are unchanged.

Title: `Activity  live` or `Activity  paused · N new`, then `N events · filter: <text|none>`; the
filter stays visible while a line is selected. **N new**: a pause cursor is recorded when the view
leaves live (scroll up, selection, `Home`); N counts events **admitted by the current filter**
arriving after it, separators excluded. Resize and selection changes do not reset it; returning to
live resets it to 0. If the selected event is evicted from the 500-event window, the selection moves
to the oldest retained event admitted by the filter.

### 3.5 Board and Requests

Board rows: `task · owner · state · age` (age = time since `Updated`), title `Board  D/N done`;
the branch is in the task detail. Board states: `working` ok, `requested` attention, `accepted`
accent, `blocked` error, `done` done, any other value muted. Each state also shows its word.

Requests (panel in wide, tab elsewhere) keeps **both** of today's categories, in two labelled
sections, with the header count equal to their sum (no double counting, as `waitingLine` today):

1. **Tracked requests waiting** (from the board): `target ← from  task`, then
   `state · age since Created (or Updated when absent) · deadline or "no deadline"`; blocked
   reason in error colour when present.
2. **Untracked threads with no answer** in the retained history: `type  from → target  message
   preview`, `asked N ago`. Never shown as accepted.

`F2`/`r` opens today's full requests overlay content (all caveats, done requests, delivery vs
acceptance), restyled.

### 3.6 Detail overlays

Enter or click opens a detail for the focused panel's selection. Each detail is a full-screen
**modal layer** that consumes every key and mouse event; a click outside the detail box
closes it like Esc and goes no further (it never selects a row behind); the wheel outside does nothing. Width capped at 100 cells (centred); full screen in the narrow tier. Esc or `q`
closes it and restores the previous **logical** focus (panel id + selected item key), even across a
tier change; if the item is gone, the panel's nearest item is selected.

Field → source → when absent:

| Detail | Field | Source | Absent / unknown |
|---|---|---|---|
| agent | status message | the retained `message` of the last status entry, multi-line (§5.3) | `no status message` |
| agent | declared | state, age, time | `never declared` |
| agent | last seen | last status/report time | `—` |
| agent | pane / pid | `AgentSnapshot.Pane`, `.PID`, `.PIDStart` (already read; added to the UI model) | `not recorded` |
| agent | receiver lease | armed | — |
| agent | queue | `Obligation` (size, pending, unread, stale), measured time; shared-stream lag with its caveat | `not measured yet` / `read failed Ns ago` |
| agent | wake | `WakeRecord` state (with its meaning), tries, escalation, reason | `no wake record` |
| agent | gap | `GapPossible` → "possible, not proven" | — |
| agent | gate | open challenges (ref, opener, age) | `none` |
| agent | context | parsed tokens, raw value | `not reported` / `not a token count: <raw>` |
| agent | board | tasks owned (task · state · branch) | `none` |
| agent | recent activity | up to 5 lines **from the retained window** (labelled so) | `none in the retained window` |
| message | (today's detail, reordered) | body + fidelity warning, then from/to/date, then id/thread | unchanged |
| task | task, owner, state, branch, updated; request metadata when tracked | board entry | `—` per field |
| request | today's `requestBlock` content | board entry + thread | unchanged |

No herdr runtime state is shown: busmon has no source for it and does not poll herdr.

`y` copies: agent → the status message as retained; message → unchanged (`y` message, `Y`
thread); task → `task owner state branch` on one line; request → today's plain request block.

## 4. Theme

`theme.go` is the only place that names a colour. Roles: `text`, `muted`, `dim` (decoration
only — never the sole carrier of information), `accent`, `ok`, `attention`, `error`, `done`,
`dispatch`, `line`, `selection`. Every state is carried by a glyph **and** a word.

| Role | dark | light | 16-colour |
|---|---|---|---|
| text | `#E6EDF3` | `#1F2328` | default fg |
| muted | `#7D8A99` | `#5A6572` | silver |
| dim | `#4A5563` | `#B0B8C1` | gray |
| accent | `#7CC4FA` | `#0969DA` | teal |
| ok | `#7EE2A8` | `#1A7F37` | green |
| attention | `#E8BC63` | `#9A6700` | olive |
| error | `#F08A8A` | `#CF222E` | red |
| done | `#9DA7F5` | `#5A4FCF` | blue |
| dispatch | `#D7A6F5` | `#8250DF` | purple |
| line | `#2E3A48` | `#D0D7DE` | gray |
| selection bg | `#22324A` | `#DDF4FF` | reverse attribute |

- **Background is the terminal's** (`tcell.ColorDefault`) everywhere. tview's global `Styles` are
  **not** modified (only `tview.Borders` is set once); instead constructors in `theme.go` apply the
  roles explicitly to every primitive busmon creates: boxes, frames and titles, list rows, the
  input field and its **autocomplete list**, overlays and the modal layer, tabs, selections. A test
  inspects cell styles (not only runes) for each of them.
- `--theme auto|dark|light`, env `AGENT_BUS_BUSMON_THEME`, flag wins. `auto`: `COLORFGBG`
  background index 7 or 15 → light, anything else or absent → dark.
- Colour depth: truecolor when tcell reports ≥ 2^24 colours, else the 16-colour column. `COLORTERM`
  is only a hint tcell already consumes, not proof. `NO_COLOR` (any value) → no colour at all,
  including colours inside rendered text: attributes only (bold for the focused panel's title and
  borders, reverse for the selection and the active tab).
- The focused panel's border uses `accent`; others `line`. No panel is recoloured by an agent state.
- Validation backgrounds (manual check, §8.6): black `#000000`, dark blue `#1E1E2E`, white
  `#FFFFFF`, warm light `#FDF6E3`; plus 16-colour and `NO_COLOR`. No universal contrast promise.

## 5. Glyphs, text and alignment (hard requirement)

1. **Closed UI glyph set**, every one of uniseg width 1 at the pinned version:
   `● ○ ▲ ▼ ✓ · ▶ → ← ↑ ↓ … — █ ░ ▇ │ ┃ ─ ╭ ╮ ╰ ╯ ›`. No glyph of width 2 (`⏰ ⌛`) and no emoji-capable
   symbol (`✉ ⚠ ✂ 👂 🔒 ⬢ ⧉`) is drawn by busmon; their meanings are the words of §3.3. `theme.go`
   exports the set. A test asserts each has width 1; an AST test over `cmd/busmon` non-test files
   asserts that **string and rune literals** contain no non-ASCII rune outside `theme.go` (comments
   are not scanned; user data is not concerned).
2. **Cell arithmetic only.** One helper measures, pads and truncates by **grapheme cluster** with
   the pinned uniseg widths; truncation appends `…` and the result is exactly the target width — a
   cluster that would straddle the edge is dropped and the gap padded with spaces. tview colour tags
   are escaped after measurement.
3. **Three representations of untrusted text** (agent names, messages, tasks, branches, request
   fields):
   - **raw** — unchanged; used for copy, the filter and the stored event.
   - **preview** (one line, rows): `\t \r \n` → space; removed: other C0 controls, DEL, C1
     controls, bidi controls (U+061C, U+200E, U+200F, U+202A–U+202E, U+2066–U+2069); kept: combining
     marks, ZWJ (U+200D), variation selectors, emoji and CJK clusters (measured, usually 2 cells).
   - **detail** (multi-line, overlays): same removals, but `\n` kept and `\t` expanded to the next
     4-cell stop; long lines wrap by cluster width.
4. **Frames are drawn by one helper** (rounded corners, title left, label right, inside the top
   border, both truncated before they could touch a corner — the right label gives way first). The
   scrollbar uses the right-border cells between the corners only: `▲` first data row, `▼` last data
   row, `┃` thumb, `│` track; the corners never change.
5. Documented limit (README): box-drawing and several UI glyphs are East-Asian "ambiguous" width;
   alignment assumes the terminal renders ambiguous width as narrow (the Western-locale default).

## 6. Interaction

Initial focus: the **Agents panel**. One key table per **mode** drives both the handlers and the
footer hint line, so the footer can never advertise a key that does something else. Capture order:
overlay (modal) → input / filter (text modes) → panel → global.

| Mode | Key | Action |
|---|---|---|
| any | `Ctrl-C` | quit |
| panel | `Tab` / `Shift-Tab` | next / previous **visible** panel (wide: Agents → Activity → Board → Requests) |
| panel | `1`–`3` (medium), `1`–`4` (narrow), click on a tab | switch tab; focuses that panel |
| panel | `↑ ↓` `j k`, `PgUp PgDn`, `Home` `g` | move selection / first item |
| panel (not Activity) | `End` `G` | last item |
| Activity | `End` `G` | **return to live** (clear selection, follow the tail, N new → 0) |
| panel | `Enter` `v`, click on a selected row | open the detail |
| panel | `y` | copy the selected item (§3.4, §3.6) |
| panel | `/` | open the **filter** line (Activity's filter, from any panel; focuses Activity) |
| panel | `i` `:` | focus the **input** line; `@` focuses it pre-filled with `@` |
| panel | `F1` `?` / `F2` `r` | help / requests overlay |
| panel | `q` | quit |
| panel | `Esc` | in order: clear selection → clear the Activity pause (back to live) → clear the filter → nothing. Never quits |
| input | text keys, digits, `q`, `Tab` | are text (Tab completes `@agent` as today) |
| input | `Enter` | send (directive if `@agent body`, else notify) — unchanged rule; the hint line shows the target before sending |
| input | `Esc` | back to the previous panel; the **message draft** is kept |
| filter | text keys | edit the filter; `Enter` applies, `Esc` cancels the edit (the previously applied filter stays). Filter draft and message draft are separate |
| overlay | `Esc` `q`, click outside the box | close |
| overlay | `↑ ↓ j k PgUp PgDn g G` | scroll |
| overlay | `y` / `Y` | copy (§3.6) |
| overlay | everything else, incl. tab keys and the wheel outside the box | ignored |

Mouse: wheel scrolls the panel under the pointer (viewport only, §3.3.4) without changing focus or
opening anything; click focuses that panel and selects the row; click on the selected row opens it;
click on the scrollbar track pages; dragging the thumb is not supported.

The input line is a borderless single row above the footer (`›` prompt). Help (`F1`) has three
sections — **Keys** (generated from the key tables), **States** (§3.3.1–3.3.2, board states,
activity types), **Delivery** (queue, wake states, gap, lag, armed, gate caveats) — and keeps the
full legend of every glyph, word and counter (`helpCoversEveryBadge` covers the new vocabulary).

Resizing keeps the logical focus, the selected item per panel, the Activity live/paused state with
its pause cursor, both drafts, and an open overlay. A tier change maps focus to the same logical
panel (Board in wide → tab 2 in medium); a panel that is not visible is never focused.

## 7. Code structure and UI loop

`cmd/busmon/main.go` (1192 lines) is split by responsibility; the data flow (bus reads, cursors,
`shared` state under its mutex) is unchanged except for the UI-model fields named in §3.3 and §3.6
(PID, measurement validity and times).

| File | Responsibility |
|---|---|
| `theme.go` | roles, palettes (dark, light, 16, none), theme/depth resolution, style constructors, glyph set |
| `cells.go` | text representations (§5.3), grapheme measure/pad/truncate, the frame helper |
| `layout.go` | `computeLayout` (pure), tier, arranging the stable primitives into the root, logical focus |
| `listview.go` | one custom tview primitive for selectable, scrollable single-line lists: frame, inner rect, viewport, selection by key, scrollbar, keyboard and mouse handlers, paste ignored |
| `agents.go` | agent rows, comparator, ctx parser, flags |
| `activity.go` | event → row mapping, pause cursor, N new, filter wiring |
| `board.go` | board and requests rows |
| `header.go` | header allocation and rendering |
| `detail.go` | modal layer; message, agent, task, request details |
| `keys.go` | key tables per mode, footer hints, dispatch to actions |
| `help.go` | three-section help |
| `main.go` | flags, CLI paths (`--list`, `--delete`, `--reset`), connection, tick and tail goroutines, app start |

Agents, Activity, Board and Requests are all `listview` instances; overlays stay `TextView`s inside
the modal layer.

**Stable primitives.** Every primitive is created **once** at start-up. A size change never
recreates widgets: it only re-arranges the existing primitives in the root container. Rules:

- `Draw` and `SetBeforeDrawFunc` do no I/O, no waiting, and never call `SetFocus` (tview holds the
  application lock there; `SetFocus` would deadlock). `SetBeforeDrawFunc` only compares the screen
  size with the last laid-out size and, on change, starts `go app.QueueUpdateDraw(relayout)` —
  asynchronously, never a synchronous queue from inside the draw.
- `relayout` runs on the UI goroutine: `computeLayout`, re-arrange, restore the logical focus with
  `SetFocus`.
- Tail goroutines and asynchronous detail loads write to the shared model or through
  `QueueUpdateDraw` into the stable primitives, so no update can land in a discarded widget; the
  `detailGen` guard stays.

`--list` stays above the project-required exit and above `--reset` (CLAUDE.md "Things that
bite"). `tview.Borders` is set once to the rounded set; no other global tview state is changed.

## 8. Testing

All Redis-backed tests use only `AGENTBUS_TEST_REDIS_URL` (never `:6380`). UI tests use a fake
data source, an injected clock and injected colour capabilities.

1. **Geometry** — `computeLayout` at 160×45, 120×40, 120×12, 119×40, 119×12, 160×13, 100×30,
   80×24, 80×11, 79×24, 64×22, 40×12, 30×6, 30×5, 1×40 and 10×40: rectangles inside the screen,
   pairwise disjoint, minima respected, ≥ 1 data row in the active view, the expected tier
   (`too small` for 30×5, 1×40, 10×40).
2. **Alignment** — the real screen and every overlay on `tcell.SimulationScreen` at the sizes
   above (except too small), with fixtures: 20 agents, 32-char names, messages with decomposed
   accents, ZWJ emoji, variation selectors, CJK, tabs, newlines, ESC sequences, bidi controls and
   tview-tag look-alikes; empty and full board; Redis down; filter + selection; every flag and state.
   Assertions on full `SimCell`s (runes **and** styles): each frame's four corners at the expected
   cells; every right-border cell is `│`, or one of `▲ ▼ ┃ │` in a scrollable frame's data rows;
   the left border is `│` on every row; no row wider than the screen; health and dispatch present in
   the header. A recording wrapper around the screen fails the test on any write outside the
   rectangle of the primitive that made it (SimulationScreen alone would clip silently).
3. **Glyphs and text** — glyph widths; the AST literal scan; the three representations (preview
   and detail removals, raw unchanged; copy byte-for-byte equal to the retained text).
4. **Model** — comparator (insertion of a more urgent agent, departure, group change, unknown
   queue never moves groups); ctx parser (zero, `k`, `M`, decimals, ` ctx`, percentages, legacy
   fields); flags table; measurement validity (start, first success, failure after success,
   recovery); N new (arrival, filter, resize, eviction).
5. **Application** — tview `Application` on a simulation screen with injected key and mouse events,
   each test bounded by a timeout: resize during an asynchronous detail load (no deadlock, update
   lands in the live primitive, overlay kept); wheel + tick (no jump); click selection under
   reorder; Enter/click open; Esc ladder; `q` and digits typed while composing; filter vs message
   drafts; modal blocks outside clicks; Tab never reaches a hidden panel; footer hints equal the
   key table for each mode.
6. **Parity** — existing filter, detail (incl. `detailGen`), tracking, limit, `--list`/`--delete`/
   `--reset` and copy tests keep their intent; tests asserting exact tview colour tags are rewritten
   to assert meaning (word, glyph, role); read-inventory counters (§9) are tested apart from
   rendering.
7. **Live check before merge** — busmon built from the branch against a seeded project on the
   isolated Redis, in a herdr tab and in a terminal outside herdr, at wide / medium / narrow sizes,
   dark and light themes, on the four validation backgrounds, 16-colour and `NO_COLOR`; screens
   captured into the PR. OSC52 copy and the mouse wheel checked by hand in a real terminal.

## 9. Read inventory (unchanged cadence)

Every 1 s tick, as today: pilot driver, usage, budgets, agents hash, per-agent open challenges,
board (requests and threads from the retained window). Every 5th tick (`wakePoll`), as today:
`DispatchAlive`, and per agent `Obligation` + `Wake`. The redesign adds **no** read per tick or per
draw; the agent detail renders from the model. The only on-demand read remains the thread read when
a message detail opens, behind `detailGen`.

## 10. Parity checklist

Filter grammar and window semantics (`filter.go`); selection by event identity; message detail
with fidelity markers and `detailGen`; OSC52 copy (`y`, `Y`) with today's copied text; live tail with
pause on scroll and explicit return to live, never forcing scroll-to-end per refresh; the report
` (+N)` marker and day separators; `--limit` precedence (flag > env > 25, ≤ 0 replays all, UI
window capped at 500); `--list` / `--delete` / `--reset` routing and confirmations; directive vs
notify composition and `@agent` completion; mouse enabled; the read inventory of §9; declared state
never overwritten by silence; no "offline"/"dead"/"active" inferred from silence or presence.

## 11. Release

v0.9.0: `docs/releases/v0.9.0.md` (with the live-check captures), README busmon section (tiers,
keys, `--theme`, ambiguous-width note), CLAUDE.md busmon paragraph updated (keys, focus, files,
badges → words). Wherever ACP/CLI drivers were announced for v0.9, they move to v0.10.

## 12. Settled during implementation (rev 3)

- §5.1: the literal scan allows the UI glyph set anywhere, not ASCII only — the hazard is a glyph
  outside the set (width 2 or emoji-capable), and every glyph in the set is width 1.
- §3.6: a message detail starts with one attribution line (`from → to  date (age)`) above the body,
  so the author stays visible at 40×8; the technical block follows the body as specified.
- §3.6: in the narrow tier an overlay takes the full width **below the header**, so bus health and
  dispatch stay visible while a detail is open.
- §3.3.1: until the operator moves in Agents, the selection follows the top (most urgent) row.
- §3.4: an evicted selected event moves the selection to the event now at its former position
  (the list's departed-key rule), not to the oldest retained one.
- §3.2: the header's waiting text is today's `waitingText` (count and age of the oldest).
- §3.6 (user request, 2026-10-11): a click outside an open detail closes it, like Esc; the click
  is consumed, so it never selects or opens a row behind.
- §3.4: the wide time column is 7 cells (`HH:MM` and its gap), not 9; the two cells go to `from`
  and `to`. The help (F1) lists every binding, alternates included (`enter v`, `F1 ?`, `F2 r`).
- §7: tview initialises the screen it is given exactly once; the colour depth is read from that
  screen (a throwaway screen to probe colours broke the real terminal). tview delivers keys to the
  root primitive, so the main page and the modal layer relay them to the focused child.
