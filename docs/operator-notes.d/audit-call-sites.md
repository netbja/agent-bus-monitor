- **The audit trail, step 2: who writes it (audit spec §2; master's answers).** In the dispatcher's
  fenced scripts, in the same step as the act (no lease: neither the act nor the line): `ci-handoff` (the
  owner's hand-off, ok; the loop's stop, refused), `quota-pause` (a new pause or a repause, never the
  passes that refresh it), `merge` (recorded at the merge queue's close-on-merge: agentbus never merges,
  the forge does; the branch, head and base). At the bus, so the CLI and busmon both write them:
  `approve` and `reject` (a verdict; `reject` joined §2's list), `answer` (the person's, human and typed),
  `pause`, `resume` (a pause lifted, a suspend ended), `suspend`, each refused when the state refuses it.
  In the CLI: `push` and `deliver` (`agentbus deliver`; deliver refused when the approvals stop it),
  `retire`, and `force` for `agents forget`, `swap`, `mission load` and `shutdown` with `--force`. Each
  line is written after its act, best effort: one that cannot be written is said on stderr and never
  fails the act. Fixed parts only: a message, a reason or an answer's text never enters the trail.
  Host-level acts (step 2b), the person their actor (identity.as, else `person`): `update` (`agentbus
  update`: installed, rolled back, not confirmed; the target the new tag) and `reinstall` (setup's
  install over an existing install, not in a dry run), each on the bus of every project with a running
  service on this host (`update` also on `--project`'s); `force` for `restore --force` (each project whose
  keys it was asked to replace: replaced, not confirmed, or failed; written after the restore, so the
  restored trail keeps it) and `suspend --force` (each project, the busy agents counted). Not written:
  `agentbus update --rollback` and `--restore`. `retro-apply` comes with the retro build.
