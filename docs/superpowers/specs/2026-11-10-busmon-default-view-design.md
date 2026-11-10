# busmon's default view: back to the person's chosen view after a while without input (default-view)

Status: spec, for review (subject `default-view`). Not built. The person asked for it (relayed by
master, 2026-11-10): after X minutes without a key, busmon goes back to a view they chose, or shows
it when the idle view ends. §9 holds the questions left to them, each with a recommendation.

## 1. Today, and the idea

busmon stays where the person left it. Left on History (H) at an entry from yesterday, or on
Tokens (U), it still shows that page an hour later, when the person glances at it from across the
room. Worse, an open page holds the idle view off: `enterIdle` waits on `busyForModal()`, and
`busyForModal()` holds while **any** reader page is open (`m.rd.open()`, `tea_dialogs.go`), so a
busmon left on a page never gets its calm screen.

The idea: the person chooses **a default view** (the dashboard, or one page such as Requests or the
plan) and a delay. After that long without input, busmon closes whatever pages are open and shows
that view, as if they had pressed its key, unless they are in the middle of writing or answering
(§4). Coming back from the idle view, they then find their default view, not yesterday's entry
(§5).

It is the same kind of feature as the idle view, and it shares its clock and its idea of input:
one last-input time, one list of what counts.

## 2. The settings

Two keys in `config/keys.go`, group Display, read by busmon, applied at once like the other Display
keys:

- **`display.default_view`**, kind `String`, default `dashboard`, env `AGENT_BUS_DEFAULT_VIEW`.
  - Its values, in the page's order: `dashboard`, `requests` (F2), `services` (F3), `health` (F5),
    `plan` (p), `tokens` (U), `history` (H), `projects` (M). Each is one page a single key opens
    from the dashboard, and each is read-only until the person acts on it (§9 Q1).
  - Not offered: settings (C), About (A), help (F1), the notifications setup, a reader page of one
    item (an agent, a message, a task, a request), the triage walk. They are places one goes to do
    something, not places to wait.
  - A file value outside the list is refused with "want dashboard, requests, services, health,
    plan, tokens, history or projects"; an env value outside it is skipped (`envSkip`), as
    `display.icons` does: never a broken screen.
- **`display.default_view_after`**, kind `Duration`, default `0` (off), env
  `AGENT_BUS_DEFAULT_VIEW_AFTER`.
  - The idle view's rules, reused as they are: `fileIdleView` for the file and the page (`0`, or a
    Go duration from `1m` to `24h`), `envIdleView` for the env (whole seconds or a duration, `0` or
    `1m`–`24h`, anything else skipped). Below a minute the view would move while the person is
    still reading.
  - Both keys go in the registry only: the env names are not written elsewhere (the suite's rule).

## 3. What counts as input

Exactly what counts for the idle view, `isInput` (`idleview.go`), and the same field,
`m.ui.lastInput`:

- **Counts**: a key press (one that does nothing included), a paste, a click, a wheel turn, and,
  while the idle view is shown, a bare motion and a focus after a blur.
- **Does not count**: a poll (`pollMsg`), a tick, a tail batch, a resize, a fade frame, a blur, a
  mouse release, a notice, the bell, a new message or report, an agent changing state. busmon's own
  activity never resets the delay; only the person does.
- **Bare mouse motion on the dashboard** does not count, as decided for the idle view (§8 Q4 there):
  busmon reports motion there only with a button held (`MouseModeCellMotion`).
- **A busmon started and left alone** counts from its first poll, as the idle view does
  (`lastInput` set at the first `pollMsg`).

## 4. Going back

On each poll (once a second), busmon goes back to the default view when all of these hold:

- `display.default_view_after > 0`;
- `m.now - m.ui.lastInput >= display.default_view_after`, with `lastInput` not zero;
- busmon is not already showing the default view: for `dashboard`, no reader page is open; for a
  page, the reader stack holds that page alone (§4.2);
- it has not gone back already since the last input (`m.ui.wentBack`, cleared by every input): it
  goes back once per absence, never again every second;
- **nothing the person is writing or answering is open**: `busyWriting()`, below.

### 4.1 Never over a draft or an answer: `busyWriting()`

`busyForModal()` is the wrong test here: it holds while any page is open, which is exactly what
the return closes. `busyWriting()` is a new predicate, in `tea_dialogs.go` beside it, holding
when the person has something in hand:

- the compose window (`m.ui.compose`), the line input (`m.ui.line`, the filter's edit included), a
  typed message draft (`m.ui.msgDraft`);
- the ask chooser (`m.ui.askChoose`), and an `ovAsk` page anywhere in the stack (an open ask, the
  person's answers being typed);
- the notifications setup (`ovNotifySetup`) anywhere in the stack: a step may hold a masked field;
- the settings page (`ovSettings`) while a value is being edited, or while a reset's `y/n` is asked;
- a confirmation waiting for its `y`: the pause popup (`m.ui.pausePopup`), the Projects page's
  delete (`m.ui.del`), a `d` removal (`m.ui.confirm`) and a `Z` sleep (`m.ui.sleepAsk`). The last
  two are **not** in `busyForModal()` today; any other `y/n` a page holds (a stop on the Projects
  page, a settings reset) is listed by its field while building;
- the waiting reminder (`m.ui.reminder`), the summary (`m.ui.summary`), the tour (`m.ui.tour`), the
  triage walk (`m.ui.walk`, and its end page): each is a modal the person must close themselves.

The predicate is the list above, one test per member; a member added to `busyForModal()` later
that holds the person's typing must be added here too (a test lists `busyForModal()`'s members
and fails on one this spec has not placed in or out).

While `busyWriting()` holds, nothing happens; the return comes at the first poll after it ends,
if no input came meanwhile (closing a draft is an input, so it starts the delay again).

### 4.2 What going back does

- **Every reader page is closed**, top to bottom, without its own close side effects that would
  write anything (a page that marks entries read on close or on open does not do it here: the
  return reads nothing on the person's behalf; checked per page while building, §8).
- **Then the default view is opened as its key opens it**: `dashboard` opens nothing more;
  `requests` is `m.push(page{kind: ovRequests})`, `plan` is `m.pushPlan()`, `services` is
  `m.servicesCmd(0)`, and so on: the same calls `tea_update.go` makes for F2, p, F3, F5, U, H, M.
  A page that loads (History, Projects, Services) loads as on its key; its error shows as on its
  key.
- **The dashboard as it is**: the focused panel, the lower panel's tab and an applied filter are
  kept (the filter is the person's own setting; its edit, if open, holds the return off, §4.1).
  **The feed goes back to following** (`f` on, scrolled to the newest), since the point is what is
  current (§9 Q4).
- **No animation and no notice**: the next frame shows the view. Nothing is sent, answered, marked
  read or written to `config.toml`.
- **At `TierTooSmall`** it still goes back (the "too small" screen shows over it, as over the
  dashboard), so a larger window later shows the default view.

## 5. With the idle view

Both features read the same `lastInput`; each has its own delay. Three rules make them fit:

1. **The default view does not hold the idle view off.** `enterIdle` keeps `busyForModal()`, but
   a reader stack holding only the default view's page is not "a page open" for it. So a person
   whose default view is Requests gets Requests after the first delay and the idle view over it
   after the second. Any other page still holds the idle view off, as today (§9 Q5).
   **The exception lives in `enterIdle` only, never inside `busyForModal()`** (foureyes): that
   predicate also holds back the waiting reminder (`maybeReminder`), the automatic recap
   (`tea_signals.go`) and the tour (`tour.go`), and they keep waiting while any page is open, the
   default view's included, as today. A test checks that a reminder due while the default view's
   page is shown does not open.
2. **The return can happen under the idle view.** The return ignores `m.ui.idle` (it is in
   `busyForModal()`, not in `busyWriting()`); the model keeps polling under the view, so going
   back is drawn on the first frame after waking. The waking input still does nothing else (idle
   view §3), and it clears `wentBack` like any input.
3. **So the order of the two delays decides what the person sees**, with no third setting:

| Delays | After the first | After the second | Waking from the idle view shows |
|---|---|---|---|
| default view 5m, idle view 10m | the default view | the idle view, over it | the default view |
| default view 10m, idle view 10m | both at the same poll: the return, then the idle view over it | | the default view |
| default view 30m, idle view 10m | the idle view, over what was left (only with no page open, as today) | the return, under the idle view | the default view after 30 min, else what was left |
| default view off, idle view 10m | the idle view, as today | | what was left, as today |

"Shows it when the idle view ends" is the second row: the same delay for both. Whether waking
should also show the default view when its own delay has not passed yet is §9 Q3.

## 6. The settings page (C)

Group **Display**, two lines under "Idle view after":

- **Default view**: a choice, cycled like Theme and Icons (`settingChoices`, the values of §2 in
  that order); the note says "the view busmon goes back to after the delay below: dashboard is the
  panels, the others are their page (F2 requests, p plan, U tokens…)".
- **Back to it after**: a duration with the idle view's rule; the note says "0 = never; from 1m to
  24h; your keys, clicks and the wheel reset it".

Changing either applies at once. The key presses on the page are input, so nothing moves while the
person is there; when they leave page C and wait, the new values apply. The page itself is not a
default view (§2), and it is closed by a return like any page unless a value is being edited
(§4.1).

## 7. What it never does

- Close the compose window, the line, a draft, an ask, the notifications setup, a settings edit, a
  confirmation, the reminder, the summary, the tour or the triage walk (§4.1).
- Act as the person: no send, no answer, no "read" mark, no priority, no write to `config.toml` or
  the bus.
- Go back again and again while the person is away: once per absence (`wentBack`).
- Move while the person types: every key resets the delay, so a slow reader on a page who presses
  no key for the whole delay is the one case it moves under them, which is why it is off by default
  and at least a minute (§9 Q2).
- Read Redis, or anything the poll does not already read.

## 8. Tests (for the implementation)

- The registry: both keys, their defaults, env names only there; `display.default_view` takes each
  of the eight values and refuses `settings`, `about`, `x` in the file, and skips them in the env;
  `display.default_view_after` with the idle view's values (0, 1m, 24h taken; 30s, 25h, "soon"
  refused or skipped).
- With a fake clock, from a page left open (History) and from the dashboard:
  - no return before the delay, the return at it, counted from the first poll for a busmon left
    alone;
  - each input of §3 resets the delay; a poll, a tick, a tail batch, a resize, a release and a
    bare motion on the dashboard do not;
  - once per absence: after a return, the next polls do nothing until an input, then the delay
    runs again;
  - no return while each member of `busyWriting()` holds (one case each), and the return at the
    first poll after it ends with no input meanwhile;
  - a test that lists `busyForModal()`'s members and fails on one `busyWriting()` has not placed.
- What it does, per value: the stack ends empty for `dashboard`, and holding exactly the page its
  key opens for the seven others (the same page kind and the same load command); the dashboard's
  focus, tab and filter kept; the feed following; no read mark, no bus write, no config write
  (a fake bus and config that fail the test on any write).
- With the idle view: the four rows of §5, each with a fake clock: what is shown after each delay
  and on waking; the default view's page does not hold the idle view off, any other page does; the
  waking input does nothing else.
- The settings page: the two lines in Display, the choice cycles in §2's order, the duration
  refuses 30s; a change applies at once.

## 9. Questions for the person

1. **Which views can be chosen.** Recommended: the dashboard and the seven one-key pages of §2
   (requests, services, health, plan, tokens, history, projects). Or fewer: dashboard, requests and
   plan only, the three a person waits on.
2. **The delay once turned on, and the default.** Recommended: off by default, like the idle view,
   and 10 minutes suggested in the page's note when turned on. Or on by default at 30 minutes, back
   to the dashboard.
3. **Waking from the idle view before the default view's own delay** (the third row of §5).
   Recommended: no, one clock per feature; the person who wants it on waking gives both the same
   delay (the second row). Or: waking from an idle view that lasted longer than the default view's
   delay always shows the default view.
4. **The dashboard on return.** Recommended: focus, tab and filter kept; the feed back to following
   the newest. Or the feed left where it was scrolled.
5. **The idle view over the default view's page** (§5 rule 1). Recommended: yes; without it, a
   default view other than the dashboard would never get the idle view. Or the idle view only from
   the dashboard, as today.
6. **busmon's first screen.** Recommended: the default view is also what busmon opens on, so the
   choice means "my busmon starts here and comes back here". Or busmon always opens on the
   dashboard, and the default view only applies after the delay.
7. **The names.** Recommended: "Default view" and "Back to it after" on page C,
   `display.default_view` and `display.default_view_after`. Or "Home view" (`display.home_view`),
   if the person prefers the word.
