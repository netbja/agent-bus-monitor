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

Bernard's answers to the open questions (2026-11-02):

- **Outside herdr, start launches herdr itself** (`herdr --session <project>`), and the team
  opens inside it ("Outside herdr" below).
- **The team line also changes a role's client**, chosen from that role's `fallback_profiles`,
  not only keeps or drops roles ("The assistant").
- **A reduced team is kept in the project's template and as the default team for future
  projects** in `config.toml`, the registry key `start.team` ("What is written").

## What start is, and is not

`start` is a front door over `agentbus up`, not a second launcher. It deduces, asks, writes
the one or two things `up` cannot deduce, then runs `up`'s own code path (`runUp` with its
`upDeps`). Every rule of `up` therefore holds unchanged: bootstrap, the template, `checkClients`,
the trust gate, the services and their env file, `waitBroker`, `upLock`, and relaunch's rule
that no existing pane is ever typed into.

```
agentbus start [<path>] [--yes] [--dry-run] [--name <project>] [--as <name>] [--roles <r[:profile],...>]
```

`--roles` names the team, each role optionally with the profile it runs on
(`master,coder,foureyes:review`); a profile must be the role's own or one of its
`fallback_profiles`, checked as the team line checks it.

`<path>` defaults to the current directory. The flags pre-answer the assistant; `--yes` takes
every deduced value without asking. Its block in `main()` sits above the project-required
exit, beside `up`'s: no project is known until start has deduced one.

## What is deduced, and from where

| Value | Source, in order | Rule |
|---|---|---|
| Folder | `<path>`, else the current directory; then its Git root, and inside a linked worktree (`.worktrees/x`) the main checkout, as `trustKey` resolves it | must exist and be a directory; a team works in one checkout and makes its own worktrees, so a worktree never becomes a project of its own |
| Project | an existing template whose `working_dir` is that folder; else the folder's base name, lower-cased, every run of characters outside `[a-z0-9_-]` turned into one `-`, a leading non-letter dropped, cut at 32 | `bus.ValidName`; nothing valid left = asked, never invented; two templates naming that folder = both listed and asked (with `--yes`: refused, naming both) |
| Your name | `identity.as` (`config.toml`, then `busmon.toml`: `personName`); else `$USER` normalised by the project's rule | a `$USER` that normalises to nothing valid = asked; the name is shown, never silently written |
| Team | the template's agent tabs (and the `--profile` each tab's command carries) when the project exists; else `start.team` (`config.toml`); else `roles.toml`'s `boot` roles, in its order, each on its own profile | each role's client looked up with `lookAgentCLI` (PATH, the usual per-user dirs, the login shell); a `start.team` role the manifest no longer has as `boot` is dropped, and a profile no longer among the role's own or `fallback_profiles` falls back to its own, each said in one line; master is always in (added back, said, when `start.team` lacks it) |
| Services | systemd `--user` / launchd when `up` would use them, else tabs | as `up` decides it today (`d.Services`) |
| herdr session | `HERDR_SESSION`, then `HERDR_SOCKET_PATH`; outside herdr, the project's name | as `up` (`herdrSession`); outside herdr, the session start launches ("Outside herdr") |

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
  6  herdr      session shop             launched by start (you are outside herdr)

  Enter: start the team   1-5: change   q: quit, nothing written
```

Line 6 shows only outside herdr; it is information, not a choice (inside herdr the team opens in
the current session, as `up`).

Changing a line asks one question, with the deduced value as the default, checked by the same
rule as the table above; a refusal says the rule and asks again:

```
  Project [shop]: Shop!
    a lowercase letter, then lowercase letters, digits, - or _, up to 32
  Project [shop]: shop-api
```

The team line lists each role with the client it runs on. A number toggles that role; `c` and a
number changes its client. It never adds a role the manifest does not have as `boot`:

```
  Team  1 master    claude (default)     ✓
        2 coder     claude (default)     ✓
        3 foureyes  claude (default)     ✓   also: review (codex)
        4 sentinel  claude (light)       ✓
  number: on/off   c then number: client   Enter when done
  > 4
        4 sentinel  claude (light)       -
  > c3
    foureyes runs on: 1 default (claude) ✓   2 review (codex) ✓
  > 2
        3 foureyes  codex (review)       ✓
```

The client choice offers the role's own profile and its `fallback_profiles`, in the manifest's
order (`rolecfg.Fallbacks`: defined, another provider, at most 3), each with its client looked
up as the line above (`lookAgentCLI`; an unknown answer counts as found, as `up` fails open). A
profile whose client is confirmed absent is listed with `✗` and its install page, and cannot be
chosen. A role with no `fallback_profiles` offers nothing to choose and says so: `no other client
configured for coder (fallback_profiles in roles.toml)`; the shipped `roles.toml` declares none
for its `boot` roles, so on it the choice is empty until the person adds one, which start never
does for them.

master cannot be removed (it is the escalation target and the pilot); the screen says so. Its
client can be changed like any other role's. foureyes off is allowed, with one line: `no
reviewer: 4-eyes reviews will wait for one`.

A team that differs from the complete one on its default profiles (a role off, or a client
changed) shows one more line before Enter, the second write it causes:

```
  This team becomes the default for new projects (start.team in config.toml).
```

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
   A team other than the complete one on its own profiles adds `--roles <r[:profile],...>` to
   bootstrap, which passes it to `roles template`: `Template` takes the role list, each with an
   optional profile, default the `boot` roles on their own profiles; an unknown or non-`boot`
   role, or a profile that is neither the role's own nor one of its `fallback_profiles`, is
   refused. A role on another profile gets `--profile <p>` in its tab's command (`agent-launch
   <role> <project> --profile <p>`, the form `agent-launch` and the resume hook already accept);
   a role on its own profile gets none. **Without `--roles` its output stays byte for byte what
   it is today**, so `up`'s behaviour does not move (held by the existing template and
   `tests/roles_scripts_test.sh` dry-run tests, plus one comparing the default call with the
   old signature's output).
3. **`config.toml`: `start.team`**, through `config.Set`, whenever the person confirms a team in
   the assistant (or passes `--roles`) that differs from the complete one on its own profiles,
   for a new project or an existing one; the screen said so before Enter. Confirming the
   complete team again removes the key (`config.Unset`), so new projects follow the manifest's
   `boot` roles once more. A team taken unchanged (Enter on the deduced line, `--yes` alone)
   writes nothing.

`start.team` is a registry key (`config/keys.go`), so `agentbus config` shows it with its source
and busmon's settings page lists it:

| Field | Value |
|---|---|
| Name | `start.team` |
| Kind | `String`: comma-separated `role` or `role:profile`, the `--roles` syntax |
| Default | `""`: the manifest's `boot` roles on their own profiles |
| Env | none (a team is a person's choice kept in a file, not a session's override) |
| ReadBy | `agentbus start` |
| File rule | each item a valid name, optionally `:` and a valid name, no duplicate role; the manifest is not read here (`config` imports no `rolecfg`), so start checks the roles and profiles at use, as the Team row says |
| Group | `Identity` (who you are and the team you start with); `TestEveryKeyHasItsGroup` holds it |

### A role's own profile in a project

The configured profile of a role is then the template's when its tab names one. One function,
`ownProfile(m, project, role)`, answers it: the `--profile` of the role's tab in the project's
template (`rolecfg.ProjectsDir`/`<project>.toml`, split as `tabProfile` splits it) resolved with
`m.ResolveWith`, else `m.Resolve(role)` (no template, no tab for the role, as for a pop, or a tab
without `--profile`). A template that exists but cannot be read or parsed is an error, never a
silent fall back to `roles.toml`: a reader that acts (swap, reload) refuses with it, a reader that
reports (doctor) shows `?`.

Every reader that today takes a role's own profile from `m.Resolve(role)` reads it through
`ownProfile` instead:

| Reader | What it does with the own profile |
|---|---|
| `tabProfile` (`trust.go`), so `tabExecutable`, `checkClients` and the trust gate | the client a tab launches: `foureyes:review` is checked as codex and opens codex's trust gate, not claude's |
| `roles doctor` | the configured model, so a client chosen at start is never `model≠` drift |
| `swap`: own profile and target set | the targets are the manifest's own profile plus its `fallback_profiles`, minus the current own; so with foureyes on `review`, `swap foureyes --profile default` is allowed without `--force` |
| `swap`: provider rule | `checkFallbacks` checks "another provider" against the manifest's profile at load; swap also refuses a target on the current own profile's provider, since it would be held with it |
| `swap --back`, the `{p}:fallback` record (`OwnProfile`, `OwnProvider`), the `hold:<provider>` mark it reads | back to the template's profile, and the own provider watched is the template's |
| `swap`'s recovery line | `agent-spawn <role> <p> --profile <own>` when own differs from the manifest's, so a recovery respawns on the project's profile |
| `reload` preflight and proof | the client executable checked before the kill and the client/provider `replaced` proves against: the relaunch runs the template's command, so the proof must expect the template's client, or a sentinel reload of a role whose client was chosen at start would kill it and then fail |
| `reload`'s recovery lines | as swap's |

`resumeProfile` (the resume hook) needs no change: it already adds `--profile` for a running
profile that differs from the manifest's. A choice made at start is the project's configuration,
not a fallback: it writes no `{p}:fallback` record, busmon shows no `fallback` flag, and no
`back:` notice follows.

A team change on an **existing** project:

- The template on disk is compared with what `Template` produces for its current team. If they
  differ (a hand edit: another command, an added tab), the change is refused, naming the file:
  `shop.toml was edited by hand: change its tabs there, or move it aside to let start write
  it`. bootstrap writes with a redirect, so a rewrite would erase the edit.
- On an **open workspace**, dropping a role whose agent is live, or changing its client, is
  refused, pointing at `agentbus retire <role>` first (for a client change while it runs,
  `agentbus swap <role> --profile <p>` is the temporary route, and the template is unchanged): `up`'s relaunch never closes a tab, so the agent would keep
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
(`--index` and `--cron` stay `up`'s flags), any `config.toml` key but `identity.as` and
`start.team`.

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

`up` outside herdr provisions and prints how to open the workspace. `start` goes on and launches
herdr itself, so the team opens inside it:

1. It provisions exactly as `up` does outside herdr (bootstrap, the template, the services).
2. It runs `herdr --session <project>` as a child that inherits start's terminal (stdin, stdout,
   stderr), the same process a person typing that command would get; herdr launches the session,
   or attaches to it when one of that name already runs.
3. In parallel it asks herdr where that session lives: `herdr session list --json` (herdr's own
   answer: `name`, `running`, `socket_path`), every 250 ms until the session is listed `running`,
   bounded at 15 s. agentbus never computes herdr's config directory: `herdrSession` and
   `sessionFromSocket` only parse a path they are given.
4. It then runs `up`'s code path (`runUp`) with `--session <project>` and an environment that
   says it is inside that session, since `up` opens a workspace only when `HERDR_ENV=1`
   (`up.go`; outside herdr it provisions and prints the hint, `--session` or not):
   `HERDR_ENV=1`, `HERDR_SESSION=<project>`, `HERDR_SOCKET_PATH=<socket_path>` from step 3, on
   top of start's own environment (the same three a herdr pane gives a plugin). Its herdr calls
   reach the session through that socket. That run's output goes to `<state>/start.log` (0600),
   never to the terminal herdr now draws.
5. start waits for herdr to exit and returns its exit status. If step 3 or 4 failed, it then
   prints the error and the log's path; the session and its workspace stay (they are herdr's).

What `up` would say on the terminal goes to `start.log` while herdr draws, so start says the two
lines a person needs **before** handing the terminal over, on the assistant's screen: the clients
`checkClients` finds missing (the Team line already shows them) and, for each client whose trust
reader says the folder is not trusted yet, `codex does not trust ~/src/shop yet: answer its dialog
in the foureyes tab`. Once herdr draws, that dialog is visible in its pane, and the dispatcher's
existing `prompt` flag and desktop ring (A10) cover a dialog left unanswered.

start does not own the terminal beyond that: it hands it to herdr for herdr's whole run and never
writes to it meanwhile. Without a terminal (a script with `--yes`), start cannot hand one to
herdr: it provisions, prints today's `herdr --session <project>` line, and exits 0. A second
`start` from inside the session finds the project open, as any start inside herdr.

## Failure paths

| What | What start does |
|---|---|
| `agentbus` installed but no broker (docker down, compose never run) | bootstrap's own error, plus `./setup.sh status` to see why |
| herdr not on PATH | says herdr is needed and where to get it (setup's step 2 text); writes nothing |
| Outside herdr, the session is not listed `running` within 15 s | herdr keeps the terminal; once it exits, start prints `herdr session <project> did not start` and `agentbus up <project>` to run inside it; exits 1 |
| Outside herdr, `up` fails inside the launched session | after herdr exits, the error and `<state>/start.log`; exits 1 |
| A `start.team` role or profile the manifest no longer allows | dropped or back to the role's own profile, said on the screen; never refused |
| A client change for a role whose agent is live | refused, pointing at `agentbus retire <role>` (or `swap` for a temporary change) |
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
- **Client choice**: only the role's own profile and its `fallback_profiles` are offered; one
  with a confirmed-absent client cannot be chosen; a role without fallbacks says so; the chosen
  profile is in its tab's command as `--profile <p>` and absent for its own; `--roles
  coder:nope` is refused before anything is written; a client change for a live role is refused.
- **Default team** (`start.team`): written through `config.Set` when a confirmed team differs
  from the complete one, removed when the complete one is confirmed, untouched by `--yes`
  alone; a new project's deduced team reads it; a stale role or profile in it is dropped or set
  back with a line; master added back when missing; the key's file rule refuses `a:b:c`,
  `Coder` and a duplicate role; `TestEveryKeyHasItsGroup` and `TestNoEnvLiteralOutsideTheRegistry`
  still pass.
- **Own profile** (`ownProfile`, a table: no template, a tab without `--profile`, a tab with
  one, a pop with no tab, an unreadable template), then one test per reader of the table above:
  `tabProfile`/`checkClients`/trust gate check codex for `foureyes:review`; doctor reports no
  drift; swap's target set includes the manifest's own profile and excludes the current own,
  refuses a target on the current own's provider, and its recovery line carries `--profile`;
  `swap --back` and the `{p}:fallback` record use the template's profile and provider; reload's
  preflight checks the template's client executable and its proof accepts the template's
  client/provider, and refuses on an unreadable template before any kill; a choice at start
  writes no `{p}:fallback`.
- **Outside herdr** (fake herdr): start runs `herdr --session <p>` with its terminal; polls
  `herdr session list --json` until the session is `running`; the `up` run gets `HERDR_ENV=1`,
  `HERDR_SESSION=<p>` and `HERDR_SOCKET_PATH` equal to the listed `socket_path` (recorded by the
  fake `upDeps`), and reaches `openWorkspace`: the fake herdr records the workspace list, the
  workspace and tab creation and the focus, each call made with that `HERDR_SOCKET_PATH` (not
  `up`'s provisioning-only hint); its output goes only to `start.log`; start returns
  herdr's exit status; a session never listed running ends as the failure table says; the
  missing-client and untrusted-folder lines are on the screen before herdr starts; without a
  terminal it prints the command and launches nothing.
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

## Questions for Bernard

Answered 2026-11-02 (recorded at the top and in the sections above):

1. **Outside herdr**: start launches herdr itself (`herdr --session <project>`), the team opens
   inside it.
2. **The team line**: it also changes a role's client, chosen from its `fallback_profiles`.
3. **A reduced team**: kept in the project's template and as the default team for future
   projects, `start.team` in `config.toml`.

Still open:

4. **The name**: a new `agentbus start`, or `agentbus up` with no project argument doing it?
   `start` is proposed, so `up`'s strict, scriptable form stays as it is.
