- **Losable work is read by the merge-queue pass (cleaning-up-agents step 2, spec §3.1 item 5).** After the queue, each
  pass (every 5 minutes, the dispatcher) judges every agent of `{p}:agents` and every role holding a branch on the
  board: a branch it holds open (a claim's branch, a request's `request.branch`) whose local tip is neither origin's
  tip of that branch, nor in it, nor in the base, is `unpushed <branch>`; a stash whose message names the role as a
  whole name (`On coder/x: wip` names coder, never coder2) is `stash`. Both make an agent without a terminal `lost`.
  The finding is written fenced, replaced whole, to `{p}:losable` (role → branches, stashes, at in Redis TIME;
  runtime, expires 1 h after the last write); readers take an entry under 15 minutes old, else the item reads
  `unknown: losable work`. A failed git or broker read writes nothing, so the last finding ages out. In local mode
  (no origin, or a local-path origin) nothing is pushed, so no branch is unpushed; stashes still count. `git stash
  list` runs with the merge queue's environment and `log.showSignature` off. A stash made by hand in the services'
  repository on a role's branch counts for that role until it is dropped.
