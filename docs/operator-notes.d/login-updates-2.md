- **busmon says when a newer Codex is published (login-updates 4, `cmd/busmon/codexrelease.go`).**
  Beside the release check, under the same switch (`updates.check`, `AGENT_BUS_NO_UPDATE_CHECK=1`):
  the installed version is the oldest `client_fp` label among the project's Codex agents (no Codex
  agent, or no label: no request); the published one is `version` from
  `https://registry.npmjs.org/@openai/codex/latest`, asked at most once a day (cache
  `${XDG_CACHE_HOME:-~/.cache}/agentbus/client-codex.json`, 3 s, silent offline and cached for the
  day), re-judged every hour in the background, never on the UI path. Newer: the header shows
  the whole line, `codex <new> available (installed <old>): npm install -g @openai/codex@latest`,
  after the release item and the first to go when space is short, and About's THIS BUILD a
  `codex` row with the command as its note. Nothing installs it; after the person does, the client
  fingerprint flow reloads the Codex agents. No line for Grok and agy (no version endpoint) or Claude
  Code (it updates itself).
