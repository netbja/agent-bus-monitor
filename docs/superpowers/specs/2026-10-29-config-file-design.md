# One config file, and `agentbus config` to say where each value comes from

Status: design. Plan v0.24, task `config-spec`. Spec only: nothing here is built yet.

## Why

A setting today can live in five places: a flag, an environment variable (about thirty
`AGENT_BUS_*` and four `AGENTBUS_HEALTH_RUNAWAY_*`), `~/.config/agentbus/busmon.toml`, the
project's service env file, or a default in the code. Nothing says which one won. The worst case
is real: a value exported in the shell that ran `agentbus up` is **frozen** into the services' env
file (`service.CollectEnv` copies every `AGENT_BUS_*`), so the dispatcher keeps it after the person
unsets it in the shell, and nothing shows that.

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
| `~/.config/agentbus/services/<p>.env` (0600) | written by `agentbus up`: `PATH`, `REDIS_*`, `HERDR_*`, `XDG_*`, Go's, **every `AGENT_BUS_*` of the shell** except project and agent (`AGENTBUS_*` is **not** copied: `envPrefixes` is `AGENT_BUS_` only), plus the two per-project facts below | the project's services (systemd `EnvironmentFile`, launchd) |
| Per-project facts (env file) | `AGENT_BUS_ESCALATE_TO` (`up --escalate-to`, default `master`), `AGENT_BUS_RELOAD` (`up --reload`: unset = on, `propose`, `off`; `agentbus reload` reads the caller's env, else the env file) | the services; `agentbus reload` |
| Tunables (env only) | the registry rows below | each reader, at its own call |
| Connection (env only) | `REDIS_URL`, or `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD`/`REDIS_PASSWORD_FILE` | `bus.Connect` |
| Identity and launch (env only, set by the launcher) | `AGENT_BUS_AGENT`, `_PROJECT`, `_ROLE`, `_PROFILE`, `_CLIENT`, `_PROVIDER`, `_MODEL`, `_MODEL_TIER`, `_REASONING`, `_CLIENT_FP` | the agent's own commands |
| Internal (env only) | `HERDR_*`, `AGENT_LAUNCH_*`, `AGENTBUS_BIN`, `AGENTBUS_REPO`, `AGENTBUS_PROJECTS_DIR`, `AGENTBUS_SERVICE_MANAGER`, `AGENTBUS_SETUP_*`, `AGENTBUS_PLUGIN_*`, `AGENTBUS_TEST_*` | scripts, the installer, the plugin, tests |

## The proposal, minimal

### One file: `config.toml`

`$XDG_CONFIG_HOME/agentbus/config.toml`, else `~/.config/agentbus/config.toml`, beside the files
that are already there. One per machine (per user), not per project.

It holds **only the person's preferences**: busmon's tables, unchanged, plus the tunables below.
Nothing else moves in. **The per-project facts get no key**: `AGENT_BUS_ESCALATE_TO` and
`AGENT_BUS_RELOAD` are what `agentbus up` records for one project on purpose, and a machine-wide
default would silently apply to every project the person brings up; they stay in the env file,
`agentbus reload` keeps its rule (caller's env, else the env file, no file step), and `agentbus
config --project` shows them as facts of that project.

### The registry

One Go table, `config/keys.go`, is the only place a tunable's env name, file key, default and rule
are written; every reader resolves through it. **Each variable keeps its exact env rule** (what the
code accepts today, byte for byte: `AGENT_BUS_AUTO_CLEAR` is on only for `1`, `TOKENS` off only for
`0`, `QUOTA` off only for `off`, an invalid runaway value makes the check unknown); only the
**file** value is typed.

| File key | Env | Flag | Type, default | Env rule today | Read by |
|---|---|---|---|---|---|
| `waiting.popup_after` | — | — | duration, 5m | (file only) | busmon |
| `waiting.snooze` | — | — | duration, 15m | (file only) | busmon |
| `waiting.bell` | — | — | bool, true | (file only) | busmon |
| `waiting.fade` | — | — | bool, true | (file only) | busmon |
| `recap.auto_after` | — | — | duration, 8h | (file only) | busmon |
| `display.theme` | `AGENT_BUS_BUSMON_THEME` | busmon `--theme` | `auto`/`dark`/`light`, auto | one of the three; anything else stops busmon with an error | busmon |
| `display.limit` | `AGENT_BUS_BUSMON_LIMIT` | busmon `--limit` | int, 25; 0 or negative = all retained history | any integer after trimming spaces (0 or negative = all history, so `-1` is valid); a non-numeric value is ignored and the next source down applies | busmon |
| `identity.as` | `AGENT_BUS_HUMAN` | busmon `--as` | name, `pilot` in busmon | a valid name; anything else stops busmon with an error | busmon, `agentbus identity` |
| `updates.check` | `AGENT_BUS_NO_UPDATE_CHECK` | — | bool, true | `1` turns it off | busmon |
| `dispatch.context_alert` | `AGENT_BUS_CONTEXT_ALERT` | — | percent 0–100, 80; 0 = off | a number in 0–100, else 80 | dispatch service, busmon, health |
| `dispatch.prompt_grace` | `AGENT_BUS_PROMPT_GRACE` | — | seconds 0–3600, 30; 0 = off | an integer in 0–3600, else 30 | dispatch service |
| `dispatch.quota` | `AGENT_BUS_QUOTA` | — | bool, true | `off` turns it off; anything else on | dispatch service |
| `dispatch.quota_warn` | `AGENT_BUS_QUOTA_WARN` | — | percent, 85 | a number, else 85 | dispatch service |
| `dispatch.quota_hold` | `AGENT_BUS_QUOTA_HOLD` | — | percent, 95 | a number, else 95 | dispatch service |
| `dispatch.quota_max_age` | `AGENT_BUS_QUOTA_MAX_AGE` | — | duration, 30m | a Go duration, else 30m | dispatch service |
| `dispatch.quota_long_hold` | `AGENT_BUS_QUOTA_LONG_HOLD` | — | duration, 2h; `0` = off | `0` off, a positive duration, else 2h | dispatch service |
| `dispatch.tokens` | `AGENT_BUS_TOKENS` | — | bool, true | `0` turns it off | dispatch service |
| `dispatch.auto_clear` | `AGENT_BUS_AUTO_CLEAR` | — | bool, false | `1` turns it on | dispatch service |
| `bus.report_max` | `AGENT_BUS_REPORT_MAX` | — | int > 0, 500 | a positive integer, else 500 | each writer, per call |
| `bus.report_full_max` | `AGENT_BUS_REPORT_FULL_MAX` | — | int > 0, 8000 | a positive integer, else 8000 | each writer, per call |
| `bus.verdict_max` | `AGENT_BUS_VERDICT_MAX` | — | int > 0, 10000 | a positive integer, else 10000 | each writer, per call |
| `health.runaway.silent_after` | `AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER` | — | duration, 30m | a positive duration; invalid makes the check unknown | dispatch service (health) |
| `health.runaway.context_growth` | `AGENTBUS_HEALTH_RUNAWAY_CONTEXT_GROWTH` | — | tokens, 100000 | a positive integer; invalid makes the check unknown | dispatch service (health) |
| `health.runaway.growth_span` | `AGENTBUS_HEALTH_RUNAWAY_GROWTH_SPAN` | — | duration, 10m | a positive duration; invalid makes the check unknown | dispatch service (health) |
| `health.runaway.screen_repeats` | `AGENTBUS_HEALTH_RUNAWAY_SCREEN_REPEATS` | — | int, 4 | a positive integer; invalid makes the check unknown | dispatch service (health) |

`bus.*` caps are applied by **each writer** at its own `XADD`, so they are per writer, not per
bus: two machines on one broker with different values cap the same stream differently. That is
true with env today; the file does not change it, and `agentbus config` says "per writer".

**Stays in its own file:** `quota.toml` and `models.toml` hold structured definitions (readers,
tier rules), not settings; merging them would make `config.toml` large for a few people's needs.
`agentbus config` lists them with their path and whether they exist. `roles.toml` is a manifest
and stays one.

### Precedence

**flag > environment > `config.toml` > `busmon.toml` (legacy) > default**, **per key**,
everywhere. A machine with both files therefore mixes them key by key: a key absent from
`config.toml` still comes from `busmon.toml`, and `agentbus config` names the file of each.

**The services** do not see the person's shell; they see the env file `agentbus up` wrote, then
`config.toml`, then the default:

- For `AGENT_BUS_*` keys: **env file > `config.toml` > default**. A shell value frozen into the env
  file wins over the file: `agentbus config --project <p>` shows it (`env file (frozen at up)`),
  and `agentbus up` now prints which tunables it froze, so the person learns it when it happens.
- For `AGENTBUS_HEALTH_RUNAWAY_*`: `CollectEnv` never copies them, so a shell export has never
  reached the dispatch service. For the services, **`config.toml` > default**: the file is the
  first source that works for them, and `agentbus config --project` says so.
- The services find the **same** `config.toml` because `XDG_CONFIG_HOME` is one of the variables
  `CollectEnv` copies (`service/env.go`, `envKeys`): load-bearing, so tested.

A change to `config.toml` reaches a running service at its next start (`agentbus service restart
<kind>`); nothing is reloaded live. `agentbus up` keeps copying the shell's `AGENT_BUS_*` into the
env file (removing that would change behaviour people rely on).

### No secrets in the file

The file is a preference file a person may share or paste. **Env only, never read from
`config.toml`:** `REDIS_URL` (it can embed a password), `REDIS_PASSWORD`, `REDIS_PASSWORD_FILE`,
and anything a client or provider authenticates with. The registry has no key for them.

`agentbus config` shows the connection as facts only: which variable is set (`REDIS_URL set
(env)`, `REDIS_PASSWORD set (env)`), the host and port, never a password nor a URL. The same rule
holds in the `--project` column: the env file (0600) holds `REDIS_URL`/`REDIS_PASSWORD`, and
`agentbus config` prints from it only registry keys and the two per-project facts, never any other
line of it.

### Errors and unknown keys

A file that does not parse, or a registry key whose value has the wrong type or is out of range,
is a **warning, never a failure**: that key falls back to the next source down (an invalid **env**
value keeps its exact rule above, including the runaway check reading unknown), and the warning
names the file, the line and the key, as busmon does today with a bad duration. One deliberate
change: an invalid `theme` or `as` **in a file** used to stop busmon like an invalid flag or env
value does; through the registry it now warns and falls back, while the flag and env keep stopping
it.

**An unknown key is a warning, and its value is never printed** (only its name and line), so a
pasted secret is never echoed whatever its name. Among unknown keys only, one whose **whole name**
(the last dotted part, lower case) is `password`, `passwd`, `secret`, `token`, `api_key`, `apikey`,
`credential`, `credentials` or `url` is called out as a secret that does not belong in the file
(still not echoed). A registry key is never refused, so `dispatch.tokens` is a setting, never a
secret. `agentbus config` lists every warning; other commands print each one once to stderr. A
service never exits over its config.

### `agentbus config`

```
agentbus config [--project <p>] [--json]
```

Read-only, above the project-required exit like `models`: it needs no project and **no broker**;
`--project` only reads that project's env file, locally. One row per registry key:

```
key                       value     source                     read by
dispatch.context_alert    70        config.toml                dispatch, busmon, health
dispatch.prompt_grace     30        default                    dispatch
display.theme             light     env AGENT_BUS_BUSMON_THEME busmon
display.limit             25        busmon.toml (legacy)       busmon
identity.as               bernard   config.toml                busmon, agentbus identity
bus.report_max            500       default                    each writer (per writer)

files: config.toml ok · busmon.toml present (legacy: 1 key still read from it)
       quota.toml absent · models.toml ok
connection: REDIS_HOST localhost, REDIS_PORT 6380, REDIS_PASSWORD set (env)
warnings: none
```

With `--project <p>`, a column **services see** gives the value the dispatch service will use and
its source (`env file (frozen at up)`, `config.toml`, `default`), marked where it differs from
this shell's, plus the project's two facts (`escalate_to`, `reload`) from the env file. `--json`
gives the same rows. A flag appears as a source only for the command that has it, so `agentbus
config` itself reports env, file and default, and says which flag would override each key.

### Migration

**None needed, and nothing is rewritten.** `busmon.toml` stays read, per key, below
`config.toml`, so every machine keeps working; `agentbus config` names it as legacy and counts the
keys still read from it.

`agentbus identity set <name>` writes `[identity] as` to **`config.toml`** from now on, append-only
as today, with today's refusals applied to `config.toml` (it already names you; it has an
`[identity]` section without `as`; it does not parse). A `busmon.toml` that names the person does
not refuse it: the new name in `config.toml` wins, and the command says so (`recorded <name> in
config.toml; busmon.toml's name <old> is now shadowed`). `agentbus identity` with no argument
prints the effective name and its file. Removing the `busmon.toml` read is a later decision.

## Out of scope

Per-project config files and machine-wide defaults for the per-project facts; live reload; a
`config set` command (the file is edited by hand); busmon writing settings; merging `quota.toml`,
`models.toml` or `roles.toml`; any secret or the broker connection in the file; YAML or JSON
configs; changing what `up` copies into the env file (`AGENTBUS_HEALTH_RUNAWAY_*` included); new
settings (only existing ones get a file key).

## Tests

- Registry: every row has its env name (where one exists), default, type and env rule. **No
  registry env name appears as a string literal outside `config/keys.go`** (a test over the Go
  sources, like `TestEveryFlagLiteralIsInTheTable`): today's readers go through injected `getenv`
  functions and wrappers (`num("AGENT_BUS_QUOTA_WARN")`), which an `os.Getenv` check would miss.
- Env rules unchanged: for each row, today's accepted and rejected env values give today's result
  (`AUTO_CLEAR=true` stays off, `TOKENS=false` stays on, `QUOTA=no` stays on, an invalid runaway
  value makes the check unknown; `AGENT_BUS_BUSMON_LIMIT=-1` replays all history, and
  `AGENT_BUS_BUSMON_LIMIT=abc` with `display.limit = 40` in the file gives 40, without it 25). With no `config.toml`, every existing test passes as is.
- Precedence: each step wins over the next, per key (flag, env, `config.toml`, `busmon.toml`,
  default), and a machine with both files mixes them per key; for the services, env file over
  `config.toml` over default for `AGENT_BUS_*`, `config.toml` over default for the runaway keys.
- The services' path: a `config.toml` under a non-default `XDG_CONFIG_HOME` is found by a service
  whose env file was written by `up` from that shell.
- Errors: an unparsable file, a wrong type, an out-of-range value each fall back and warn with file,
  line and key; an unknown key warns without its value; an unknown `password` or `token` key is
  called out and not echoed; `dispatch.tokens = false` is a setting, never refused.
- `agentbus config`: the rows, the sources, the legacy count, `--project` with a frozen env-file
  value and the two facts, `--json`, no broker dialled; it never prints `REDIS_PASSWORD` nor
  `REDIS_URL`, from the shell **or** from the env file (a fixture with both in each).
- `agentbus identity set`: writes `config.toml`; refused by a `config.toml` that names you; a
  `busmon.toml` name shadowed and said so.
- `up` prints the tunables it froze into the env file.
