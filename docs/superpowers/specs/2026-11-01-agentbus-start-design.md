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
| Folder | `<path>`, else the current directory; then its Git root, and inside a linked worktree (`.worktrees/x`) the main checkout, as `trustKey` resolves it | must exist and be a directory; a team works in one checkout and makes its own worktrees, so a worktree never becomes a project of its own |
| Project | an existing template whose `working_dir` is that folder; else the folder's base name, lower-cased, every run of characters outside `[a-z0-9_-]` turned into one `-`, a leading non-letter dropped, cut at 32 | `bus.ValidName`; nothing valid left = asked, never invented; two templates naming that folder = both listed and asked (with `--yes`: refused, naming both) |
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

A project whose workspace is open, every agent live:

```
agentbus start — ~/src/shop

  shop is running: 4 of 4 agents live, services active.
  Enter: focus its workspace   q: quit
```

Some agents dead (their pid gone, by `up`'s own `snapLife` reading):

```
  shop is open: 2 of 4 agents live (foureyes, sentinel are not).
  Enter: relaunch foureyes and sentinel in new panes (agentbus up's rule)   q: quit
```

Either way Enter runs `up`; the screen only says what `up`'s relaunch will find.

## What is written

Nothing before Enter (or `--yes`). Then two kinds of writes.

### What start itself writes

1. **`config.toml`**: `[identity] as = "<name>"` through `config.Set` (the settings page's
   writer: the smallest edit, a symlink followed, a comment kept), only when no name is
   recorded. A recorded name is never replaced here.
2. **The template's team**, only on a change. A new project's template is bootstrap's (below).
   A team other than the complete one adds `--roles <r,r,...>` to bootstrap, which passes it to
   `roles template`: `Template` takes the role list, default the `boot` roles, an unknown or
   non-`boot` role refused. **Without `--roles` its output stays byte for byte what it is
   today**, so `up`'s behaviour does not move (held by the existing template and
   `tests/roles_scripts_test.sh` dry-run tests, plus one comparing the default call with the
   old signature's output).

A team change on an **existing** project:

- The template on disk is compared with what `Template` produces for its current team. If they
  differ (a hand edit: another command, an added tab), the change is refused, naming the file:
  `shop.toml was edited by hand: change its tabs there, or move it aside to let start write
  it`. bootstrap writes with a redirect, so a rewrite would erase the edit.
- On an **open workspace**, dropping a role whose agent is live is refused, pointing at
  `agentbus retire <role>` first: `up`'s relaunch never closes a tab, so the agent would keep
  running outside its template. Adding a role is allowed: the new template has its tab, and
  `up`'s relaunch on the open workspace creates a tab that is missing (`Create`), in a new tab,
  typing into no pane.

### What bootstrap and up write, unchanged

start runs them as `up` does; every write below is theirs, on every run, and start adds none:

- `link-role-skills.sh --missing-ok` (bootstrap `new`): the roles' skill links in the clients'
  skill directories.
- `docker compose up -d` from the checkout (bootstrap's `ensure_broker`): the broker started if
  it is not. Its password file (`redis.env`) is setup's; neither start nor bootstrap writes it.
- The template, for a new project (bootstrap `new`, from `Template`).
- In service mode (`startServices`): the services' env file (rewritten when its content
  changes), the `{p}:services` `installed` marker, each kind's unit (reinstalled, changed or
  not), **each kind's restart budget reset** (`service.Budget.Reset` removes its file), and the
  services started, or restarted when the unit or the env changed.
- herdr: a new workspace and its tabs for a project not open; on an open one, relaunch's new
  panes for dead agents and new tabs for missing ones, and the old panes it proves it may close.

Never written by either: `roles.toml`, any client config, Git, a cron entry, the index
(`--index` and `--cron` stay `up`'s flags).

## Idempotence

A second `start` on a running team, every agent live, writes **nothing of its own**: no name
(already recorded), no template (no team change), no pane typed into, no tab or pane created
(relaunch finds every agent live; `typedIntoExisting` stays the invariant). It is not write-free:
it runs `up`, and `up` in service mode rewrites what the list above names on every run. Two of
those have an effect a person may notice: **each service's restart budget is reset** (the count
of restarts the OS manager has left starts again) and the `installed` marker is written again.
That is `up`'s behaviour today and start keeps it; changing it is `up`'s business, not this
spec's. With some agents dead, the second start is `up`'s relaunch: new panes for those only.
A start after a reboot is `up`'s relaunch too.

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
| A team change on a hand-edited template | refused, the file named, nothing written |
| Dropping a live role on an open workspace | refused, pointing at `agentbus retire <role>` |
| Two templates on one folder | asked; `--yes` refuses, naming both |

## What it never does

- Type into a pane that already exists (relaunch's rule is `up`'s, used as is).
- Read a credential: the trust readers return a bool, the client lookup reads paths only, and
  `config.toml` holds no secret.
- Edit a client's config (`~/.claude.json`, `~/.codex/config.toml`, `~/.kimi-code/`, …) or install a
  client. The skill links bootstrap places in the clients' skill directories are bootstrap's, as
  today (listed above), never a config file.
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
- **Writes**: the name only when none is recorded, through `config.Set`; the template only when
  new or its team changed (byte-identical otherwise); `--roles` reaches `Template` and an
  unknown role is refused before anything is written; `Template` without a role list equals
  today's output.
- **Team change**: a hand-edited template refused and left byte-identical; dropping a live role
  on an open workspace refused with `agentbus retire`; adding one creates its tab and types
  into no pane.
- **Folder**: a linked worktree resolves to its main checkout; two templates on one folder
  asked, and refused with `--yes`.
- **Idempotence**: start twice through `upDeps` fakes on a running workspace, every agent
  live: the second writes no file of start's own (config directory and template byte-identical),
  creates no tab or pane and types into none (`typedIntoExisting`); the fake service manager and
  paths record what `up` writes, and the test asserts exactly that list: env file written only
  if its content changed, the marker, the units, **every kind's budget file removed**, the
  services started. With one agent dead: one new pane, the others untouched.
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
