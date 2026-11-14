- **busmon's triage walk is For you's live order minus `visited`** (busmon-triage, same spec §4,
  `triage.go`): `walkNext` opens the first `forYou` row not visited (never `merge:unknown`) through
  `openForYouItem`, the function Enter uses too (an unread line straight to its message page). The
  walk's page is the reader's bottom page (`walk.serial`); `walkCheck`, run by `pop` and
  `closeReader`, ends the walk once that page has left the stack, except while `walk.moving`
  replaces it. The terminal acts call `walkDone`: `gotAnswer` with no question left, and `gotSend`
  (now returning a command) for a send to the waiting agent (a plan task's act went with its row). `n`/`p`
  on the walk's page go to `walkStep`, never `readerStep`; on an ask page they are bound only for
  that (`shellAskTable`). `busyForModal` counts `m.ui.walk`, so the reminder and the recap wait.
  When `refresh` drops the walk's ask page (answered or closed elsewhere) it sets `walk.lost`, and
  `afterUpdate` (`walkLost`) moves the walk on, since `refresh` cannot return a command.
