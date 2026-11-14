- **Templates live in agent-bus's own directory (v0.20).** `rolecfg.ProjectsDir`:
  `AGENTBUS_PROJECTS_DIR`, else `$XDG_CONFIG_HOME/agentbus/projects`, else
  `~/.config/agentbus/projects`; bash reaches it through `agentbus roles projects-dir` (handled
  before the manifest is loaded, so it needs no `roles.toml`). There is deliberately **no migration
  code** and no fallback to herdr-plus's directory: old templates are moved by hand, once.
  `tests/lib.sh` points `AGENTBUS_PROJECTS_DIR` at a temp dir, so a suite that forgets it never
  writes the real `~/.config`. `up` takes the herdr session from `--session`, `HERDR_SESSION`, then
  `HERDR_SOCKET_PATH` (`…/sessions/<name>/herdr.sock`, or `…/herdr/herdr.sock` for `default`), and refuses services when a socket path
  names none.
