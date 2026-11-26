- **`agentbus board drift` (board-drift step 5, spec §5).** A read-only report, one line per finding, run in
  the project's checkout (the merge queue's git readers and its `Delivered` test, the base fetched with no ref
  written): an `accepted` or `review` request whose branch tip or recorded commit is in the base (`merged:`),
  a review with no tip and no commit (`cannot be judged:`), an open request whose body left the stream
  (`body lost:`), a `required` role or a pop owning open requests with no terminal (`no terminal:`, the pop
  with its `pop --resume` line). What could not be read (no git, the holder, no roles.toml) is a `not judged:`
  line, never "no drift". It prints `no drift` and exits 0, else exits 1; `--json` lists the findings. An
  accepted request with no `review_base` whose head is already an ancestor of the base is not reported: a
  branch just made cannot be told from a fast-forward merge. Observers are refused (the fetch writes objects).
  The sentinel's daily review forwards its output to master verbatim, with the health digest; it never closes
  or edits a task.
