# busmon's idle view: a calm screen while nobody looks (idle-view)

Status: implemented (subject `idle-view-impl`). The person chose layout A from the mock-ups
(three frames: you, team, next, with the big count), a 10-minute delay once turned on (off by
default), and to stay in the view when a decision arrives (§8). §4 and §5 describe layout A; the
rotating frames of the first draft were not kept.

## 1. The idea

busmon often stays open on a screen nobody is reading. The dashboard is dense: panels, the feed,
fades. After a delay with no input, the person wants busmon to show a minimal view instead,
like a screensaver:

- three frames, built around what matters at a glance: **how many things need the person**,
  **what state the agents are in**, and **what comes next**;
- a bottom strip with **token use** and the **plan's progress**.

Any key or any mouse movement brings the dashboard back at once. That input only wakes busmon:
it never runs as a command.

## 2. The setting

- **`display.idle_view_after`**, in `config/keys.go`: kind `Duration`, default `0` (off), env
  `AGENT_BUS_IDLE_VIEW_AFTER`, read by busmon. An invalid env value is skipped, never a broken
  screen.
- **Settings page** (`C`, Display group): the label is "Idle view after", and the note says "a
  calm screen after this long without input; 0 = off". It applies at once, like the other
  Display keys.
- **Below 1 minute** the value is not taken: a few seconds would turn the view on while the
  person is still reading. This needs two new validators, since `fileDuration(true)` and
  `envStallDuration` both accept `30s` (foureyes):
  - **file rule `fileIdleView`**: `"0"`, or a Go duration from `1m` to `24h`. Anything else is
    refused with "want 0 (off) or a duration from 1m to 24h, such as \"10m\"". The settings page
    uses the same rule, so it refuses the same values.
  - **env rule `envIdleView`**: whole seconds or a Go duration, either `0` or from `1m` to `24h`.
    Anything else, `30s` included, is **skipped**: the file's value or the default (off)
    applies, as for the other `env` rules.

## 3. Idle detection in Bubble Tea

- **The last input.** `Model` gains `lastInput time.Time`. `Update` sets it to `m.now` for every
  input message: `tea.KeyPressMsg`, `tea.PasteMsg`, `tea.MouseClickMsg`, `tea.MouseWheelMsg`,
  `tea.MouseMotionMsg` and `tea.FocusMsg`. Nothing else counts: `tickMsg`, `tailMsg`, a resize,
  a fade frame or a notice is not the person.
- **Its start (reviewer-codex).** `m.now` is zero until the first poll (`applySnapshot` sets it),
  and so is a new `lastInput`. A zero `lastInput` would read as "idle since 1970" and enter the
  view at the first tick. So the first poll sets `lastInput = at` while it is zero, and the entering rule below also requires `!lastInput.IsZero()`. A busmon started and left
  alone counts its delay from its first poll.
- **Entering.** On each poll (once a second), busmon enters the idle view when all of these
  hold:
  - `idle_view_after > 0`;
  - `m.now - lastInput >= idle_view_after`;
  - `!m.lastInput.IsZero()` (above);
  - `!m.busyForModal()` (`tea_dialogs.go`), the predicate the reminder and the automatic recap
    already wait on. It covers the compose window, the reader stack, line input, a typed message
    draft (`msgDraft`), the ask chooser, the pause popup, the reminder, the summary, the tour and
    the walk between two pages (foureyes). A draft, a question being answered or the tour is
    never hidden. The idle view waits until they are closed.
  - The tier is not `TierTooSmall`.
- **The mouse.** busmon runs `MouseModeCellMotion` today, which reports motion only with a
  button held. In the idle view, `View()` sets `MouseModeAllMotion` and `ReportFocus = true`,
  so a bare movement, or coming back to the terminal window, arrives as a message. Back on the
  dashboard, the mode returns to `CellMotion`. This avoids a flood of motion messages while the
  person works (§8 Q4: bare motion does not count on the dashboard).
- **Focus reports (foureyes).** A terminal reports the current focus as soon as the reports are
  turned on (Ghostty sends it at once), so the view's first frame brings a `tea.FocusMsg`. While
  the view is shown, a focus wakes it only after a `tea.BlurMsg` seen during that showing: the
  person left the window and came back. A blur alone does nothing, and each entry starts with
  no blur seen.
- **Leaving.** In the idle view, the first input message:
  1. sets `idle = false` and `lastInput = m.now`;
  2. **returns without dispatching it.** `q`, `enter`, a digit, a click on a panel, a wheel turn:
     none of them acts. The person sees the dashboard as they left it, and the next input acts.
  - A paste is dropped too: there is no field open to receive it, since the view never opens
    over one.
  - The release that follows a waking click (`tea.MouseReleaseMsg`) then reaches the dashboard.
    `Update` has no case for it, so it does nothing. It is not counted as input either.
- **Instantly.** The dashboard is drawn from the model, which kept polling under the idle view,
  so the first frame after waking is current. No fade or slide plays on the way back.
- **A modal that is due while the view is shown (foureyes).** `busyForModal()` also returns true
  while the idle view is shown. So the waiting reminder (`maybeReminder`), the automatic recap
  (`recapPending`) and the automatic tour wait, as they already wait for a draft. They open as soon
  as the view is left, on the dashboard, where the next key reaches them. No modal
  ever opens hidden under the view. The pause popup and the ask chooser open only on a key, and
  that key would wake the view first. The bell is not a modal, and it still rings (§4).

## 4. What the frames show (existing data only): layout A

Every frame is drawn from the snapshot busmon already polls each second (`m.snap`, `m.forYou`).
No new read, and no new key in Redis. The three frames show together; nothing takes turns.

| Frame | Shows | Source |
|---|---|---|
| **you** | the count of decisions and merges waiting for the person, large (5-row block digits), the header chips' number; under it `♙ 2 decide   ◆ 1 merge` (a part at 0 left out). At 0: `○` and "nothing needs you". | `stripOf(m.forYou)`, as the header computes it |
| **team** | one row per agent not idle (waiting, blocked, working, and the resting states that need a look: quota, paused): its state glyph, name, state word and status message; then `○ N idle` for the rest (idle, answered, done, none). Rows that do not fit end in `+N more`. Dormant agents are left out, as on the dashboard. | `m.snap.agents`, `stateLook`, `visibleAgents` |
| **next** | the plan's task in hand (in progress or in review, else waiting for the person or blocked), its owner, then `then: <first upcoming task>`. No plan: "no plan loaded"; all done: "every task is done". | `m.snap.planState` |

- **The strip** (the bottom row): the tokens part of the `U` page's title (the fullest window of a
  provider not held, `anthropic 42% 5h`, and each held provider with its time left,
  `openai held 1h20m`); then the plan's name and progress (`plan v0.29 6/11 55%`), left out with no
  plan; joined by ` · `, and the clock (`HH:MM`) on the right.
- **The thread** (idle-animation, the person's animation A): the row above the strip is a dotted
  line across the whole screen width, with a 3-cell thread sliding along it, one cell to the right
  per second:

  ```
  ·································━━━·······························································
  anthropic 62% 5h · xai held 1h20m · plan v0.29 6/11 55%                                  16:04
  ```

  The thread enters at the left edge, leaves at the right one, and enters again: over one pass
  (the width plus 3 seconds) every cell of the row carries it. The dots are `RoleDim`, the thread
  `RoleAccent`. Its step is the whole seconds since the view was entered, read from `m.now`, so it
  moves with the 1 s poll and needs no clock of its own.
- **A decision while idle** (§8 Q1, the person: stay): the view stays, the count changes at the
  next poll, and the bell rings as on the dashboard.

## 5. Sizes

- **Side by side** when it fits: you on the left, as tall as the right column, team over next on
  the right (the mock-up's 120x32).
- **Stacked** otherwise: you, team, next, one width, at most the screen's (the mock-up's 80x24);
  team rows that do not fit end in `+N more`.
- **One line per frame** when even that does not fit: `2 need you  ♙ 2 decide`, the state counts
  (`3 waiting · 1 working · 4 idle`, the last ones dropped first, never the idle count), and
  `next: <task>`. The strip drops the clock, then the plan's name.
- **The thread row** is kept at every size the view is shown at; the frames' free area ends above
  it.
- **TooSmall**: the idle view is not entered. The dashboard's own "too small" screen stays.

## 6. Burn-in

- **Drift.** The frame's block moves by one cell every 60 s along a slow path inside the free
  area: a diagonal walk from the middle that turns back at each edge, seeded per session. The strip shifts
  sideways in the same way, by up to 4 cells. Over an hour, no cell keeps the same glyph lit.
- **Dim roles.** Frames are `RoleDim`, text `RoleMuted` or `RoleDim`, except the count, which is
  `RoleAccent`, and each team row's one-cell state glyph, which keeps its state's colour. There are
  no reverse-video pills and no bars at full intensity.
- **The thread** (§4) is the one motion at the poll's pace: a step a second, one cell at a time,
  never a blink (the thread is always drawn; only its place changes), and it crosses the whole
  width, so it marks no cell for long. The dots under it are the view's one static row; they are
  drawn dim.
- **No fast animation.** The drift (60 s) and the 1 s poll are the only clocks. The waiting fade (`fadeMsg`) is not scheduled while the view is shown, which also saves
  CPU on an unattended screen.

## 7. NO_COLOR and 16 colours

The frames carry their meaning in words and digits, never in colour alone. Under `NO_COLOR`, the
roles fall back to the terminal's colour. The count is bold, and the states are words, so they
still read. The block digits are made of `█` and space, which need no colour. The thread keeps
moving under `NO_COLOR`: `━` on `·` reads without colour, and the burn-in reason holds without it;
no colour is never a reason to blink it or to stop it. With 16 colours,
the palette's accent and grey apply as elsewhere.

## 8. The person's answers

1. **A new item while idle:** stay, and update the count (the bell already calls).
2. **Layout:** A, the number (three frames: you, team, next). The one-sentence layout (C) may come
   later as a setting; it is not built.
3. **The delay:** 10 minutes once turned on; off (0) by default.
4. **Bare mouse motion on the dashboard:** not counted (the recommendation): keys, clicks, wheel
   and focus reset the delay; motion is reported only while the view is shown, and wakes it.
5. **Leaving the terminal window** (`tea.BlurMsg`): no answer was asked for; the view comes only
   after the delay, and a blur never brings it on. While the view is shown, a blur makes the next
   focus wake it (§3, focus reports).

## 9. Tests (for the implementation)

- The setting: registry entry; `fileIdleView` takes 0, 1m and 24h and refuses 30s, 25h and
  "soon"; `envIdleView` uses the same values and skips the others (30s gives the default, off);
  the page label.
- With a fake clock:
  - a busmon started and left alone does not enter before the delay counted from its first
    poll, and enters at it (reviewer-codex);
  - entering happens after the delay, and not before;
  - an input message resets the delay, and `tickMsg`, `tailMsg` or a resize does not;
  - the view is not entered while `busyForModal()` holds: one case per member (compose, reader
    stack, line input, a typed draft, the ask chooser, the pause popup, the reminder, the summary,
    the tour, the walk), nor at TooSmall;
  - a reminder due while the view is shown does not open; it opens on the first tick after the
    view is left. The same holds for a pending recap.
- Leaving: `q`, `enter`, a click, a wheel turn, a motion, a paste and a focus after a blur each
  wake the view and do nothing else. The terminal's initial focus report keeps the view, and so
  does a blur; a blur from an earlier showing does not carry over. The model is unchanged except `idle` and `lastInput`, and busmon does not
  quit on `q`.
- `View()` sets `MouseModeAllMotion` and `ReportFocus` only while the view is shown.
- Layout A: the three frames and the strip side by side (140x40), stacked (80x24, 60x20, with
  `+N more`), one line each (40x12, the idle count kept); "nothing needs you" with no digit.
- Goldens: the idle view at 60x20, 80x24, 140x40 and 140x60, dark and light, and 140x40 NO_COLOR,
  with the fixture's fixed seed.
- The thread: for a width, each step draws the row w cells wide with `━` on the cells it covers
  (3, fewer at the edges) and `·` elsewhere, `RoleAccent` and `RoleDim`; one step moves it one cell
  right; over a pass every cell carries it. On screen, it stays put between two polls in the same
  second and moves one cell at the next second, at 140x40, 40x12 and under `NO_COLOR`.
- Drift: over two hours the walk stays in the free area, moves at most one cell a step, and
  visits many places; on screen, the frame stays put within a minute and moves after it.
