- **busmon's Board flags a late or lost tracked request (views-redesign step 3).** One line per task (TASK, OWNER,
  STATE, AGE), the alerts first, oldest alert first, then the open tasks newest first; done tasks are hidden, `d`
  shows or folds them, and the label says `N open · M done (d)`. An ALERT column appears only while a row has one:
  `no acceptance <age>` (requested, not accepted past `board.no_accept_after`, default 30m), `no reply <age>`
  (accepted, no reply past `board.no_reply_after`, default 2h), `body lost` (the request's body no longer in the
  stream, at once); both settings are in Shift+C's Display group, `0` off, applied at once. Enter on a tracked
  request opens its diagnostics (the request's page), on a plain task its task page. The frame's bottom border names
  who acts: `resend (dispatch)`, `cancel/reassign (master)`; busmon changes no request itself. With master's
  terminal gone (or no master entry), an alert row says `master is down; self-repair relaunches it` and
  cancel/reassign leave the footer. Work's `between agents` line still opens the Requests sub-view (its retirement
  waits for the person's decision on the untracked threads, notices, asks and their keys).
