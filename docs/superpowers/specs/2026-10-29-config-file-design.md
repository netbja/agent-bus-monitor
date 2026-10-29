# One config file, and `agentbus config` to say where each value comes from

Status: design. Plan v0.24, task `config-spec`. Spec only: nothing here is built yet.

## Why

A setting today can live in five places: a flag, an environment variable (about thirty
`AGENT_BUS_*`), `~/.config/agentbus/busmon.toml`, the project's service env file, or a default in
the code. Nothing says which one won. The worst case is real: a value exported in the shell that
ran `agentbus up` is **frozen** into the services' env file (`service.CollectEnv` copies every
`AGENT_BUS_*`), so the dispatcher keeps it after the person unsets it in the shell, and nothing
shows that.

The goal is small: **one file** for the person's own preferences, and **`agentbus config`**, which
prints each setting's effective value and its **source**. No new behaviour beyond that; small,
fast, nothing to learn for someone who never opens the file.

## Inventory: what exists today

| Where | What | Read by |
|---|---|---|
| Flags | busmon `--theme`, `--limit`, `--as`; agentbus per command (`--project`, `--host`, `up --escalate-to`, `up --reload`, …) | the process given them |
| `~/.config/agentbus/busmon.toml` | `[waiting] popup_after snooze bell fade`, `[recap] auto_after`, `[display] theme limit`, `[identity] as` | busmon; `agentbus identity` (writes `[identity]`) |
| `~/.config/agentbus/quota.toml` | `[providers.<p>]` reader definitions (kind, path, match, windows, argv, every) | the dispatch service |
| `~/.config/agentbus/models.toml` | `[[tier]]` match rules, `[families.<f>] order` | `agentbus models`, the dispatch service |
| `~/.config/agentbus/services/<p>.env` (0600) | written by `agentbus up`: `PATH`, `REDIS_*`, `HERDR_*`, `XDG_*`, Go's, **every `AGENT_BUS_*` of the shell** except project and agent, plus `AGENT_BUS_ESCALATE_TO` and `AGENT_BUS_RELOAD` | the project's services (systemd `EnvironmentFile`, launchd) |
| Tunables (env only) | `AGENT_BUS_CONTEXT_ALERT` (80), `_PROMPT_GRACE` (30 s), `_QUOTA` (`off`), `_QUOTA_WARN`, `_QUOTA_HOLD`, `_QUOTA_MAX_AGE`, `_QUOTA_LONG_HOLD` (2 h), `_TOKENS` (`0` = off), `_AUTO_CLEAR` (off), `_REPORT_MAX` (500), `_REPORT_FULL_MAX` (8000), `_VERDICT_MAX` (10000), `_BUSMON_THEME`, `_BUSMON_LIMIT` (25), `_NO_UPDATE_CHECK`, `_HUMAN`, `_RELOAD`, `_ESCALATE_TO` (`master`), `AGENTBUS_HEALTH_RUNAWAY_*` (4) | each reader, at its own call |
| Connection (env only) | `REDIS_URL`, or `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD`/`REDIS_PASSWORD_FILE` | `bus.Connect` |
| Identity and launch (env only, set by the launcher) | `AGENT_BUS_AGENT`, `_PROJECT`, `_ROLE`, `_PROFILE`, `_CLIENT`, `_PROVIDER`, `_MODEL`, `_MODEL_TIER`, `_REASONING`, `_CLIENT_FP` | the agent's own commands |
| Internal (env only) | `HERDR_*`, `AGENT_LAUNCH_*`, `AGENTBUS_BIN`, `AGENTBUS_REPO`, `AGENTBUS_PROJECTS_DIR`, `AGENTBUS_SERVICE_MANAGER`, `AGENTBUS_SETUP_*`, `AGENTBUS_PLUGIN_*`, `AGENTBUS_TEST_*` | scripts, the installer, the plugin, tests |

## The proposal, minimal

### One file: `config.toml`

`$XDG_CONFIG_HOME/agentbus/config.toml`, else `~/.config/agentbus/config.toml`, beside the files
that are already there. One per machine (per user), not per project: the per-project facts stay
where `up` records them (the env file's escalation target and reload policy).

It holds **only the person's preferences**: busmon's tables, unchanged, plus the dispatcher and
bus tunables above. Nothing else moves in:

```toml
[waiting]        # busmon, as in busmon.toml today
popup_after = "2m"
[display]
theme = "dark"
limit = 25
[identity]
as = "bernard"

[dispatch]       # read by the dispatch service of every project on this machine
context_alert = 80        # AGENT_BUS_CONTEXT_ALERT, 0 = off
prompt_grace = "30s"      # AGENT_BUS_PROMPT_GRACE
quota = "on"              # AGENT_BUS_QUOTA ("off")
quota_warn = 85
quota_hold = 95
quota_max_age = "30m"
quota_long_hold = "2h"
tokens = true             # AGENT_BUS_TOKENS=0 is false
auto_clear = false        # AGENT_BUS_AUTO_CLEAR=1 is true

[bus]
report_max = 500
report_full_max = 8000
verdict_max = 10000

[health.runaway]          # AGENTBUS_HEALTH_RUNAWAY_*
silent_after = "30m"
```

Each key has exactly one environment variable (the existing name) and, where one exists, one flag.
The table of keys is **one Go registry** (`config/keys.go`: file key, env var, flag, default,
type, who reads it), and every reader resolves through it, so `agentbus config` cannot drift from
what the code does.

**Stays in its own file:** `quota.toml` and `models.toml` hold structured definitions (readers,
tier rules), not settings; merging them would make `config.toml` large for a few people's needs.
`agentbus config` lists them with their path and whether they exist. `roles.toml` is a manifest
and stays one.

### Precedence

**flag > environment > `config.toml` > `busmon.toml` (legacy) > default**, per key, everywhere.
This is today's order for busmon (flag, env, file, default); agentbus gains the file step.

**The services.** A project's services do not see the person's shell; they see the env file that
`agentbus up` wrote, then `config.toml`, then the default. So for them the order is **env file >
`config.toml` > default**, and the freeze above stays visible: `agentbus config --project <p>`
shows, for each key, what the services see and why (`env file (frozen at up)`). A change to
`config.toml` reaches a running service at its next start (`agentbus service restart <kind>`);
nothing is reloaded live.

`agentbus up` keeps copying the shell's `AGENT_BUS_*` into the env file (removing that would change
behaviour people rely on), but it now prints which tunables it froze, so the person learns it the
moment it happens.

### No secrets in the file

The file is a preference file a person may share or paste. **Env only, never read from
`config.toml`:** `REDIS_URL` (it can embed a password), `REDIS_PASSWORD`, `REDIS_PASSWORD_FILE`,
and anything a client or provider authenticates with. The registry has no key for them, and the
loader rejects unknown keys, so a `password = …` line is an error that names the line, never a
value that gets used. `agentbus config` shows the connection as facts only: which variable is set
(`REDIS_URL set (env)`, `REDIS_PASSWORD set (env)`), the host and port, never a password or a URL
with credentials.

**Also env only**, because they are a process's identity or plumbing, not a preference: the
identity and launch variables, `HERDR_*`, `AGENT_LAUNCH_*`, `AGENTBUS_*` internals and test
variables. They are not in the registry and not printed by `agentbus config`.

### Errors

A file that does not parse, or a value of the wrong type or out of range, is a **warning, never a
failure**: that key falls back to the next source down, and the warning names the file, the line
and the key (as busmon does today with a bad duration). An unknown key is a warning too, except
under a secret-looking name (`password`, `token`, `secret`, `url` with credentials), which is
refused with its line and never echoed. `agentbus config` lists every warning; other commands print
each one once to stderr. A service never exits over its config.

### `agentbus config`

```
agentbus config [--project <p>] [--json]
```

Read-only; no project needed (above the project-required exit, like `models`). One row per
registry key:

```
key                       value     source                    read by
dispatch.context_alert    70        config.toml               dispatch
dispatch.prompt_grace     30s       default                   dispatch
display.theme             light     env AGENT_BUS_BUSMON_THEME busmon
display.limit             25        busmon.toml (legacy)      busmon
identity.as               bernard   config.toml               busmon, agentbus
bus.report_max            500       default                   every writer

files: config.toml ok · busmon.toml present (legacy: move its keys to config.toml)
       quota.toml absent · models.toml ok
connection: REDIS_HOST localhost, REDIS_PORT 6380, REDIS_PASSWORD set (env)
warnings: none
```

With `--project <p>`, a column **services see** reads the project's env file: the value the
dispatch service will use and its source (`env file (frozen at up)`, `config.toml`, `default`),
and a mark where it differs from what this shell would use. `--json` gives the same rows. Flags
appear as a source only for the command that has them, so `agentbus config` itself reports env,
file and default, and says which flag would override each key.

### Migration

**None needed, and nothing is rewritten.** `busmon.toml` stays read (below `config.toml`), so every
machine keeps working; `agentbus config` names it as legacy and says which keys to move. `agentbus
identity set` writes `[identity]` to `config.toml` from now on (append-only, as today), and still
reads an older `busmon.toml` name. Removing the `busmon.toml` read is a later, separate decision.

## Out of scope

Per-project config files; live reload; a `config set` command (the file is edited by hand); busmon
writing settings; merging `quota.toml`, `models.toml` or `roles.toml`; any secret or the broker
connection in the file; YAML or JSON configs; changing what `up` copies into the env file; new
settings (only existing ones get a file key).

## Tests

- Registry: every key has an env var, a default and a type; every `AGENT_BUS_*` tunable listed in
  the inventory is in it, and an AST test fails when a reader calls `os.Getenv`/`getenv` on a
  registry variable directly (the rule that keeps `config` honest, like the command-table tests).
- Precedence: each step wins over the next (flag, env, `config.toml`, `busmon.toml`, default), per
  key, with fixtures in a temp `XDG_CONFIG_HOME`; for the services, env file over `config.toml`
  over default.
- Errors: an unparsable file, a wrong type, an out-of-range value each fall back and warn with file,
  line and key; an unknown key warns; a secret-looking key is refused and its value never printed.
- `agentbus config`: the rows, the sources, the legacy note, `--project` with a frozen env file
  value, `--json`; it never prints `REDIS_PASSWORD` or a URL's credentials (a fixture with both).
- `up` prints the tunables it froze into the env file.
- Behaviour unchanged: with no `config.toml`, every existing test passes as is.
