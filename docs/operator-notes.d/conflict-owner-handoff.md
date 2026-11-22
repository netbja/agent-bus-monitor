- **A conflicting approved PR goes back to its owner (conflict-owner §4, build B).** After the
  conflict check, each pass hands every conflict git confirmed to its owner, once per episode (the PR,
  or the branch in the git fallback, and its head; a base move under the same head is the same
  episode and sends nothing), with master's copy (wake=no): "Hand-off k of N". `{p}:conflicts` holds
  one field per PR (`pr:<n>`) or branch (`br:<digest>`), `last|count|first|escalated`, written by
  `FencedConflictTrack` under the dispatch lease (`first` from Redis `TIME`); every directive is a
  `FencedCmdOnce` with its mark in `{p}:quota` (`conflict:`, `conflict-copy:`, `conflict-esc:`).
  Past `conflict.max_handoffs` (default 3; 0 sends the first to master) or `conflict.escalate_after`
  (default 2h, from `first`, the owner held or not; 0 no time bound) master gets the escalation,
  woken, and the owner nothing more until the field goes. An owner `blocked` or `waiting` gets
  nothing and is not counted (the age still runs). The field goes, with its marks, when an approved
  head of the PR is clean or the PR is closed or merged (git fallback: the branch gone, or its tip
  in the base, approved or not: a reset or fast-forward resolves it; a read that fails keeps it); a fix
  in review or turned draft keeps it, so three failed fixes reach the cap. Only a
  pass that read everything forgets; a failed one touches nothing. What could not be written joins
  `conflict_check` ("conflicts: …"). The hand-off says rebase, resolve, test and have the new head
  reviewed: it is delivered as usual (only master and docs push here); it names `origin/<base>`, or
  the local `<base>` in local mode.
