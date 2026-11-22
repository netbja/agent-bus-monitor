- **An item's page ends with its Context (triage-dedupe step 3, spec §5).** The page of a For you
  item (a merge, an ask, a gate's thread, a waiting agent, a `--needs` report) lists, under its own
  fields, the messages attached to it: a report, directive or notice naming it (#<n>, PR <n>, its
  branch, the ask's id), a
  waiting agent's newest report, or a reply on its thread (its ref is the ask, the gate's challenge
  or the report). Newest first, at most 3, each "<n>  <sender>  <age>  <headline>"; a `--needs`
  report folded into the item comes first and says "needs your answer" or "for you to read"; more
  are counted ("n older in Activity"). `1`-`3` open one in full, and a click does; on an ask's page
  the digits choose an option, so only a click opens them. A page with nothing attached has no
  section. An agent page that is no waiting item's shows none. A page opened from its item keeps it, so
  a `--needs read` report read by opening it still shows its replies.
