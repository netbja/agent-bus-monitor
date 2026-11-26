- **The Board's alerts say what they measure (board-drift step 4, spec §7).** "no acceptance 40m since
  request", "no reply 3h since accept", "body lost (request 27h)". "No reply" fires only on an
  `accepted` request: one in `review` waits on its reviewers, a `blocked` one on someone else. The AGE
  column is now CHANGED (the last transition, `Updated`, as before), and a task's detail lists the
  request's own times: Requested, Accepted, Responded, and In review (since its last change, while in
  review). From 100 columns the alert column is 34 wide, so the starting point is never cut. Since
  step 3, "no reply 2h since report" counts from a report on the request (`report --task`) when it is
  newer than the accept, and the detail lists Reported too.
