# The installer in Go: a thin `setup.sh`, then `agentbus setup` — design

Status: draft for review (coder, 2026-10-27). Task `installer-spec`, branch `docs/installer-spec`,
review subject `installer-spec`. Spec only; no code in this change.

## Decision and goal

Bernard, ask `1793110186603-0` ("Go, lot suivant"): `setup.sh` (1077 lines of bash today) becomes
a thin script that checks Go and builds `agentbus`, then hands over to `agentbus setup
install|update|uninstall|status`, written in Go, with **the same outputs** and **the same
end-to-end tests** (`tests/installer_test.sh`, 113 asserts, fake `docker`/`herdr`/`systemctl`/
`launchctl` first on `PATH`).

Why: the bash carries most of the project's portability traps (GNU vs BSD `sed -i`, `readlink -f`,
`stat`, `/dev/tcp` port probes), parses JSON with `awk` (`plugin_rows`), and cannot share code with
`service/`, `rolecfg/`, `models/` and `bus/`, which already do half its work in Go.

## Inventory: what `setup.sh` does today

| Step | Today (bash) | In Go |
|---|---|---|
| Flags | `--bin-dir --dry-run --install-herdr --force (no-op) --no-docker --no-skills --yes/-y --purge -h`; `uninstall --dry-run` refused | same flags, same refusals, same usage text (`agentbus setup --help` prints it, `./setup.sh --help` too) |
| Run from a clone | `go.mod` and `cmd/agentbus` beside it, else fatal | same check, against the repository the thin script passes (`--repo`) |
| Preflight | OS/arch, `git`, `curl` (only for `--install-herdr`), Go ≥ `go.mod`'s version, Docker + compose (`docker compose` or `docker-compose`); a dry run lists every gap, a real run stops after them | Go floor in the thin script (it must build); the rest in Go, same messages, same "every gap in a dry run" rule |
| herdr | on PATH, or downloaded with `--install-herdr` (herdr.dev installer, `curl`) | same, through the runner |
| Build | `go build -ldflags` with `buildinfo.Version` (`git describe` when a `v*` tag, else `dev`), `buildinfo.Repo`; `agentbus` and `busmon` into a temp dir, then `install_file` (copy beside, rename over: atomic) into the repository **and** `BIN_DIR` | same ldflags, computed only in Go; same atomic rename; same two destinations |
| herdr plugin | `herdr plugin link <checkout>` as `netbja.agentbus`; the linked path recorded in `$XDG_STATE_HOME/agentbus/plugin.path`; only that registration is ever unlinked; a disabled one or one from another checkout is reported and left | same, reading `herdr plugin list --json` with `encoding/json` instead of `awk` |
| Key binding | the `prefix+up → netbja.agentbus.projects` block in `~/.config/herdr/config.toml`, idempotent; setup's old herdr-plus block replaced only when byte for byte unedited (`keybind_swap`, exit 3 on an edited block); `herdr server reload-config` when a server runs | same exact-block rule (bytes, never a TOML rewrite, which would reformat the person's file) |
| herdr-plus | reported by `status` when an older setup installed it, never removed | same |
| Services hint | project services still running the previous binary are named, with how to move them on | same, through `service.Manager` directly (no `agentbus service list` subprocess) |
| PATH | `BIN_DIR` on PATH; nothing earlier shadows the new binaries | same |
| External skills | `git clone --branch $SKILLS_REF` of mattpocock/skills into `$XDG_DATA_HOME/agentbus/skills/mattpocock`, with a marker; a folder without the marker is the person's | same, with the marker |
| Role skills | `scripts/link-role-skills.sh` | `rolecfg` skill linking directly (the script already calls `agentbus roles`) |
| Launcher links | `agent-launch`, `agent-spawn` symlinked into `BIN_DIR`; only links to these exact files are ours | same rule |
| Redis | port probe on 127.0.0.1:6380, else `docker compose up -d` (skipped with `--no-docker`) | `net.DialTimeout` probe; compose through the runner |
| AI clients | inventory of installed clients (optional, informational) | same list |
| Identity | asked once on a terminal, written to `busmon.toml [identity] as`; skipped with `--yes`, a dry run, no terminal, `AGENTBUS_SETUP_NO_IDENTITY=1` | same skips |
| Models | `agentbus models --refresh` at the end; `AGENTBUS_SETUP_NO_MODELS=1` skips | `models.Refresh` in process, same skip |
| Summary | next steps, warnings count | same text |
| `status` | repository rev, binaries (version, shadowing), launcher links, skills, plugin registration and its action, key binding, project services, Redis state | same lines |
| `uninstall` | services first (fatal if one cannot be removed: nothing else is touched), binaries, launcher links, role-skill links, key binding (backup, edited block left), plugin (only the recorded one), skills (marker, no local changes), Redis (stop by default, `down -v` only with `--purge` or an explicit `n`→`y`); every step asks, `--yes` takes the cautious default, no terminal and no `--yes` stops | same order, same questions, same defaults |
| `update` | refuses a dirty tree; `git pull --ff-only`; `vA -> vB`, release notes per new tag, changed skills and `agentbus roles skill-users`; then **re-execs the new `setup.sh install`** | see below |

## What stays in bash, and why

- **`setup.sh` itself, ~60 lines**: find the repository, check `git` and Go against `go.mod`
  (the one thing Go cannot check before it is built), `go build -o <tmp>/agentbus ./cmd/agentbus`
  (unstamped: it only runs `setup`), then `exec <tmp>/agentbus setup --repo <dir> "$@"`. A missing
  or old Go prints today's message. `install.sh` stays its alias.
- **`scripts/bootstrap`, `agent-launch`, `agent-spawn`, `scripts/lib/roles.sh`**: they are not the
  installer. `agentbus up` runs bootstrap; the launchers `exec` clients. Porting them is another
  change, not this one.

## `update`: running the new installer, not the old one

Today `update` pulls, then `exec`s the pulled `setup.sh`, because the pull may change the steps.
The Go version keeps that shape exactly: after `git pull --ff-only` and the `vA -> vB` lines, it
`syscall.Exec`s **the repository's `setup.sh install <same flags>`**. The thin script builds a new
`agentbus` from the pulled tree and execs it; the running old binary never performs the install.
`AGENTBUS_SETUP_FROM=<vA>` is passed so nothing is printed twice.

## Safety: tests can never reach the live broker or services

The live broker shares the compose project and container name with every copy of the repository
(`name: agent-bus`, `container_name: agent-bus-redis`); an unstubbed `uninstall --yes` in a test
once stopped it (memory: stub docker in installer tests). Three layers:

1. **One runner.** Every external command (`docker`, `herdr`, `git`, `go`, `systemctl`,
   `launchctl`, `curl`) goes through `setup.Runner`. Go unit tests construct the package with a
   fake runner only: the real runner lives in `cmd/agentbus`, and the package's constructor refuses
   a nil one. No exec exists in a unit test.
2. **The broker belongs to a checkout.** Before `stop` or `down -v`, `uninstall` reads the
   container's label `com.docker.compose.project.working_dir` (`docker inspect`) and acts only if
   it is **this** checkout (resolved paths). The test's copy of the repository is another
   directory, so even an unstubbed run says "the broker was started from <dir>, not this checkout:
   left as it is". `status` reports the same fact.
3. **The end-to-end tests keep their fakes first on `PATH`** (`tests/installer_test.sh`,
   unchanged), and gain one assert: the fake `docker` serves an `inspect` with a foreign
   `working_dir`, and `uninstall --yes` must not call `compose stop`.

Services are only ever touched through `service.Manager` (its tests use fake managers already).

## Linux and macOS

- systemd `--user` vs launchd: already `service/`.
- No `sed -i`, `readlink -f`, `stat` flags or `/dev/tcp`: `os`, `filepath.EvalSymlinks`, `net`.
- `docker compose` vs `docker-compose`: detected once, same order as today.
- herdr config path: `~/.config/herdr/config.toml` on both (as today).
- Terminal detection for `ask`: `golang.org/x/term`-free, `os.Stdin.Stat()` char-device check, as
  `busmon` does for `--reset`; `NO_COLOR` and a non-terminal stdout disable colour.

## Outputs

**Byte-identical** for every line the tests assert and for the help text: the same `log_ok`/
`log_info`/`log_warn`/`log_error`/`log_step` prefixes and colours, the same step titles (`1/5` …
`5/5`), the same wording. The messages still say `./setup.sh install` (the command the person
types). **Stated differences:**

- the build log on a failed build is Go's own output (same 15-line tail);
- `status` reads the plugin registry with a JSON parser, so a listing herdr changes in a
  compatible way (new fields) no longer reads as "could not be read";
- the Redis ownership check adds one line when the broker belongs to another checkout.

## Tests

- **Unit** (`setup/`, fake runner, fake FS root, no network): every row of the inventory, the
  keybinding exact-block swap (unedited, edited, absent, two copies), the plugin record rule,
  the skills marker rule, `uninstall` order and its stop-on-service-failure, `--yes` defaults, the
  no-terminal stop, ownership of the broker, `update`'s exec arguments.
- **End to end**: `tests/installer_test.sh` unchanged in its 113 asserts plus the ownership one,
  run against the old and the new `setup.sh` during the rollout.
- **Golden output**: a dry-run install and a `status` captured from today's bash in a throwaway
  HOME, compared line by line with the Go output (the stated differences excluded by name).

## Rollout

1. `agentbus setup` lands beside the bash; `scripts/setup-go.sh` is the thin script. The test suite
   runs twice (`SETUP=./setup.sh`, `SETUP=./scripts/setup-go.sh`); both must pass.
2. Switch: `setup.sh` becomes the thin script; the old one moves to `scripts/setup-legacy.sh` for
   one release, documented in the release notes.
3. Remove the legacy script once a release has shipped without a report against the Go installer.

## Open questions for review

1. Rebuild `agentbus` stamped inside Go (proposed: one source for the version logic, one extra
   cached build), or let the thin script stamp it and the Go side copy `os.Executable()`?
2. Keep `--force` as a no-op forever, or drop it at the switch with a one-release warning?
