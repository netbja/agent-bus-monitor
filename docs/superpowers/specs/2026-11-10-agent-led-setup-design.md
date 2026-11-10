# An agent-led setup: the person's own agent installs agent-bus with them (agent-led-setup)

Status: spec, for review (subject `agent-led-setup`). Not built. It answers the plan task
`idea-agent-led-setup` (`docs/plans/v0.27.json`) and the first idea of the open question
[2026-11-03-deepseek-superagent-ideas](../../memory/open-questions/2026-11-03-deepseek-superagent-ideas.md).
§9 holds the questions left to the person, each with a recommendation.

## 1. The idea, and why

Today a new operator follows [the quick start](../../quickstart.md): clone, `./setup.sh install`,
`agentbus statusline --print-install` and a hand edit of `~/.claude/settings.json`,
`agentbus identity set`, `agentbus start`, and later the notifications. Each step is short, but
the person must read, choose and type every one, and know which ones apply to their machine.

DeepSeek's prototype (`SETUP.md`, checkout at `8c32a9d`) turns this around: the person opens the
coding agent they already use in the fresh clone and says "set this up". The agent follows one
file. It takes **as little of the person's attention as possible**, which is also this project's
principle:

- **a silent probe first**: it runs read-only commands and keeps the result to itself;
- **one question per message**, each with **a recommended answer** the person accepts with one
  word, and one sentence on why it matters; a question the probe already answers is skipped;
- **one full plan, confirmed once, before anything runs**;
- **never a secret in the chat**: every step that needs a password, a token, a login or `sudo`
  goes into one message of exact commands the person runs in their own terminal.

agent-bus takes that shape. **Nothing new is built in Go**: the setup is one instruction file the
agent reads, plus two pointers to it (§3). Every action it plans is an existing command
(`./setup.sh`, `agentbus identity set`, `agentbus start`…), so the agent-led path and the manual
path are the same path, and the manual one stays the reference.

## 2. Who runs it, and with what rights

- **The agent is the person's own client session** (Claude Code, Codex, Grok, Kimi…), started by
  them in the clone. It is **not** a bus agent: before the install there is no bus, no project, no
  role and no tool rules of ours. It runs with the rights the person's client gives it, and the
  client's own prompts are the person's control over each command.
- So the file is **instructions, not a boundary**. It keeps the agent away from every act the
  bus reserves to the person (§6), and the commands that already refuse an agent caller keep
  refusing it: `agentbus notifications add/setup/show-topic` and `test`
  ([notify guided setup](2026-11-08-notify-guided-setup-design.md) §2), whose ancestor check
  does not catch an unregistered session, which is why the file hands them to the person (§5.4).
- It needs no model of ours and no network beyond what `./setup.sh install` already fetches.

## 3. Where the file lives, and how the agent finds it

- **`SETUP.md` at the repository's root**, written for an agent, in English, as short as the
  prototype's (under 200 lines). The human guide stays `docs/quickstart.md`.
- **`AGENTS.md` and `CLAUDE.md` get one line at the very top**: "Asked to install or set up
  agent-bus on this machine? Read `SETUP.md` and follow it; the rest of this file is for
  developing agent-bus." A client loads one of these two first; without that line the agent
  would read the development team's rules (worktrees, 4-eyes, the bus) and act on them.
- **The quick start and the README** gain one sentence in §1: "Or open your coding agent in the
  clone and say: *read SETUP.md and set up agent-bus*."
- A test checks that `SETUP.md` names only commands and flags that exist (`agentbus help <cmd>`
  lists them), so the file cannot drift from the binaries. It is the one piece of code.

## 4. The probe (silent, read-only)

Run once, from the clone, before the first question. Nothing it runs writes anything:

```sh
uname -sm; git --version; go version; docker version --format '{{.Server.Version}}'
docker compose version --short; command -v herdr && herdr --version
command -v claude codex grok kimi            # the agent CLIs on PATH
./setup.sh install --dry-run                 # the installer's own checks, nothing changed
command -v agentbus && ./setup.sh status     # an earlier install
agentbus identity 2>/dev/null; git config user.name
```

What it decides:

- **A missing prerequisite becomes a plan item, not a question.** Go too old, Docker absent or its
  daemon down, the person not in the `docker` group: these need `sudo` or a package manager, so
  they are **person steps** (§5.4) with the exact command for the detected OS. herdr missing is a
  plan item the agent may run (`--install-herdr`), since `./setup.sh` already offers it.
- **An earlier install** (`./setup.sh status` prints ✓ lines) turns the setup into an update: the
  first question becomes "update it, or leave it?" and the plan uses `./setup.sh update`.
- **The agent CLIs found** are checked against the clients the shipped roles run on (§5.1 Q3);
  none found stops the setup with one person step (install one, and log in to it).
- **`./setup.sh status` saying `Redis password: the historical default`** adds a plan item (§5.3).

## 5. The questions, the plan, the run

### 5.1 The questions, in order, one per message

Recommendations in brackets. Each message stays under 150 words.

1. **Your name on the bus.** "In busmon you write as a person under this name." [the probe's
   `git config user.name`, lowercased to `^[a-z][a-z0-9_-]{0,31}$`; skipped when `agentbus
   identity` already prints one.]
2. **The first project.** "Which folder should the first team work in?" [the parent folder's
   most recently changed Git clone other than agent-bus itself, else "none for now": the setup
   then stops after the install.] The project's name is not asked: `agentbus start` deduces it,
   and the plan shows it.
3. **The team.** "Who should work on it?" [`master,coder,foureyes`, plus `reviewer-codex` when
   `codex` is found, since a review by another maker's model catches more.] One sentence teaches
   why: one agent plans, one implements, one reviews.
   - **The question is the set of roles, never their clients.** `agentbus start --roles` takes a
     role, or `role:profile` with only the role's own profile or one of its `fallback_profiles`
     (reviewer-codex); the client follows from the profile in `roles.toml`. The setup does not
     edit `roles.toml` (§6), so it cannot put a role on another client.
   - Before asking, the agent reads each candidate role's profile and client with
     `agentbus roles fallbacks <role>` (`profile  claude-opus  claude-code  anthropic  opus`) and
     offers only roles whose client the probe found. A role whose client is missing is named with
     its client ("foureyes runs on claude-code, not installed") and is either left out or its
     client goes into the person steps; the person picks which.
   - The plan (§5.2) shows that table, role → profile → client → model, as the manifest gives it:
     what the person confirms is what `start` will launch. Putting a role on another client is
     the manifest's business (`roles.toml`, its fallbacks), out of this setup (§9 Q7).
4. **Notifications.** "Should your phone ring when the team needs you?" [not now: busmon shows
   it; you can set it up later with `agentbus notifications setup`.] A yes adds a person step,
   never an agent step (§5.4).
5. **Claude Code's status line** (only when `claude` is found). "agent-bus reads Claude Code's
   rate limits and context fill from its status line; may I add it to `~/.claude/settings.json`?"
   [yes.] A no leaves the block printed in the hand-over.

Not asked, listed in the plan as defaults: the bin dir `~/.local/bin`, Docker started, the
external skills fetched, the herdr plugin linked, `prefix+up` bound, the project's services
installed by `agentbus start`.

### 5.2 The plan

One message, under 300 words: what is installed and where, the commands the agent will run, in
order, the person steps, the defaults, the team as role → profile → client → model (§5.1 Q3), and
what is left untouched. It ends with one question:
"Go?" Nothing runs before a yes. A change to any answer sends the whole plan again.

### 5.3 The run (the agent's own commands)

In this order, each command printed before it runs, the agent stopping at the first failure:

1. `./setup.sh install [--install-herdr]` (or `update`).
2. `./setup.sh rotate-redis-password --yes` when the probe saw the historical default; this
   requires that the command prints no password (to verify while building, §8).
3. `agentbus identity set <name>`.
4. The status-line block, when the person said yes: the agent reads `~/.claude/settings.json`,
   shows the diff, and writes it only after the person's yes to that diff (the person's client
   config is theirs; a second, explicit confirmation).
5. `agentbus start <folder> --dry-run --roles <team>`, its output shown as is, with
   `agentbus roles fallbacks <role>` for each role: a refused role or profile stops the run.

The team itself is **not started by the agent** (§6): the last step is a person step.

### 5.4 Person steps

One message, before the run when a prerequisite is missing, else after it, with exact commands
and nothing to fill in:

- prerequisites needing `sudo` (Go, Docker, the `docker` group and its re-login);
- logging in to an agent CLI (`claude`, `codex login`…);
- `agentbus notifications setup`, on their own terminal, when they said yes to question 4: the
  topic or webhook stays between them and that command;
- **starting the team**: `agentbus start <folder> --roles <team>`, from their own terminal (it
  opens herdr), since it launches agents with their rights.

The agent waits for "done", then checks what it can without a secret: `go version`,
`docker version`, `agentbus notifications status`, `agentbus --project <p> agents`.

### 5.5 Verify and hand over

`./setup.sh status` with no `✗`, then one message under 300 words: where busmon is (`prefix+up`
in herdr, or `busmon --project <p>`), how to give the first task (the quick start's §3), the
three habits (start with one small task, read reports in busmon, the person decides merges), and
where to read more (`docs/quickstart.md`, `docs/OPERATIONAL-GUIDE.md`).

## 6. What the agent must not do

- **Ask for, read, show or repeat a secret**: no token, password, webhook, ntfy topic, API key;
  it never reads `notify.toml`, the Redis password, the env files the services read, or a
  client's credentials; it never runs `notifications show-topic`.
- **Run anything before the plan's yes**, apart from the probe; run a command the plan did not
  list; or use `sudo`.
- **Start agents or open herdr** in the person's workspace (§5.4): a team acts with the person's
  rights and is launched by the person.
- **Act on the bus**: no `agentbus cmd`, `report`, `request`, `verdict`, `gate` or `pilot`. It is
  not a team member, and the bus's rules assume every writer is one.
- **Write in the person's projects**: the folder of question 2 is only read by `start --dry-run`.
- **Edit this repository**: no commit, no change to `roles.toml`, templates or `config.toml` by
  hand; settings go through `agentbus` commands only.
- **Install anything the installer does not**: no other package, no `curl | sh` except herdr's,
  through `--install-herdr`.
- **Uninstall or purge.** `./setup.sh uninstall` stays the person's command.

## 7. How it fits what exists

- **`setup.sh` / `agentbus setup`**: unchanged. The agent is one more caller of its documented
  flags, and `--dry-run` is the probe's core. `--dry-run`'s summary line (`0 missing, N
  warning(s)`) is what the agent reads, never a scrape of the ✓ lines; if that proves too thin,
  `agentbus setup install --dry-run --json` is a later, separate step (§9 Q5).
- **`agentbus start`**: unchanged. The agent uses only `--dry-run`; the person runs the real one.
  Its own questions (name, team) are the ones question 2 and 3 answer, so the person types
  `--yes`-free commands that the plan already showed.
- **The notify guided setup** (2026-11-08, on main as `agentbus notifications setup` and busmon's
  page C): agent-led setup only points to it. Both hold the same rule from two sides: the secret
  is written by agentbus on the person's own act, never by an agent.
- **The prototype's other ideas** (`observe` mode, GitHub source) are not part of this spec.

## 8. To build (when the person says go)

One PR, docs and one test:

1. `SETUP.md` (§4–§6), and the pointer lines in `AGENTS.md`, `CLAUDE.md`, `README.md`,
   `README.fr.md` and `docs/quickstart.md`.
2. `TestSetupMdNamesRealCommands`: every `agentbus <cmd> --flag` and `./setup.sh <sub> --flag` in
   `SETUP.md` is known to the binaries' help.
3. Checked by hand and written in the PR: `rotate-redis-password --yes` prints no password; a
   live run of `SETUP.md` with Claude Code and with Codex on a scratch user account (as the quick
   start's outputs were), its transcript summarized, no secret in it.
4. A decision note superseding the first idea of the 2026-11-03 open question.

## 9. Questions for the person

1. **Build it at all.** Recommended: yes, as one file and one test (§8). It costs little, keeps the
   manual path as the reference, and makes the first contact one sentence. Or no: the quick
   start is enough for a tool with one operator.
2. **Who starts the team.** Recommended: the person (§5.4), since starting agents gives them the
   person's rights and opens their workspace. Or the agent runs `agentbus start` after the plan's
   yes, which saves one step.
3. **The status-line edit.** Recommended: the agent writes it after a second yes on the diff
   (§5.3 step 4). Or always a person step: the agent only prints the block.
4. **The pointer in `AGENTS.md` and `CLAUDE.md`.** Recommended: yes, one line at the top (§3);
   without it a client that loads `CLAUDE.md` first reads the development rules. Or only the
   README sentence, and the person names `SETUP.md` themselves.
5. **A JSON dry run for the installer.** Recommended: not now; the summary line and the exit
   status are enough for the probe. Or `agentbus setup install --dry-run --json` in the same
   release, so the agent never reads human text.
6. **Notifications in the setup.** Recommended: offered as question 4, done as a person step. Or
   left out entirely, mentioned only in the hand-over.
7. **A role whose client is not installed** (the shipped core roles all run on Claude Code).
   Recommended: the setup only reports it, with the client to install as a person step, and keeps
   to the manifest's profiles (§5.1 Q3). Or a later step gives the core roles `fallback_profiles`
   on other clients (codex, grok, kimi), so `start --roles coder:<profile>` can pick one; that is a
   manifest change with its own review, not part of this spec.
