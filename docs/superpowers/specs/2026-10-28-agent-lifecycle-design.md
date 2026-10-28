# Agent lifecycle: hand-off and clear, reload — design

Status: draft for review (coder, 2026-10-27). Task `lifecycle-spec`, branch
`docs/lifecycle-spec`, review subject `lifecycle-spec`. Spec only; no code in this change.

## The problem

Two moments in an agent's life still need a person at the keyboard:

1. **A full context.** At `AGENT_BUS_CONTEXT_ALERT` % (default 80) the dispatcher tells the agent
   to commit WIP and hand off (`dispatch/contextalert.go`). The agent does, and then someone must
   type `/clear` in its pane and point it at its hand-off. Tonight that someone was the person,
   once per agent.
2. **A client update.** The observe pass flags `update` and sentinel gets one directive per
   update episode (spec `2026-10-21-client-reload-design.md`). That spec stops at a proposal: the
   person types `/exit`, then `agentbus up <p> --resume`.

Bernard decided both on 2026-10-27 (master's directive `1793134885283-0`):

- **"/clear auto: Dispatcher, spec."** After a declared hand-off the dispatcher, the only pane
  writer, may type `/clear`, a single named exception to "it types only the wake line", under
  strict conditions; then it wakes the agent on its hand-off.
- **"Reloads are sentinel's job; master reloads sentinel."** An `agentbus reload <role>` that
  types nothing: SIGTERM the verified idle pid, then the v0.21 relaunch path brings it back
  resumed in a new pane and closes the old one once proven. Proven by hand tonight.

This spec defines both: the hand-off declaration, the auto-clear rule, the reload command, who
may run it, the invariants they change (and the ones they keep), races, tests and rollout.

## Part A — the hand-off declaration

The clear must follow a hand-off the agent *declared*, not one the dispatcher infers from a
report's wording. So the declaration is a fact of its own.

### Options

| Option | For | Against |
|---|---|---|
| A report kind `handoff` | no new key; busmon already shows reports | the report stream is capped (~1000) and unfenced; anyone may write any kind; a kind cannot be consumed once |
| A status field (`status … --handoff <id>`) | rides on the heartbeat | a later status (idle, working) would have to carry or drop it; `mergeSnapshot` would need a new merge rule |
| **A record `{p}:handoff` (recommended)** | one owner per field, one script, consumed once, tied to the verified identity | a new key (not in `projectKinds`) |

### Recommended: `agentbus handoff <self> --report <id>`

- The agent publishes its hand-off as usual (`agentbus report <self> "<hand-off>"`, the full text
  retained), then runs `agentbus handoff <self> --report <report-id>`. The skill (agent-bus, plus
  the context directive's text) says so; the directive becomes `context N%: at your next stopping
  point commit WIP, publish your hand-off with agentbus report, then agentbus handoff <you>
  --report <id>`.
- `Bus.DeclareHandoff` writes field `<agent>` of the hash `{p}:handoff` in **one Lua script**:
  `{report, session, pid, pid_start, at}` with `at` from Redis `TIME`. The script refuses unless
  the report id exists in `{p}:report` with `agent == <agent>`, and the caller's identity matches
  the stored verified one: `pid`/`pid_start` equal to the snapshot's (the command runs under the
  client, so `AgentIdent` reads them, as `status` does) and `session == IdentSession`. A
  declaration from anyone else, or from a stale session, writes nothing and says why.
- `{p}:handoff` is not in `projectKinds` and `HasKeysBesidesDispatch` ignores it, like
  `{p}:observed`: it never discovers or revives a project. busmon shows `handoff` as a flag on the
  agent's row while a record is pending (display only).
- **Void when the agent moves on.** A record is void, and the dispatcher deletes it (fenced), when
  any of these holds: the agent declared `working` after `at`; its `IdentSession` differs from the
  record's (it cleared or was relaunched by other means); its pid differs. A void record is never
  acted on.

A hand-off without a clear is still useful: the person or master can read `agentbus handoff`
(list) and act by hand. The record is what auto-clear acts on; it does not make auto-clear happen
by itself (Part B's switch does).

## Part B — auto-clear: the dispatcher types `/clear`

### The rule

On a pass (after the pause check), for each agent with a non-void hand-off record, the dispatcher
types `/clear` into its pane **only when all of these hold**, in this order (cheap first):

1. `AGENT_BUS_AUTO_CLEAR` is not `0`.
2. The agent's client is in the **clear allowlist**: clients whose `/clear` semantics are known
   (it ends the conversation, keeps the process, argv and environment, starts a new session id,
   fires `SessionStart` with `source: clear`). Today only Claude Code (`client == claude-code`,
   herdr client `claude`). Any other client is never cleared; the record stays for a person.
3. Not paused (`{p}:pause` absent), no swap intent `{p}:swap:<agent>` (Part C reuses it, so a
   reload in progress also blocks), the provider not held (`hold:<provider>`: a cleared agent
   would only be woken into a held quota).
4. No open ask by the agent (`open:<agent>` in `{p}:asks`), no open gate **on** it
   (`{p}:gate:<agent>`) and none it opened on another agent (`Bus.ReviewsOf`): a reviewer mid-review
   or an author waiting for a verdict holds context worth keeping until that closes. Only gates
   younger than `bus.DefaultHorizon` (24 h) count: stale gates pile up (58 on foureyes one morning)
   and would hold a hand-off forever. When a hand-off waits on a gate, the escalation target gets
   one `wake=no` notice per record (`FencedCmdOnce`, mark `clear-wait:<agent>` = session) naming
   the gate, so it can be closed (`agentbus gate close`) or answered.
5. Declared `idle` (the last status is `idle` and newer than the record's `at`), not `armed`,
   and no wake in progress (`{p}:wake:<agent>` not `prompting`).
6. The pane passes `identityOK` (pid + start time of the verified identity among the pane's
   foreground processes), is **unfocused**, and its client's signature reads **ready** and not
   busy, by two identical reads (`Inspect`, as for a wake).

### Writing it: reserve, type, prove

- **Reserve, fenced.** One script checks the lease, that the hand-off record is unchanged, and
  that the mark `clear:<agent>` in `{p}:quota` is not already the record's session; then it sets
  the mark (value = session) and writes the wake record state `clearing` with `at` from `TIME`.
  Once per session: a dispatcher restart never clears twice.
- **Re-inspect, then type.** Immediately before typing, `Inspect` again (ready, unfocused,
  identity). Then a new `Runtime.Clear(ctx, pane)` types exactly `/clear` and Enter through herdr
  (`agent prompt <pane> /clear`). `Clear` is the only method besides `Prompt` that writes to a pane;
  its text is a constant, never a parameter. The reservation records the newest entry ids of
  `{p}:status` and `{p}:report` written by the agent at that moment.
- **Prove.** Within 30 s, the agent's verified identity shows the **same pid** with a **new
  `IdentSession`** (Part D: the `SessionStart` hook records it), **and** the agent wrote no status
  or report after the ids the reservation recorded. If it did, a turn ran between the last inspect
  and the clear (see Races): its work is gone from the conversation and the hand-off does not
  cover it, so the dispatcher does not resume from it. It deletes the record and sends the agent
  a directive `your context was cleared after a turn your hand-off does not cover: read agentbus
  reports <report-id>, check your last status and reports, then publish a fresh hand-off if work
  is in flight`, plus one `wake=no` notice to the escalation target.
  Otherwise the dispatcher deletes the hand-off record and writes, fenced, a directive to the
  agent: `context cleared: re-run your boot steps (load your role's skills, read the memory
  index), then read your hand-off: agentbus reports <report-id>` (its `ref` the report id). The
  role prompt survives `/clear` (`--append-system-prompt` is argv), but the skills loaded at boot
  and the habits they teach (drain first) do not, hence the boot steps. That directive is an ordinary
  obligation, so the **ordinary wake** follows on the next pass, with the fixed wake line. Nothing
  else is typed.
- **No proof.** The record is kept, the mark stays (no retry for that session), the wake record
  goes to `escalated`, and the escalation target gets one `wake=no` notice: `auto-clear of <agent>
  not confirmed in 30 s; look at its pane`. The person's desktop gets the same through the context
  alert's `notify-send` runner.

### Configuration

`AGENT_BUS_AUTO_CLEAR`: `0` never; `1` (proposed default) clear on a declared hand-off. Read by
the dispatch service from the project's env file (`agentbus up --auto-clear 0` writes it), like
`AGENT_BUS_CONTEXT_ALERT`. **Proposed default: on.** The real gate is the agent's own declaration:
with the switch on and no `agentbus handoff`, nothing is ever cleared. With it off, the dispatcher
still deletes void records and busmon still shows `handoff`.

## Part C — `agentbus reload <role>`

### What it does

```
agentbus reload <role> [--fresh] [--dry-run] [--reason <text>]
```

Relaunches one agent on the same role, profile and client, resuming its conversation, without
typing anything into any pane:

1. **Preflight** (nothing changes before every check passes; swap's checks, `swap.go`):
   - the role is in `ROLES_TOML`; its client's executable is on PATH;
   - a live verified identity: `{p}:agents` has pane + pid, and `ProcStart(pid) == pid_start`;
   - not the caller's own pane (`HERDR_PANE_ID` ≠ the snapshot's pane);
   - not on a fallback profile (`Bus.Fallbacks`): the template's command carries the role's own
     profile, so a reload would silently undo a swap. Refused with `agentbus swap <role> --back`
     as the way;
   - `openWork`: no open challenge on either side (`Bus.ReviewsOf`), no mission attempt in
     progress, no open ask by it, and no board task it owns in state `accepted` (`Bus.Board`:
     accepted work is in flight even while the agent is idle between turns). This is the one
     readiness rule: the sentinel's reload pass (Part C) and master's last reload apply the same
     `openWork`, never a narrower copy;
   - no running turn: not observed busy (fresh `{p}:observed`), not declared `working` within
     `Recent` (2 min), declared `idle`; no wake `prompting`;
   - a **resume source** (below), unless `--fresh`.
2. **Intent**: `Bus.BeginSwap` with `kind=reload` (the same `{p}:swap:<role>` key, `SET NX PX
   180000`, and its `{p}:swaps` ledger `begin` in the same script). Reusing the swap intent is
   deliberate: the dispatcher already neither prompts, escalates, resumes nor (Part B) clears an
   agent under it, and `OpenChallenge`/`request` already refuse new work on it. After the intent,
   `openWork` is read again, and an in-flight wake is waited for (10 s), as in swap. What reads
   the kind:
   - busmon flags `reloading`, not `swapping`;
   - `Bus.Interrupted` closes a dead caller's `begin` as an interrupted reload (`failed`, kind
     reload);
   - `swap --back`, the `back:<p>:<role>` notices and `{p}:fallback` ignore reload entries, and a
     reload never writes or deletes the fallback record;
   - `agentbus swaps` lists them with their kind.
3. **Stop**: re-read the snapshot and `ProcStart`; if pid and start still match, `SIGTERM` the
   pid. Wait up to 15 s (`PIDGone`) for it to exit. No `SIGKILL`: a client that ignores SIGTERM
   is a failure (below), never forced.
4. **Relaunch**: the v0.21 relaunch path for **this role only** (`relaunchUp` with a role filter,
   `Resume: !fresh`): the tab's command is now gone (dead pid, plain shell), so it runs the
   template command plus `--resume <session>` in a new pane split from the tab, proves the new
   identity in that pane (60 s), and closes the exact old pane if it is still a plain shell
   (`closeOldPanes`).
5. **Proof**: a new pid, alive with its start time, bound in the new pane, same client and
   provider, and, when the reload answers an update episode, a `client_fp` equal to the installed
   fingerprint. Then `EndSwap`, ledger `reloaded`, and a report line.

### The resume source

The session to resume is the agent's **`IdentSession`**, the session proved with its process,
valid by `sessionIDPattern`, for a client with launcher resume (Claude Code). Not `Session` (an
unverified status may set it). With Part D, `IdentSession` follows `/clear`, so a reload after an
auto-clear resumes the *new* conversation, not the one before the hand-off (the limit the
client-reload spec stated). A client without launcher resume (Codex today), or no valid session,
is refused unless `--fresh`, which relaunches without resume and says the conversation is lost.

### Failure and recovery

Every failure after the intent is a `failed` entry in `{p}:swaps` with the step, and prints the
recovery:

| Step | State left | Recovery printed |
|---|---|---|
| preflight / intent | nothing changed | the reason |
| SIGTERM refused, or pid still alive at 15 s | old agent still running, intent released | `the client did not exit; nothing was replaced` |
| relaunch failed (template, split, launcher) | old client gone, pane a shell | `agent-launch <role> <p> --resume <session>` in that pane, or `agentbus up <p> --resume` |
| no proof in 60 s | maybe a new agent starting, old pane kept | `agentbus agents`; `agentbus up <p> --resume --dry-run` |
| old pane not closed | new agent proven | the pane id and why (as `closeOldPanes` says today) |

A caller that dies mid-reload leaves a `begin` without end; the next swap or reload of that role
closes it as interrupted (`Bus.Interrupted`), as today.

### Who may run it

| Caller | May reload | When |
|---|---|---|
| sentinel | any role but itself | on a client-update episode for that agent's client (`{p}:observed` `update` true for it), or a health `fail` it is told to remediate |
| master | sentinel | any time sentinel is idle (who reloads sentinel, decided) |
| sentinel | master | last of an update episode, only when the preflight passes for master (idle, `openWork` empty: no open challenge, mission attempt, open ask or `accepted` board task it owns) and no pilot lease is held by another driver (`{p}:pilot`) |
| a person | anyone | with an explicit `--as <person>` (never an inherited `AGENT_BUS_HUMAN`) |

The CLI enforces the table from `AGENT_BUS_AGENT` and the bus facts; like `gate close --as`, it
cannot prove who runs it. The identity guard (busmon-a-identity, `af19f2e`) refuses every
unlisted write from a herdr pane without `AGENT_BUS_AGENT` before argv is parsed; `reload` with an
explicit `--as <person>` is added to its list, exactly as `answer --as` is, so a person may reload
from their own shell pane. `handoff` is not listed: only an agent (which has `AGENT_BUS_AGENT`)
declares one. The launcher's tool rules allow exactly `agentbus reload *` for sentinel
and `agentbus reload sentinel` for master (`[roles.*.tool_rules]`), nothing broader.

### The person's yes

**Proposed default: none needed.** Bernard: "it is sentinel's task". On an update episode
sentinel reloads the ready agents one at a time (ready = `reload`'s preflight passes: declared
idle, not busy, not on a fallback, and `openWork` empty: no open challenge either side, no mission
attempt, no open ask, no `accepted` board task it owns),
master last, and reports each one; agents not ready are retried on its next wake. **Opt-out:**
`AGENT_BUS_RELOAD=propose` restores the client-reload spec's proposal (sentinel → master → `ask`),
and `AGENT_BUS_RELOAD=off` makes `reload` refuse for every caller but a person. Both are in the
project's env file (`agentbus up --reload propose|off`).

## Part D — `IdentSession` follows `/clear`

`/clear` keeps the process and starts a new session id. Today `IdentSession` changes only with an
identity proof (a status with a pid, a bind), and the context pass reads only `IdentSession`, so
after a clear the dispatcher would keep reading the old session's fill until the agent's next
`status`. And Part B's proof needs the new session.

The launcher's `SessionStart` hook already runs on every session start, `clear` included
(`agentbus roles report-resume`, `cmd/agentbus/reportresume.go`), with `session_id` and `source`
on its stdin. It gains a second step, split across the detach:

- **In the foreground**, before it detaches (it detaches with `Setsid` and `Process.Release`,
  `reportresume.go`, after which the sender is reparented to init or a subreaper and an ancestry
  check would fail or race), the hook proves its ancestry: the stored verified pid and its start
  time found among its own ancestors (`AncestorAmong`, `ProcStart`). No proof, no write.
- **The detached sender** receives the proved pid and start and the session id, and calls
  `Bus.ProveSession(agent, pid, pid_start, session)`: one WATCH/MULTI through `putSnapshot` that
  replaces `IdentSession` only when pid and start still equal the stored verified identity and the
  session id passes `sessionIDPattern`. The hook still never prints on
stdout and always exits 0; a failed write is logged to `<state>/resume.log`. A subagent's start
(`agent_id` set) proves nothing, as today.

## Invariants

- **The one pane writer stays the dispatcher.** Nothing else types. `reload` types nothing: it
  signals a verified pid and uses v0.21's relaunch, which runs a command only in a pane it has just
  created.
- **The exception surface is one constant.** The dispatcher types two things, ever: the fixed
  wake line, and the literal `/clear`. Never message content, never a parameter. `Runtime.Clear`
  takes no text. The test `typedIntoExisting` (relaunch) is unchanged; the dispatcher gets its own
  invariant test, `dispatchTypes`, asserting that every `Runtime` write in the fake runtime is
  either a wake line matching `WakeText`'s format or exactly `/clear`, and that `/clear` only ever
  follows a reserved `clearing` record.
- **Every dispatcher write stays fenced**: the reservation, the mark, the record's deletion, the
  resume directive, the escalation.
- **Declared state is never overwritten.** Auto-clear does not change the agent's declared state;
  the agent declares again after its drain.
- **No process of ours restarts a client except `reload`,** and `reload` only under the swap
  intent, from the callers in the table.

## Races

| Race | Outcome |
|---|---|
| A turn starts between the last inspect and `/clear` (a background task's notification, a scheduled wakeup) | the re-inspect right before typing catches most. Otherwise Claude Code queues `/clear` and runs it **after** that turn: the clear succeeds and the turn's work leaves the conversation without a hand-off. The proof detects it by any status or report written after the reservation, and the agent is told to rebuild from its reports instead of resuming from the stale hand-off (Part B). A turn that wrote nothing on the bus is the residual: its work survives only in Git and files |
| The person focuses the pane | unfocused is checked in the re-inspect; a focus after it may see `/clear` typed: the person sees it, as with a wake line |
| A message arrives for the agent after its hand-off | it stays in the cmd queue (unacked) and survives the clear; the agent drains it after the resume directive |
| The agent declares `working` after its hand-off | the record is void: no clear |
| The agent clears by hand | its new `IdentSession` voids the record |
| Two dispatchers | the lease; the reservation script checks it |
| `reload` and auto-clear on one agent | the swap intent blocks the clear; a clear `clearing` blocks the reload (wake not settled) |
| `reload` while a wake is typed | the intent, then a 10 s wait for `prompting` to settle (swap's rule) |
| pid reuse before SIGTERM | pid and `pid_start` re-read right before the signal |
| The old pane reused by herdr | `closeOldPanes` closes it only if still a plain shell at that moment |
| A deleted project | fenced writes write nothing; `reload`'s intent script checks the `installed` marker like the services |

## Tests

All with the fake herdr runtime and a **disposable** broker (`AGENTBUS_TEST_REDIS_URL`), never
the user's herdr or Redis.

- **bus**: `DeclareHandoff` refuses a foreign report, another pid, another session; `ProveSession`
  moves `IdentSession` only for the verified pid; the clear reservation is once per session and
  refuses without the lease; void rules (working after, session changed, pid changed).
- **dispatch** (`Decide` stays pure; a new pure `DecideClear(record, snap, inspection, marks,
  pause, swap, asks, gates, cfg)`): one table case per condition of Part B's rule, each one
  vetoing alone; the happy path types exactly `/clear` once, then after the proof writes the resume
  directive, and the next pass wakes with the wake line; no proof in 30 s escalates once and never
  retries; `AGENT_BUS_AUTO_CLEAR=0` never types; a non-Claude client never types;
  `dispatchTypes` over every test of the package.
- **cmd/agentbus** (`reloadDeps` like `swapDeps`, every outside call faked): each preflight refusal
  changes nothing, an idle agent owning an `accepted` board task included (refused before the
  intent, and refused after it when the task is accepted between the two reads), for sentinel's
  reload of a peer and of master alike; the caller table; SIGTERM only after a fresh pid+start match; no SIGKILL on
  timeout; relaunch filtered to the role; `--fresh` without a session; each failure row writes a
  `failed` ledger entry and prints its recovery; `--dry-run` writes nothing.
- **hook**: `report-resume` on `source: clear` proves the ancestry in the foreground (a fake
  process tree with and without the client among its ancestors), passes pid/start to the sender,
  which writes only while they equal the stored identity; it still exits 0 with nothing on stdout.
- **identity guard**: `reload <role> --as <person>` from a pane without `AGENT_BUS_AGENT` passes
  the guard; `reload <role>` without `--as` there is refused; `handoff` there is refused.
- **proof after a queued clear**: a status or report written after the reservation turns the
  resume directive into the rebuild directive, plus the notice.
- **Live checks before default-on** (this machine, read-only first): Claude Code's `/clear` keeps
  the pid, argv and environment and fires `SessionStart` with `source: clear` and a new
  `session_id`; typing `/clear` with the slash menu open submits `/clear` (not another command);
  `claude --resume <id>` after SIGTERM restores the conversation (done by hand tonight; to repeat
  with the role launcher).

## Rollout

1. **D + A**: the hook's `ProveSession`, `agentbus handoff`, the record, busmon's flag, the new
   context-directive text and the skill. Nothing is typed yet; useful at once to the person.
2. **C**: `agentbus reload`, the caller table, tool rules, sentinel's skill section (reload ready
   agents on an update episode, master last, report each), `AGENT_BUS_RELOAD`.
3. **B**: auto-clear behind `AGENT_BUS_AUTO_CLEAR`, after the live checks above pass on this
   machine; default on.

Each step is its own PR with its own 4-eyes review; the decision note
`docs/memory/decisions/2026-10-28-agent-lifecycle.md` is written with step 3, when the exception
to "types only the wake line" becomes real, and CLAUDE.md's dispatcher paragraph names it.

## Open questions for Bernard

foureyes' first review settled the proposals below; they stay open for the person.

1. A hand-off with an open gate on the agent **waits** (gates younger than 24 h only, one notice
   per wait), rather than clearing anyway.
2. Reload **reuses** `{p}:swap:<role>` with `kind=reload`; its readers are listed in Part C.
3. Sentinel **reloads master** last, only when the preflight passes for master (idle, `openWork`
   empty, accepted tasks included) and no other driver holds the pilot lease.
