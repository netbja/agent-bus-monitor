---
id: 2026-10-26-host-dispatcher
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-26
last_verified: unknown
author: architect
confidence: unknown
---

## Finding/decision

**One dispatcher per host. The conductor stays one unit per project.** This revises the
[project services decision](2026-10-14-project-services.md) for dispatch only. Bernard chose this
on 2026-10-26 (option A of the design; his answer to master's ask is `1793035843860-0`). It is
decided but not built: nothing here describes running code yet.

- **Host dispatcher.** One `agentbus-dispatch` user service per host. It runs one project runner
  per enrolled project, and each runner is today's `dispatch.Run`. Every runner keeps its own
  `{p}:dispatch` lease and token, Redis client, herdr session, log, crash file and restart budget.
  Fenced writes, `Decide`, the wake line and the lease timings do not change.
  - *Why:* every per-project dispatcher repeated work that belongs to the host. Each one read the
    same quota sources every minute, spawning `codex app-server` once per project. Each project
    also added a unit, a budget and a log, and a binary update needed one restart per project.
- **Quota collection is shared, quota policy is not.** One reading a minute for the host. Warnings,
  holds, the coordinator's quota pause and resume directives stay inside each runner, with that
  project's thresholds.
- **Isolation between projects is a contract:**
  - every goroutine goes through one recovering helper;
  - every external call carries a deadline;
  - a watchdog over both the reconcile and the health loop gives up the lease of a runner that
    stops making progress, then replaces it;
  - no Redis client, herdr runtime, log or budget is shared. Each project's decision log goes to
    its own file, never the process's stderr;
  - every project setting comes from the project's env file through one lookup, following an
    inventory of the environment reads (broker, herdr, escalation, quota policy, health inputs,
    project directory). Only host-scoped keys may come from the process environment.
- **Legacy units never step aside on their own.** Until `up`/`migrate` removes it, a per-project
  dispatch unit contends through the lease. Stepping aside because a host dispatcher is merely
  installed would fail open.

  *Why:* goroutines share one process, so isolation can only be cooperative.
- **A deleted project stays deleted, enforced atomically.** The lease is acquired only while the
  `{p}:services` `installed` marker exists, checked in the same script. Enrolment is host-local
  (`services/<p>.env`, not `<p>.disabled`). `down <p>` disables one runner and never stops the
  host dispatcher.
- **The conductor is not merged.** It runs repository code with that project's environment and
  checkout. An integration can last minutes and must never be interrupted by another project's
  reconfiguration. A runaway test must cost one project only.
- **Rejected:** a parent process that forks one child per project (option B), because it is a
  supervisor of our own, which the services decision rules out. A template unit (option C), because
  it saves only unit files.

## When it applies

Any change to how dispatch is started, stopped, restarted, enrolled or observed; to `agentbus
up`/`down`/`service` for dispatch; to dispatch's `{p}:services` record; to quota collection; to
`busmon --delete`'s local cleanup. It does not apply to the missions conductor, which stays under
the project services decision.

## Evidence

- Design, decided: `docs/superpowers/specs/2026-10-26-global-dispatcher-design.md` (branch
  `docs/global-dispatch-spec`). It covers options, isolation contract, restart budget, migration
  and tests.
- Decision: Bernard's answer `1793035843860-0` to master's ask, relayed in thread
  `1793035500842-0` (Redis ids, bounded retention: the spec's status line records it durably).
- Not yet built or verified: `confidence` stays `unknown` until the implementation and its tests
  merge.

## Limits / what would invalidate it

- Isolation is cooperative. A goroutine stuck in a call that ignores its context leaks until
  restart. A late stuck prompt can type one extra wake line. One project's memory use is felt by
  all. If any of these proves frequent in practice, reconsider per-project processes.
- A project on a broker or herdr session that the host dispatcher cannot reach is still served
  through its own env file. A setting read from the process environment would break the
  per-project contract.
- Until migration is complete, legacy per-project dispatch units may coexist. The lease keeps them
  exclusive.
