# v0.21.1: herdr's native restore through the launcher — design

Status: draft for review (coder, 2026-10-21). Task `v0211-spec`, branch `docs/v0211-spec`.
Research and spec only; no code in this change.

Sources read: [journal checkpoint, roadmap item 1](../../memory/journal/2026-10-20-v021-handoff.md);
herdr **v0.9.5** (tag `v0.9.5`, commit `2ecf19a`): `docs/next/.../session-state.mdx`,
`add-herdr-support.mdx`, `integrations.mdx`, and the source behind them (`src/app/api/panes.rs`,
`src/terminal/state.rs`, `src/persist/restore.rs`, `src/agent_resume.rs`, `src/detect/mod.rs`,
`src/integration/assets/claude/herdr-agent-state.sh`); `scripts/agent-launch`, `rolecfg/launch.go`,
`rolecfg/template.go`, `dispatch/herdr.go`; Claude Code settings docs (hooks merge across layers).

## The problem

After a herdr server restart (reboot, `herdr session stop`/start), herdr restores the layout and
resumes agent panes from a session reference. For Claude Code that reference comes from the
official integration `herdr:claude`: its `SessionStart` hook reports the session id, and herdr
restores the pane by typing `claude --resume <id>`. That command bypasses `agent-launch`: no role
rules (`--settings`), no model or effort from the profile, no `AGENT_BUS_*` environment, no
`agentbus bind`. The agent comes back as a bare Claude session that does not know it is `master`,
which is the likely cause of a resumed master losing its identity.

## Answers first

### 1. How `herdr:claude` and a launcher-reported resume command coexist (herdr 0.9.5, from source)

- **`herdr:claude` reports session identity only.** Its hook sends `pane report-agent-session
  --source herdr:claude --agent claude --agent-session-id <id>` on `SessionStart`. Herdr stores it as
  the pane's *persisted session* (`set_agent_session_ref`), not as hook *authority*: Claude's
  `idle`/`working`/`blocked` still come from screen detection. So a Claude pane normally has no
  hook authority.
- **A reported resume command beats the official session at restore.** `pane_restore_startup`
  (`persist/restore.rs`) checks the pane's saved `agent_resume` first, and only without one falls
  back to the official plan (`claude --resume <id>`). So if our command is recorded, it is the one
  herdr runs.
- **Who may record a resume command** (`can_record_reported_resume`, `state.rs`): the source that
  holds hook authority, or, when nobody holds it, any source whose `--agent` label matches the
  agent herdr detects in the pane (no recent process exit). A Claude pane launched by us therefore
  accepts a command reported by our own source (`agentbus`) with `--agent claude`, **without** taking
  the pane over.
- **A state report would collide.** `report-agent` from our source, once `herdr:claude` has
  reported the session, is ignored as an owner conflict (`current_session_owner_conflicts`: a
  non-official source cannot take over). If it arrived first, it would take hook authority and then
  own the pane's state: `herdr agent get`, which our dispatcher reads (`dispatch/herdr.go`), would
  show whatever we last reported instead of what the screen shows. **So we report no state.**
- **The command is dropped** when another agent takes the pane, when the detected agent changes, or
  when the pane returns to its shell (the agent exited): herdr's own safety net, which is the
  behaviour we want (a retired agent is not resurrected).
- **Caveat.** The docs say "your source must hold the pane first". The code's no-authority branch
  (detected agent matches) is what lets a session-only report through. This is the one point the
  live test must confirm on 0.9.5, and a future herdr may tighten it: the design falls back to
  today's behaviour if it does (below).

### 2. Is metadata-only reporting possible?

**Not for resume.** `pane report-metadata` takes title, display agent, state labels and tokens;
it carries no resume command, and the docs state metadata "changes presentation without taking
over ... session restore authority". It remains useful later for presentation (e.g. display
`coder · claude`), which is out of scope here. What we do use is the next-lightest report:
**`report-agent-session` with a resume command and no session id**. For a non-official source herdr
derives no session reference from it (`session_ref_from_report` returns none), so it changes
neither state, nor authority, nor the persisted session: it only records the command.

## Options

| | Approach | Verdict |
|---|---|---|
| A | A Claude Code `SessionStart` hook, added by the launcher to the `--settings` it already passes, reports `report-agent-session --source agentbus --agent claude -- <launcher argv>` | **Recommended** |
| B | `agent-launch` reports before it execs the client | Rejected: the session id of a new session is not known yet; Claude is not detected yet, so herdr answers `resume_not_accepted` unless we take authority with a state report, which then freezes the state the dispatcher reads |
| C | Metadata only | Impossible (above) |
| D | Turn herdr's restore off (`resume_agents_on_restore = false`) and relaunch through `agentbus up` | Heavier: v0.21 relaunch opens a new pane per dead agent and needs the person to run `up`; it loses herdr's automatic restore. Kept as the manual fallback it already is |
| E | Become a full lifecycle reporter (report state on every change) | Rejected: duplicates herdr's screen detection, and a missed report leaves a wrong state where the dispatcher reads it |

## Recommended design (A)

### What the launcher adds

For a `claude-code` profile, `rolecfg.build` always passes `--settings` (today only when the role
has tool rules), with one hook besides the permissions:

```json
{"permissions": {...},
 "hooks": {"SessionStart": [{"hooks": [{"type": "command",
   "command": "<abs agentbus> roles report-resume"}]}]}}
```

Claude Code merges hook lists across settings layers, `--settings` included, so `herdr:claude`'s
own hook in `~/.claude/settings.json` keeps running beside ours. The agentbus path is absolute
(`AGENTBUS_BIN`, else `os.Executable()`), so the hook does not depend on the client's `PATH`.

### `agentbus roles report-resume` (new, internal, no project, no broker)

Sits in the `roles` block above the project-required exit, like the other script verbs. It reads
the hook's JSON on stdin and the environment Claude inherited from the launcher, and:

1. exits 0 silently unless `HERDR_ENV=1`, `HERDR_PANE_ID`, `HERDR_BIN_PATH`, `AGENT_BUS_AGENT`,
   `AGENT_BUS_PROJECT` and `ROLES_TOML` are set, `hook_event_name` is `SessionStart`, the input
   has a `session_id`, and it is not a subagent (`agent_id` present, as `herdr:claude` checks);
2. builds the argv (pure function, unit-tested):
   `env ROLES_TOML=<manifest> <repo>/scripts/agent-launch <role> <project> [--profile <p>] --resume <session_id>`
   - it mirrors the template's own command (`rolecfg/template.go`); `env` satisfies herdr's
     "plain command name" rule and the absolute script path needs nothing on `PATH`;
   - `--profile` only when `AGENT_BUS_PROFILE` is not the role's own profile (a swapped role);
     this needs `agent-launch` to accept `--profile` **and** `--resume` together (today it takes one
     or the other; `LaunchProfileResume` already supports both);
   - refused (logged, nothing reported) if any argument holds an apostrophe or control character,
     or the whole exceeds 64 arguments / 8 KiB;
3. **prints nothing on stdout** (a SessionStart hook's stdout becomes Claude's context), starts the
   report in a detached child and exits 0 at once, so Claude's startup never waits on herdr;
4. the child runs `"$HERDR_BIN_PATH" pane report-agent-session "$HERDR_PANE_ID" --source agentbus
   --agent claude --seq <unix ns> -- <argv>`, retrying once a second for up to 15 s while herdr
   answers `resume_not_accepted` (Claude not yet detected), and logs the outcome to
   `~/.local/state/agentbus/<project>/resume.log` (the services' state directory).

It runs on every `SessionStart`: `startup`, `resume`, `clear`, `compact`. So the recorded command
follows the conversation when `/clear` starts a new session.

### Fallback and failure

If the report is never accepted (a future herdr requiring authority, `HERDR_BIN_PATH` missing,
retries exhausted), nothing changes from today: `herdr:claude`'s `claude --resume` still restores the
conversation, without the launcher. The log line says so. Nothing here can stop a session starting.

### Out of scope

- Other clients. The launcher refuses resume for them today. Codex has the same bypass
  (`herdr:codex` restores `codex resume <id>`); it needs a Codex hook and `agent-launch --resume`
  for Codex, which is its own change.
- Display metadata (`report-metadata`), and `--release` on exit (herdr clears the command when the
  shell returns).

## Test plan

### Unit and script tests (no herdr, no broker)

- `resumeArgv`: role, project, manifest, repo, session id → exact argv; with a swapped profile;
  refusal on apostrophe, control character, 65 args, > 8 KiB.
- `report-resume` gate: each missing variable, non-`SessionStart`, subagent input, no `session_id`
  → exit 0, no herdr call, empty stdout.
- Retry: a fake `HERDR_BIN_PATH` that answers `resume_not_accepted` twice then succeeds → three
  calls with the same argv and increasing `--seq`; one that always refuses → stops at 15 s, logs.
- Detach: the hook returns before a fake herdr that sleeps 5 s has finished.
- `build`: a `claude-code` profile with no tool rules now gets `--settings` with the hook; with
  rules, both. **This changes the byte-identical dry-run outputs in `rolecfg` and
  `tests/roles_scripts_test.sh`; those expectations are updated and named in the PR.**
- `agent-launch <role> <p> --profile <x> --resume <id>` parses and passes both.

### Live test (Bernard, attached to a separate herdr session)

Restore runs only once a client attaches, so this needs a person at a terminal. Nothing touches the
team's session or the live broker.

Preconditions, checked by coder beforehand and listed in the hand-off: the branch built in its
worktree (`go build -o agentbus ./cmd/agentbus`), the test broker up (`docker start
agentbus-v08-test`).

Observed on 2026-10-21: `herdr integration status` reports `claude: outdated (v7 < v11)`. v7 meets
the v6 minimum for native restore, so the test is valid as it is, and it tests what runs today.
Updating it (`herdr integration install claude`) rewrites `~/.claude/settings.json` hooks for every
Claude session on the machine: Bernard's decision, before or after the test, not coder's.

What Bernard does:

1. In a terminal **outside** any herdr session: `herdr --session abm-resume-test`.
2. In its first pane:
   ```bash
   export REDIS_URL=redis://127.0.0.1:6392 AGENTBUS_BIN=/data/projects/agent-bus-monitor/.worktrees/<branch>/agentbus
   /data/projects/agent-bus-monitor/.worktrees/<branch>/scripts/agent-launch coder abm-resume-test
   ```
3. In Claude, type: `Remember the word PELICAN. Reply only OK.`
4. From **another** terminal (not inside that session): `herdr session stop abm-resume-test`.
5. Send coder the session file path from `herdr session list --json` (coder checks that the pane's
   saved `agent_resume` is the `env ROLES_TOML=… agent-launch coder abm-resume-test --resume <id>` argv).
6. Attach again: `herdr --session abm-resume-test`.
7. Expected: the pane runs that command by itself, Claude resumes. Ask: `What word did I give you?`
   → `PELICAN`. Then run in another pane: `agentbus --project abm-resume-test agents`
   (with the same `REDIS_URL`) → `coder` bound to the new pane and pid.
8. In Claude, type `/clear`, then repeat steps 4–6: the pane resumes the **new** (empty) session,
   not the old one.
9. Clean up: `herdr session stop abm-resume-test`; coder deletes the test project from the test
   broker (`REDIS_URL=… busmon --delete abm-resume-test --yes`).

Pass: step 7 resumes through the launcher (identity bound, role settings present: `ps` shows
`--settings` on the claude process) and step 8 follows `/clear`. If step 5 shows no `agent_resume`,
herdr refused the session-only report: the caveat above is real, and the fallback (today's
behaviour) is what Bernard will have seen.

## Open questions for review

1. Herdr's docs and code disagree on whether a report must hold the pane. Accept relying on the
   code path, verified live on 0.9.5, with the silent fallback? (Proposal: yes; the log records it.)
2. `--settings` now on every Claude launch, not only roles with tool rules. Any objection?
   (Proposal: no; it carries only the hook when there are no rules.)
3. Source name `agentbus`: herdr reserves the `herdr:` prefix only. (Proposal: `agentbus`.)
4. Update the Claude integration from v7 to v11 before the live test? (Proposal: test on v7
   first, as installed; v11 only narrows which `SessionStart` sources its hook matches.)
