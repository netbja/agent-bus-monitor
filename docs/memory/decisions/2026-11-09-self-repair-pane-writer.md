---
id: 2026-11-09-self-repair-pane-writer
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-11-09
last_verified: 2026-11-14
author: coder
confidence: verified
---

## Finding/decision

**The dispatcher's self-repair may run the template command in a new pane it creates, to relaunch
a dead agent; it still never types into an existing pane.** The person accepted it on 2026-11-09
(the answers to the self-repair spec's part 8, Q2, relayed by master's directive of that day). It
was decided then and is **built since step 7b** (2026-11-13): `dispatch.self_repair = on`, the default,
relaunches a dead agent through `relaunch.ServiceRole`, which splits a new pane and runs the template
command there; R3 and R4 are built since step 8 (a network hold and a closed workspace told
to master once each); start at login (step 9) follows its own step.

This is a third exception to "the dispatcher types only the fixed wake line", next to `/clear`
after a declared hand-off. It **amends in writing** three texts, rather than working around them:

- the [event-driven dispatch decision](2026-10-10-event-driven-dispatch.md): its rule "the only
  automated process that types into panes is `agentbus dispatch`" stands. The watcher is a loop of
  that same dispatcher, not a second writer. What it writes grows by one thing, the template's own
  command, into a pane it has just split;
- the [agent lifecycle decision](2026-10-28-agent-lifecycle.md): reload stays sentinel's job. A
  self-repair is a relaunch **without a kill**, of an agent whose process is already gone, by the
  dispatcher;
- `CLAUDE.md`'s rule "the dispatcher is the one automated pane writer", which names this
  exception.

What the person decided with it (same answers):

- **Default on.** `shadow` mode only in a development mode (`AGENT_BUS_DEV=1`); outside it the
  setting takes `on` or `off`, and `shadow` is refused at validation.
- **Required agents are recreated** when their tab is gone (`required = true` in `roles.toml`);
  optional ones (a pop, for example) are only reported. A required agent is stopped on purpose by
  `retire` or `suspend`, never by closing its tab.
- **Every client resumes** (Claude Code, Codex, Kimi, Gemini): the session id of each is captured
  and proved. A client with no resume at all (Grok, to verify) is only reported.
- **Budgets** 1 per 30 min and 3 per 24 h per agent by default, set in `config.toml` and busmon's
  settings screen (`C`); master may reset a spent budget.
- **Master is relaunched first.** It takes the pilot lease the usual way once the old one expires.
- **Notifications:** every event to master on the bus; the desktop only for a failed proof and a
  spent budget.
- **Start at login is an option**, off by default, which amends the
  [project services decision](2026-10-14-project-services.md)'s "never at login": one Go
  interface, one backend per platform: systemd `--user enable`, and a macOS LaunchAgent with
  `RunAtLoad`. A Linux without systemd and Windows are left open, since the services still need a
  manager there: the "never a hidden process" rule of that decision is **not** lifted. **No linger**:
  without a session, herdr and the workspace are not open, so there is nothing to relaunch into.

- *Why:* a dead agent today waits for an AI's turn (master, sentinel) or the person's hand. If
  master itself dies, nothing recovers. The person wants an autonomous automaton for the cases
  whose facts are measurable.

## When it applies

The dispatch service's self-repair loop, once built, on a host where process identity is
verified: Linux (`/proc`) and, since step 10, macOS (`sysctl kern.proc.pid`, `dispatch.procStat`). The rules, preconditions,
intent, budgets and proof are the spec's:
[self-repair watcher](../../superpowers/specs/2026-11-09-self-repair-watcher-design.md).

## Evidence

- The spec's part 8, answered by the person on 2026-11-09 (their answers file, relayed and
  summarised in master's directive `self-repair-answers`).
- PR 389 (the spec, reviewed by reviewer-codex and foureyes), PR 388 (`snapLife` delegating to
  `pidListedNoMore`, guarded by `TestSnapLifeReadsAGonePidDead`).

## Limits / what would invalidate it

The exception covers a pane the dispatcher has **just created**, and only the template command of
the role it relaunches. Typing into an existing pane, typing message text, or relaunching an agent
whose departure was declared (reload, swap, retire, suspend, pause, hand-off) breaks the rule
instead of extending it.
