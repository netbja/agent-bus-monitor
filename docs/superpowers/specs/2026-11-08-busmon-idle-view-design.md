# busmon's idle view: a calm screen while nobody looks (idle-view)

Status: spec for review (subject `idle-view-spec`), the person's idea. No code on this branch.

## 1. The idea

busmon often stays open on a screen nobody is reading. The dashboard is dense: panels, the feed,
fades. After a delay with no input, the person wants busmon to show a minimal view instead,
like a screensaver:

- 2 or 3 frames that take turns, built around what matters at a glance: **how many things need
  the person**, and **what state the agents are in**;
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
  view at the first tick. So the first poll, in `applySnapshot`, sets `lastInput = at` while it is
  zero, and the entering rule below also requires `!lastInput.IsZero()`. A busmon started and left
  alone counts its delay from its first poll.
- **Entering.** On the existing 1 s `tickMsg`, busmon enters the idle view when all of these
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
  person works (§8 Q4 asks whether bare motion should also count on the dashboard).
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
  (`recapPending`) and the automatic tour wait, as they already wait for a draft. They open on the
  first tick after the view is left, on the dashboard, where the next key reaches them. No modal
  ever opens hidden under the view. The pause popup and the ask chooser open only on a key, and
  that key would wake the view first. The bell is not a modal, and it still rings (§4).

## 4. What the frames show (existing data only)

Every frame is drawn from the snapshot busmon already polls each second (`m.snap`, `m.forYou`,
`m.feed.feed`).
No new read, and no new key in Redis.

| Frame | Shows | Source |
|---|---|---|
| **A: needs you** | the count of For you items, large (3-row block digits on Wide and Medium), "need you" under it, then the first item's one-line title (`Preview`) muted. At 0: "nothing needs you", and nothing else: no source records when For you last had an item (foureyes). | `m.forYou`, the items For you lists, as `forYou(m.snap, m.feed.feed)` computes them at each poll |
| **B: agents** | one row per agent: its glyph or icon (`display.icons`), name, state word (`working`, `idle`, `waiting`, `held`, `dormant`) and the time in that state; then the line `3 working · 2 idle · 1 waiting` | `m.snap.agents`, the same state the agents panel draws |
| **C: last word** (optional, §8 Q2) | the last report's first line, who sent it and when | `m.feed.feed`, the newest report |

- **Turns.** The frames take turns every 20 s, on a `tea.Tick` that runs only while the view is
  shown. While a For you item exists, **frame A stays up** and the others do not show. What
  needs the person is never hidden behind a turn.
- **The strip** (the bottom row, on every frame):
  - **tokens**: what the `U` page's title says, minus "Tokens today". That is the fullest
    window of a provider not held (`anthropic 42% 5h`), and each held provider with its time
    left (`openai held 1h20m`), from `tokensTitle`'s inputs;
  - **the plan**: the plan's name and `planProgress`'s narrow form (`6/11 55%`). With no plan
    loaded, this part is left out;
  - the two parts are joined by ` · `, and the clock (`HH:MM`) is on the right.
- **The bell still rings.** A new ask or wait rings as on the dashboard (`ring`). The idle view
  does not wake on it (§8 Q1), but frame A shows the new count at the next tick.

## 5. Tiers

- **Wide and Medium**: the frame is a centred block of at most 50x9 cells, with the strip on the
  last row. Frame A has block digits.
- **Narrow**: one line per frame, centred. Frame A reads `2 need you · <first title>`. Frame B
  gives the counts line only. The strip is cut with `FitLine`: the clock goes first, then the
  plan's name, so `6/11 55% · anthropic 42%` stays.
- **TooSmall**: the idle view is not entered. The dashboard's own "too small" screen stays.

## 6. Burn-in

- **Drift.** The frame's block moves by one cell every 60 s along a slow path inside the free
  area: a bounded walk that changes direction at each edge, seeded per session. The strip shifts
  sideways in the same way, by up to 4 cells. Over an hour, no cell keeps the same glyph lit.
- **Dim roles.** Everything is `RoleMuted` or `RoleDim`, except the count in frame A, which is
  `RoleText` (and `RoleAccent` while it is above 0). There are no reverse-video pills and no
  bars at full intensity.
- **No fast animation.** The frame turn (20 s), the drift (60 s) and the 1 s poll are the only
  clocks. The waiting fade (`fadeMsg`) is not scheduled while the view is shown, which also saves
  CPU on an unattended screen.

## 7. NO_COLOR and 16 colours

The frames carry their meaning in words and digits, never in colour alone. Under `NO_COLOR`, the
roles fall back to the terminal's colour. The count is bold, and the states are words, so they
still read. The block digits are made of `█` and space, which need no colour. With 16 colours,
the palette's accent and grey apply as elsewhere.

## 8. Questions for the person

1. **A new item while idle:** should the view wake by itself and show the dashboard, or stay and
   only update frame A (and ring the bell)? The recommendation is to stay. The bell already
   calls, and waking on every event would make the view rare on a busy team.
2. **Frame C** (the last report's first line): keep it, or only frames A and B?
3. **The delay** when turned on: the recommendation is 10 minutes. Is 0 (off) the right default?
4. **Bare mouse motion on the dashboard:** count it as activity, which needs
   `MouseModeAllMotion` all the time and one cheap message per movement? Or count only keys,
   clicks, wheel and focus, with motion waking the view only once it is shown? The recommendation
   is the second.
5. **Leaving the terminal window** (`tea.BlurMsg`): should busmon enter the idle view at once
   when the window loses focus, or only after the delay?

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
- Leaving: `q`, `enter`, a click, a wheel turn, a motion and a focus message each wake the view
  and do nothing else. The model is unchanged except `idle` and `lastInput`, and busmon does not
  quit on `q`.
- `View()` sets `MouseModeAllMotion` and `ReportFocus` only while the view is shown.
- Frame A stays up while For you is not empty; otherwise the frames take turns every 20 s.
- Goldens: frames A, B and C, plus the strip, at Wide, Medium and Narrow, dark, light and
  NO_COLOR, with a fixed drift seed.
- Drift: after 60 ticks of 60 s each, the block has moved and stayed inside the frame.
