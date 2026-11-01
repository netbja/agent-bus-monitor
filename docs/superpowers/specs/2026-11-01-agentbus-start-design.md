# agentbus start: one command from install to a running team

Status: spec, not built. Plan v0.26 task `agentbus-start`, review subject `agentbus-start-spec`.

## Why

After `./setup.sh install`, a person still has to know four things before a team runs: the
project's name (a valid one), the folder, `agentbus up <p> <path>`, and that herdr must be
running first. The stranger test showed each of them stops someone. `agentbus start`, run in
the project's folder, does what `up` does with every value deduced, shows them once, lets the
person change them, then launches.

Bernard's decisions (2026-10-31):

- **One command** from install to a running team.
- **Hybrid**: the values are deduced (the project from the folder, the person's name, the team),
  then a short assistant lets the person change them before the launch.
- **The default team is the complete one**: master, coder, foureyes, sentinel (the shipped
  `roles.toml`'s `boot` roles).

## What start is, and is not

`start` is a front door over `agentbus up`, not a second launcher. It deduces, asks, writes
the one or two things `up` cannot deduce, then runs `up`'s own code path (`runUp` with its
`upDeps`). Every rule of `up` therefore holds unchanged: bootstrap, the template, `checkClients`,
the trust gate, the services and their env file, `waitBroker`, `upLock`, and relaunch's rule
that no existing pane is ever typed into.

```
agentbus start [<path>] [--yes] [--dry-run] [--name <project>] [--as <name>] [--roles <r,r,...>]
```

`<path>` defaults to the current directory. The flags pre-answer the assistant; `--yes` takes
every deduced value without asking. Its block in `main()` sits above the project-required
exit, beside `up`'s: no project is known until start has deduced one.

## What is deduced, and from where

| Value | Source, in order | Rule |
|---|---|---|
| Folder | `<path>`, else the current directory; then its Git root when inside a repository | must exist and be a directory |
| Project | an existing template whose `working_dir` is that folder; else the folder's base name, lower-cased, every run of characters outside `[a-z0-9_-]` turned into one `-`, a leading non-letter dropped, cut at 32 | `bus.ValidName`; nothing valid left = asked, never invented |
| Your name | `identity.as` (`config.toml`, then `busmon.toml`: `personName`); else `$USER` normalised by the project's rule | a `$USER` that normalises to nothing valid = asked; the name is shown, never silently written |
| Team | the template's agent tabs when the project exists; else `roles.toml`'s `boot` roles, in its order | each role's client looked up with `lookAgentCLI` (PATH, the usual per-user dirs, the login shell) |
| Services | systemd `--user` / launchd when `up` would use them, else tabs | as `up` decides it today (`d.Services`) |
| herdr session | `HERDR_SESSION`, then `HERDR_SOCKET_PATH` | as `up` (`herdrSession`) |

A project name already used by a template for **another** folder is never taken over: the
deduced name gets `-2`, `-3`, … (the first free one) and the assistant says why.

## The assistant

One screen, on the terminal start was run from (`/dev/tty`, as setup's `Prompter`), plain text,
no TUI. Enter launches; a digit changes that line; `q` or Ctrl+C quits having written nothing.

A new project:

```
agentbus start — ~/src/shop

  1  Project    shop                     from the folder's name
  2  Folder     /home/charlie/src/shop   Git root
  3  You        charlie                  from $USER (not recorded yet)
  4  Team       master  coder  foureyes  sentinel
                claude ✓ (all four)
  5  Services   systemd --user           dispatch and missions

  Enter: start the team   1-5: change   q: quit, nothing written
```

Changing a line asks one question, with the deduced value as the default, checked by the same
rule as the table above; a refusal says the rule and asks again:

```
  Project [shop]: Shop!
    a lowercase letter, then lowercase letters, digits, - or _, up to 32
  Project [shop]: shop-api
```

The team line toggles roles by number and never adds one the manifest does not have:

```
  Team: 1 master ✓  2 coder ✓  3 foureyes ✓  4 sentinel ✓   number to toggle, Enter when done
  > 4
  Team: 1 master ✓  2 coder ✓  3 foureyes ✓  4 sentinel -   number to toggle, Enter when done
```

master cannot be removed (it is the escalation target and the pilot); the screen says so.
foureyes off is allowed, with one line: `no reviewer: 4-eyes reviews will wait for one`.

A client that is missing is shown, never installed:

```
  4  Team       master  coder  foureyes  sentinel
                claude ✗ not found: https://docs.anthropic.com/claude-code (each role needs it)
```

With every client missing, Enter is refused with `up`'s own pointers (`checkClients`), and
nothing is written.

A project that already runs:

```
agentbus start — ~/src/shop

  shop is running: 4 of 4 agents live, services active.
  Enter: focus its workspace   q: quit
```

## What is written

Only after Enter (or `--yes`), and only what changed or does not exist yet:

1. **`config.toml`**: `[identity] as = "<name>"` through `agentbus identity set`'s own path,
   only when no name is recorded. A name already recorded is never replaced here (the
   settings page or the file does that).
2. **The template** (`<projects dir>/<project>.toml`) through `scripts/bootstrap`, as `up`
   does. A team other than the complete one adds `--roles <r,r,...>` to bootstrap, which
   passes it to `roles template` (new: `Template` takes the role list, default the `boot`
   roles; an unknown or non-`boot` role refused). An existing template is rewritten only when
   the person changed its team; otherwise it is left byte for byte.
3. **The services' env file**, by `up`, unchanged.

Nothing else: no `roles.toml` edit, no client config, no Git, no cron, no index (`--index` and
`--cron` stay `up`'s flags).

## Idempotence

A second `start` on a running team changes nothing: the template is not rewritten, the name
is already recorded, and `up` on an open workspace focuses it and runs relaunch, whose table
starts only a dead agent in a new pane and types into no existing pane (`typedIntoExisting`
stays the invariant). Its output is `up`'s (`checking its tabs`, then one line per tab:
`running`, `skipped (busy)`, `relaunched`). A start after a reboot is `up`'s relaunch too.

## Outside herdr

`up` outside herdr provisions and prints how to open the workspace. `start` does the same and
names the exact next command: `herdr --session <project>`, then `agentbus start` again in that
session (it then sees the provisioned project and opens it). See open question 1.

## Failure paths

| What | What start does |
|---|---|
| `agentbus` installed but no broker (docker down, compose never run) | bootstrap's own error, plus `./setup.sh status` to see why |
| herdr not on PATH | says herdr is needed and where to get it (setup's step 2 text); writes nothing |
| No terminal and no `--yes` | prints the deduced values, says `--yes` takes them, exits 2; writes nothing |
| Folder name gives no valid project | asks; with `--yes`, refuses with the rule and `--name` |
| Template name taken by another folder | a free `-N` name, said on the screen |
| A client missing for some roles | starts the others (as `up`), names the missing ones and their install pages |
| Trust gate pending | `up`'s message (the pane to answer in); a second `start` continues |
| Broker answers late | `up`'s `waitBroker` |
| Another `up`/`start` for the project | `upLock`: `nothing done` |
| Ctrl+C in the assistant | nothing written |

## What it never does

- Type into a pane that already exists (relaunch's rule is `up`'s, used as is).
- Read a credential: the trust readers return a bool, the client lookup reads paths only, and
  `config.toml` holds no secret.
- Edit a client's config (`~/.claude.json`, `~/.codex/`, `~/.kimi-code/`, …) or install a client.
- Edit `roles.toml`, replace a recorded name, or take over another folder's project name.
- Run `setup.sh`: it is the binary setup installs; when it is missing there is no `start`.

## Tests

- **Deduction** (pure, a table): folder → project (`My Shop` → `my-shop`, `2048` → asked,
  `_x` → `x`, a 40-character name cut at 32, an existing template's folder wins over the base
  name, a template of another folder → `-2`); name (config.toml, busmon.toml, `$USER`,
  `$USER` = `Root.Admin` → `root-admin`, none valid → asked); team (template tabs, else the
  `boot` roles in manifest order).
- **Assistant** (a fake TTY, transcripts): Enter launches with the deduced values; each line's
  change and refusal; master cannot be toggled off; `q` and Ctrl+C write nothing (the config
  directory and the projects directory byte-identical); `--yes` asks nothing; no TTY without
  `--yes` exits 2 and writes nothing.
- **Writes**: the name only when none is recorded; the template only when new or its team
  changed (byte-identical otherwise); `--roles` reaches `Template` and an unknown role is
  refused before anything is written.
- **Idempotence**: start twice through `upDeps` fakes on a running workspace: the second writes
  no file and types into no pane (`typedIntoExisting`).
- **Never**: a test that the assistant's code path opens no file under the clients' config dirs
  (the fakes' recorded reads), as `trust_test.go` does for the trust readers.
- `TestEveryDispatchedCommandIsInTheTable` and `TestEveryFlagLiteralIsInTheTable` get `start`
  and its flags through the command table.

## Open questions for Bernard

1. **Outside herdr**: should `start` launch herdr itself (`herdr --session <project>`, then the
   team opens inside it), or only print that command? Launching is one step fewer; printing
   keeps `start` from owning a terminal it did not open.
2. **The team line**: only keep or drop roles, or also change a role's client (for example
   foureyes on Codex when Codex is installed, from its `fallback_profiles`)? Dropping only is
   simpler; a client choice is what a person with two subscriptions will want.
3. **A reduced team**: remembered only in that project's template, or also as a default team
   for future projects (`config.toml`)? The template only is proposed.
4. **The name**: a new `agentbus start`, or `agentbus up` with no project argument doing it?
   `start` is proposed, so `up`'s strict, scriptable form stays as it is.
