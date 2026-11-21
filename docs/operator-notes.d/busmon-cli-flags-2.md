- **busmon's flags, the person's corrections (busmon-cli-flags items 1, 4, 5).** `-l`'s MASTER is the
  registered `master` agent's declared state (`bus.ProjectSummary.MasterState`, the agent named `bus.ACLMaster`), `-`
  without one; the pilot lease (`Master`) is no longer shown there. `--url` (`-u`) sets `REDIS_URL` before
  any connection (`applyURL`), so a unix socket or an ACL user reaches every path, and a switched busmon
  inherits it without the URL in its argv (`--url` is a one-shot in `switchArgv`); `--host` beside a URL,
  given or in `REDIS_URL`, exits 2. The picker is `--new` (`-k`); `--pick` still parses, unlisted.
  With `broker.acl` on, busmon still logs in as its own ACL user (`bus.ConnectAsContext`): a URL's
  user and password are replaced, so `--url` names the broker's address there, never another user.
