---
id: 2026-10-10-event-driven-dispatch
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-10
last_verified: 2026-10-11
author: claude
confidence: inferred
---

## Finding/decision

v0.8 adds `agentbus dispatch`, a deterministic process (no LLM) that wakes **unarmed** agents
through herdr when `{project}:cmd` holds work for them. This **revises on purpose** the earlier
rule "don't reintroduce a pane-relay / `Restart=always` watcher daemon". The replacement rule:

> The only automated process that types into panes is `agentbus dispatch`, and it types a wake
> line, never a message; master's manual pane injection remains the fallback while no dispatcher
> runs.

Shape of the decision, each part with its reason:

- **Fixed wake line, no content.** `[agentbus#<nonce>] <project>: <n> message(s) waiting for
  <agent>. Run: agentbus --project <project> subscribe --drain <agent>`. Peer text is never typed
  into a terminal; delivery stays in Redis (`subscribe --drain`, PEL first, ACK after output).
- **Drain, not plain subscribe.** A plain `subscribe` floors at "now" and acknowledges older
  entries unseen — exactly the backlog the agent was woken for. Entries past the 24h horizon are
  delivered flagged `stale` with `age_s`, never acted on without confirmation.
- **Type only into a ready pane.** herdr state idle/done, no client busy signature, a visible
  empty input (probed per client; an unprobed client is never prompted), a settled screen, the
  pane not focused, and the pane's foreground process matching the pid + start time the agent
  registered with `agentbus status`. Anything else defers and escalates after 10 minutes (4× for
  `working`) to the role `master`, plus a `notify`.
- **Bound identity (v0.10.1).** An agent whose commands do not descend from its pane (Codex runs
  them through a shared `codex app-server`, whose env even names another session's pane) cannot
  register itself. `agentbus bind <agent> [--pane P] --pid N` records a pid checked against the
  pane's foreground set; `agent-launch` binds its own pid just before exec hands it to the client.
  An unverified `status` keeps a verified identity; a verified one replaces it.
- **Bounded duplicates, fenced writes.** One prompt per agent per 60 s, three per obligation,
  enforced by a Lua reservation fenced by the `{p}:dispatch` lease token; every dispatcher write
  is fenced so a dispatcher that lost its lease writes nothing.
- **Conditional master sleep.** Master may stop arming only while `agentbus dispatch --status`
  reports up; otherwise the v0.7 rule (master stays armed, wakes peers by hand) stands. The
  sentinel, woken by cron rather than by the dispatcher, checks `dispatch --status` and notifies
  the human when it is down.
- **A herdr pane, not systemd.** The bootstrap template gains a `dispatch` tab
  (`--no-dispatch` omits it). *Superseded on 2026-10-14 (v0.12):* dispatch and missions now run
  as user services (systemd `--user`, launchd), with herdr tabs only as an explicit fallback —
  see [project services](2026-10-14-project-services.md).

Why the old rule no longer holds (spec §11): `cmd/busbridge` was dropped for hard-coded pane ids
and because wake-on-exit made a relay unnecessary for Claude Code. Since v0.7 other clients are
first-class and their background-task wake is unverified; staying reachable by re-arming costs
tokens every idle window (the cost PR #38 removed); and pane ids are now self-registered and
pid-verified.

## When it applies

Any change to how agents are woken, to the skills' arm/sleep rules, to what may type into a herdr
pane, or to the `{p}:dispatch` / `{p}:wake:{agent}` keys and the `wake=no` / `escalation` /
`blocked` protocol additions.

## Evidence

- Design spec, revision 3 (`specs/2026-10-10-event-driven-dispatch-design.md`, kept out of the repository):
  §3 policy, §10 runtime probes per client (2026-10-10, herdr session `busmon`), §11 the revision
  of the pane-relay rule, §13 review traceability (Codex counter-audit and spec review).
- Implementation plan (`plans/2026-10-10-event-driven-dispatch.md`, kept out of the repository).
- Branch `feat/v0.8-dispatch`, commits `ce79170`…`e869259` (protocol, drain, lease and fenced
  writes, `dispatch/` package, busmon indicators), plus the docs commit that adds this note.
- At `e869259`: `go test ./... -count=1` passed against a disposable Redis 8.6.3
  (`AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392`, no skipped test in `bus`, `cmd/agentbus`,
  `dispatch`); `go build ./...` and `go vet ./...` passed.

## Limits / what would invalidate it

- Active since v0.8.0 (merged); check Git, not this note.
- Live acceptance, 2026-10-11 ([v0.10.0 release notes](../../releases/v0.10.0.md)): agy was
  woken on its own twice and escalation fired after 10 minutes; Codex 0.162 could not be
  verified (the app-server case above, fixed by `bind` in v0.10.1). Still not measured: 30 min
  idle with zero prompts and zero token growth, focus and typed-input deferral on every client.
- Accepted residuals (spec §3.3, §6): a dispatcher suspended between its token check and the herdr
  call can emit one extra prompt; `busmon --delete` while a dispatcher runs can orphan one fenced
  write; a focus switch inside the inspect→prompt gap is not caught; entries trimmed before an
  agent's group read them are lost to it (surfaced as `✂?`, not counted).
- Client signatures rot with client releases (Kimi already disagreed with herdr's busy detection).
  A client whose screen no longer matches is deferred and escalated, not prompted — but a
  signature that matches the wrong screen would type into a busy pane.
- If a second automated pane writer appears, or the dispatcher starts typing message content, the
  rule above has been broken, not extended.
