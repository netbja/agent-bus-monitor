---
id: 2026-11-19-ci-loop-and-status-ideas
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-19
last_verified: 2026-11-19
author: architect
confidence: inferred
---
# A CI loop to the branch's owner, newer-client detection, richer Claude state: three ideas from agent-orchestrator (waiting for the person)

## Finding/decision

A 30-minute, read-only study of agent-orchestrator ("AO", checkout at
`/data/AI-Docs/agent-orchestrator`), on three topics the person picked. The person has already said
yes to a CI loop, and wants us to "improve our simple robust automaton, not copy the gas factory".
Each idea below is the smallest change that fits the dispatcher, the merge queue and the holder as
they are. Nothing is specced or built.

### 1. A failed check or a conflict goes back to the branch's owner (the person said yes)

**How AO does it.** A poller (`backend/internal/observe/scm/observer.go`, every 30 s) reads each
tracked PR's checks, reviews and mergeability through `gh`/GraphQL and stores them. The lifecycle
manager (`backend/internal/lifecycle/reactions.go`, `ApplyPRObservation`) turns them into "nudges"
to the session that owns the PR: failing CI (check names, URL, a sanitized log tail), unresolved
review comments, a merge conflict. Each nudge is sent once per (key, signature): the key is the PR
plus the kind, the signature is the set of failing checks or the review state, persisted so a
restart does not resend. The conflict nudge re-arms when the PR is seen mergeable again.
CI and review nudges wait while the agent needs input. The merge-conflict nudge is exempt: it
takes an "urgent" path that may reach an agent idle at a prompt, if AO can prove the prompt is an
idle composer. No nudge reaches an agent blocked on a permission dialog. AO pastes the message
text into the agent's terminal.

**What we have.** The merge-queue chore already runs `gh pr list` on every pass
(`mergequeue/forge.go`, `ReadGitHub`) and records `Conflicting` in `{p}:merge-queue`. That record
is shown (`agentbus merge-queue`, busmon) and goes to nobody. No check status is read. This
repository has no GitHub Actions, so for us "CI" is the reviewers and the release gate; a forge
check matters for the person's other projects.

**Smallest change.**

- Add `statusCheckRollup` to the fields of the `gh pr list` call the chore already makes, so no new
  process and no new cadence.
- Find the owner from the board. The board entry that records the PR's head branch names its owner;
  a PR with no such entry goes to master.
- Send one directive per episode with `FencedCmdOnce`: the dispatcher's fenced once-only command,
  already used for quota and run-drain notices. The episode is the PR, its head sha and the sorted
  names of the failing checks, or `conflict` with the base sha. It wakes the owner the usual way.
- Send nothing while the owner is `blocked` or `waiting` (blocked comes from the screen check),
  and send it on the next pass after that.
- The text is the PR, the failing check names and their URLs. **Never the log**: the owner reads it
  with `gh run view --log-failed`, so CI output never reaches the bus or a pane.
- **Review comments are deferred.** Our reviews already reach the author on the bus: a verdict goes
  to whoever opened the challenge. What is not routed is a comment a person leaves on the forge.
  It would use the same mechanism, with one episode per review id and the forge's review state.
  It is left out of the first step, because it needs a second `gh` read (threads) on its own
  cadence, and it should wait until someone actually reviews on the forge.

**Risks.**

- Wake churn on a flaky check. The episode includes the head sha, so a re-run with no new push does
  not resend.
- Owner mapping. A branch pushed without a board entry goes to master, which is noisy but safe.
- The rule "the dispatcher never runs repository code" is kept: `gh` reads the forge only, as today.
- The dispatcher still types only the wake line; the directive text goes on the bus.

### 2. Knowing a newer client exists, before anyone installs it

**How AO does it.** AO installs each agent CLI release side by side and switches a `current` link
(`docs/harness-version-refresh.md`). It asks the package source for the latest release with one HTTP
read, and starts no package manager: the npm registry's `latest` tag, or PyPI
(`backend/internal/service/systeminstall/manager_versions.go`). An agent idle for 10 minutes is
restarted onto the new executable with its own resume flag, after a check that the resume id
exists, at most four at a time. It also learns a model's minimum CLI version from the API's
`claude_code_version_too_old` rejection and greys that model out
(`backend/internal/adapters/agent/claudecode/model_versions.go`).

**What we have.** The second half already exists: `dispatch/clientupdate.go` sees that a client
was updated on disk and gives sentinel one directive per update episode, and reload is sentinel's
job (decision 2026-10-28). The models table is refreshed daily. We do not know that a newer release
*exists*. `cmd/agentbus/redislatest.go` already does exactly that for Redis: one read a day, a host
cache, a health check.

**Smallest change.** A `client-version` health check built on the `redislatest` pattern. Once a day
per host, read the latest release of each client the roles use (Claude Code, Codex: a registry
JSON), and report `newer available` in health and For you.

The comparison must be with the version **installed now**, not with the snapshot. The snapshot's
`ClientFP` is the launch fingerprint: after an upgrade on disk it still describes the old running
client, so comparing with it would recommend an update that is already installed. The installed
version comes from the on-disk probe (`realClientProbe`, the same one that fills
`Observation.Update`), read once per client per host. When the probe gives no version label, the
check says `unknown`, never `newer available`. The update itself stays the person's (or a quiet-update step):
the dispatcher installs nothing. Optionally, in the same check, recognise `claude_code_version_too_old`
on a stuck screen and name it.

**Risks.**

- Channel mismatch: Claude Code's native installer has its own update channel; npm `latest` may
  lead or lag it. The check must name its source, and say "unknown" rather than guess.
- A network read from the dispatch service: bounded, daily, cached; a failure ages to unknown,
  never to ok, as `redislatest` does.

### 3. Reading Claude Code's state from more hooks

**How AO does it.** Hooks are the source of truth (`claudecode/activity.go`,
`DeriveActivityState`):

| Hook | State |
|---|---|
| `UserPromptSubmit`, `PreToolUse`, `PostToolUse` | active |
| `PermissionRequest` | **blocked**, with the tool named |
| `Stop`, `StopFailure` | idle |
| `Notification` | `idle_prompt` → idle, `permission_prompt` → blocked, `agent_needs_input` → waiting_input |
| `SessionEnd` | exited, except `clear` and `resume` |

Blocked is cleared by the `PostToolUse` of that same tool, and other tools' traffic never clears
it. The screen is only a fallback, for a turn that died without a `Stop` (`terminal_activity.go`).
The displayed status is computed when read, never stored (`docs/architecture.md`, "Status
derivation").

**What we have.**

- Turn hooks on `UserPromptSubmit`, `Stop` and `SessionStart` only (`cmd/agentbus/turnhook.go`,
  `rolecfg/launch.go`); the Grok profile also has `StopFailure`.
- Blocked, stuck and login-expired come from reading the screen (agent-screen-blocked, steps 3a and
  3b).
- The context fill comes from the statusLine (decision 2026-10-27).

**Smallest change: a hook-observed fact, not a state flip.** It cannot be three more hooks wired
into the turn path. `Bus.TurnState` only flips between `idle` and `working`, and refuses to leave
any other state, so a declared `waiting` or a gate is never overwritten (`bus/turnstate.go`). A
hook that set `blocked` there would also have nothing to clear it: `Stop` and `UserPromptSubmit`
cannot leave `blocked`, and the screen check writes an observation (`bus.Observation`), never the
declared state.

So the change is a new observed fact beside the screen's, with its own rules:

- `PermissionRequest`, and `Notification` with `permission_prompt`, record "blocked on a dialog
  (tool)" with its Redis time;
- the fact clears on the next hook of the same session showing the dialog was answered
  (`UserPromptSubmit`, `Stop`, `StopFailure`), and is stale past a bound, after which the screen
  decides;
- readers (For you, the stuck check, the wake gate) treat it as they treat the screen's blocked
  observation, earlier and with the tool named;
- the declared state is never touched: a declared `blocked` is the 4-eyes gate's, and busmon draws
  it as one (`cmd/busmon/agents.go`), apart from the screen's observation; `StopFailure` can also join the turn hooks as an `idle`
  flip, as Grok's profile already does.

That is a small design of its own (provenance, freshness, clearing), to spec before wiring. We
should not add `PostToolUse` on every tool call to clear sooner: it sits on the hot path, and our
hook has a 2-second budget.

**Risks.**

- A hook can fire for a sub-agent's dialog. AO correlates by tool id for that reason; ours would
  read the hook's session id and ignore what is not the main session.
- The hook budget is on every event, so it must stay a single bus write.
- Claude-only: Codex and Grok keep the screen.

## When it applies

Three ideas, each to spec separately, if the person wants it.

- **Idea 1**: the person already said yes, so it is the next spec.
- **Idea 2**: small and independent; it can follow when the queue allows.
- **Idea 3**: independent, but a design first (a new observed fact), not a wiring.

## Evidence

AO, read at its local checkout, HEAD `134a2f0fee7b7d9267447a85578d6486452890e7`, not modified:

- `docs/scm-observer.md`, `backend/internal/lifecycle/reactions.go` (`ApplyPRObservation`,
  `sendOnce`, `formatCIFailureMessage`);
- `docs/harness-version-refresh.md`, `docs/harnesses/model-catalogs.md`,
  `backend/internal/service/systeminstall/manager_versions.go`,
  `backend/internal/adapters/agent/claudecode/model_versions.go`;
- `docs/architecture.md` (Durable session facts, Status derivation),
  `backend/internal/adapters/agent/claudecode/activity.go`, `terminal_activity.go`.

Ours, on `origin/dev` `bc3b20e2`:

- the merge queue: [mergequeue/forge.go](../../../mergequeue/forge.go),
  [mergequeue/derive.go](../../../mergequeue/derive.go),
  [cmd/agentbus/mergequeuechore.go](../../../cmd/agentbus/mergequeuechore.go);
- [bus/once.go](../../../bus/once.go) (`FencedCmdOnce`),
  [dispatch/clientupdate.go](../../../dispatch/clientupdate.go),
  [cmd/agentbus/redislatest.go](../../../cmd/agentbus/redislatest.go);
- the state rules: [bus/turnstate.go](../../../bus/turnstate.go) (idle and working only);
- the turn hooks: [cmd/agentbus/turnhook.go](../../../cmd/agentbus/turnhook.go),
  [rolecfg/launch.go](../../../rolecfg/launch.go), [rolecfg/grokhooks.go](../../../rolecfg/grokhooks.go);
- the decisions it builds on: [event-driven dispatch](../decisions/2026-10-10-event-driven-dispatch.md),
  [agent lifecycle](../decisions/2026-10-28-agent-lifecycle.md),
  [context fill](../decisions/2026-10-27-context-fill.md).

## Limits / what would invalidate it

- The study was time-boxed. The AO sources were read at the named files only, not run.
- AO's hook semantics are taken from its code comments; the Claude Code version they hold for was
  not checked.
- The npm-vs-native channel question in idea 2 is open.
- Idea 1 assumes every PR's head branch is on a board entry. Today master's requests record a
  branch only sometimes. If the person's other projects push branches outside the board, the
  owner mapping needs a rule of its own.
