- **The automatic update, step 4a: the quiet test and the check pass (quiet-update-auto §4.3,
  §4.5).** `agentbus update --auto --check` finds the release with a fetch that never prompts
  (`GIT_TERMINAL_PROMPT=0`; `GIT_SSH_COMMAND=ssh -o BatchMode=yes` unless the person set one; two
  minutes at most), says why it would not install it (a `docs/releases/<tag>.migrate` in the tag, or a
  failed automatic install of that tag in the history), and judges each project with a running
  `dispatch` or `missions` or an active holder: **in work** when an agent is `working`, an agent's
  status changed under 10 minutes ago (the broker's `TIME`), or a mission is `checking` or
  `integrating`. A read that fails defers (exit 1): it fails closed, holders included (a holder unit or supervised record it cannot read, through the managers' new `HolderUnitsStrict`), and so do a tag or an update history it cannot read. It records `last_check` and the
  `waiting` tag with its first date in `<updates>/auto.json`, and stages nothing. `--auto` alone is
  the person's act (refused under an agent); until step 4b it installs nothing. `--auto --yes` is
  refused: `updates.auto = install` is its yes.
