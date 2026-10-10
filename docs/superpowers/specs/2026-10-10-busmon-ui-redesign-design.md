# busmon v0.9.0 — UI redesign (btop-style, responsive)

- **Status:** approved in brainstorm 2026-10-10 (sections 1–4), rev 1
- **Base:** `main` @ 0baba13 (v0.8.0)
- **Inputs:** user brainstorm (style, usage, sizes, layout C, sort, theme, keys); Codex audit
  `.superpowers/brainstorm/codex-busmon-ui.md` (8 readability defects, approach comparison,
  parity table); browser mockups in `.superpowers/brainstorm/2815610-1791649884/content/`.

## 1. Goal

busmon looks like a 1980s block terminal: double frames everywhere, chips that read as one
sentence of state + badges, a status line that puts the broker's health last, and a palette
where a normal `idle` looks like a warning. v0.9.0 makes it a **modern, readable dashboard**
in the style of btop, without touching the bus protocol.

What the user said:

- Style: **btop-like dashboard** (rounded panels, inline bars, dense but legible).
- Usage: **mostly read-only** — a dashboard left open in a herdr tab; typing is rare.
- Sizes: **responsive** by column count; busmon must also run in a terminal outside herdr.
- Layout: **C — a btop process-table** of agents, then Activity + Board + Requests.
- Many agents: the Agents panel is **height-capped with a scrollbar**; **Enter or click** on an
  agent opens its detail (full message).
- Sort: **attention first** ("it will make busmon feel alive").
- Context: shown in **raw tokens**, never as a percentage.
- Theme: **inherit the terminal background** by default, **light theme** available.
- **Frames must never be misaligned** — treated as a hard requirement with its own tests.

Success: at a glance, the user sees whether the bus and the dispatcher are up, which agents
need attention and why, and what just happened; every frame is pixel-straight at every size.

## 2. Scope

In scope: everything under `cmd/busmon/` that draws or handles input; `README.md` and
`CLAUDE.md` busmon paragraphs; `docs/releases/v0.9.0.md`.

Out of scope: `bus/`, `dispatch/`, `cmd/agentbus/`, the stream layout and every protocol
contract; new dependencies (tview v0.42.0 + tcell v2.13.9 + their transitive `uniseg` stay the
only UI libraries). The ACP/CLI drivers move from v0.9 to v0.10.

Unchanged behaviour (parity, see §9): `--limit` / `AGENT_BUS_BUSMON_LIMIT`, `--reset`,
`--list`, `--delete`, the 1 s poll tick and its reads, `Recent` + `TailFrom` cursors, the
filter grammar, message/thread detail with the `detailGen` guard, OSC52 copy, directive vs
notify composition with `@agent` autocomplete, mouse support.

## 3. Screen layout

busmon picks a **tier** from the terminal width on every draw where the size changed:

| Tier | Width | Composition |
|---|---|---|
| wide | ≥ 120 | header (1 line) · Agents table · Activity (left) + Board and Requests (right) · footer |
| medium | 80–119 | header (2 lines) · Agents table (compact) · tabbed area `1 Activity · 2 Board · 3 Requests` · footer |
| narrow | < 80 | header (2 lines, short) · tab bar `1 Agents · 2 Activity · 3 Board · 4 Req` · one full-height view · footer |

A height below 12 rows shows the narrow tier's single view with a one-line header; below 6
rows busmon shows only `terminal too small (WxH)` — it never draws overlapping frames.

Wide tier, 160×40 (demo data; colour omitted):

```text
  busmon uidemo   ● bus ok   ● dispatch up   pilot master   1 request waiting         anthropic  session ██░░░░░░░░ 25%  week ████░░░░░░ 44%          18:26:20

 ╭─ Agents ────────────────────────────────────────────────────────────────────────────────────────────────── 14 agents · 4 working · 2 blocked · 1-8 of 14 ──╮
 │    agent     state     updated             queue       ctx     flags             message                                                                   ▲
 │▶ ▲ grok      blocked   ████░░░░░░ 6m       ·           —       gate 1            needs review of PR #46                                                    ┃
 │  ▲ ops       blocked   ████████░░ 2m       ·           —       gate 1            waiting for deploy approval                                               ┃
 │  ○ agy       idle      ░░░░░░░░░░ 18m      2 msg       —       waking            standing by                                                               ┃
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
 ╰──────────────────────────────────────────────────────────────────────────────────────────────╯ ╭─ Requests ───────────────────────────────╮
  › @agent message sends a directive · plain text notifies                                         ...
  tab  focus   ↑↓  select   enter  detail   y  copy   /  filter   end  live   F2  requests   F1  help   q  quit
```

Narrow tier, 64×22 (first rows):

```text
  busmon uidemo  ● bus  ● dispatch  1 req                18:26
 anthropic 25%/44% · pilot master
  1 Agents   2 Activity   3 Board   4 Req
 ╭─────────────────────────────────────────────── 14 agents ──╮
 │▶ ▲ grok      blocked    6m  ·  gate needs review of PR #46 │
```

### 3.1 Header

Order is fixed, most important first, so truncation can only ever drop the least important:
project · **bus health** (`● bus ok` / `▲ bus down 42s` in error colour, with the duration) ·
**dispatch** (`● dispatch up` / `○ dispatch down`) · pilot (`pilot <name>` / `autonomous`) ·
requests waiting (attention colour when > 0) · account budget gauges per provider
(`session ██░░ 25%  week ████ 44%`, gauge colour ok < 75 % ≤ attention < 90 % ≤ error) · clock.
Medium/narrow move pilot and budgets to the second line (narrow: `anthropic 25%/44%` text, no
gauge). Health and dispatch are never truncated; budgets are dropped whole (provider by
provider, from the right) before anything to their left. No budget published → nothing drawn
(unknown never looks like 0 %).

### 3.2 Agents panel

One row per agent, a btop-style table.

| Column | wide | medium | narrow | Content |
|---|---|---|---|---|
| selection | ✓ | ✓ | ✓ | `▶` on the selected row (plus row tint) |
| glyph | ✓ | ✓ | ✓ | `●` working · `○` idle · `▲` blocked · `✓` done · `◇` active (report-only) · `·` no state declared |
| agent | ✓ | ✓ | ✓ | name, truncated by cell width |
| state | ✓ | ✓ | ✓ | the **declared** state word, or `—` when none was ever declared |
| updated | ✓ (bar + age) | age only | age only | 10-cell bar that empties linearly over `staleAfter` (10 min) since the declaration, then the age. It is the age of the **declaration**, never a liveness signal; the help says so |
| queue | `N msg` | `N` | `N` | messages waiting for this agent (today's `✉N`: pending + unread obligations); `·` when 0 |
| ctx | ✓ | — | — | own context in raw tokens (`182k`), `—` when unknown. **Never a percentage** |
| flags | up to 2 words | 1 word | 1 word | by priority, see below; the rest are in the detail |
| message | ✓ | ✓ | ✓ | the status message, one line, truncated with `…` |

Flags (priority order; error colour for the first two, attention for the next three, violet for
dispatcher actions, muted for the rest): `escalated` (wake escalated) · `gate N` (open 4-eyes
challenges) · `deferred` (wake deferred) · `waking` (wake prompting/prompted) · `unobserved`
(prompt sent, nonce not seen) · `pilot` (holds the pilot lease) · `armed` (receiver lease
live) · `stale N` (entries past the horizon) · `gap?` (entries may have been trimmed unread).

The old `⌛N` shared-stream lag and the `⧉` pane marker leave the row: lag and pane move to the
agent detail. This retires both v0.8.0 known badge issues (inflating `⌛N`, spurious `✂?` for
new agents) from the main screen; the detail labels lag as "entries in the shared stream not yet
read by this agent's group — not a work queue".

**Sort — attention first**, alphabetical inside each group: (1) escalated or blocked or gated,
(2) queue > 0 or a wake deferred/in flight, (3) working, (4) active, (5) idle, (6) done,
(7) no state declared. The selection follows the **agent**, not the row index, when the order
changes; the viewport scrolls to keep it visible only if it was visible before.

**Height cap:** `rows = clamp(agentCount, 4, max(4, screenHeight/3))` in wide and medium; narrow
uses the full view height. When agents exceed `rows`, the right border carries a scrollbar
(`▲` top, `▼` bottom, `┃` thumb proportional to the visible share, `│` track) **in the border
column itself**, and the title shows `first-last of N`. The title also shows
`N agents · W working · B blocked`.

### 3.3 Activity

Columns `time · from · to · type · message` (wide: `HH:MM`, from 9, to 10, type 10; medium
compacts widths; narrow drops `to` into the message as `→ x`). One line per event, never wrapped:
newlines and tabs become spaces, control characters are removed, the full text is in the detail.
Type colours: directive/request/reply accent · blocked error · notify attention · wake/escalation
violet · report default · status muted. Title: `Activity  live` or `Activity  paused · N new`,
then `N events · filter: <text|none>`; the filter stays visible while a line is selected.
Day separators (v0.5.1) remain, as a muted full-width rule with the date.

### 3.4 Board and Requests

Board: header row `task · owner · state · age`, state coloured (working ok, requested attention,
blocked error, done done-colour), title `Board  D/N done`; branch moves to the detail. Requests
panel (wide) / tab (medium, narrow): one entry per waiting request — `target ← requester  text`,
then `state · age · deadline`; title `Requests  N` in attention colour when N > 0. `F2`/`r` still
opens the full requests overlay.

### 3.5 Detail overlays

Enter or click opens a detail for the focused panel's selection: **agent**, **message**
(existing detail, reorganised), **board task**, **request**. Layout: body first, then facts.

- **Agent detail:** full status message (wrapped), then `Declared` (state · age · time),
  `Pane` (id, pid, herdr state if known), `Receiver lease`, `Queue` (waiting · lag explained ·
  wake record state), `Gate` (open challenges), `Context` (raw tokens or why unknown), `Board`
  (task · state · branch), then the agent's last 5 activity lines. `y` copies the status
  message exactly.
- **Message detail:** body and fidelity warning first, then from/to/date, then id/thread (the
  technical block is last). `y` message, `Y` thread — unchanged.
- Width capped at 100 cells (reading width), centred; full screen in the narrow tier. Esc or
  `q` closes and restores the previous focus, selection and scroll.

## 4. Theme

`theme.go` is the only place that names a colour. Roles: `text`, `muted`, `dim`, `accent`
(focus, links, direction), `ok`, `attention`, `error`, `done`, `dispatch` (violet), `line`
(borders), `selection` (row tint). Every state is also carried by a glyph **and** a word, so
colour is never the only signal.

| Role | dark | light | 16-colour |
|---|---|---|---|
| text | `#E6EDF3` | `#1F2328` | default fg |
| muted | `#7D8A99` | `#5A6572` | silver |
| dim | `#4A5563` | `#B0B8C1` | gray |
| accent | `#7CC4FA` | `#0969DA` | teal |
| ok | `#7EE2A8` | `#1A7F37` | green |
| attention | `#E8BC63` | `#9A6700` | olive |
| error | `#F08A8A` | `#CF222E` | maroon/red |
| done | `#9DA7F5` | `#5A4FCF` | blue |
| dispatch | `#D7A6F5` | `#8250DF` | purple |
| line | `#2E3A48` | `#D0D7DE` | gray |
| selection bg | `#22324A` | `#DDF4FF` | navy / reverse |

- **Background is never painted** (`tcell.ColorDefault`) in either theme: busmon sits on the
  terminal's own background. Only the selection row and the focused-tab chip set a background.
- `--theme auto|dark|light`, env `AGENT_BUS_BUSMON_THEME`, flag wins. `auto`: `COLORFGBG`
  (background index 7 or 15 → light, otherwise dark); absent → dark.
- Colour depth: truecolor when tcell reports ≥ 2^24 colours (or `COLORTERM=truecolor|24bit`),
  else the 16-colour column. `NO_COLOR` (any value) → no colour at all; attributes only (bold
  for the focused panel title, reverse for the selection).
- The focused panel's border uses `accent`; others use `line`. The blocked/escalated state does
  not recolour a whole panel.

## 5. Glyphs and alignment (hard requirement)

1. **Closed glyph set**, all single-cell (East Asian Width N/Na, or A treated as narrow):
   `● ○ ◇ ▲ ▼ ✓ · ▶ → ← … █ ░ │ ┃ ─ ╭ ╮ ╰ ╯ ›`. No glyph of width W (`⏰ ⌛`), no emoji or
   emoji-capable symbol (`✉ ⚠ ✂ 👂 🔒 ⬢ ⧉`) anywhere in busmon output; their meanings become the
   words of §3.2. `theme.go` exports the set; a test asserts each has `uniseg` width 1 and a
   source scan asserts no other non-ASCII rune is drawn by busmon code.
2. **Cell-width arithmetic only.** Every pad, column and truncation uses a single helper that
   measures with tview's width functions (grapheme clusters), truncating with `…` so the result
   is **exactly** the target width — a wide character that would straddle the edge is replaced by
   a space.
3. **Untrusted text is normalised** before layout (agent names, messages, task names, branch,
   requests): `\t`, `\r`, `\n` → space; other C0/C1 controls and zero-width / bidi control
   characters removed; tview colour tags escaped. Emoji and CJK inside messages are allowed and
   measured as 2 cells.
4. **Frames are drawn by one helper** (rounded corners, title and right-label inside the top
   border) used by every panel and overlay; the scrollbar replaces right-border cells, never adds a
   column.
5. Documented limit: box-drawing characters assume the terminal treats ambiguous-width
   characters as narrow (the default in Western locales); `README.md` says so.

## 6. Interaction

Initial focus: **Agents panel** (read-mostly). One key table drives both the handler and the
footer hint line, so the footer can never advertise a key that does something else.

| Action | Keys / mouse |
|---|---|
| next / previous panel | `Tab` / `Shift-Tab` (Agents → Activity → Board → Requests); medium `1`–`3`, narrow `1`–`4`, or click a tab |
| move | `↑↓` `j k`, `PgUp` `PgDn`, `Home` `End` (`g` `G`), mouse wheel on the hovered panel |
| detail | `Enter` (`v`) or click on a row |
| copy | `y` line / message; `Y` thread (in a message detail) |
| filter Activity | `/` |
| back to live | `End` / `G` on Activity, or `Esc` |
| compose | `i` or `:` focus the input; `@` focuses it pre-filled with `@` |
| back / close | `Esc`: close overlay → leave input (draft kept) → clear selection → clear filter → (top level) nothing. Esc no longer quits |
| help / requests | `F1` `?` / `F2` `r` |
| quit | `q` outside the input; `Ctrl-C` anywhere |

The input line is a borderless single row above the footer (`›` prompt), with the existing
`@agent` completion and directive-vs-notify rule; the footer shows the target before sending.
Help (`F1`) is three sections — **Keys**, **States**, **Delivery** — and keeps the full legend
of every glyph, word and counter (`helpCoversEveryBadge` keeps covering all of them).

Resizing keeps focus, the selected agent / event / task, the Activity live-or-paused mode and the
input draft. A tier change maps focus to the nearest panel (e.g. Board focused in wide → tab 2
in medium).

## 7. Code structure

`cmd/busmon/main.go` (1192 lines, views as closures in `main`) is split by responsibility; data
flow (bus reads, cursors, 1 s tick, `shared` state under its mutex) is unchanged.

| File | Responsibility |
|---|---|
| `theme.go` | roles, palettes (dark, light, 16), colour-depth / NO_COLOR / theme resolution, glyph set |
| `cells.go` | normalise, measure, pad and truncate by cell; the frame helper |
| `layout.go` | tier selection, building the Flex/Pages tree per tier, rebuild on size change, focus/selection carry-over |
| `header.go` | header line(s), health/dispatch/pilot/requests/budget gauges |
| `agentsview.go` | custom tview primitive: table, attention sort, selection by name, scrollbar, mouse |
| `render.go` | Activity, Board, Requests row rendering (existing functions adapted to columns) |
| `detail.go` | message detail (reordered) + agent / task / request details |
| `keys.go` | the key table, footer hints, dispatch to actions |
| `help.go` | three-section help |
| `main.go` | flags, CLI paths (`--list`, `--delete`, `--reset`), bus connection, tick and tail goroutines, app start |

`--list` stays above the project-required exit and above `--reset` (CLAUDE.md "Things that
bite"). `tview.Borders` is set once to the rounded set; no other global tview state is changed.

## 8. Testing

All Redis-backed tests use only `AGENTBUS_TEST_REDIS_URL` (never `:6380`).

1. **Alignment suite** — the real main screen (all tiers) and each overlay drawn on
   `tcell.SimulationScreen` at 160×45, 120×40, 119×40, 100×30, 80×24, 79×24, 64×22, 40×12 and
   30×8, with fixtures: 20 agents, names of 32 chars, messages with emoji / CJK / tabs / newlines /
   colour-tag look-alikes, empty and full board, Redis down, filter active + selection, every flag.
   Assertions on cells: each frame's four corners at the expected cells; the right border at the
   same column on every row of the frame; no cell of a frame overwritten by content; no row wider
   than the screen; health and dispatch present in the header at every size.
2. **Glyph tests** — every glyph in the set has width 1; a source scan of `cmd/busmon/*.go` (non
   test) finds no non-ASCII rune outside `theme.go`.
3. **Tier tests** — tier boundaries (119/120, 79/80, heights 12/6); resize wide→medium→narrow→wide
   keeps focus, selected agent, Activity mode and input draft.
4. **Interaction tests** — tview `Application` on a simulation screen with injected key and mouse
   events: attention sort and selection-follows-agent on reorder; scroll and scrollbar thumb;
   Enter and click open the agent detail; paused Activity counts `N new`; filter survives
   selection; Esc ladder; `q` ignored while composing; footer hints equal the key table.
5. **Parity** — existing filter, detail (incl. `detailGen`), tracking, limit, `--list`/`--delete`/
   `--reset` and copy tests keep their intent; tests asserting exact tview colour tags are rewritten
   to assert meaning (word, glyph, role) and the theme separately.
6. **Live check before merge** — busmon built from the branch, run against a seeded project on the
   isolated Redis, in a herdr tab and in a terminal outside herdr, at wide / medium / narrow sizes,
   dark and light themes; screens captured (`herdr pane read`) into the PR. OSC52 copy and mouse
   wheel checked by hand in a real terminal.

## 9. Parity checklist (must hold after the redesign)

Filter grammar and window semantics (`filter.go`); selection by event identity; message detail
with fidelity markers and `detailGen`; OSC52 copy (`y`, `Y`); live tail with pause on scroll and
explicit return to live, never forcing scroll-to-end per refresh; the report ` (+N)` marker
(full text retained) and day separators; `--limit` precedence
(flag > env > 25, ≤ 0 replays all, UI window capped at 500); `--list` / `--delete` / `--reset`
routing and confirmations; directive vs notify composition and `@agent` completion; mouse
enabled; the 1 s poll of pilot, usage, budgets, challenges, agents, dispatch and wake records;
declared state never overwritten by silence; no "offline"/"dead" inferred from silence.

## 10. Release

v0.9.0: `docs/releases/v0.9.0.md` (with the live-check captures), README busmon section (tiers,
keys, `--theme`, ambiguous-width note), CLAUDE.md busmon paragraph updated (keys, focus,
files). The roadmap note that put ACP/CLI drivers at v0.9 is moved to v0.10.
