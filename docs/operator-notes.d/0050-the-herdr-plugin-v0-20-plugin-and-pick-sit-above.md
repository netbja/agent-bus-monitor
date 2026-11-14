- **The herdr plugin (v0.20): `plugin` and `--pick` sit above the project-required exit.**
  `agentbus plugin projects|busmon|busmon-pane` and `busmon --pick` need no project (they answer
  "which project?"), so their blocks in `main()` stay above that exit, and `--pick` above
  `--reset`/`--delete` too. The picker (`projlist/`: templates in the projects directory x the
  broker's `bus.Projects`, state present/absent/`?`) writes nothing to Redis and **never
  provisions**: it only runs `agentbus up <p>` for a project the broker confirms; an absent one is
  `agentbus up <p> <path>` by hand. Calls to herdr go through `herdrBin`; the plugin's panes get
  `HERDR_SESSION`/`HERDR_SOCKET_PATH`/`HERDR_BIN_PATH` from herdr and the cwd is the plugin root
  (`./agentbus`, `./busmon`). Decision:
  `docs/memory/decisions/2026-10-19-herdr-plugin.md`.
