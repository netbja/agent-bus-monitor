# Sending while the project is paused — design note

Status: draft (coder, 2026-10-23). Task `pause-popup`, from Bernard.

**Why.** While `{p}:pause` runs, the dispatcher wakes nobody, so a directive, a message or an answer
sent from busmon waits silently until the pause ends.

**When.** A send asks first only when all of these hold:

- it can wake an agent: a directive to an agent or `@all` (line or compose window), or an answer
  (the ask panel's choice or a free answer). A plain-text broadcast wakes nobody and never asks;
- the poll's pause fact says paused, with `until` still in the future. A pause that has ended is
  never asked about: the dispatcher ends it on its next pass.

**What it shows.** A popup on its own top page, above the line, the compose window or the ask
panel: `Project paused until 14:30 (reason, by master).` and three buttons.

| Button | Key | Effect |
|---|---|---|
| Resume now | `r` | the existing `Bus.Resume` (its directive to master included), then the send |
| Send anyway | `s` | today's send |
| Cancel | `c` or `Esc` | nothing is sent; the draft stays where it was |

`←`/`→` and `Tab` move between buttons and `Enter` presses the selected one, which is Cancel by
default. A click on a button presses it. Colours and glyphs come from `theme.go` roles only.

**The same send.** The popup replays exactly the send that asked, through the one send path
(`ui.send`, or `sendAnswer` for an answer). Once the person has chosen to send, the same text to the
same recipient does not ask again (an acknowledgement keyed on recipient and text).

**Edge cases.**

- A failed pause read sends as today, and its flash adds `pause ?`. The `?` convention: a read we
  could not make is never presented as a fact.
- A failed `Resume` sends nothing and says why.
- The popup reads the shared model only; no I/O in draw.
