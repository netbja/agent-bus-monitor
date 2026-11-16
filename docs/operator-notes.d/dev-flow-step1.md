- **A project may deliver to its own base (dev-flow-step1).** A project template
  (`<projects-dir>/<project>.toml`) may set `[delivery] base = "dev"`: `agentbus deliver` then opens
  the PR into `dev` (`--base` still wins), and the conductor starts a change whose `missions.json`
  names no `base` from it. A project without the line keeps the machine's `delivery.base` (default
  `main`), as before. `agentbus config --project <p>` shows the base and where it comes from. A base
  that is not a branch name stops `deliver` (unless `--base` is given) and the conductor, with the
  template named: nothing is delivered to a base nobody chose. The conductor reads the base once, at
  start: after editing the template, run `agentbus --project <p> service restart missions`. The merge
  queue still records the machine's base until the next step reads the project's there too.
