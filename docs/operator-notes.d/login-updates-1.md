- **An expired Claude Code login is told to the person (login-updates 2.1/2.4, `quota/claudeauth.go`,
  `dispatch/loginnotice.go`).** The dispatch service reads Claude's transcripts (`CLAUDE_CONFIG_DIR`,
  else `~/.claude`; `projects/*/*.jsonl` touched in the last 6 h, 256 KiB tails), at most once a
  minute: the newest entry the client wrote for an auth failure (`isApiErrorMessage`, `error:
  "authentication_failed"`, model `<synthetic>`, all client-set) with no served answer newer in any
  session means expired; a `/login` in any session clears it. One notice for the person (`for=person`,
  mark `login-expired:claude-code`, episode the host's local date of Redis `TIME`): at most one a day
  per project, sent even while paused, naming no agent (the login is the account's). Nothing else
  changes: no hold, no retry, nothing typed; an expired agent still costs one failed turn per wake.
  No credential file is read. Codex, Grok and agy (screen fixtures) and Claude's pre-expiry banner
  are later slices.
