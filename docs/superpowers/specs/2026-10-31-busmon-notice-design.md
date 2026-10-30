# busmon notice: a quiet line for what happened by itself (design, step 1: mock-up)

Status: mock-up for review; no code yet.
Task: busmon-notice (master's directive of 2026-10-31, from Bernard's spec): a discreet line at
the bottom of busmon whose text fades out and disappears, shown for the automatic actions the
person did not ask for and might otherwise miss.

## What it is

One reusable component, `notice(text, level)`, with three levels:

| Level | Glyph | Colour (role) | For |
|---|---|---|---|
| info | `·` | `RoleMuted` | something started or changed on its own (a pause, a reload begins) |
| done | `✓` | `RoleOK` | something finished well (a reload proven, a health check back to ok) |
| attention | `▲` | `RoleAttention` | something to look at, not an emergency (health warns, a reload failed) |

Rules, all of them from the spec:

1. **One at a time.** A queue, shown in arrival order, never two lines at once. While others
   wait, each shows 2 s instead of 4 s. The queue holds 5; past that the rest are collapsed into
   one last line: `· 3 more automatic actions (R: recap)`.
2. **No bell, no sound, no unread mark, no count in the header.** A notice is never a decision.
3. **Never for asks, prompts or messages to the person**: those keep their circuit (For you,
   the `●` marks, the bell). Never for what the person did themselves (`human=yes`).
4. **Only what happens while busmon runs.** Nothing is read at start for what happened while
   away: the backfill and the first poll only set the baseline. The recap (`R`) remains the
   place for "what happened while I was away".
5. **Fed by data busmon already reads**: no new stream, no new key, no new daemon (sources below).
6. **View stays pure**: the notice, its queue and its start time live in the Model, set in Update
   (`tailMsg`, `pollMsg`); its fade rides the `fadeMsg` schedule; View draws `m.now` against it.

## Where it goes (decision for Bernard)

**A — right end of the footer (recommended).** The footer keeps its hints; the notice takes the
right end of the same row, and the hints give way from the right as they already do for lack of
room. No row is lost, and the hints the person reads most (left) stay.

```
 tab focus   1-4 tabs   ↑↓ j k select   enter detail   ! answer   / filter       · dispatch: project paused until 16:56
```

**B — the whole footer row, like the flash.** The hints disappear for 4 s. Simplest, but it
hides the keys exactly when something happened.

```
 · dispatch: project paused until 16:56 (lunch)
```

**C — its own row above the footer.** Never covers anything, but one row is lost on every
screen for a line that is empty most of the time.

The flash (`m.ui.flash`, the answer to the person's own key) always wins: a notice waits while a
flash shows. Under a modal (the tour, a dialog, the compose window) the notice waits too.

## How it fades

- **True colour, `fade = true`:** shown at full colour for 3 s, then its text blends towards
  the background over 1 s (≈10 frames), then the row is the hints again. The frames ride the
  existing `fadeMsg` schedule, only while a notice is on screen.
- **16 colours, no colour, `fade = false`:** shown as is for 4 s, then dropped at once. No
  animation, nothing faked with dim attributes.

Frames, true colour (the notice at the right end, A):

```
t=0.0  … ! answer   / filter       ✓ coder reloaded (claude 2.1.314)      full colour
t=3.3  … ! answer   / filter       ✓ coder reloaded (claude 2.1.314)      blending
t=3.7  … ! answer   / filter       ✓ coder reloaded (claude 2.1.314)      almost background
t=4.0  … ! answer   / filter   i write   F1 help   F5 health              gone, hints back
```

## Sources: what shows, from what

Each line is detected from data busmon already reads: the live tail (events after start only),
or a transition between two polls (never against the first poll).

| Event | From (already read) | Detected as | Line | Level |
|---|---|---|---|---|
| project paused | `{p}:pause`, polled | absent → present | `dispatch: project paused until 16:56 (lunch)` | info |
| project resumed | `{p}:pause`, polled | present → absent | `project resumed` | done |
| quota pause | `{p}:pause` with the dispatcher's quota reason | absent → present | `dispatch: paused for claude's quota until 15:44` | attention |
| reload begins | `{p}:swap:*` intents, polled (`SwapIntents`) | a `reload` intent appears | `sentinel reloads coder (client update)` | info |
| reload ends | intent gone, the agent's identity a new pid | intent present → absent | `✓ coder reloaded` | done |
| swap to a fallback | intents, then the agent's `fallback` flag | intent gone, fallback set | `coder now runs on kimi-alt (quota)` | info |
| auto `/clear` | the tail: dispatch's resume directive (`context cleared: …`) | live cmd from `dispatch` | `coder: context cleared after its hand-off` | info |
| health worsens | `{p}:health`, polled | ok → warn, or any → fail | `▲ health: redis-version warns (behind in 8.2)` | attention |
| health recovers | `{p}:health`, polled | warn/fail → ok | `✓ health: dispatch ok again` | done |
| sentinel restarts a service | `{p}:health`'s service check, polled | fail → ok after sentinel's note | `✓ sentinel restarted missions` | done |

Not notices: an agent's ask or waiting state, a pane's prompt, an escalation to master, a
`for=person` notify (they are For you items), anything `human=yes`.

## Open points for the review

1. Placement A, B or C (Bernard).
2. A failed reload: the intent's end alone cannot tell success from failure; the reload
   ledger (`{p}:swaps`) can, and busmon does not read it today. Reading its newest entries
   each poll is one more read of an existing key, within rule 5. Proposed: yes, bounded to the
   entries newer than busmon's start.
3. Durations: 4 s alone, 2 s while others wait, 1 s of fade. Short enough to stay discreet, long
   enough to read one line.
