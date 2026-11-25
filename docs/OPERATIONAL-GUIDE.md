# Operations guide: running a team with agent-bus

**English** · [Français](OPERATIONAL-GUIDE.fr.md)

This guide is for the person who runs the team: starting a project, giving it work, watching it,
merging what it delivers, updating the tools and stopping. The [README](../README.md) covers
installation and what each piece is. `agentbus help <command>` gives any command's exact flags.

In the examples the project is `myapp`. `-p myapp` is short for `--project myapp`; you can
`export AGENT_BUS_PROJECT=myapp` instead.

## 1. The picture

- **Your machine runs, per project:**
  - **the holder**, a user service that hosts every agent's terminal;
  - **the dispatcher**, a service that wakes agents when work waits for them, escalates silences,
    relaunches dead agents and watches quotas, context and health;
  - **the missions conductor**, a service that drives large changes.
- **The agents** (master, coder, foureyes, sentinel, and pop roles on demand) talk over the Redis
  bus.
- **What they never do:**
  - nothing types into an agent's terminal except the dispatcher's one-line wake (and `/clear` after a
    declared hand-off, `/compact` when none comes);
  - the dispatcher never runs repository code.
- **You** watch in `busmon`, answer in busmon, and step into an agent's terminal with `t` (busmon) or
  `agentbus attach`.
- **Closing busmon** or your terminal stops nothing: the services and the agents keep running.

## 2. Start a project

From the project's folder:

```bash
agentbus start              # deduce, show, ask only what is missing, then start and open busmon
agentbus start --dry-run    # the plan only
agentbus start --yes        # take the deduced values
```

`start` deduces the project's name from the folder, your name, and the team, which is the boot roles
or `--roles master,coder,foureyes:review`. It then runs `agentbus up myapp <path>` and opens busmon.

`agentbus up myapp ~/code/myapp` does the same without the deduction, and without opening busmon.
Its useful flags:

- `--as <your-name>`;
- `--resume` resumes the agents' sessions;
- `--escalate-to <agent>` names who gets escalations (master by default);
- `--reload on|propose|off`;
- `--dry-run`.

**What to expect the first time:**

- **The client's trust dialog.** When Claude Code or Codex has never opened the folder, `up` starts that
  client's first agent alone and says which agent waits on its trust dialog. Attach to it (`t` in
  busmon), answer, and its other agents start.
- **busmon needs a project.** Run `busmon -p myapp`. `busmon --list` lists the broker's projects, and
  `busmon --new` the templates.
- **Who you are.** busmon writes as you (`--as`, `AGENT_BUS_HUMAN`, or `agentbus identity set
  <name>`, else `person.name`, then `pilot`).

**Reach an agent:**

```bash
agentbus -p myapp attach master            # F12 then d comes back; F12 F12 sends one F12
agentbus -p myapp attach coder --observe   # watch only
agentbus -p myapp screen coder --lines 20  # its last screen, read-only
```

The prefix key is `attach.prefix` (default `f12`). Kimi and agy roles need `--prefix <key>`, with a
key the client lets through: `f1`-`f12`, `ctrl+<letter>`, `ctrl+\` or `ctrl+]`.

**Grow or shrink the team.** master pops a role when the work needs it. You can do it too:

- `agentbus -p myapp pop architect` adds a role, and `--profile <p>` picks one of its profiles;
- `agentbus -p myapp retire architect` removes it;
- a finished pop is closed by the dispatcher itself.

## 3. Give the team work

| | Small task | Large change |
|---|---|---|
| Size | an hour to a day; one feature or fix | several days; several agents in parallel |
| Who drives | master, in its conversation | the conductor, from a plan in Git |
| What you do | write the request, answer questions, merge | approve the plan, decide escalations, merge |

### A small task: ask master

1. **Write the request in busmon** (`i`), with the goal and how you will judge it done:
   ```text
   @master Add CSV export to the reports page. Done when: an Export button downloads the
   current filter as CSV, with tests, reviewed, delivered as a PR.
   ```
   You can also attach to master and type there.
2. **master dispatches.** coder takes the task, works test-first on its own branch, then challenges
   two reviewers.
3. **Watch** the board and the activity in busmon. When an agent needs a decision, master asks you
   with a question you answer in busmon (*For you*).
4. **Delivery.** Once both reviewers approve the exact commit, master delivers it, and the PR appears in
   your merge queue (section 5).

### A large change: missions

A change that takes days must not depend on one conversation, so its plan lives in Git:

1. **Ask for a plan, not code:**
   ```text
   @master Plan "auth-v2" as missions: docs/changes/auth-v2/brief.md and missions.json on a
   branch, committed; send me the commit. Do not start it.
   ```
   For a design-heavy change, master pops the architect first.
2. **Review the plan.** Each mission names:
   - its `agent` and its `reviewer` (never the same);
   - its `depends_on`;
   - the `allowed_paths` it may touch;
   - its `test`;
   - a `brief`.
   The change has its own `test`, and optionally `"docs": "docs"` to have the docs agent update the
   documentation at the end.
3. **Load it** at the reviewed commit:
   ```bash
   agentbus -p myapp mission load docs/changes/auth-v2/missions.json@<commit>
   ```
   The conductor then works mission by mission:
   - it sends each mission when its dependencies are in, independent ones in parallel;
   - it checks each delivery (allowed paths, tests green);
   - it opens the review;
   - it merges the approved mission into `change/auth-v2`, never into your base;
   - it runs the change's suite.
4. **Follow it:** `agentbus -p myapp mission status auth-v2`, `mission show auth-v2/t3`.
5. **A blocked mission** (failed tests, a rejected review, too many attempts) goes to master, who decides
   or asks you:
   - `mission retry <change>/<id>`;
   - `mission reassign <change>/<id> <agent>`;
   - `mission skip <change>/<id> --reason …`;
   - `mission abort <change> --reason …`.

   To change the plan, commit a new `missions.json` and load it again. Unchanged missions keep their
   state.
6. **The end.** master gets a `complete` notice, and delivers `change/auth-v2` once it is reviewed.

## 4. Watch the team

**busmon** (`busmon -p myapp`):

- **The header** shows the services. `waiting-holder` means the dispatcher waits for the holder, and
  `holder-behind` means agents still run on an older build (section 7).
- ***For you*** lists what waits for you: questions, decisions, PRs to merge, notices.
- **The agents** show each agent's state:
  - `working`;
  - `idle`;
  - `blocked`;
  - `waiting`, meaning a person must decide;
  - `done`.

  Its context fill and quota show beside it.
- **The Plan tab** (`4`; `6` under 80 columns) shows the loaded plan as a roadmap: NOW, APPROVED,
  NEXT by priority then file order, LATER (blocked, then low priority) and DONE, folded until `d`.
  Enter opens a task; `p` or `P` opens the same roadmap as a drawer. From 130 columns a Releases
  column sits on its right (`V` shows or hides it): the next release's decided ideas, the current
  plan, the past releases; Enter opens a release's plan, read only.
- **Keys:**
  - `F3` the services and the holders, with their logs (Enter);
  - `F5` measured health;
  - `M` every project of this machine;
  - `U` tokens;
  - `H` history;
  - `F1` every key.

**From a shell:**

```bash
agentbus -p myapp agents                  # declared states
agentbus -p myapp service status          # dispatch and missions: state, last record, log path
agentbus -p myapp service logs dispatch -n 100
agentbus -p myapp health                  # measured health and its notes
agentbus quota                            # every provider's usage windows
agentbus -p myapp merge-queue             # what waits for your merge
```

**Quotas.** The dispatcher reads usage windows locally:

- near the limit (`dispatch.quota_warn`, 85 %) the agents holding work are told to finish their step;
- at `dispatch.quota_hold` (95 %) that provider's agents are held until the window resets;
- `agentbus swap <role> --profile <fallback>` moves a role to another profile, and `--back` returns it.

**Pause.** `agentbus -p myapp pause --for 2h` (or `--until 18:00`, `--until-reset`) wakes nobody
until then, and `agentbus -p myapp resume` ends it early.

**A stuck agent:**

1. Read its screen (`agentbus screen <agent>`), or attach.
2. If it sits on a client prompt, answer it there.
3. If its process died, the dispatcher relaunches it in a new terminal (self-repair, at most
   `dispatch.self_repair_per_day` a day). `agentbus -p myapp self-repair reset <agent> --as <you>`
   clears a crash loop.

## 5. Review, delivery and merging

- **4-eyes.** Every change is reviewed by two reviewers other than its author. A verdict names
  the exact commit it read (`agentbus verdict --head <sha>`); a push after an approval needs a new
  approval. `agentbus -p myapp verdicts --subject <s>` shows the state.
- **Delivery.** `agentbus deliver <branch>` belongs to master and the docs agent. It pushes the
  approved head and opens the PR:
  - with `gh` on GitHub, `glab` on GitLab;
  - with the page to open when the CLI is missing, and on Bitbucket;
  - in local mode, with no `origin`, it gives you the `git merge` to run.
- **The base.** The PR goes to the project template's `[delivery] base` (in
  `~/.config/agentbus/projects/<project>.toml`), else `delivery.base` (default `main`).
  - `agentbus config --project myapp` shows which applies.
  - After changing the template, run `agentbus -p myapp service restart missions`.
- **Merging is yours.** The merge queue (`agentbus merge-queue`, and *For you* in busmon) lists each PR
  on an approved head, with its reviewers. You merge on the forge.
- **Promotions.** With a `dev` base, `main` moves only by master's fast-forward to a released `dev`
  head, and only on your yes.
- **The CI loop** (GitHub with `gh`):
  - when a PR's checks have finished and one failed, the author is woken with the failing checks and
    their links;
  - after `ci.max_handoffs` hand-offs (default 3) the loop stops for that PR, and you get a notice;
  - `agentbus -p myapp ci reset <pr>` gives the author more tries, and is yours alone;
  - a check that never finishes keeps a PR silent: silence is not "green";
  - `ci.loop = false` turns the loop off.
- **The run budget** is off by default. Set `run.max_duration` (e.g. `8h`) or `run.max_deliveries`.
  - At the limit, the run drains: master is told, agents can no longer send new requests, and you
    get a notice.
  - It stops when nothing is in flight, or after `run.drain_max` (2h), and the project pauses.
  - `agentbus -p myapp run` shows the run, and `agentbus -p myapp run reset` starts a new one.

## 6. A team that runs for days

- **Context.**
  - At `dispatch.context_alert` (80 %, Claude Code only) an agent writes a hand-off report and
    declares it (`agentbus handoff`).
  - The dispatcher then types `/clear` (`dispatch.auto_clear`, on by default), and the agent
    resumes from its report.
  - No hand-off 10 minutes after the fill reached `dispatch.context_urgent` (90 %): the dispatcher
    types `/compact` into the idle pane, once, and tells master (`dispatch.auto_compact`, on by
    default, yours to turn off in busmon's settings).
- **Reload after a client update.** `agentbus -p myapp reload <role>` restarts an idle agent on its
  role, resuming its session; `--fresh` starts a new one. sentinel proposes or does it, following
  `--reload on|propose|off`.
- **Notifications on your phone or desktop.** Set them up with `agentbus notifications setup`, then
  `agentbus notifications test`. The targets live in `notify.toml`, and `notify.*` chooses which events
  are sent.
- **Backups.** `agentbus backup` writes one archive of every project of this machine: Redis keys,
  settings, templates, roles and history, with secrets left out and listed.
  - `backup.schedule` makes it automatic.
  - `agentbus restore <archive>` puts it back. It refuses a live project.
- **History.** `agentbus -p myapp history` re-reads the past, `history.keep_days` and `history.max_mb`
  bound the archive, and busmon's `H` shows it.
- **After a reboot.**
  - With `services.start_at_login` on, the services and the holder start at login.
  - The dispatcher then judges the stop. After an unplanned stop it relaunches the agents on their
    sessions, when `services.unplanned_restart` allows it, the repository is intact, and it is
    within `services.unplanned_max` (3 restarts in 24 h). Otherwise it suspends the project.
  - You are told once per boot, and `agentbus -p myapp resume` brings a suspended project back.
  - Without it, `agentbus up myapp --resume` brings the team back.
- **Before a planned reboot:**
  - `agentbus suspend --reason "OS updates"` pauses every project, lets running turns end, records a
    snapshot and stops the services. `--project <p>` suspends one project.
  - After the reboot, `agentbus resume --all` (or `agentbus -p myapp resume`) brings back the
    services, then the agents on their sessions, then lifts the pause.

## 7. Update the tools

`agentbus update` installs the latest release tag, with a snapshot to roll back to
(`--check` prints the plan, `--rollback` restores, `--list` shows the snapshots). It refuses a
`dev` build, which is updated from its clone. Its automatic mode is not built. The path from a
clone:

1. **Update and install:**
   ```bash
   cd ~/agent-bus-monitor && ./setup.sh update
   ```
   It pulls with `--ff-only` and rebuilds. It then lists this machine's dispatch and missions services
   still on the old build, and restarts them on your yes. It never touches holders or agents.
2. **The new holder.** On its new build, the dispatcher starts that build's holder. The agents stay
   on the old holder until you move them, and busmon's header says `holder-behind`.
3. **Move the agents**, one at a time, each resumed on its session:
   ```bash
   agentbus holder drain myapp
   ```
   It moves only idle agents. Exit code 3 means some were skipped (busy, or with background work
   running): run it again later. `--ignore-background` moves an agent whose background work is
   running or unknown, and is yours alone.
4. **The old holder goes.** When every agent is on the new build, `holder drain` removes the old
   build's holder itself. `agentbus -p myapp service gc` does the same by hand, and health's
   `holder-leftovers` check points to it.
5. **Reopen busmon** so it runs the new binary.

`agentbus -p myapp service restart holder --kill-agents` restarts the holder in one go and kills
every agent it holds. It is refused without the flag. `agentbus up myapp --resume` then brings the
agents back.

## 8. Stop

| What | Command |
|---|---|
| Nobody woken for a while | `agentbus -p myapp pause --for 2h` / `resume` |
| Every project asleep, to be resumed | `agentbus suspend` / `agentbus resume --all` |
| Stop a project's services, holder and agents | `agentbus down myapp` (deletes nothing; `agentbus up myapp --resume` brings it back) |
| Tell the agents the work is over | `agentbus -p myapp shutdown` (refused while work is in flight) |
| Remove a project's services | `agentbus -p myapp service remove` |
| Delete a project's bus keys | `busmon --delete myapp` (only asleep; backs it up, then asks) |
| Remove agent-bus from the machine | `./setup.sh uninstall` (asks at each step) |

## 9. Traps

1. **The project is required.** Pass `-p <p>` or export `AGENT_BUS_PROJECT`. Names are lowercase: a
   letter, then letters, digits, `-` or `_`, 32 at most.
2. **Arguments have an order.** In `agentbus status <agent> <state> <message>`, the message comes
   last. `--ref abc`, `--ref=abc` and `-r abc` work, while `-ref abc` and bundled short flags do not.
3. **An exported `AGENT_BUS_*` stays with the services.** `agentbus up` copies them into the
   project's environment file. Unsetting one in your shell changes nothing until the next `up`
   without it.
4. **An attached terminal is the agent's.** What you type there goes to the agent's client. Run
   `agentbus` commands from your own shell.
5. **Silence from the CI loop is not green.** The loop reads GitHub only, and acts on a PR only once
   all its checks have finished.
6. **A self-hosted GitLab** whose host name does not start with `gitlab.` reads as no forge. Name it
   so, or set `delivery.forge`, which applies to every project on the machine.

## 10. Commands at a glance

| Task | Command |
|---|---|
| Start, stop | `agentbus start`, `agentbus up <p> [--resume]`, `agentbus down <p>` |
| Agents | `agentbus -p <p> agents`, `attach <a>`, `screen <a>`, `pop <role>`, `retire <role>`, `reload <role>`, `swap <role> --profile <f>` |
| Services | `agentbus -p <p> service status`, `service logs <kind> -n 100`, `service restart <kind>`, `agentbus holder drain <p>`, `service gc` |
| Work | `agentbus -p <p> board`, `request`, `mission load\|status\|show`, `merge-queue`, `ci reset <pr>`, `run` |
| Questions | `agentbus -p <p> asks --open`, `answer` (busmon is easier) |
| Watching | `busmon -p <p>`, `agentbus -p <p> health`, `agentbus quota`, `agentbus tokens`, `agentbus projects` |
| Sleep | `agentbus -p <p> pause`, `resume`, `agentbus suspend`, `agentbus resume --all` |
| Safety | `agentbus backup`, `agentbus restore`, `agentbus notifications` |
| Settings | `agentbus config [--project <p>]`, busmon `C` |
