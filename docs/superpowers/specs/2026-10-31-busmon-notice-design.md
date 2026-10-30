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

Each line comes from data busmon already reads: the live tail (events after start only), a
transition between two polls (never against the first poll), or, for reloads and swaps, the
ledger `{p}:swaps`, which records every one of them durably.

| Event | From | Detected as | Line | Level |
|---|---|---|---|---|
| project paused | `{p}:pause`, every tick | absent → present, `By` not the person | `dispatch: project paused until 16:56 (lunch)` | info |
| quota pause | `{p}:pause`, every tick | absent → present, `By == "dispatch"` and `Reason == bus.QuotaPauseReason(provider)` | `dispatch: paused for claude's quota until 15:44` | attention |
| project resumed | the tail: the directive `FencedResume` writes from `dispatch` ("Project resumed: the pause …") | live cmd from `dispatch` with that prefix | `project resumed` | done |
| reload begins | `{p}:swaps` ledger, `begin` with `kind=reload` | a new entry, `By` not the person | `sentinel reloads coder: client update` | info |
| reload ends | ledger `reloaded` / `failed` (kind reload) | a new entry | `✓ coder reloaded` / `▲ coder: reload failed (agentbus swaps)` | done / attention |
| swap to a fallback | ledger `done` | a new entry, `By` not the person | `coder now runs on kimi-alt: quota` | info |
| swap back | ledger `back` | a new entry, `By` not the person | `✓ coder back on its own profile` | done |
| auto `/clear` | the tail: dispatch's directive "context cleared: …" | live cmd from `dispatch` | `coder: context cleared after its hand-off` | info |
| health worsens | `{p}:health`, every tick | ok → warn, or any → fail | `▲ health: redis-version warns (behind in 8.2)` | attention |
| health recovers | `{p}:health`, every tick | warn/fail → ok | `✓ health: dispatch ok again` | done |
| sentinel restarts a service | `{p}:health` service check plus sentinel's note (`{p}:health:notes`), every tick | fail → ok with a newer sentinel note | `✓ sentinel restarted missions` | done |

The text after a colon (`client update`, `quota`) is the ledger entry's `Reason`; with none, the
line ends at the name.

**Never the person's own act (rule 3), by its author field.** A pause, a reload (`agentbus reload
--as <person>`) and a swap can be the person's. A notice shows only when the author (`PauseRecord.By`,
`SwapEvent.By`) is `dispatch` or an agent registered in `{p}:agents`; any other author, the
person's name included, shows nothing. An early `agentbus resume` by the person (`Bus.Resume`)
does write the same resume directive, but from the person, so the `from == dispatch` test leaves it
out. A pause with no valid notify target gets no directive at all, so its end shows nothing.

**Cadence, honestly.** busmon reads the pause and the health every tick (1 s), and the swap
intents and fallbacks only every fifth tick (`wakePollDue`, 5 s). The notices therefore read the
ledger, not the intents: one bounded read of its newest entries every fifth tick (`SwapLog`, the
entries after the last id seen; the first read only records that id). `SwapLog` returns them newest
first: they are reversed before they join the queue, so the queue stays in arrival order. Every
reload and swap still retained in the ledger is seen, its line up to 5 s late. The ledger is capped
(`MAXLEN ~` 1000), so in principle entries could be trimmed between two reads: when the oldest
entry read is newer than the saved cursor (a gap), one line says so instead of pretending,
`▲ some reloads or swaps were not shown (agentbus swaps)`, attention. A pause shorter than one
tick can still be missed; that is accepted (it would end before anyone could read about it).

Not notices: an agent's ask or waiting state, a pane's prompt, an escalation to master, a
`for=person` notify (they are For you items), anything `human=yes`.

## Open points for the review

1. Placement A, B or C (Bernard).
2. Durations: 4 s alone, 2 s while others wait, 1 s of fade. Short enough to stay discreet, long
   enough to read one line.

Settled in review (foureyes, reviewer-codex): the ledger `{p}:swaps` is read for reloads and swaps
(an existing key, one bounded read per fifth tick, within rule 5), which also shows a failed reload;
the person's own pauses, resumes, reloads and swaps are left out by their author field; the
resume is taken from the dispatcher's directive in the tail, not from the record's disappearance.
