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

> **Quick Automated Setup**: You can run `./install.sh` (or `./install.sh --dry-run`) directly from the repository root to perform preflight checks, herdr & herdr-plus setup, binary builds, and Redis container launch in one idempotent run.

Ensure you have the following installed:
- **Go** (version 1.22 or newer)
- **Docker** and **Docker Compose**
- **Python** (version 3.11 or newer)
- **Git**, **curl**, **make**

### Step 2: Install `herdr`

`herdr` is the terminal workspace and multiplexing manager tailored for AI coding agents.

1. Download and install the official binary:
   ```bash
   curl -fsSL https://herdr.dev/install.sh | sh
   ```
2. Verify `herdr` is in your `PATH`:
   ```bash
   herdr --version
   ```

### Step 3: Install and Build the `herdr-plus` Plugin

`herdr-plus` provides fuzzy project selection and multi-tab workspace orchestration.

1. Clone `herdr-plus` into your local tools directory:
   ```bash
   mkdir -p ~/Tools/herdr-plugins
   git clone https://github.com/cloudmanic/herdr-plus.git ~/Tools/herdr-plugins/herdr-plus
   ```

2. Compile the plugin binary:
   ```bash
   cd ~/Tools/herdr-plugins/herdr-plus
   make build
   ```
   *Verify that the executable exists at `~/Tools/herdr-plugins/herdr-plus/bin/herdr-plus`.*

3. Configure the shortcut in `~/.config/herdr/config.toml`:
   ```toml
   # herdr-plus Projects picker: Prefix (Ctrl+B) then Up Arrow
   [[keys.command]]
   key = "prefix+up"
   type = "plugin_action"
   command = "cloudmanic.herdr-plus.projects"
   description = "herdr-plus: projects"
   ```

### Step 4: Build and Install `agent-bus-monitor`

1. Navigate to the `agent-bus-monitor` repository:
   ```bash
   cd /data/projects/agent-bus-monitor
   ```

2. Compile and install Go binaries to your `PATH`:
   ```bash
   go build -o busmon ./cmd/busmon
   go build -o agentbus ./cmd/agentbus
   go install ./...
   ```

3. Symlink role skills for your AI clients:
   ```bash
   scripts/link-role-skills.sh
   ```

4. Start the local Redis broker:
   ```bash
   docker compose up -d
   docker compose ps
   # Verify redis:8-alpine is listening on 127.0.0.1:6380
   ```

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

Assume your target project is at `/data/projects/myapp`.

1. **Create and attach the project's herdr session**:
   *Note: herdr registers plugins per active session, so start the session first.*
   ```bash
   herdr --session myapp
   ```

2. **Provision with `bootstrap`** (from inside the herdr session):
   ```bash
   /data/projects/agent-bus-monitor/scripts/bootstrap new myapp /data/projects/myapp
   ```
   This command:
   - Ensures the Redis broker is running.
   - Links `herdr-plus` into the active session.
   - Generates the workspace template at `~/.config/herdr-plus/projects/myapp.toml`.
   - *(Optional flags: `--index` to build code index, `--cron` for daily sentinel review).*

3. **Open the Team Workspace**:
   - Press `Ctrl+b` then `Up Arrow` (or run `herdr plugin action invoke cloudmanic.herdr-plus.projects`).
   - Select `myapp`.
   - `herdr` automatically spawns all configured tabs:
     - `master`: Team coordinator
     - `coder`: Implementation agent
     - `foureyes`: Code review agent
     - `sentinel`: Health & budget monitoring
     - `busmon`: Live TUI monitor
     - `dispatch`: Automatic wake daemon

4. **Assign the First Task**:
   - Switch to the `busmon` tab.
   - In the bottom `INPUT` line, type:
     ```text
     @master Initialize the project structure and prepare the roadmap.
     ```
   - The master picks up the directive, updates its status, creates tasks on the `:board`, and commands `coder` via `agentbus cmd coder ...`.

---

### Scenario B: Resuming an EXISTING Project (`recall`)

1. **Open the project's herdr session**:
   ```bash
   herdr --session myapp
   ```

2. **Recall the project**:
   ```bash
   /data/projects/agent-bus-monitor/scripts/bootstrap recall myapp
   # or simply: /data/projects/agent-bus-monitor/scripts/bootstrap myapp
   ```

3. **Re-open tabs** via the `Projects` picker (`Ctrl+b` then `Up`).
   > **Note**: Recall restores tabs with fresh agent sessions. To resume an earlier conversation in Claude Code, use native resume:
   > ```bash
   > claude --resume  # then pick myapp:master or myapp:coder
   > ```

---

## 6. Command Reference Summary

| Action | Command |
|---|---|
| Open live dashboard | `busmon --project myapp` |
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
