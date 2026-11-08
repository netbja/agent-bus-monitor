# Operational Guide: Agent Bus Monitor (`agent-bus`)

This practical guide complements the `README.md`. It provides a complete step-by-step walkthrough to install, configure, and use `agent-bus-monitor`, the `herdr` workspace manager, the `netbja.agentbus` herdr plugin, and day-to-day team workflows on new and existing projects.

---

## Start a new project

Once agent-bus is installed ([section 3](#3-step-by-step-terminal-installation)), one command, run in
the project's folder, starts a team:

```bash
cd ~/code/myapp
agentbus start
```

`agentbus start` deduces what it can: the project's name (from the folder, or the template a
previous run wrote), your name (`agentbus identity set`, else `$USER`) and the team (every boot role of
`roles.toml` by default: master, coder, foureyes and sentinel as shipped). It shows them once and asks only for what it cannot deduce.
Then it runs `agentbus up` for you. Outside herdr, it opens the herdr session itself
(`herdr --session <project>`) and the team opens inside it.

- `agentbus start --yes` takes the deduced values without asking.
- `agentbus start --dry-run` shows the plan and starts nothing.
- `agentbus start ~/code/myapp` names the folder instead of the current one.
- `--name <project>`, `--as <your-name>` and `--roles master,coder,foureyes` set a value
  yourself; a reduced team is kept for the project and for the next new ones.

**When to use `agentbus up` instead.** `up` is the strict, scriptable form: no questions and no
deduction, the project named on the command line. Use it:

- for a project that already exists: `agentbus up myapp` reopens it, and relaunches any agent that
  is gone ([Scenario B](#scenario-b-resuming-an-existing-project));
- with `--resume`, to bring the Claude Code agents back on their recorded sessions;
- in a script that names everything: `agentbus up myapp ~/code/myapp --as <your-name>`;
- for its other flags (`--tabs`, `--no-dispatch`, `--escalate-to`, `--dry-run`).

[Section 5](#5-walkthrough-launching-and-managing-projects) walks through both.

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
     - **Header**: broker link, dispatcher and conductor state, what is for you, health, pause, one usage gauge per provider.
     - **For you**: the questions, decisions, gates and unread messages only you can settle.
     - **Agents**: declared state (`working`, `idle`, `blocked`, `waiting`, `done`), freshness, queue, context fill, flags in words.
     - **Activity · Board · Requests · Plan**: the live feed, the shared task board, who owes whom an answer, the batch's plan.
     - **Pages**: services (F3), health (F5), recap (`R`), plan (`P`), tokens (`U`), history (`H`), about (`A`: the versions, `y` copies them), help (F1).
     - **Input**: `@coder ...` sends a directive that wakes the agent.

4. **Event-Driven Dispatcher (`agentbus dispatch`)**
   - A user service (systemd `--user`, launchd) watching the `{p}:cmd` stream.
   - When a directive arrives for an idle, unarmed agent, it types a single wake command into its `herdr` pane (only when the pane is idle and not focused). A wake that waits 30 s on the pane you are in earns one desktop notice (notify-send); leave the pane and the next pass wakes it. `dispatch.focus_notice` (page C, Attention; `0` = off) sets the delay.
   - The agent wakes up, drains waiting tasks via `agentbus subscribe --drain <agent>`, executes work, and returns to sleep.
   - It also reads the provider quotas, the agents' context fill and the project's health, and acts on them by rule (section 5, scenarios C and D).

---

## 2. Architecture Advantages

- **Zero Token Cost While Idle**: With the event-driven dispatcher, idle agents do not run token-wasting polling loops; they are awakened only when concrete work is assigned.
- **Claude by Default, Adaptable to Any Model**: Uses Claude Code by default (`roles.toml`), with full support for mixed teams running OpenAI Codex or Kimi CLI through declarative profiles (`docs/AGENT-PROFILES.md`).
- **100% Local, Ready for Remote**: Operates entirely on your local workstation (`127.0.0.1:6380`), while natively supporting remote servers (a remote box, GPU clusters) via secure SSH port forwarding.
- **Observability & Governance**: Full human visibility through `busmon`, enforced with mandatory 4-eyes code review gates (`foureyes`, `security`).

---

## 3. Step-by-Step Terminal Installation

Everything goes through `./setup.sh`, from a clone of the repository. Every step is idempotent,
so re-running it is safe.

### Step 1: System Prerequisites

- **Linux or macOS.**
- **Go**: the version `go.mod` requires, currently 1.26.3.
- **Docker** and **Docker Compose**, for the Redis broker.
- **Git** and **curl**.
- **herdr** 0.9.0 or newer; `--install-herdr` installs it when it is missing.
- **At least one agent CLI**, installed by you: Claude Code (`claude`), Codex CLI (`codex`), Kimi
  Code CLI (`kimi`) or Grok Build (`grok`). The install never fails without one: it lists where to
  get each, and the bus and busmon work meanwhile. `agentbus up` with none of the team's CLIs starts
  nothing and says what to install; with only some missing it starts every tab and warns with them.

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

**Found it in herdr's plugin list?** Install it as above, not with `herdr plugin install`: the plugin
needs the broker, the binaries and the skills that `./setup.sh` sets up, and setup links it into
herdr for you. `herdr plugin install netbja/agent-bus-monitor` stops with the same advice and installs
nothing.

Make sure `~/.local/bin` is on your `PATH`; `install` warns when it is not, or when another copy
shadows it.

**Claude Code users, once per machine:** `agentbus statusline --print-install` prints the
`statusLine` block to put in `~/.claude/settings.json`. Without it agent-bus can read neither Claude's
usage windows nor each session's context fill (scenarios C and D).

**The installer is in Go.** `./setup.sh` builds `agentbus` and runs `agentbus setup`, with the same
subcommands and flags (`install`, `update`, `status`, `uninstall`) as before. It also refuses to
start, stop or remove a Redis container that another copy of the repository started (it reads the
container's Compose `working_dir` label). Should it fail on a machine, the former bash installer
remains for one release as `scripts/setup-legacy.sh` (same subcommands and flags): report what made you
use it, since the next release removes it.

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
  mkdir -p ~/.config/agentbus/projects && mv ~/.config/herdr/plugins/config/cloudmanic.herdr-plus/projects/*.toml ~/.config/agentbus/projects/
  ```

### Step 3: Check, update, remove

```bash
./setup.sh status       # versions, PATH shadowing, skills, herdr plugin, key binding, Redis
./setup.sh update       # git pull --ff-only, then install; prints vA -> vB and the release notes
./setup.sh uninstall    # asks at every step; --yes = cautious defaults; --purge = Redis data too
./setup.sh rotate-redis-password   # replace the broker's password (asks first; --dry-run, --yes)
```

**The broker's password.** An install on a fresh broker writes a random password to
`~/.config/agentbus/redis.env` (0600); compose and every agentbus and busmon process read it, so
nothing needs exporting. An install made before keeps the historical `AgentBus2025!` (health
`redis-password` warns) until you run `./setup.sh rotate-redis-password`: it checks first, asks,
sets the new password on the running broker, proves it, recreates the broker's container (a few
seconds, the data kept) so a reboot keeps it, and restarts the running project services. If the
broker refuses the change, or the new password fails its proof and the broker is set back, the file
is put back too (the error says so if that fails); if the broker might hold the new password (it
cannot be set back, or neither password can be proved), the new file is kept, the old one in
`redis.env.prev` (on a first rotation, the historical default), and the error says to try both. An exported `REDIS_PASSWORD` wins over
the file; the rotation refuses to run while one is exported. Reopen busmon after it, and copy the
file to any machine that reaches the broker through a tunnel.

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
All processes run on the same workstation. Agents and `busmon` connect directly to `127.0.0.1:6380`, with the password in `~/.config/agentbus/redis.env` (an install from before keeps `AgentBus2025!` until it is rotated).

### Hybrid Setup with Remote Servers (a remote box / GPU)
For security, the broker binds strictly to loopback (`127.0.0.1:6380`). Remote access uses SSH tunnels:

- **Scenario A: Monitoring a remote bus from your local machine**:
  ```bash
  ssh -NL 6381:localhost:6380 user@remote-server &
  REDIS_PORT=6381 busmon --project myproject
  ```
- **Scenario B: Remote agents joining your local bus**:
  From the remote server, open a tunnel to your workstation:
  ```bash
  ssh -L 6380:localhost:6380 user@local-workstation -N
  ```
  Agents on the remote server can then publish and subscribe on `localhost:6380` seamlessly.

---

## 5. Walkthrough: Launching and Managing Projects

### Scenario A: Starting a Team on a NEW Project

Assume your target project is at `~/code/myapp`. The short way is `cd ~/code/myapp && agentbus start`
([Start a new project](#start-a-new-project)); below is what it does, step by step, with `agentbus up`.

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
   `--as`, you are `Pilot [human]`. The name is lowercase, like an agent's: `--as charlie` shows
   `Charlie`. `export AGENT_BUS_HUMAN=charlie` sets it for every busmon.

3. **Assign the first task** from busmon's input line (`i`):
   ```text
   @master Initialize the project structure and prepare the roadmap.
   ```
   `@agent` wakes that agent and `@all` wakes every agent. Plain text is a broadcast nobody
   reads, so busmon asks for a second Enter before sending it.

**What a first run may tell you:**
- `agentbus up MyApp …` is refused: a project name is lowercase (a letter, then letters, digits,
  `-` or `_`, 32 at most), and the message gives the lowercase command to run instead.
- The client's own trust dialog: when Claude Code or Codex has never opened the folder, `up`
  starts that client's first agent alone and names the pane where the dialog waits; answer it
  there, and the client's other agents start.
- `busmon` started without a project says to pass `--project <p>` and points to `busmon --list`
  and `busmon --pick`.
- `agentbus --project myapp agents` before any agent has joined says so instead of printing nothing.
- From a shell, `agentbus` writes as `AGENT_BUS_AGENT`, and as `anonymous` when it is unset. Inside
  a herdr pane with it unset, a command that writes is refused with the fix, so an agent never posts
  under the wrong name. In busmon you write as a person (`--as`).

---

### Scenario B: Resuming an EXISTING Project

1. Start herdr (`herdr --session myapp`).
2. Run `agentbus up myapp`, or press `Ctrl+B` then `↑` and pick `myapp`.
   - **After a reboot**, herdr restores the workspace and its tabs, and relaunches the Claude
     Code agents with the resume command each one reported (`agent-launch <role> myapp --resume
     <id>`): same role, rules and bus identity. Switching to the workspace starts its stopped
     services (the plugin's workspace hook); until then busmon shows `dispatch down`.
   - **On an open workspace**, `agentbus up myapp` checks every tab: `alive`, `relaunched`,
     `relaunched (new pane)`, `relaunched (new tab)`, `skipped (busy)`, `unknown`. A dead agent
     comes back in a new pane beside its old one; nothing is ever typed into an existing pane.
     Once the new agent is proven, the old pane is closed if it is still a plain shell.
     `--dry-run` prints the plan, `--resume` resumes the Claude Code agents' recorded sessions.
   - **One conversation by hand:** `agent-launch coder myapp --resume <session-id>`. Never run
     `claude --resume` by hand: the agent would lose its role rules and its bus identity.
   - `agentbus projects` lists every known project with its broker state, services and agents,
     and names the `agentbus up` to run.

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
  `missions idle`, `missions busy`, `missions failed` and so on. `F3` (or `s`) shows dispatch,
  missions and the Redis broker as cards (state, pid, last error, log size; Redis version,
  uptime, memory, keys), read again every 5 seconds while the page is open; `↑↓` (or `←→` when the
  cards sit side by side) chooses one, Enter or `l` opens a service's log, and Enter on the Redis
  card opens its read-only page: memory, persistence, clients, each project's keys and streams,
  the port bind and the container's last log lines. F5 shows the project's
  health, `R` a recap of what happened while you were away (unread first), `P` the batch's
  plan, `U` the tokens. F1 lists every key; `t` there replays the first-launch tour.
- **The notice line.** At the right end of the footer, for a few seconds, busmon tells what
  happened by itself while you watched: a pause the dispatcher or an agent set (a quota pause
  with `▲`), the project resumed, an agent's context cleared after its hand-off, a reload begun,
  done or failed, a role moved to a fallback profile or back, a health check worsening or
  recovering (after a fail, sentinel's note on it when it wrote one). One at a time, 4 s each, 2 s
  while others wait; past five, the rest in one `N more automatic actions (R: recap)` line. It never
  rings or counts, never shows your own act, a question or a message for you (those are in For
  you), and a flash or a dialog holds it back. What happened while busmon was closed is in `R`.
- **For you.** The For you panel, next to Agents, holds what only you can settle: an agent's
  open questions (`ask`), an agent waiting for your decision (`wait`), a 4-eyes gate you opened
  or that holds you (`gate`), and messages to you that you have not opened (`unread`). The
  header counts them (`♙ 4 for you · ● 2 unread`). Enter opens one. Every page reads
  `Label:  value`, with the values of a block in one column.
- **Quotas: stop before the limit.** The dispatch service reads every provider's usage windows
  (used %, reset) from local sources, every minute, with no model call. busmon's header shows one
  gauge per provider (`claude 5h 82% ~25m · codex 5h 96% held 03:10 · grok wk 59%`), filled in
  eighths of a cell and sliding to a new value in a true-colour terminal; `agentbus quota`
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
    gets, or at the latest the window's length after the last hit, since Kimi states no reset
    (a `billing cycle` limit states no length and is never held). An agy `Individual quota
    reached … Resets in …` holds until the reset it states; nothing ends it earlier, and the quota
    may be one model's, so if it holds agents that could still work, turn it off with
    `[providers.google] kind = "off"` in `quota.toml`.
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
  they appear in For you as `ask`, the header counts them (`♙ 2 for you`) and the agent row fades `waiting`.
  Press `!` or `w` (or Enter in the agent's detail) to open them (with no question open, `w` writes to master instead): one tab per question, `↑↓` or a digit to pick an option, Enter to
  send that answer at once, `Other…` to write your own, `←→` to change question, Esc to close. An
  answered question shows its answer and never comes back. `agentbus asks --open` lists them in a
  terminal, `agentbus answer <id> <question> --choice <label>` answers without busmon. Long texts
  (reports, messages) are shown as Markdown: headings, lists, paragraphs; a text written on one
  line gets a paragraph at each upper-case label (`DECISION 1 -`, `VERIFIED:`) and a list for
  inline choices (`(X) … (Y) …`).
- **An agent waiting for you.** When master (or any agent) needs your decision it declares
  `waiting`: it moves to the top of Agents with ♙ and the word `waiting` slowly fading in and out, For you lists it, the header counts it
  (`♙ 1 for you`), and the terminal bell rings once per new question, which herdr shows on the busmon tab.
  Open it (Enter on its row): its last report, the decision it needs, comes right after its
  status message. Answer with `@master …`; the agent then declares `working` again. It stays in
  For you until you have answered.
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
- **Requests: who owes whom an answer.** The Requests tab (`3`) groups threads by what they
  wait for: owed by you, waited on by you, tracked requests, reviews still gated, directives
  between agents, notices. Answered threads and those older than 24 hours are folded (`o` shows
  them): an older unanswered directive wakes nobody. Enter expands a group or opens a thread;
  `d` then `y` closes a thread with a reply from you, its history stays. A tracked request is
  closed by its owner (`agentbus request done`). The header carries the same counts: what you owe
  in orange (`2 requests waiting · oldest 3h`), what agents owe each other muted
  (`4 between agents`), each absent at 0.
- **The plan.** When master loads the batch's plan (`agentbus plan load docs/plans/<name>.json@<commit>`),
  the Plan tab (`4`) and `P` show its tasks by state (waiting for you, answered, blocked, in
  review, in progress, upcoming, done), each with its review, PR and asks. A merge waits for you
  only once its PR is linked ("merge PR N"); a question of yours you answered moves the task to
  answered, master to record. A task waiting for you has an
  `Action:` row: Enter or `s` opens the compose window to master, prefilled, to start it. A task
  reads done only when
  master records it (`agentbus plan done <task>`) or it needs no merge and is delivered and
  approved.
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

### Scenario D: A team that runs for days

Contexts fill up, clients update, quotas run out. Each case has its rule; none of them needs you
to watch a pane.

- **Context fill.** With the `statusLine` installed (section 3), Claude Code reports each
  session's context fill and busmon shows it next to the tokens (`88k 85%`). At 80%
  (`AGENT_BUS_CONTEXT_ALERT`, `0` turns it off) the agent gets one directive: at its next stopping
  point, commit its work, publish its hand-off with `agentbus report <self> --to master`, then
  declare it. master gets one notice, and the desktop one notification (`notify-send`, when it is
  on `PATH`). Once per agent and session; never during a pause.
- **Hand-off.** An agent declares its hand-off with the id of that report:
  `agentbus --project myapp handoff coder --report <id>`. busmon flags it `handoff`, and
  `agentbus --project myapp handoff` lists them. Any later report or non-idle status from the
  agent voids it. Then clear the agent: type `/clear` in its pane and point the fresh session at
  the report (`agentbus --project myapp reports <id>`).
- **Automatic `/clear` (on by default).** The dispatcher types that `/clear` itself, once per session, for a Claude Code
  agent whose hand-off stands, that is idle, with no open question or review, in a ready pane;
  then it proves the new session and tells the agent to read its hand-off. To turn it off for every
  project, set `auto_clear = false` under `[dispatch]` in `~/.config/agentbus/config.toml`; for one
  project, set it on the `up` line (`AGENT_BUS_AUTO_CLEAR=0 agentbus up myapp`). `up` builds the services' env file,
  `~/.config/agentbus/services/myapp.env`, from its shell: the `AGENT_BUS_*` variables except the
  project and agent names, plus the escalation target and the reload setting, which it keeps from
  the old file unless you change them (`--escalate-to`, `--reload`). When the result differs from
  the file it replaces it, so any other line added by hand is lost at the next `up`. Its lines are
  `KEY="value"`; an unquoted one stops the dispatcher on an invalid configuration. To turn it back on
  for that project, run `up` again without the variable.
- **Client updates.** When a client is updated under a running agent, busmon flags the agent
  `update`, health `client-updates` warns, and sentinel gets one directive. sentinel restarts
  each outdated agent on its role and profile, resuming its conversation:
  `agentbus --project myapp reload coder`. A reload runs only for an idle agent with no open
  work (no review, mission or accepted task; its own open question only with `--fresh`, since a
  resumed conversation keeps it) and never for the caller's own pane. It checks the relaunch
  before it stops anything, stops the agent with SIGTERM, relaunches it in its tab (a pop role
  through `agent-spawn`) and proves the new process. master may
  reload only sentinel. You may reload anyone: `agentbus --project myapp reload coder --as
  <your-name>` (`--dry-run` says what would happen, `--fresh` starts a new conversation).
  `agentbus up myapp --reload propose` makes every reload wait for you; `--reload off` leaves
  them to you alone. For every project at once, busmon's settings page (`C`, Services) has
  **Auto-restart on update**: `on` (the default), `propose` or `off`; a project's `--reload`
  overrides it. The dispatcher reads it at each update, so a change needs no restart. An agent
  that was busy when the update came is reloaded once it is idle: sentinel gets one more
  directive for it. Without a registered sentinel nothing restarts by itself: master asks you.
- **Quotas run out.** At 95% a provider's agents are held (scenario C). When the reset is more
  than 2 hours away (`AGENT_BUS_QUOTA_LONG_HOLD`), master tells you which held agents had work in
  flight, so you can move a role to one of its `fallback_profiles` in `roles.toml`:
  `agentbus --project myapp roles fallbacks coder` lists them, `agentbus --project myapp swap coder
  --profile codex` replaces the agent (every check before anything is closed: live identity, no
  open review, no mission in progress, no running turn without `--now`), `swap coder --back`
  returns it, and `agentbus --project myapp swaps` is the ledger. busmon flags `swapping`, then
  `fallback <profile>`. `agentbus --project myapp retire <role>` closes one role's tab with the
  same checks.
- **Health.** The dispatch service measures the project in the background: services, agent
  identities and launches, model drift, runaway work, command delivery, quota freshness,
  secret-like files tracked in Git, Redis exposure, versions, the herdr plugin.
  `agentbus --project myapp health` lists the checks, `health detail <id>` gives the evidence and
  what to do; busmon shows the pill in its header and the checks in F5. A missing or stale reading
  is `unknown`, never `ok`. A new failure wakes sentinel once; it may restart a failed service or
  refresh the model table, and escalates everything else to master.
- **Redis version.** Health `redis-version` compares the broker with the latest Redis release of
  its own major, read once a day from GitHub's release list. Behind in its major is a warning; a
  newer major is only named, since changing major is your decision. No check for 48 hours reads
  `unknown`. You update the broker yourself; sentinel never restarts or upgrades it.
- **Stall guard.** Three health checks, each after 15 minutes (`dispatch.stall_after`,
  `clear_stall_after`, `quiet_after` in `config.toml`; `0` turns one off). `team-stall` fails when every
  agent is idle, nothing is open (request, accepted task, ask, gate, pause, swap, hand-off, mission,
  waiting message) and the plan has a task ready for an agent: sentinel sends master one directive
  naming it. When only your own tasks are ready it is a warning for you. You get one notice if
  master cannot act (held, or blocked on a prompt), or if the stall lasts twice the threshold.
  `clear-stall` warns when a hand-off was not cleared, with the dispatcher's reason (when it is your
  focused pane, switch away). `quiet-agent` warns when an agent received work and wrote nothing
  since. Panes must be observed: where they are not (macOS), `team-stall` reads `unknown`.
- **History archive.** The dispatch service appends every stream (`cmd`, `report`, `status`,
  `notify`, `verdicts`, `health:log`, `swaps`) each minute to one JSONL file per UTC day in the
  project's state directory, `history/YYYY-MM-DD.jsonl` (0600 in 0700), so a thread outlives the
  stream's day and a half. `history.keep_days` (90, `0` = no archive) and `history.max_mb` (256)
  in `config.toml`. A field that looks like a credential is written as `[redacted]`, whole; this
  is best effort, so treat the directory as sensitive. A hole the dispatcher missed is a `gap`
  line, never silent. You may delete the directory at any time.
  `agentbus history` reads it, then the live streams: `--agent`, `--thread`, `--kind`, `--since`/`--until`
  (`3d`, `2026-10-30`, `14:20`), `--grep`, `--limit` (200), `--json`. On another host it sees only the
  live streams, and says so.
- **GitHub reachable.** Health `github` runs every 15 minutes when the project's Git remote is on
  github.com: `ssh -o BatchMode=yes -T git@github.com` with the service's own SSH identities, and
  `git -c credential.helper= ls-remote` on the repository's HTTPS URL with no prompt. A probe is
  reached when GitHub answers, even with "permission denied" or "authentication required" (the
  authentication is then unknown); only a network error is a failure. Both reached is `ok`, one
  only a warning for you (`Check your git remote and credentials; try: git ls-remote <your remote
  URL>`), both down `unknown` for an hour, then a warning. It never fails and never wakes sentinel,
  and it makes no claim about pushing. A dispatcher restart starts the hour over.
- **Tokens.** `agentbus tokens` shows the tokens spent per project, side by side, over 7 days;
  `--project myapp --by agent` (today) or `--by day` narrows it, `--json` for scripts. They are
  counted from the Claude Code transcripts of the sessions the bus has proved, every 5 minutes.
  An agent's model and context (`agentbus usage`) come from that same proved session, never from
  one a status merely names.
  busmon's `U` page shows the same with the account windows' 24-hour history. No cost is shown,
  by rule: on a subscription the figure would be fiction.
- **Backups.** `agentbus backup` writes one archive, `agentbus-backup-<host>-<time>.tar.zst`
  (0600), of every project of this host (or of the ones named with `--project`): each project's
  Redis keys read at one instant (keys with an expiry are skipped), `config.toml` and its
  siblings, the templates, each template's `roles.toml` and prompts, and the history archive.
  The broker's password and the services' env files are secrets: they are left out and listed
  in the archive's manifest, unless you pass `--secrets`, which encrypts them with
  [age](https://age-encryption.org) to a recipient (`--recipient age1...` or `backup.age_recipient`
  in `config.toml`; `age` must be on PATH). Only the public key is given: agentbus never creates,
  reads or keeps the private key, so keep it where you keep your other keys, not on the disk the
  backup protects; without it `secrets.age` cannot be opened by anyone. `restore --identity
  <your key file>` opens it. Archives go to `backup.dir` (default `~/.local/share/agentbus/backups`);
  a backup on the same disk does not survive the disk, so point `backup.dir` at another one (a
  mount, a synced folder). `backup.keep` (7) archives are kept, never the newest one covering a
  project. `agentbus backup list` shows them; `--dry-run` shows what a run would read and write.
  `agentbus restore <archive>` checks every member against the manifest first, then refuses a
  live project (its dispatch or conductor lease held, a service active, its herdr workspace open,
  an agent's process alive: run `agentbus down <p>` and close its workspace first), even with
  `--force`; when the service manager cannot be asked it refuses too (on a host that runs none,
  set `AGENTBUS_SERVICE_MANAGER=none`). A project's keys are restored in one step, all or nothing; keys already there, or a
  file that differs, are kept unless you pass `--force` (it asks first). Runtime keys (agents,
  services, wake records, holds) are not restored unless `--with-runtime`: `agentbus up <p>`
  writes them again. `--dry-run` prints each key's and file's action; `--redis-only` and
  `--files-only` narrow it. Restoring onto an older Redis than the archive's is refused: upgrade
  the broker first.
  A files restore writes a project's roles files where its archived template says they are: the
  absolute path of its `roles.toml` (a checkout, say `/data/projects/<p>/roles.toml`), not under your
  home. On another machine, check that path with `--dry-run` first; a differing file there is kept
  unless `--force`.
  To send each archive elsewhere, set `backup.after_command` in `config.toml` (for example
  `"restic backup /data/backups/agentbus"`, an `rclone copy`, an `rsync`; empty by default). It
  runs after each complete archive, in `backup.dir`, with no shell (write `sh -c '…'` for a pipe),
  given `AGENTBUS_BACKUP_ARCHIVE`, `AGENTBUS_BACKUP_DIR`, `AGENTBUS_BACKUP_PROJECTS` and
  `AGENTBUS_BACKUP_SCHEDULED`, and an environment without the broker's or anything named like a
  secret (`backup.after_env` lists names to keep, such as `["RESTIC_PASSWORD_FILE"]`), for at
  most `backup.after_timeout` (30m). A failure is recorded and warned about; the archive is never
  touched. These three settings are yours only: no variable sets them, agentbus never writes them,
  and no agent may edit `config.toml`.
  Backups are made by hand by default. `backup.schedule = "daily"` makes the dispatch service make
  one whenever a project of this host has no archive younger than a day (checked hourly, one
  archive for all the projects, whichever project's service gets there first). Set `backup.dir` in
  `config.toml` with it: a service's environment can resolve the default to another folder.
  Unattended, the secrets are included only when `backup.age_recipient` is set; with it but no
  `age` on the service's PATH, the archive is made without them and the failure recorded in
  `~/.local/state/agentbus/backup-last.json`. A restore is never scheduled.
  Health `backup` (every 15 minutes, per project) warns, for you, when no archive covering the
  project is younger than `backup.warn_after` (`168h`, a week; `0` turns this reminder off), when
  the latest scheduled backup that covered it failed (until a newer archive covers it, made by hand
  or not; one that left out the secrets for want of `age` clears only with an archive holding
  them), or when `backup.after_command` failed for its latest archive. Its detail names the folder
  it read. Sentinel never runs a backup or a restore.
- **Notifications away from the desk.** The dispatch service can tell you on your phone or desktop
  what waits for you ([spec](superpowers/specs/2026-11-03-notifications-design.md)): a decision
  (an open question, an agent declared `waiting`, a plan task for you), a team stall, a health
  check that fails, a notice only you can act on, a pane stuck on its client's prompt; a for-you
  warning only if you add `for-you` to `notify.events`. The targets live in
  `~/.config/agentbus/notify.toml`, which `agentbus notifications setup` writes for you on a
  terminal (or `add ntfy`, `add discord --webhook-file F`, `add desktop`; then `remove`, `rename`,
  `show-topic` for a second phone), never an agent; mode `0600` in a
  folder no one else can write: a topic, a token or a webhook is a secret, so it never goes in
  `config.toml`, on the bus or in a log, and no agent may read the file. busmon's C page, group
  Notifications, ends with an assistant per kind (`Set up ntfy`, `Set up Discord`, `Set up the
  desktop`): Enter shows the steps, the file's state (target names and kinds, never a topic or a
  URL), a table to copy with `y` (for ntfy a topic drawn on the spot, kept nowhere) and each
  target's last test; you may still paste it into the file yourself. `setup` and `add ntfy` draw the
  topic and show it, its link and a QR code to scan on your terminal only, never in a log; they turn
  `notify.on` on, list the target and send a test (`--no-test` skips it).

  ```toml
  [ntfy.phone]
  topic = "agentbus-7f3c9a1b2d"      # 12+ letters, digits, - or _: on a public server its name is its only lock
  # server = "https://ntfy.sh"       # the default; your own server here
  # token = "tk_..."                 # optional access token
  # self_hosted = true               # your own server: full text allowed there (never for ntfy.sh)

  [discord.team]
  webhook = "https://discord.com/api/webhooks/..."

  [desktop.here]                     # notify-send on this host
  ```

  Then in `config.toml` (or the C page of busmon, group Notifications):

  ```toml
  [notify]
  on = true
  targets = ["phone", "here"]
  # events = ["decision", "stall", "fail", "blocked", "prompt"]   # the default; add "for-you"
  # detail = "summary"           # or "full": an agent's text, scrubbed, only to a private target
  # delay = "2m"                 # what you answer at the desk within it never buzzes
  # max_per_hour = 6             # per target, for the whole host; fail and stall never held back
  # quiet = "22:00-07:30"        # local time; held, then sent as one message when it ends
  # quiet_exempt = ["fail", "stall"]
  ```

  `summary` sends no text an agent wrote ("master asks a question"). `full` adds it, scrubbed of
  URLs, paths and emails and cut to 120 characters, but only to the desktop or to an ntfy server
  you mark `self_hosted = true`: that mark is your word that the server is yours. ntfy.sh and
  Discord always get the summary. The first pass for a target sends nothing that already stood
  (it is in busmon); after that each new event is sent once per target, grouped in one message per
  pass. While a desktop target sends prompts, the dispatcher's own prompt bell stays silent, so a
  prompt never rings twice. `agentbus notifications status` shows each target, whether it is
  listed and its last send (never a URL); `agentbus notifications test [<target>]` sends one
  message now. The test is yours: agents are refused it, so on Linux it reads the broker's agents
  first and refuses when the broker is down. Health `notifications` (every 5 minutes, for you)
  warns when the file is open to others, a listed target is missing or refused, a send failed,
  or a source of the project cannot be read (then nothing is sent). Answers from the phone are not
  possible: two-way is a later spec.

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
4. **Finish.** master delivers the reviewed branch (`agentbus deliver`): you get the pull request
   to merge, the page to open it, or in local mode the `git merge` command. You merge.

For a one-line fix you can skip master: `@coder Fix the typo in the README title.`

### Large change: plan it as missions, let the conductor drive

A change that takes days must not depend on one agent's conversation: a context fills up, a
session restarts. The plan therefore lives in Git, and the conductor carries it out step by step.

1. **Ask for a plan, not code:**
   ```text
   @master Plan the change "auth-v2" as missions: write docs/changes/auth-v2/brief.md and
   docs/changes/auth-v2/missions.json on a branch, commit, and send me the commit. Do not start it.
   ```
   For a design-heavy change, have master pop the architect first (`agent-spawn architect
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
   by the conductor: master delivers it once reviewed (`agentbus deliver change/auth-v2`), and you
   merge.

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
| Spawn extra agent | `agent-spawn architect myapp` (from master) |
| Spawn a second coder | `agent-spawn coder2 myapp` (from master): two tasks at once, each in its own worktree, never one package in both |
| Every known project | `agentbus projects` |
| Questions waiting for you | `agentbus --project myapp asks --open` / `answer <id> <q> --choice <label>` |
| Pause, resume | `agentbus --project myapp pause --for 2h` / `resume` |
| Provider quotas | `agentbus quota` |
| Project health | `agentbus --project myapp health` / `health detail <id>` |
| Tokens spent | `agentbus tokens` / `--project myapp --by agent` |
| Back up this host's projects | `agentbus backup` / `backup list` |
| Restore an archive | `agentbus restore <archive> --dry-run`, then without it |
| Notification targets, a test message | `agentbus notifications status` / `notifications test [<target>]` |
| Declared hand-offs | `agentbus --project myapp handoff` |
| Reload an agent | `agentbus --project myapp reload coder --as <your-name>` |
| Move a role to a fallback | `agentbus --project myapp swap coder --profile <p>` / `--back` |
| Every command | `agentbus help` / `agentbus help <command>` |
| Purge project history | `busmon --project myapp --reset` |

---

## 8. Best Practices & Common Traps

1. **`AGENT_BUS_PROJECT` is Mandatory**: run `export AGENT_BUS_PROJECT=<name>` in agent shells to avoid repeating `--project`.
2. **Flag Syntax**: `--ref abc123`, `--ref=abc123` and the short form the help shows (`-r abc123`) all work. A single dash with a long name (`-ref`), bundled shorts (`-ab`) and attached values (`-rabc123`) do not.
3. **Positional Order**: arguments must follow the exact order: `agentbus status <agent> <state> <message>`. The message is always last.
4. **Naming Conventions**: project and agent names must match `^[a-z][a-z0-9_-]{0,31}$` (lowercase, starting with a letter).
5. **An exported `AGENT_BUS_*` stays with the services**: `agentbus up` copies the `AGENT_BUS_*`
   variables of its shell (except the project and agent names) into the project's env file, and
   the services keep one after you unset it in your shell, until the next `up` without it. The
   escalation target and the reload setting stay even then, until `--escalate-to` or `--reload`
   changes them.
6. **Delivery to your forge, or none**: the bus and the missions conductor need only Git (the
   conductor merges and tests on local branches and never pushes). master and the docs agent
   deliver a reviewed branch with `agentbus deliver`, and only when the 4-eyes approval names its
   exact commit. The forge is read from `origin` (`delivery.forge = "auto"` in
   `~/.config/agentbus/config.toml`, the target branch `delivery.base`, `main` by default):
   - **GitHub / GitLab**: it pushes the approved commit and opens the pull or merge request with
     `gh` or `glab` when installed and logged in (`gh auth login`, `glab auth login`); otherwise it
     pushes and gives you the page to open it;
   - **Bitbucket**: it pushes and gives you the page;
   - **local** (no `origin`, or `forge = "local"`): nothing is pushed or fetched; master gives you
     `git switch main && git merge --no-ff <sha>` and records the merge once you say it is done.
   A self-hosted GitLab whose host name does not start with `gitlab.` reads as local: give it such
   a name, or set `delivery.forge`, which then applies to every project on this machine.

---

## 9. The infra Role and Planned Specs

- **The `infra` role.** A pop role for Ansible, Terraform/OpenTofu and IT automation, on a manual
  profile: only exact syntax and lint commands run without a prompt; plans, checks, downloads and
  every change go through you, and secrets are denied. master pops it only with your yes. It was
  adopted from the reviewed draft in `examples/drafts/infra/`, whose README gives the adoption for
  another project: master asks you, then
  `agentbus --project myapp roles adopt infra --ask <id> --from examples/drafts`.
- **One spec, not built.** The [feedback loop](superpowers/specs/2026-10-29-feedback-loop-design.md)
  (`agentbus feedback`: read-only statistics over the reviews the bus retains; a skill change
  proposed as a pull request only after your yes). The
  [config file](superpowers/specs/2026-10-29-config-file-design.md) is built: one `config.toml` for
  your preferences, `agentbus config` to show each setting's value and where it comes from, and the
  local model endpoints (`[llm.<name>]`: `url`, `model`, `remote = true` for a host that is not
  loopback; a key or token is refused).

---

## 10. Licence

MIT, see [LICENSE](../LICENSE).
