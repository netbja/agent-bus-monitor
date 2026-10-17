---
name: agent-bus-master
description: "Run from the MASTER agent (the pilot-lease driver) inside herdr to coordinate peer agents over the Agent Bus: know when `agentbus dispatch` wakes idle peers for you and when you must wake one through its herdr pane yourself (the bus alone cannot reach a peer that is not armed), handle the dispatcher's `escalation` and `blocked` notices, resync an agent by injecting text into its herdr pane, unblock an agent stuck on an on-screen question, dispatch a multi-task implementation plan across an implementer + reviewer pair, and send tracked requests whose acceptance and completion are recorded rather than inferred. Use when you hold the pilot lease and need to drive other agents' panes, or when you're running a task-by-task plan through the bus."
---

# Agent Bus — Master Skill

You are the **master** (you hold the pilot lease), running inside herdr. This skill drives peer
agents' herdr panes over the Agent Bus.

## Client portability

Roles are independent of clients/providers. Use the configured launch profile via
`agent-spawn`; do not choose a model inside this skill. Skill invocation, background
wake support and session reset commands differ between clients. References to `/clear`
below describe Claude Code: for another client, use its verified native reset procedure
after the same hand-off, and never inject an unsupported slash command into its pane.
Read required skills directly when native invocation is unavailable.
For a repository security review, dispatch the `security` role on demand. It reports
security evidence and proposed corrections; foureyes retains functional review. Reuse
existing tracked findings for the same root cause. Security is advisory, without a new
automatic merge gate.

## Check first
- `HERDR_ENV=1` — you must be inside a herdr pane (you control panes via the `herdr` CLI). If unset, stop.
- `AGENT_BUS_PROJECT` and `AGENT_BUS_AGENT` exported (see `docs/AGENT-BUS-GUIDE.md`).
- You hold the lease: `agentbus pilot status` prints `piloted by <you>`. If not, claim it with a
  session-length TTL (`agentbus pilot claim --ttl 12h`) or stop — only the master drives panes.
  The lease is TTL'd (default 90s) and **nothing renews it automatically**: a bare `claim` expires
  in 90s and busmon flips to "autonomous (no master)". Re-claim it when you broadcast the budget.

## Agent → pane bridge
Each peer registers its pane (`HERDR_PANE_ID`) via its `agentbus status` heartbeat:
```bash
agentbus agents --json          # {"claude1":{"state":...,"pane":"w1:p1"},...}
agentbus pane claude1           # just the pane id; non-zero exit if none
```
herdr pane ids are NOT durable. Before acting, confirm the stored id is still live with
`herdr pane list`; if it's gone, re-resolve by matching the agent/cwd in that output.

## Choosing who works on it

The model follows the role (`roles.toml`): you never name a model. To get a stronger model on a
task, give it to a role that runs one (`agentbus roles fallbacks <role>` and the manifest show
which).

- **Pop architect** (`agent-spawn architect <project>`) for: a design or architecture choice; a spec
  touching several modules or services; a disagreement between coder and foureyes that survived
  one round; a cross-cutting risk (security, data loss, deployment, money). Not for a routine
  change: that stays with coder.
- You run on a lighter model than architect. On a heavy decision, have architect instruct it
  (options, risks, recommendation) and put the decision to the person with an ask, rather than
  deciding alone.
- **Close architect** once its recommendation is delivered and recorded (a report; a memory note
  if it is durable): `agentbus retire architect`. Never `agentbus shutdown`: that stops the whole
  team. The same goes for security (pop for a review, retire after the verdict) and docs.
- `agentbus retire <role>` refuses while the agent takes part in an open review or mission or runs
  a turn (`--now` takes the turn); it closes its tab and records it in `agentbus swaps`.

## When the model drifts

The sentinel refreshes the model table (`agentbus models`) and runs `agentbus roles doctor` daily,
and reports agents whose launched model is not the one `roles.toml` now configures, whose model the
provider refused or no longer lists, or for which the provider names a newer model (busmon flags
them `model≠`). A profile may name a `tier` (frontier, workhorse, fast) instead of a model: each
launch takes the provider's current model of that tier from the table.
Ask the person whether to relaunch each one; on a yes, when the agent is idle,
`agentbus swap <role> --back` relaunches it on its own profile and model, with the usual checks and
the resume of its work. Never relaunch without that yes: a model change changes cost and behaviour.

## Spawn a peer
The team's boot roles (coder, foureyes, sentinel) come up with the workspace. When you need a
role that isn't up — design work, or surge capacity — **pop** it. Popping runs the *exact* boot
recipe (`agent-launch`), so a popped agent is identical to a booted one:
```bash
agent-spawn architect "$AGENT_BUS_PROJECT"   # design work -> architect (model per roles.toml)
agent-spawn coder "$AGENT_BUS_PROJECT"       # surge -> a second implementer
```
`agent-spawn <role> <project>` opens a new herdr tab running `agent-launch <role> <project>`;
it requires `HERDR_ENV=1` (you're inside herdr) and a role defined in `roles.toml`. The new
agent arms its own `subscribe` and publishes `status`, so it shows up in busmon and in
`agentbus agents` within a few seconds — dispatch to it once it reports `online`.

## Resync — inject text into an agent's pane
```bash
pane=$(agentbus pane claude1) || { echo "claude1 has no registered pane"; exit 1; }
herdr pane send-text "$pane" "<text / context to inject>"
herdr pane send-keys "$pane" Enter
```

## Quota running out: the dispatcher handles it

Since v0.13 the dispatch service reads every provider's usage windows (`agentbus quota` shows
them) and acts on them, with no help from you:
- at **85%** of a window it sends you one directive, `Quota <provider> NN% (<window>, resets
  HH:MM): finish what is in flight, start nothing new.` Do exactly that: land or park the task
  in flight, dispatch nothing new to agents of that provider.
- at **95%** it stops waking that provider's agents (busmon shows them `held`) until the reset;
  their directives wait in their queues.
- when **your own** provider reaches 95%, it pauses the whole project until the reset and sends
  you the resume directive then. Nothing to schedule.

You can still pause by hand (the human asks, or no dispatcher runs):

```bash
agentbus pause --until-reset quota      # or: --for 2h, --until 02:15
agentbus pause --status                 # what is set, and when it ends
```

While paused the dispatcher wakes nobody (no prompt, no escalation) and busmon shows
`paused · resumes in 1h52 (quota)`. At the deadline the dispatcher ends the pause and sends you
one resume directive (`--to` another agent, or `AGENT_BUS_ESCALATE_TO`, changes who): drain it and
restart the team's work. `agentbus resume` ends it early. Never schedule a resume with a sleep or a
background shell in your own session: it is invisible to the team and dies with your pane.

Since v0.15, when a hold ends, the dispatcher tells each agent of that provider whose accepted
task or `working` turn was cut to resume it, and sends you one `wake=no` notice naming them. You
do not restart them yourself; their verdicts and reports come back as usual.

## When a provider is out for long: ask, then swap

When a hold will last more than 2 h (a weekly limit), the dispatcher sends you one directive:
`<provider> is held until <time> (<N>h, <window> window). Held agents with work in flight: …`.
Then:

1. For each held agent named, run `agentbus roles fallbacks <role>`. A role with no fallback
   waits: say the expected wait in your status.
2. If at least one role has a fallback, open **one** ask, one question per role (the question id
   is the role name), at most 6; ask first for the roles with work in flight, the rest after this
   ask is answered. Options: one per fallback ("Swap to <profile>: <its client and model>", recommended when the
   wait is over a day; put in its description the cost and any "same provider as coder" note from
   `roles fallbacks`), then "Wait for the reset".
3. On a swap answer, run `agentbus --project <p> swap <role> --profile <fallback>`. It refuses
   while the agent takes part in an open review or mission, or runs a turn (`--now` takes the
   turn); then it closes the old tab, launches the role on the fallback, proves the new process and
   sends it the resume of its work. If it refuses or fails, reply in the ask's thread
   (`agentbus reply --ref <ask id> …`) with what it printed, so the person sees why.
4. On "Wait", do nothing: the resume restarts the agent at the reset.
5. Once the provider is back and the swapped agent's work is done, swap it back to its own
   profile (`agentbus swap <role> --profile <its profile>`): that is not a new decision.

Never swap without the person's yes, and never swap yourself: when **your own** provider is out
for long, the dispatcher tells the person in busmon how to swap you from a terminal.

## When you need the human's decision: `agentbus ask`

A decision only the human can take (a GO, a merge, a date, a choice between options) is an
**ask**, never a question inside a report or a `notify`. One question per decision, with options:

```bash
agentbus ask master --file /tmp/ask.json        # prints the ask id; you are declared waiting
```

```json
{"title": "Opening of DCA act 4",
 "questions": [
  {"id": "date", "header": "Date T", "question": "When should the new session open?",
   "context": "Act n4 approved at 4d5d51b. Opening must be at least 48 h after its merge.",
   "options": [
     {"label": "Mon 19/10 10:00 UTC", "description": "Before the 12-13 h DCA slice", "recommended": true},
     {"label": "Wed 21/10 10:00 UTC", "description": "Two more days of margin"}]}]}
```

- Ids are lowercase names; `header` ≤ 16 characters (it is the tab), at most 6 questions and 6
  options each, one `recommended` at most with the reason in its description. `context` is short
  Markdown; the full analysis stays in a report, which the context may name.
- busmon shows the questions as tabs; the human picks an option or writes an answer. **Each answer
  arrives at once**, as a reply to you (`ref` = the ask id, `Answer to "Date T": Mon 19/10 10:00 UTC`),
  and wakes you. Act on it right away; the other questions may come later.
- Changed your mind (a review found a problem)? `agentbus ask master --replace <id> --file …`: keep
  a question's id only if its header, question and labels are unchanged (its answer is kept);
  give a reworded question a new id. Never send the new question in a `notify`.
- `agentbus asks --open` shows what is still open; `--withdraw <id>` cancels an ask.
- **The person answered outside the panel** (a message `@master …` instead of the question tabs)?
  Act on it, then close exactly the questions it answers, citing the message, and leave the others
  open: `agentbus ask master --withdraw <id> --question <qid> --note "answered by message <msg id>: <answer>"`.
  Otherwise busmon keeps showing a decision waiting for the person.
- When nothing is open any more, declare `working` again — or `idle` when the answers leave
  nothing to do (the team paused, the next step is the person's to start). One open ask at a time.
- `waiting` means a person must decide **now**: busmon fades it and rings for it. Declare it only
  while one of your asks is open. Waiting for a future instruction, or for a date, is `idle`.
- Without an ask, `agentbus status master waiting "…"` still shows you waiting, but nobody can
  answer it in busmon: prefer the ask.

## Unblock — answer an agent stuck on a question
1. **Detect** blocked peers:
   ```bash
   herdr pane list --json | jq -r '.result.panes[] | select(.agent_status=="blocked") | .pane_id'
   ```
   Map each blocked pane back to its bus agent by inverting `agentbus agents --json`.
2. **Read the question:** `herdr pane read "$pane" --source detection`.
3. **Alert a human** (one-way Signal + the bus — they may be away):
   ```bash
   hermes-notify "claude1 is BLOCKED: <question>"
   agentbus notify "claude1 BLOCKED: <question> — reply in busmon: @claude1 <answer>"
   ```
4. **Receive the answer over the bus.** The human answers in busmon (`@claude1 <answer>`, type `@`
   for agent autocomplete) or `agentbus cmd claude1 <answer>` — either way a cmd to `claude1`.
   Watch directed cmds read-only: `agentbus listen cmd`.
5. **Inject** the answer (the Resync step) into the blocked pane to unblock it. **Only inject into
   agents you have confirmed are currently `blocked`** — never interrupt an actively-working agent.

### Known edge
A cmd to a currently-blocked agent also lands in that agent's `subscribe` consumer group, so on its
next re-arm it could re-receive the answer as a directive — mitigated by the agent persisting its
`--since` cursor (see the bus guide). Don't re-inject an answer you've already delivered.

## Broadcast team budget
Give the team a regular budget readout. Nothing is teed by the agents — the sentinel's
`agentbus refresh` reads it from local artefacts (see `docs/AGENT-BUS-GUIDE.md` → "Budget &
usage"). Two scopes, two commands:
```bash
agentbus budget                                 # ACCOUNT: session/weekly window per provider
agentbus usage                                  # PER AGENT: model + context fill
agentbus refresh                                # if the numbers look stale, republish them yourself
agentbus pilot claim --ttl 12h                  # renew your TTL'd lease while you're here (keeps busmon showing you as master)
agentbus notify "budget — session 25% (resets 25m) · claude1 141k ctx · claude2 88k ctx"
```
**Read the two apart.** `budget` is the shared subscription window: when *it* is near the limit,
the agents using that provider must hold, no matter how empty an individual agent's context is. `usage` is
per-agent context fill: a single agent near its window needs a hand-off and a `/clear`, and that
says nothing about the others.
Distribution is **notify + pull**: the summary lands on `{project}:notify` (visible in busmon and to
the human), and agents read `agentbus usage` themselves on demand. Never push budget via `cmd` to
each agent — that wakes every agent's `subscribe`.

## Is a dispatcher running? — check before you rely on a wake

```bash
agentbus dispatch --status     # "dispatch up" (exit 0) or "dispatch down" (exit 1); busmon: dispatch ●/○
```

`agentbus dispatch` (the bootstrap template's `dispatch` tab) is a deterministic process — no
LLM — that wakes **unarmed** agents when a `cmd` addressed to them is waiting. It types one
fixed wake line into the agent's pane, never the message itself:
`[agentbus#<nonce>] <project>: <n> message(s) waiting for <agent>. Run: agentbus --project <project> subscribe --drain <agent>`.
It types only into a pane that is idle, not busy by its client's signature, with an empty,
visible input line, **not focused** in herdr, and whose foreground process matches the pid
and start time the agent registered with its last `agentbus status`. Anything else is
deferred, and escalated to you if it stays stuck (below). At most one wake per agent per 60s
and three per backlog before escalating.

- **While `dispatch up`:** `request send` (or `cmd`) is enough — the dispatcher wakes the
  target, and the reply, a `blocked` notice or an escalation wakes **you** the same way. You
  **may** stop arming and sleep — **only if your pane is not the one the human works in**.
  The dispatcher never types into a focused pane, so if the human is (or may be) focused on
  your pane, stay armed while you await replies, blocked notices and escalations. Check
  `dispatch --status` before you sleep, every time.
- **While `dispatch down`:** the v0.7 rules below stand unchanged — you stay armed and you
  wake peers through their panes yourself.
- **Manual pane wake is the sanctioned fallback**, not a breach of the "only the dispatcher
  types into panes" rule: use it while no dispatcher runs, or for a peer the dispatcher
  escalated to you.
- To send without waking anyone, `agentbus cmd --no-wake <target> <command>`: delivered at
  the target's next drain or subscribe. `agentbus shutdown` is already `wake=no`, and expires 10 minutes after it is sent.

### The two notices the dispatcher era adds

Both arrive as `cmd` events addressed to you; their `type` field tells them apart:

- **`escalation`** (`from: dispatch`) — a peer has had work waiting longer than the
  escalation delay (default 10m, `--escalate-after`; a peer reporting `working` is given 4×
  that) without being woken, or three wakes made no progress. Its `body`:
  `<agent>: <reason>; <n> waiting, oldest <id>`. A matching `notify` lands in busmon and the
  chip shows `⚠`. Typical reasons: the pane is focused (the human is there), the agent has no
  verified identity (registered from an older binary, outside herdr, or restarted since its
  last `status`), the pane is gone, or its client is not supported. Look at the pane (Step 1
  below) and wake it by hand, or re-pop the role. One escalation per backlog; progress resets
  it. If **you** are the stuck agent, there is no cmd — only the `notify` and `⚠`.
  **No verified identity** is permanent for a client whose commands do not run under its pane
  — Codex runs them through a shared `codex app-server`. A role popped by `agent-launch` binds
  itself; for one started by hand, bind it once you have confirmed the pane is that agent's:
  `agentbus bind <agent> --pane <pane>` lists the pane's foreground processes, then
  `agentbus bind <agent> --pane <pane> --pid <the client's pid>` records it. Rebind after the
  client restarts.
- **`complete`** (`from: conductor`) — every mission of a change revision is integrated on
  `change/<change>` and the change suite passed on its head. The body gives the counts
  (integrated, skipped) and the base. Review the branch and open the PR to the base: the
  conductor never does.
- **`blocked`** (`from:` the peer) — the peer ran `request block <task> <reason>` on a request
  you sent; in the JSON event `body` is the reason, `ref` the thread, `task` the request. Decide: answer on
  the thread, re-scope, or reassign. It is sent once per new reason, not on every repeat.

## Peers disconnect when idle — bringing one back is YOUR job (when no dispatcher runs)

A peer that has finished its work stops re-arming and goes silent on purpose: an armed
`subscribe` wakes its session every idle window, and idling armed is the most expensive way
to do nothing. So assume peers are **not listening** unless you have just woken them.

This changes how you dispatch:

- **A `cmd` to an unarmed peer is retained, but nothing will hand it over.** It stays in the
  shared stream; what fails is delivery. On an existing group a default re-arm filters and
  acknowledges everything at or below "now", unseen; on a group that does not exist yet those
  entries are skipped instead. Either way the peer never sees it unless you or it choose a
  floor on purpose. One live project has 210 such commands unread right now — among them two
  merge authorisations whose branches are still unmerged. Unread is not proof they went
  unexecuted; it does mean nobody can say they were seen.
- **Assign with `request send`.** A tracked request is recorded on the **board**, which is
  read on demand. It survives the peer's absence with no listener, and the peer picks it up
  at boot by reading `agentbus request` — no session had to idle for that to work.
- **Then wake the peer through its pane**, not the bus — in two separate steps, because the
  first one cannot be automated away.

  **Step 1, resolve and look.** `agentbus pane coder` returns what coder registered on its
  last status: a **snapshot**, and herdr ids are recycled. herdr can tell you the pane is live
  and which client and cwd it runs — it does **not** know bus agent names, so no command can
  prove the pane is still coder's. You confirm that by reading it:
  ```bash
  pane=$(agentbus pane coder) || { echo "coder has no registered pane"; exit 1; }
  herdr pane list                                    # is that id still live? same cwd?
  herdr pane read "$pane" --source recent --lines 20 # and is this actually coder?
  ```

  **Step 2, only once you are satisfied, inject.** Name the project and the exact task — the
  peer may serve several, and it wakes with no idea why:
  ```bash
  herdr agent prompt "$pane" "$AGENT_BUS_PROJECT / task-42 assigned: run agentbus request, then arm."
  ```
  `herdr agent prompt` types and submits, the way the dispatcher does. `send-text` followed by
  `send-keys "$pane" Enter` leaves the line unsubmitted in Codex (seen in the v0.10
  acceptance); if that happened, clear the input (`send-keys "$pane" ctrl+u`) before you prompt.

  Never chain the two: injecting after a bare existence check is how you type a directive into
  whatever process inherited that id. If the pane is gone the peer is not asleep, it is gone —
  pop the role again (`agent-spawn <role> "$AGENT_BUS_PROJECT"`); it boots, reads the board,
  and finds its work.

**You are the exception: you stay armed — unless `agentbus dispatch --status` reports `dispatch
up`.** Without a dispatcher you are how peers come back, and where the human and the sentinel
reach the team — a budget or context nudge sent to an unarmed master is retained but never
delivered, so it simply does not happen. Hold the lease and keep listening, even when the team
is quiet. With a dispatcher up, those nudges wake you through your pane like any peer, so you
may sleep — unless the human is (or may be) focused on your pane: a focused pane is never typed
into, so then you stay armed while anything is awaited; if the dispatcher goes down while you sleep, the sentinel's cron check notifies the
human, and the v0.7 rule applies again from your next wake.

Being armed is not being alive: your pane can die with the lease still held and the subscribe
gone. The human's recovery path does not go through the bus — they reach your pane directly in
herdr, or pop a fresh master. When the work is genuinely over, end the team deliberately with
`agentbus shutdown` rather than letting it decay into an unreachable silence.

Wake a peer because there is work for it, not to check on it. Its silence is not a fault to
correct — it is the behaviour you asked for, and `agentbus request` already tells you what it
owes you.

## Dispatching work you intend to follow up — `request`, not `cmd`

`agentbus cmd` is fire-and-forget: once sent, nothing records whether the peer took it, and
chasing it means reading the feed and guessing. A **tracked request** records the answer:

```bash
agentbus request send <task> <target> [--ref T] [--ttl 5m] <body>
agentbus request                       # EVERY tracked request, done ones included (--json)
```

- The `<task>` slug **is** the board key, so the request creates the board entry in state
  `requested`. Reusing a slug is refused and hands you the existing request id: a retry
  inspects the work that exists, it does not create a second copy of it.
- `--ttl` is a deadline on acting, and it is **optional**. Without it there is no deadline —
  which busmon states plainly. Don't set one you don't mean: once it passes, a peer that had
  not yet accepted can no longer take the work and has to come back to you. A peer that had
  already accepted can still resume after a block.
- Use plain `cmd` for what it is good at: a nudge, an answer, an FYI, anything you will not
  follow up.

**Never infer that a request was taken.** A peer's `report`, its `status`, a reply on the
thread, even bytes delivered to its subscriber — none of those is an acceptance. Only
`request accept` is, and only `request done` completes. So when you want to know where a
task stands, read `agentbus request` (or busmon's REQUESTS view) instead of interpreting
silence — and read it as "no acceptance recorded", never as "the agent is ignoring you". That is the entire point of the mechanism: it replaces your inference with a record.

## Pipeline dispatch — task-by-task with review gate

Driving a multi-task plan through an implementer (e.g. `claude-worker`) + a reviewer (e.g.
`claude-4eyes`):

1. **Point at the plan, don't paste it** — if peers share your repo/cwd, `agentbus cmd claude-worker
   "Start Task N of <plan-file-path>. TDD, one task, report + hold when green."` beats pasting the
   task's full text; peers already have the file, pasting wastes tokens and drifts from the source
   of truth if the plan changes.
2. **One task in flight at a time.** Don't dispatch N+1 until N clears BOTH: worker reports done,
   AND the reviewer reports approve. An unreviewed "done" isn't done.
3. **`agentbus reports --json` entries carry the full text in `.Full`** — no separate lookup needed,
   the plain-text tail view is the one that truncates.
4. **If you poll `agentbus reports --json` yourself (e.g. from a background watcher), track the last
   seen `.ID`, not the array length** — the endpoint is a capped/rolling recent-reports window, so
   the count plateaus once you're past the cap and a length-diff check silently stops firing even
   though new reports keep arriving.
5. **Route review over `cmd`, not the formal `challenge`/`verdict` gate** — reserve that mechanism
   for an actual blocking risk (money-path, prod migration), not routine per-task review; plain
   cmd→report round-trips are cheaper and sufficient.
6. **Independently verify what you can** (`git log`, `gh run list`, a real command) rather than
   trusting self-reports alone — cheap, and catches a report that's technically true but incomplete.
7. **Plan-level sign-off before declaring the whole thing done** — one final review against the
   plan's Definition of Done, not just the last task's diff. Per-task review doesn't prove the
   pieces integrate.
8. **The board is the shared ownership registry** — run `agentbus board` before each dispatch:
   a task already claimed by a peer is off-limits. Workers claim their task when they start
   (their agent-bus skill instructs it) and move it to `review`/`done` as it lands. If a
   dispatch bounces on a failed claim, believe the board, not your plan.

## Missions — running a large change through the conductor

For a multi-agent, multi-day change, write its plan as missions (`docs/changes/<change>/missions.json`:
agent, reviewer, dependencies, allowed paths, test, brief) and let the conductor drive it. You only
arbitrate. For a short, Claude-only change, agent teams are lighter.

- The conductor runs as the project's `missions` service, started by `agentbus up` (check:
  `agentbus service status`; with `--tabs`, in its own tab).
- Load the change at a reviewed commit: `agentbus mission load docs/changes/<c>/missions.json@<commit>`.
- Watch: `agentbus mission status [<c>]`, `agentbus mission show <c>/<id>`; busmon shows
  `missions idle/busy/failed`.
- On an **escalation** (blocked mission), choose:
  - `agentbus mission retry <c>/<id>` — a new attempt, same agent, fresh round of attempts;
  - `agentbus mission reassign <c>/<id> <agent>` — a new attempt for another agent (not its reviewer);
  - `agentbus mission skip <c>/<id> --reason "<why>"` — mark it done without a commit (recorded in the
    verdict ledger) so its dependents proceed;
  - `agentbus mission abort <c> --reason "<why>"` — stop the whole change.
- **Revised plan**: commit the new `missions.json`, then `mission load …@<new commit>` (revision n+1).
  Unchanged missions keep their state. A mission already in flight changes only with
  `--force "<why>"` (its attempt is superseded); an approved or integrating one never does; an
  integrated one stays integrated.
- **Lost Redis**: `agentbus mission load --recover docs/changes/<c>/missions.json@<commit>` rebuilds
  the change from its merge trailers on `change/<c>` (integrated missions come back integrated;
  one merged under an older definition comes back blocked for you to decide).

The conductor merges only into `change/<c>`, never `main`, never pushes. Merging `change/<c>` to
`main` is a normal PR, yours to open.

## Record the decisions you make, or they die in the pane

You make the calls the team will be living with: what gets dispatched, what is out of scope,
which approach was rejected and why. Those decisions are worth more than the tasks they
produced, and a herdr pane is the one place they are guaranteed to be lost.

When you settle something durable, write it — or ask the agent that did the work to — under
[the shared memory index](../../docs/memory/INDEX.md), following
[PROTOCOL.md](../../docs/memory/PROTOCOL.md): the decision, when it applies, the evidence, and
what would invalidate it. Link it from your handoff and say whether it is committed yet.

Not every dispatch is a decision. A rejected approach with its reason is; "task 12 assigned"
is board state and already recorded there.

## Shutdown — stop the team when the work is done
Idle agents are not free: every armed `subscribe` wakes its session on each
heartbeat window. When the plan is done (final sign-off passed, board all
`done`), bring the team down instead of leaving it in standby:
1. `agentbus shutdown` — refused while a board task isn't done or a peer is
   still working/blocked (a stale >10min heartbeat doesn't block: dead, not
   busy). `--force` overrides when you know better. Sentinel may nudge you
   toward this when it sees all-quiet.
2. Each peer receives a `shutdown` directive: it reports, sets `done`, and
   stops re-arming. Give them a minute to land their final status.
3. Close the peer tabs: `herdr tab list`, then `herdr tab close <id>` for each
   agent tab (busmon can stay for the human; close the `dispatch` tab too unless the project
   stays live — with the panes gone, any later cmd would only end in an escalation). Release
   the pilot lease:
   `agentbus pilot release`.
4. Tell the human the team is down. Your own pane goes last — leave it to the
   human, or close it once everything else is gone.

## Gotchas

- **Shared session budget**: the whole team often draws one account's session pool — plan
  against the reset time `agentbus budget` prints, not just the percentage. **≥ 75%**: wrap-up
  mode — finish the in-flight task, get it reviewed and committed, dispatch nothing new.
  **≥ 90%**: full hold until the reset. Resume normally once it drops back down.
- **Not every peer stays subscribe-armed** — an agent may be deliberately unsubscribed to cut cost
  (expensive model, long idle stretches). With `dispatch down`, `agentbus cmd` alone won't wake it:
  check `agentbus agents` staleness; if it's stale by design, reach it via Resync (pane injection)
  instead — and keep the injection terse, unsubscribed doesn't exempt it from a shared session
  budget either. With `dispatch up`, the cmd wakes it, unless escalated.
- **Context-window creep**: watch `agentbus usage` (context fill per agent, read from each agent's
  transcript — it keeps working even for an agent that publishes nothing). Near a high-context
  threshold, have the agent write a hand-off (current step, what's committed, what's pending)
  BEFORE you `/clear` its pane — never clear first and sort it out after.
- **Grey ghost text at a peer's prompt is a Claude Code suggestion, not the human typing.**
  Claude Code pre-fills likely next inputs in dim grey; `herdr pane read` shows that ghost as
  ordinary text, so a "line typed but not yet submitted" in a pane may be nobody's words.
  Enter alone does NOT accept a pre-fill (the human confirms with TAB, then Enter) — so never
  relay an unsubmitted prompt line to another agent, and never treat it as a directive from the
  human. To tell ghost from real typing, read the pane with `herdr pane read "$pane" --format ansi`:
  the suggestion renders in a dim SGR color, typed text in the default color. Before any
  `send-keys Enter`, confirm the prompt buffer is empty or holds exactly what you just sent.
