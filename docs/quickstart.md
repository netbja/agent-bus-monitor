# Quick start: a team on your project in five minutes

This guide takes a new operator from a clean machine to a first task done, an answered question and
a stopped project. For everything else, read the [README](../README.md) and the
[operational guide](OPERATIONAL-GUIDE.md).

**Where agent-bus stands.** Keep these limits in mind before you start:

- **It is v0.x.** Commands and screens still change between releases, so read the
  [CHANGELOG](../CHANGELOG.md) when you update.
- **It is built for one operator.** One person runs a team of agents on their own machine. It has
  no accounts and no access control: the bus is a Redis bound to `127.0.0.1`.
- **Linux comes first.** It is developed and used daily on Linux. The installer also runs on macOS,
  but some of the dispatcher's checks read `/proc`, which only Linux has. On macOS no agent's
  process is verified, so the dispatcher never wakes an agent there.

Every output below was printed by a command that was run in a scratch folder, on a scratch project
called `myshop`, and each output block shows the exact command that printed it. Two kinds of
command were only shown, not run, because they would change the machine or start real agents:
the real install and the real `agentbus start`. Each is labelled **Not run for this guide**, and
its dry run is shown instead. The bus steps in §3 and §4 ran on a disposable broker. Paths are
shortened to `~/…`, and your own ids, times and paths will differ.

## 1. Install (about 2 minutes)

You need Linux (or macOS), Go (the version in `go.mod`), Docker with Compose, Git, curl, and at
least one agent CLI: Claude Code (`claude`), Codex (`codex`), Grok or Kimi Code.

```bash
git clone https://github.com/netbja/agent-bus-monitor && cd agent-bus-monitor
./setup.sh install --install-herdr     # Not run for this guide: it installs for real
```

`--install-herdr` also installs herdr, the terminal workspace the agents run in, when it is
missing. To see first what would change, add `--dry-run`. On a machine that already had
everything, the dry run printed this:

```text
$ ./setup.sh install --install-herdr --dry-run
==> 1/5 Checking system platform & toolchain
    ✓ Platform: linux / amd64
    ✓ git: git version 2.56.0
    ✓ curl: curl 8.18.0 (x86_64-redhat-linux-gnu) libcurl/8.18.0 OpenSSL/3.5.9 …
    ✓ go: 1.26.10-X:nodwarf5 (go.mod requires 1.26.3)
    ✓ docker: daemon is running
    ✓ docker compose: plugin available

==> 2/5 Checking herdr workspace manager
    ✓ herdr: herdr 0.9.5 (~/.local/bin/herdr)

==> 3/5 Building and installing agentbus & busmon, linking the herdr plugin
    • [dry-run] would compile ./cmd/agentbus -> ~/.local/bin/agentbus (and ~/agent-bus-monitor/agentbus)
    • [dry-run] would compile ./cmd/busmon -> ~/.local/bin/busmon (and ~/agent-bus-monitor/busmon)
    ...
==> 4/5 Checking Redis broker service
    ✓ Redis broker: active and listening on 127.0.0.1:6380

==> 5/5 Scanning installed AI coding agents
    ✓ claude: available (~/.local/bin/claude)
    ✓ codex: available (~/.nvm/versions/node/v24.10.0/bin/codex)
    ...
 Dry run complete — nothing was changed (0 missing, 2 warning(s))
```

If you use Claude Code, also run `agentbus statusline --print-install` (not run for this guide)
and add the block it prints
to `~/.claude/settings.json`. Without it, agent-bus cannot read Claude Code's rate limits or each
session's context fill.

Then tell the bus who you are. In busmon you write as a person, under this name:

```console
$ agentbus identity set alice
recorded: you are alice on the bus (~/.config/agentbus/config.toml)
```

## 2. Start a team (about 1 minute)

The smallest useful team has three roles:

- **master** coordinates: it takes your requests, gives work to the others and asks you to decide.
- **coder** implements.
- **sentinel** watches the project's health.

Run this in your project's folder:

```bash
cd ~/code/myshop
agentbus start --roles master,coder,sentinel     # Not run for this guide: it starts real agents
```

`agentbus start` works out the project's name from the folder, takes your name from step 1, shows
what it will do, and asks before it starts anything. With `--dry-run`, it shows the plan and
starts nothing (`--yes` takes the deduced values without asking):

```console
$ agentbus start --dry-run --roles master,coder,sentinel --yes
agentbus start — ~/code/myshop

  Project   myshop  (from the folder's name)
  Folder    ~/code/myshop
  You       alice  (recorded (config.toml))
  Team      master coder sentinel  (--roles)

--dry-run: nothing written, nothing started.
```

Without `--roles`, the team is every boot role. That adds **foureyes**, the reviewer, and is what
you want once you need reviews. Each change then gets a second pair of eyes before master
delivers it.

Without `--dry-run`, `start` runs `agentbus up`. That command starts the project's background
services, opens a herdr workspace and starts one agent per tab, with **busmon** (the dashboard)
first. busmon's tour walks you through its screen once; F1 then `t` shows it again. The first
time Claude Code or Codex opens a folder, it shows its own trust dialog in the agent's pane.
`up` names that pane, and you answer the dialog there.

## 3. Your first task, end to end (about 1 minute)

In busmon, press `i` and type a directive to master. The `@` names who wakes:

```text
@master add a GET /healthz endpoint that returns 200 ok, with a test
```

Press Enter. You never wake an agent by hand: the dispatcher types a one-line wake-up into
master's pane, and master reads the message from the bus. Master turns it into a tracked request
for coder, coder implements it, and both report back. busmon's **Activity** panel shows each step,
and **Board** shows the task's state.

You can follow the same steps from a shell. Outside the project's herdr workspace, name the project
first: export `AGENT_BUS_PROJECT`, or pass `--project myshop` to each command. Inside the workspace
it is already set. The run below was made on a disposable broker, so the agents' commands were
typed by hand, each as its agent (`AGENT_BUS_AGENT=…`), to show what each one writes to the bus:

```console
$ export AGENT_BUS_PROJECT=myshop
$ AGENT_BUS_AGENT=master agentbus request send healthz coder "Add GET /healthz returning 200 ok, with a test."
1793943942464-0
$ AGENT_BUS_AGENT=coder agentbus subscribe --drain coder --brief
1793943942464-0 master [directive task=healthz]: Add GET /healthz returning 200 ok, with a test.
$ AGENT_BUS_AGENT=coder agentbus request accept healthz
$ agentbus board
TASK             OWNER        STATE     BRANCH                   AGE
healthz          coder        accepted                           0s ago
$ AGENT_BUS_AGENT=coder agentbus request done healthz "GET /healthz added, test green"
1793943942483-0
$ AGENT_BUS_AGENT=coder agentbus report coder --to master "healthz done: GET /healthz returns 200 ok, test added"
1793943942488-0
$ agentbus board
TASK             OWNER        STATE     BRANCH                   AGE
healthz          coder        done                               0s ago
$ agentbus reports
1793943942488-0  coder        note  "healthz done: GET /healthz returns 200 ok, test added"  → master pending
```

`pending` means master has not read the report yet. It reads `delivered` once master has drained
it. `agentbus board` and `agentbus agents` show the tasks and the team at a glance.

Agents never push or merge on their own. When a change is ready, master asks you, and the merge is
your decision (see the README's *Pull requests and forges*).

## 4. Answer a question (30 seconds)

When an agent needs a decision from you, it asks a **structured question** rather than burying it
in a report. busmon then does three things:

- it rings the bell;
- it lists the question first in **For you**;
- it counts the question in the header.

To answer it:

1. Select the question in **For you** and press Enter. You can also press `!` or F4 from anywhere.
2. Press an option's number (`1`–`9`) to send it at once, or move with `↑`/`↓` and press Enter.
3. For an answer in your own words, press `o`: it opens the compose window, and Ctrl+S sends it.

Your answer goes back to the agent that asked, and it wakes.

The same works from a shell, in the same session as §3 (`AGENT_BUS_PROJECT` still exported). The
run below was also made on the disposable broker: master writes its question to `q.json` and asks
which path to use, and you answer it:

```console
$ cat > q.json <<'EOF'
{"title":"healthz path","questions":[{"id":"path","header":"Path","question":"Which path for the health endpoint?","options":[{"label":"/healthz","recommended":true},{"label":"/status"}]}]}
EOF
$ AGENT_BUS_AGENT=master agentbus ask master --file q.json
1793943942504-0 open
$ agentbus asks --open
1793943942504-0  master  open  healthz path (<1m ago)
                   path  Path  open
$ agentbus answer 1793943942504-0 path --choice /healthz --as alice
answered 1793943942504-0/path; 0 question(s) still open
$ AGENT_BUS_AGENT=master agentbus subscribe --drain master --brief
1793943942483-0 coder [reply 1793943942464-0]: GET /healthz added, test green
1793943942488-0 coder [directive 1793943942488-0]: Report 1793943942488-0 for you (full text: agentbus reports 1793943942488-0): healthz done: GET /healthz returns 200 ok, test added
1793943947587-0 alice (person) [reply 1793943942504-0]: Answer to "Path": /healthz
```

Master's drain also picks up what coder sent it in §3: the done reply and the report's notice.
The last line is what master receives: your answer, marked as coming from a person.

## 5. Stop

These are the ways to stop, from the lightest:

- **Leave busmon** with `q` or Ctrl+C. The team keeps running; open busmon again with
  `busmon --project myshop`, or pick the project in herdr's Projects picker (Ctrl+B then `↑`).
- **Put the project to sleep:** `Z` in busmon, or `agentbus suspend`. This pauses the project,
  lets the running turns end, records a snapshot and stops the services. Agents and the workspace
  are kept. `Z` again, or `agentbus resume`, brings the project back. The real suspend and resume
  were not run for this guide; here is the suspend's dry run:

  ```console
  $ agentbus suspend --project myshop --dry-run
  suspend myshop: pause now, let running turns end (up to 10m0s), record a snapshot, stop the services
  myshop: would pause, wait up to 10m0s for quiet, stop the services and write ~/.local/state/agentbus/myshop/suspend.json
  ```

- **Stop the services only:** `agentbus down <project>`. This deletes nothing. On a project
  with no services installed, it says so:

  ```console
  $ agentbus down myshop
  no service installed for myshop: nothing to stop
  ```

The agents' panes stay open in herdr until you close them. Nothing on the bus is deleted unless
you ask for it with `busmon --delete <project>`, which asks for confirmation first.

## Next

- [README](../README.md): what each piece does, the first-run notes, and the settings.
- [Operational guide](OPERATIONAL-GUIDE.md), section 6: a small task and a multi-day change,
  step by step.
- `agentbus help <command>`: the usage, flags and examples of every command.
