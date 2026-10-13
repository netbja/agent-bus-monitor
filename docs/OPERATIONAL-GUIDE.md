# Operational Guide: Agent Bus Monitor (`agent-bus`)

This practical guide complements the `README.md`. It provides a complete step-by-step walkthrough to install, configure, and use `agent-bus-monitor`, the `herdr` workspace manager, the `herdr-plus` plugin, and day-to-day team workflows on new and existing projects.

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
     - Publish state/heartbeat: `agentbus status <agent> <working|idle|blocked|done> <message>`
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
- **100% Local, Ready for Remote**: Operates entirely on your local workstation (`127.0.0.1:6380`), while natively supporting remote servers (VDR, GPU clusters) via secure SSH port forwarding.
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
- registers **herdr-plus** with herdr (`herdr plugin install cloudmanic/herdr-plus --ref <tag>`)
  and checks that its Projects action exists;
- adds the `Ctrl+B` then `↑` binding, creating herdr's `config.toml` if herdr has never run, and
  tells a running herdr to reload it;
- clones the external role skills (`mattpocock/skills`, pinned) into
  `~/.local/share/agentbus/skills/mattpocock` and links the role skills. If one is still missing,
  the agent starts without it, with a warning;
- starts the Redis broker on `127.0.0.1:6380`.

Make sure `~/.local/bin` is on your `PATH`; `install` warns when it is not, or when another copy
shadows it.

### Step 3: Check, update, remove

```bash
./setup.sh status       # versions, PATH shadowing, skills, herdr-plus, key binding, Redis
./setup.sh update       # git pull --ff-only, then install; prints vA -> vB and the release notes
./setup.sh uninstall    # asks at every step; --yes = cautious defaults; --purge = Redis data too
```

busmon shows a discreet `↑ vX.Y.Z` in its header when a newer release exists. It checks once a
day; `AGENT_BUS_NO_UPDATE_CHECK=1` turns the check off.

`update` refuses a tree with uncommitted changes, then runs the `setup.sh` it just pulled. A new
herdr-plus pin reinstalls the herdr-plus that setup installed (a linked checkout is never replaced).

`uninstall` removes only what setup made, and has no `--dry-run` (run it without `--yes` and
answer each question):
- our binaries and our skill links (never a real directory);
- the key binding, if it is still exactly the block setup wrote; an edited one is kept;
- the external skills, if setup cloned them and they have no local changes.

It asks about herdr-plus separately, since herdr-plus is useful without agent-bus. For Redis it
offers: stop it and keep the data (the default), remove it together with its data, or leave it.

---

## 4. Network Topology: Local vs Remote

### 100% Local Setup (Default)
All processes run on the same workstation. Agents and `busmon` connect directly to `127.0.0.1:6380` (default password: `AgentBus2025!`).

### Hybrid Setup with Remote Servers (VDR / GPU)
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
   - starts the Redis broker if needed, links the role skills and writes the herdr-plus
     template (`scripts/bootstrap` underneath; its flags work: `--no-dispatch`, `--no-missions`,
     `--index`, `--cron`);
   - opens the workspace directly, with **busmon first and focused**, then:
     - `master`: team coordinator;
     - `coder`: implementation agent;
     - `foureyes`: code review agent;
     - `sentinel`: health and budget watch;
     - `dispatch`: wakes idle agents when work lands for them;
     - `missions`: the conductor for large changes.
   - Run again, it focuses the open workspace instead of opening a second one.

   busmon shows you as `♙ <Name> [human]`, with `· pilot` while you hold the pilot lease. With no
   `--as`, you are `Pilot [human]`.

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

---

## 6. Command Reference Summary

| Action | Command |
|---|---|
| Provision + open a team | `agentbus up myapp ~/code/myapp --as <your-name>` |
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

## 7. Best Practices & Common Traps

1. **`AGENT_BUS_PROJECT` is Mandatory**: Export `export AGENT_BUS_PROJECT=<name>` in agent shells to avoid repeating `--project`.
2. **Flag Syntax**: Always use double dashes with a space (`--ref abc123`, never `--ref=abc123` or `-ref abc123`).
3. **Positional Order**: Arguments must follow exact ordering: `agentbus status <agent> <state> <message>`. The message is always last.
4. **Naming Conventions**: Project and agent names must match `^[a-z][a-z0-9_-]{0,31}$` (lowercase, starting with a letter).
