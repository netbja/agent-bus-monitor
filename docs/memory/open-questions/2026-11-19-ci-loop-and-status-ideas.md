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
restart does not resend. The conflict nudge re-arms when the PR is seen mergeable again. Nudges
wait while the agent needs input, and none goes to an agent blocked on a permission dialog.
AO pastes the message text into the agent's terminal.

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
JSON), compare it with the installed version the agent snapshot reports, and report `newer
available` in health and For you. The update itself stays the person's (or a quiet-update step):
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

**Smallest change.**

- Install three more hooks on Claude profiles:
  - `PermissionRequest` sets `blocked`, with the tool, on the first event, with no screen
    polling;
  - `Notification` with `permission_prompt` sets `blocked`, and with `idle_prompt` sets `idle`;
  - `StopFailure` sets `idle`, as Grok already does.
- They go through the same `roles turn` path, as `AutoTurn` flips of the snapshot only.
- The screen check stays as the backstop, and as the only source for clients without hooks.
- Clearing: AO uses the `PostToolUse` of the blocking tool. The cheaper equivalent for us is the
  next `UserPromptSubmit` or `Stop`, plus the screen check. We should not add `PostToolUse` on
  every tool call: it sits on the hot path, and our hook has a 2-second budget.

**Risks.**

- A hook can fire for a sub-agent's dialog. AO correlates by tool id for that reason; ours would
  read the hook's session id and ignore what is not the main session.
- The hook budget is on every event, so it must stay a single bus write.
- Claude-only: Codex and Grok keep the screen.

## When it applies

Three ideas, each to spec separately, if the person wants it.

- **Idea 1**: the person already said yes, so it is the next spec.
- **Ideas 2 and 3**: small and independent; each can follow when the queue allows.

## Evidence

AO, read at its local checkout, not modified:

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
