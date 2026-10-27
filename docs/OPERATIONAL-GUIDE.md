# Operational Guide: Agent Bus Monitor (`agent-bus`)

This practical guide complements the `README.md`. It provides a complete step-by-step walkthrough to install, configure, and use `agent-bus-monitor`, the `herdr` workspace manager, the `netbja.agentbus` herdr plugin, and day-to-day team workflows on new and existing projects.

---

## 1. Overview & Core Features

`agent-bus-monitor` is a decoupled, asynchronous multi-agent coordination bus built on **Redis Streams** and written in **Go**. It enables multiple autonomous agents (Claude Code, OpenAI Codex, Kimi CLI, etc.) to collaborate on a software project with complete visibility and oversight for the human operator.

```
┌─────────────────────────────────────────────────────────────┐
│                       Human Operator                        │
│                  (TUI busmon + herdr tabs)                  │
└──────────────┬──────────────────────────────▲───────────────┘
               │ @agent directive             │ Live Feed / Task Board
               ▼                              │
┌─────────────────────────────────────────────┴───────────────┐
│              Redis Broker (Streams on :6380)                │
│   {p}:cmd  ·  {p}:status  ·  {p}:report  ·  {p}:board       │
└───────▲──────────────────────▲──────────────────────▲───────┘
        │                      │                      │
┌───────┴────────┐     ┌───────┴────────┐     ┌───────┴────────┐
│     master     │     │     coder      │     │    foureyes    │
│  (Coordinator) │     │ (Implementer)  │     │ (4-Eyes Review)│
└────────────────┘     └────────────────┘     └────────────────┘
```

### Key Components

1. **The Redis Broker (`redis:8-alpine`)**
   - Isolates data under project namespaces (`{project}:{stream}`).
   - Manages message streams:
     - `{p}:cmd`: Directives and addressed commands.
     - `{p}:status`: Declared states and passive heartbeat timestamps.
     - `{p}:report`: Curated summaries for humans.
     - `{p}:notify`: Global informational announcements.
     - `{p}:verdicts`: Audit ledger of approvals and rejections.
   - Maintains state keys: `{p}:board` (task ownership registry), `{p}:pilot` (coordinator lease), `{p}:armed:<agent>` (active listener leases), and `{p}:gate:<agent>` (4-eyes review gates).

2. **The `agentbus` CLI (`cmd/agentbus`)**
   - Command-line tool used by agents and scripts to interact with the bus:
     - Publish state/heartbeat: `agentbus status <agent> <working|idle|blocked|waiting|done> <message>`
     - Dispatch directives: `agentbus cmd [--ref T] <agent> <directive>`
     - Submit progress reports: `agentbus report <agent> <note>`
     - Track tasks on the shared board: `agentbus board`
     - Manage 4-eyes review gates: `agentbus gate`, `agentbus verdict`

3. **The `busmon` TUI Dashboard (`cmd/busmon`)**
   - Interactive terminal UI for live multi-agent observability:
     - **STATUS**: Broker connectivity, active master, pending requests, dispatcher state.
     - **AGENTS**: Per-agent chips (`working`, `idle`, `blocked`, `done`), age since last update, listening leases `👂`, open challenges `🔒`.
     - **BOARD**: Shared task registry status (`N/M done`).
     - **ACTIVITY**: Real-time scrolling feed of messages and reports with full-text inspection (`Enter`).
     - **INPUT**: Live command field to direct agents (`@coder ...`).

4. **Event-Driven Dispatcher (`agentbus dispatch`)**
   - Background service watching the `{p}:cmd` stream.
   - When a directive arrives for an idle, unarmed agent, it types a single wake command into its `herdr` pane (only when the pane is idle and not focused).
   - The agent wakes up, drains waiting tasks via `agentbus subscribe --drain <agent>`, executes work, and returns to sleep.

---

## 2. Architecture Advantages

- **Zero Token Cost While Idle**: With the event-driven dispatcher, idle agents do not run token-wasting polling loops; they are awakened only when concrete work is assigned.
- **Claude by Default, Adaptable to Any Model**: Uses Claude Code by default (`roles.toml`), with full support for mixed teams running OpenAI Codex or Kimi CLI through declarative profiles (`docs/AGENT-PROFILES.md`).
- **100% Local, Ready for Remote**: Operates entirely on your local workstation (`127.0.0.1:6380`), while natively supporting remote servers (host1, GPU clusters) via secure SSH port forwarding.
- **Observability & Governance**: Full human visibility through `busmon`, enforced with mandatory 4-eyes code review gates (`foureyes`, `security`).

---

## 3. Step-by-Step Terminal Installation

Everything goes through `./setup.sh`, from a clone of the repository. Every step is idempotent,
so re-running it is safe.

### Step 1: System Prerequisites

- **Go**: the version `go.mod` requires, currently 1.26.3.
- **Docker** and **Docker Compose**, for the Redis broker.
- **Git** and **curl**.

Python is no longer needed (since v0.10.2).

### Step 2: Clone and install

```bash
git clone https://github.com/netbja/agent-bus-monitor ~/agent-bus-monitor
cd ~/agent-bus-monitor
./setup.sh install --dry-run          # what is missing, what would change (changes nothing)
./setup.sh install --install-herdr    # the real install
```

`install`:
- checks the toolchain;
- builds `agentbus` and `busmon`, into the repository and into `~/.local/bin`, stamped with
  their release;
- installs **herdr** if it is missing (`--install-herdr`);
- links this checkout into herdr as the plugin **`netbja.agentbus`** (`herdr plugin link`) and
  checks that its Projects action exists; herdr-plus is no longer installed or needed;
- adds the `Ctrl+B` then `↑` (prefix+up) binding for the plugin's Projects picker, creating herdr's `config.toml` if herdr has never run, and
  tells a running herdr to reload it;
- clones the external role skills (`mattpocock/skills`, pinned) into
  `~/.local/share/agentbus/skills/mattpocock` and links the role skills. If one is still missing,
  the agent starts without it, with a warning;
- starts the Redis broker on `127.0.0.1:6380`.

Make sure `~/.local/bin` is on your `PATH`; `install` warns when it is not, or when another copy
shadows it.

### The Projects picker and the busmon action

- **Picker** (`Ctrl+B` then `↑`, or `busmon --pick`): one row per template in
  `~/.config/agentbus/projects`, with the broker's state (`present`, `absent`, `?`). Type to
  filter (substring of the name or the directory). Enter opens a project the broker confirms
  (`agentbus up <p>`). It never provisions: for an `absent` project run `agentbus up <p> <path>`.
- **busmon**: in herdr's action menu, **Agent Bus: busmon** opens busmon for the focused
  workspace's project, or the picker when the workspace is not a project.
- **Moving to v0.20**: the templates used to live in herdr-plus's directory. There is no migration
  code; move them by hand, once:

  ```bash
  mkdir -p ~/.config/agentbus/projects && mv ~/.config/herdr/plugins/config/cloudmanic.herdr-plus/projects/{ai-tradex-solana,hyperliquid}.toml ~/.config/agentbus/projects/
  ```

### Step 3: Check, update, remove

```bash
./setup.sh status       # versions, PATH shadowing, skills, herdr plugin, key binding, Redis
./setup.sh update       # git pull --ff-only, then install; prints vA -> vB and the release notes
./setup.sh uninstall    # asks at every step; --yes = cautious defaults; --purge = Redis data too
```

busmon shows a discreet `↑ vX.Y.Z` in its header when a newer release exists. It checks once a
day; `AGENT_BUS_NO_UPDATE_CHECK=1` turns the check off.

`update` refuses a tree with uncommitted changes, then runs the `setup.sh` it just pulled. The plugin
root is the checkout, so the update reaches it too. A herdr-plus installed by an older setup is
reported by `status` and never removed.

`uninstall` removes only what setup made, and has no `--dry-run` (run it without `--yes` and
answer each question):
- every project's services (their units; data and logs are kept);
- our binaries and our skill links (never a real directory);
- the key binding, if it is still exactly the block setup wrote; an edited one is kept;
- the external skills, if setup cloned them and they have no local changes.

It unlinks the herdr plugin only when setup linked it, after asking. For Redis it
offers: stop it and keep the data (the default), remove it together with its data, or leave it.

---

## 4. Network Topology: Local vs Remote

### 100% Local Setup (Default)
All processes run on the same workstation. Agents and `busmon` connect directly to `127.0.0.1:6380` (default password: `AgentBus2025!`).

### Hybrid Setup with Remote Servers (host1 / GPU)
For security, the broker binds strictly to loopback (`127.0.0.1:6380`). Remote access uses SSH tunnels:

- **Scenario A: Monitoring a remote bus from your local machine**:
  ```bash
  ssh -NL 6381:localhost:6380 user@remote-server &
  REDIS_PORT=6381 busmon --project myproject
  ```
- **Scenario B: Remote agents joining your local bus**:
  From the remote server, open a reverse tunnel:
  ```bash
  ssh -L 6380:localhost:6380 user@local-workstation -N
  ```
  Agents on the remote server can then publish and subscribe on `localhost:6380` seamlessly.

---

## 5. Walkthrough: Launching and Managing Projects

### Scenario A: Starting a Team on a NEW Project

Assume your target project is at `~/code/myapp`.

1. **Start herdr** (a session per project is handy):
   ```bash
   herdr --session myapp
   ```

2. **Provision and open the team in one command** (inside herdr):
   ```bash
   agentbus up myapp ~/code/myapp --as <your-name>
   ```
   This command:
   - starts the Redis broker if needed, links the role skills and writes the project
     template (`scripts/bootstrap` underneath; its flags work: `--no-dispatch`, `--no-missions`,
     `--index`, `--cron`);
   - starts the project's **services** under your system's service manager (systemd `--user`
     on Linux, launchd on macOS):
     - `dispatch`: wakes idle agents when work lands for them;
     - `missions`: the conductor for large changes.

     They do not live in a tab, so closing busmon, the workspace or herdr does not stop them.
     When no manager is usable, `up` says so and runs them as tabs; `--tabs` forces that;
   - opens the workspace directly, with **busmon first and focused**, then:
     - `master`: team coordinator;
     - `coder`: implementation agent;
     - `foureyes`: code review agent;
     - `sentinel`: health and budget watch.
   - Run again, it focuses the open workspace instead of opening a second one.

   busmon shows you as `♙ <Name> [human]`, with `· pilot` while you hold the pilot lease. With no
   `--as`, you are `Pilot [human]`. The name is lowercase, like an agent's: `--as bernard` shows
   `Bernard`. `export AGENT_BUS_HUMAN=bernard` sets it for every busmon.

3. **Assign the first task** from busmon's input line (`i`):
   ```text
   @master Initialize the project structure and prepare the roadmap.
   ```
   `@agent` wakes that agent and `@all` wakes every agent. Plain text is a broadcast nobody
   reads, so busmon asks for a second Enter before sending it.

---

### Scenario B: Resuming an EXISTING Project

1. Start herdr (`herdr --session myapp`).
2. Run `agentbus up myapp`, or press `Ctrl+B` then `↑` and pick `myapp`.
   > Tabs open with fresh agent sessions. To resume an earlier Claude Code conversation, use
   > `claude --resume` and pick `myapp:master` or `myapp:coder`.

   The picker reopens the workspace only. After a reboot, nothing has started the services:
   `agentbus up myapp` starts them, and busmon shows `dispatch down` until then.

---

### Scenario B2: Moving a team from before v0.12

A team whose dispatch and missions ran as tabs, or one built by hand (its own tab names, its own
agent names, no project template):

1. **Services only, from a terminal outside herdr.** Outside herdr, `agentbus up` installs and
   starts the services and opens nothing, so it never adds a second team next to yours:
   ```bash
   agentbus up myapp ~/code/myapp --session <herdr-session> --escalate-to <coordinator>
   ```
   - `--session`: the herdr session the agents live in, when its name is not the project's.
     The dispatcher looks for its panes there.
   - `--escalate-to`: the agent that receives escalations and `complete` notices, when the
     coordinator is not named `master`. It is recorded: a later `agentbus up myapp` keeps it.
2. **Close the old tabs** that ran `agentbus dispatch`, `agentbus missions` or any older wake
   loop: the dispatcher must be the only process that types into panes.
3. **Each agent declares itself once** from its own tab
   (`agentbus --project myapp status <name> idle "back"`), or the coordinator does it for the
   team. Until an agent has, the dispatcher does not know its pane and never types into it.

---

### Scenario C: Watching and Stopping a Project

- **Watch.** busmon's header reads `dispatch ready`, `dispatch waiting herdr`,
  `missions idle`, `missions busy`, `missions failed` and so on. `F3` (or `s`) opens the
  Services overlay, with the last error and the tail of each log.
- **Quotas: stop before the limit.** The dispatch service reads every provider's usage windows
  (used %, reset) from local sources, every minute, with no model call. busmon's header shows one
  gauge per provider (`claude 5h 82% ~25m · codex 5h 96% held 03:10 · grok wk 59%`); `agentbus quota`
  prints them in a terminal. At 85% of a window master gets one warning; at 95% the agents of that
  provider are no longer woken until the reset (their state column reads `quota`, the flag `held`
  when they declared blocked or done); when master's own provider is at
  95%, the whole project pauses until the reset (the agents then read `paused`; their declared
  state stays in their detail). A reading that is missing or older than 30
  minutes never stops anyone.
  - **Claude Code**, once per machine: `agentbus statusline --print-install` prints the `statusLine`
    block to put in `~/.claude/settings.json`. Claude Code then hands agentbus its usage on every
    refresh, and agentbus draws the status line itself (`Opus 5.5 · myapp · ctx 41% · 5h 12% (08:33) ·
    7d 54%`): no other tool is needed, ccstatusline can be removed. To keep another status line, add
    `--then '<its command>'`.
  - **Codex** and **Grok** work out of the box when their CLI is installed.
  - **Kimi Code** and **agy** (Antigravity) too, from their own logs. They only show a hit, never a
    percentage: a Kimi `5-hour` or `weekly` limit holds its agents until the next answer Kimi
    gets, or at the latest the window's length after the first hit, since Kimi states no reset
    (a `billing cycle` limit states no length and is never held). An agy `Individual quota
    reached … Resets in …` holds until the reset it states.
  - **Any other AI**: describe its source in `~/.config/agentbus/quota.toml` — a file and its fields
    (`kind = "json"` or `"jsonl-last"`), or a script that prints the reading as JSON
    (`kind = "command"`), or `kind = "unlimited"` for a local model. Thresholds:
    `AGENT_BUS_QUOTA_WARN` (85), `AGENT_BUS_QUOTA_HOLD` (95), `AGENT_BUS_QUOTA_MAX_AGE` (30m),
    `AGENT_BUS_QUOTA=off`, exported before `agentbus up`.
- **Pause the project** when the quota runs out: `agentbus --project myapp pause --until-reset quota`
  (or `--for 2h`, `--until 02:15`). Nobody is woken until then, and busmon's header reads
  `paused · resumes in 1h52 (quota)`. At the deadline the dispatcher ends the pause and sends master
  one resume directive. `agentbus --project myapp resume` ends it early; `pause --status` shows it.
  master does this itself when sentinel warns that the session budget is at 90%.
- **`@all`** reaches the agents listed in Agents (not the conductor, which only reports). It is still
  one message per agent, so each is delivered on its own, but Activity shows a single
  `→ @all (6)` line.
- **Questions for you.** When master needs decisions it asks them as questions (`agentbus ask`):
  the header counts the open questions (`♙ 2 waiting for you`) and the agent row fades `waiting`.
  Press `!` or `w` (or Enter in the agent's detail) to open them: one tab per question, `↑↓` or a digit to pick an option, Enter to
  send that answer at once, `Other…` to write your own, `←→` to change question, Esc to close. An
  answered question shows its answer and never comes back. `agentbus asks --open` lists them in a
  terminal, `agentbus answer <id> <question> --choice <label>` answers without busmon. Long texts
  (reports, messages) are shown as Markdown: headings, lists, paragraphs; a text written on one
  line gets a paragraph at each upper-case label (`DECISION 1 -`, `VERIFIED:`) and a list for
  inline choices (`(X) … (Y) …`).
- **An agent waiting for you.** When master (or any agent) needs your decision it declares
  `waiting`: it moves to the top of Agents with ♙ and the word `waiting` slowly fading in and out, the header reads `♙ 1 waiting for
  you`, and the terminal bell rings once per new question, which herdr shows on the busmon tab.
  Open it (Enter on its row): its last report, the decision it needs, comes right after its
  status message. Answer with `@master …`; the agent then declares `working` again. The Requests
  panel counts what is for you (`1 for you`) apart from the agents' own threads.
- **The `updated` gauge** in Agents (`●●●●●●●○○○ 3m`) is how fresh the agent's last declared
  state is: full right after the declaration, empty after 10 minutes, then the age alone. It does
  not measure work in progress.
- **Activity auto-scroll.** The Activity title reads `auto-scroll on` while new lines scroll into
  view. `f` turns it off (the view stays put and counts what arrives: `auto-scroll off · 3 new`)
  and on again; moving, clicking or opening a line keeps it on. A wheel scroll up turns it off
  (you are reading back), `End` turns it on. Today's day separator
  reads `Wed 2026-10-14 - (today)`.
- **Agents that are gone** (a replaced team, a renamed role) keep their last declared state:
  nothing guesses that an agent died. busmon leaves out of the Agents panel, and out of `@all`, an
  agent silent for more than a day that is not listening and is owed nothing; the panel counts
  them (`3 dormant`) and `a` shows them. To retire one for good, select it and press `d` (or
  `Delete`), then `y`: same as `agentbus --project myapp agents forget <name>`. An agent that is
  listening, or declared a status in the last 10 minutes, is refused.
- **Old requests.** The Requests count only covers the last 24 hours: an older unanswered
  directive wakes nobody. In the Requests panel, `d` then `y` closes a thread with a reply from
  you; its history stays. A tracked request is closed by its owner (`agentbus request done`).
- **Read the logs:** `agentbus --project myapp service logs missions -n 100`. They rotate at
  1 MiB, three files kept.
- **Stop:** `agentbus down myapp` stops the services and deletes nothing. The agents and the
  workspace stay open; close them in herdr. If the conductor was checking or integrating, its
  tests are killed with their process group, and it redoes that step at its next start.
- **Restart one service:** `agentbus --project myapp service restart dispatch`, for example
  after `./setup.sh install`, since a running service keeps its old binary.
- **Failures.** A service that fails is restarted. After 5 failures in 10 minutes it gives up
  and busmon shows `gave up`; the next `agentbus up` tries again. It is never restarted in a
  loop on an invalid configuration, on a deleted project, or when another instance already
  holds its lease.
- **Remove.** `busmon --delete myapp` also removes the project's services on this machine.
  `./setup.sh uninstall` removes every project's services first.

---

## 6. Starting Work: a Small Task, a Large Change

The team is up (`agentbus up myapp …`) and busmon is open. You can hand it work in two ways,
depending on its size.

| | Small task | Large change |
|---|---|---|
| Size | an hour to a day; one feature, one fix | several days; several agents in parallel |
| Who drives | `master`, in its conversation | the **conductor** (`missions` service), from a plan in Git |
| What survives a restart | the board and the bus messages | everything: the plan is in Git, its state in Redis |
| What you do | write the request, answer questions | approve the plan, decide on escalations, merge the result |

### Small task: ask `master`

1. **Write the request in busmon** (`i`), with the goal and how you will judge it done:
   ```text
   @master Add CSV export to the reports page. Done when: an "Export" button downloads the
   current filter as CSV, with tests, reviewed by foureyes, on a branch ready for a PR.
   ```
   You can also type in master's tab directly; busmon is just quicker to reach.
2. **master splits and dispatches.** It sends `coder` a directive, `coder` claims the task on the
   board and works on its branch, then `foureyes` reviews (the 4-eyes rule: the author never
   approves its own work).
3. **Watch** the Board panel and the Activity feed. An agent that needs you sends a message
   to master, and master asks you.
4. **Finish.** master reports the branch and the review verdict (`agentbus --project myapp verdicts`).
   You open or approve the PR.

For a one-line fix you can skip master: `@coder Fix the typo in the README title.`

### Large change: plan it as missions, let the conductor drive

A change that takes days must not depend on one agent's conversation: a context fills up, a
session restarts. The plan therefore lives in Git, and the conductor carries it out step by step.

1. **Ask for a plan, not code:**
   ```text
   @master Plan the change "auth-v2" as missions: write docs/changes/auth-v2/brief.md and
   docs/changes/auth-v2/missions.json on a branch, commit, and send me the commit. Do not start it.
   ```
   For a design-heavy change, have master pop the architect first (`scripts/agent-spawn architect
   myapp`).
2. **Review the plan.** In `missions.json`, each mission names its `agent`, its `reviewer` (never
   the same agent), `depends_on`, the `allowed_paths` it may touch, its `test` and a `brief`
   section. The change also has a `test` (the whole suite) and, optionally, `"docs": "docs"` to
   have the docs agent update the documentation at the end. `docs/changes/roles-go/` is a real
   example. Ask for changes until the plan is right: this is the cheapest moment to fix it.
3. **Load it** at the reviewed commit (you or master):
   ```bash
   agentbus --project myapp mission load docs/changes/auth-v2/missions.json@<commit>
   ```
   From then on, the conductor:
   - sends each mission to its agent as soon as its dependencies are integrated (independent
     missions run in parallel);
   - checks every delivery: only the allowed paths, the mission's tests green;
   - opens the review with the mission's reviewer;
   - merges the approved mission into `change/auth-v2` and runs the change suite.
4. **Follow it** in busmon (`missions busy`, `missions idle`), or in detail:
   ```bash
   agentbus --project myapp mission status auth-v2
   agentbus --project myapp mission show auth-v2/t3
   ```
5. **Decide when a mission is blocked** (failed tests, rejected review, too many attempts). The
   conductor escalates to master, who decides or asks you:
   `mission retry`, `mission reassign <c>/<id> <agent>`, `mission skip … --reason`, `mission abort`.
   To change the plan, commit a new `missions.json` and `mission load` it again: missions
   that did not change keep their state.
6. **Over several days.** The services keep running when you close busmon, the agents or herdr.
   Close everything in the evening if you like. The next morning, `agentbus up myapp` reopens
   the team. Missions waiting for an agent are delivered once its tab is back. After a reboot,
   `agentbus up myapp` is also what restarts the services. An agent whose context is full can
   be restarted: its mission's brief and its branch are all it needs.
7. **The end.** When every mission is integrated, master receives a `complete` notice (and the
   docs agent its directive, if the plan named one). `change/auth-v2` is never merged into `main`
   by the conductor: open that PR yourself, or ask master to, and merge it.

---

## 7. Command Reference Summary

| Action | Command |
|---|---|
| Provision + open a team | `agentbus up myapp ~/code/myapp --as <your-name>` |
| Stop a project's services | `agentbus down myapp` |
| Service state and logs | `agentbus --project myapp service status` / `service logs [dispatch\|missions]` |
| Update the tools | `./setup.sh update` |
| What is installed | `./setup.sh status` |
| Remove the tools | `./setup.sh uninstall` |
| Open live dashboard | `busmon --project myapp --as <your-name>` |
| List bus projects | `busmon --list` |
| View active agents | `agentbus --project myapp agents` |
| Inspect task board | `agentbus --project myapp board` |
| Send direct order | `agentbus --project myapp cmd coder "Fix unit test"` |
| Inspect thread history | `agentbus --project myapp thread <id-or-ref>` |
| View report summaries | `agentbus --project myapp reports` |
| Spawn extra agent | `scripts/agent-spawn architect myapp` (from master) |
| Purge project history | `busmon --project myapp --reset` |

---

## 8. Best Practices & Common Traps

1. **`AGENT_BUS_PROJECT` is Mandatory**: Export `export AGENT_BUS_PROJECT=<name>` in agent shells to avoid repeating `--project`.
2. **Flag Syntax**: Always use double dashes with a space (`--ref abc123`, never `--ref=abc123` or `-ref abc123`).
3. **Positional Order**: Arguments must follow exact ordering: `agentbus status <agent> <state> <message>`. The message is always last.
4. **Naming Conventions**: Project and agent names must match `^[a-z][a-z0-9_-]{0,31}$` (lowercase, starting with a letter).
