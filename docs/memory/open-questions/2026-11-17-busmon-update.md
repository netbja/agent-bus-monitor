---
id: 2026-11-17-busmon-update
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# Updating from busmon, without setup.sh: the `agentbus update` command

## Finding/decision

Updating means `./setup.sh update` and restarts by hand; the person wants to update from busmon. The quiet-update spec (r13, spec only) covers it, with the one-command setup and a snapshot to roll back to; idea i1 is the same subject.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): No separate work: decide whether to build the quiet-update spec, and when.

### The variant: an `agentbus update` command (the person, 2026-11-20; added by architect)

The person asked for an `agentbus update` command in place of the setup.sh script. The
quiet-update spec already designs that command (§3, §4.3). It is not built: `dev` at b83d4b7b has
no `update` command.

**What it would do,** as the spec has it:
1. **Fetch the release.** It takes the newest release tag on the release branch, not the branch's
   head, into that tag's own source tree (§4.1). A developer checkout is only notified.
2. **Build and install both binaries.** It builds them beside the current ones and runs their
   `version`. Then it installs `agentbus` before `busmon`, each by a copy and a rename. A running
   agent keeps the file it started with.
3. **Restart the project services one at a time,** dispatch then missions. Each restart waits for
   `agentbus health` to show the service green. A red check rolls the update back, from the
   snapshot taken before any change (§4.2, §4.5).
4. **Agents keep running on the old build.** The holder is never restarted.

**The reload proposal, an addition to the spec.** In the spec, an agent moves to the new build only
at its next relaunch. The proposal is to then list the agents still on the old build, each with its
`agentbus reload <role>`. Only on the person's yes, the idle agents are reloaded one at a time.
`reload` exists (lifecycle part C, `cmd/agentbus/reload.go`): it resumes the agent's session and
never types into a pane.

**What stays in setup.sh:**
- the first install: prerequisites, broker, units, skills and its questions;
- uninstall;
- the developer path: `./setup.sh update` pulls the checkout's branch, as today;
- the first update from a binary that has no `update` command (spec §4.3).

**What it must never do:**
- **Touch another project's units.** This is the person's rule, and it differs from the spec. The
  spec restarts every project's services (§4.3 step 5). setup's restart offer does too, today
  (`RestartStale`, `setup/services.go`). Under the person's rule, `agentbus --project <p> update`
  restarts only `<p>`'s dispatch and missions. It lists the other projects' stale services with
  their own `service restart` command, as `service stale` does now.
  - The rule does not isolate the other projects from the new binaries. Those are installed once
    per host, so another project's next `agentbus` call runs the new build.
- Stop or relaunch a running agent without the person's yes.
- Restart a holder.
- Install unattended. `updates.auto` defaults to `notify`.
- Write to the broker, unless the release declares a migration (spec §4.6).

**Recommendation.** No separate spec: amend the quiet-update spec once the person answers. The
amendment covers the per-project scope and, if wanted, the reload step. Build the manual
`agentbus update` first, and the `updater` service and its window later.

**Questions for the person:**
1. **Scope:** does an update restart only this project's services, with the others listed and
   their restart commands? Or every project's, as the spec says?
2. **Reload:** after an update, should it offer to reload idle agents onto the new build, one at
   a time, on your yes? Or do agents move at their next relaunch, as the spec says?
3. **First slice:** the manual `agentbus update` with its snapshot and rollback first, and the
   automatic `updater` service later?
4. **busmon:** in the first slice, should busmon's palette have `/update`? It would show the exact
   command and run it on `y`.

## When it applies

When the person asks to update, or busmon's `↑ vX.Y.Z` shows a release.

## Evidence

- [the quiet-update spec](../../superpowers/specs/2026-11-14-quiet-update-design.md): §3, §4.3
  (steps 5 and 6), §4.5.
- Today's restart offer for every project's stale services: `RestartStale` in
  [setup/services.go](../../../setup/services.go).
- `agentbus reload`: [cmd/agentbus/reload.go](../../../cmd/agentbus/reload.go) and
  [the agent lifecycle decision](../decisions/2026-10-28-agent-lifecycle.md).
- The person's variant: master's directive of 2026-11-20 (bus cmd `1795175472520-0`).

## Limits / what would invalidate it

Master never reinstalls without the person's yes. Nothing is built until the person answers the
questions above.
