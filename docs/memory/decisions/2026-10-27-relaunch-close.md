---
id: 2026-10-27-relaunch-close
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-27
last_verified: 2026-10-27
author: coder
confidence: verified
---

## Finding/decision

**`agentbus up <p>` now closes a relaunched agent's old pane, once the new one is proven.**
Bernard decided it on 2026-10-27 ("Fermer l'ancien"). Since v0.21 a dead agent has come back in
a new pane split beside its old one, and the old one was left as it was. On a team relaunched
often, those dead shells piled up in every tab.

- **Closing is not typing.** Nothing is ever typed into a pane that existed before the relaunch
  (`typedIntoExisting` still holds). Whether a shell waits at its prompt cannot be proven from
  outside, so it receives no text. Closing it needs no such proof.
- **Only the exact pane the dead agent occupied.** That is the pane its snapshot recorded, and
  only if that pane is in the tab. Never the tab, never another pane of it, never a tab that is
  not an agent (busmon). With no snapshot, nothing is closed.
- **After the proof.** `up` reads the agents' snapshots once a second for up to 60 s. The proof
  is the relaunched agent's verified identity: a new pid, alive with its recorded start time,
  bound in the pane the relaunch created (`provenIn`).
- **Re-read right before the close.** The old pane is closed only if it is still one plain
  interactive shell (`paneShell`, `idleShell`) at that moment. If it is busy, gone or unreadable,
  or there is no proof in time, it is left as it is and the output says why. `--dry-run` names
  the pane it would close.
- **Master may run it without a prompt.** Bernard also decided "Autoriser up --resume". In
  `roles.toml`, `[roles.master.tool_rules]` allows exactly `agentbus up agent-bus-monitor
  --resume` and its `--dry-run` form: no other project, flag or wildcard. Every other role's
  launch is unchanged.

## When it applies

`agentbus up <p>` on an open workspace, with or without `--resume`, when the agents' snapshots
are readable. The workspace hook never relaunches, so it never closes anything.

## Evidence

- Master's directive `1793108291620-0` (task relaunch-close).
- Code: `cmd/agentbus/relaunch.go` (`closeOldPanes`, `provenIn`), `cmd/agentbus/up.go`
  (`relaunchUp` wires the proof); tests `TestRelaunchOpenClosesTheOldPaneOnceTheNewOneIsProven`,
  `TestRelaunchOpenLeavesTheOldPaneWithoutProof`, `TestRelaunchOpenClosesOnlyTheRecordedPane`,
  `TestRelaunchOpenDryRunSaysWhatItWouldClose`, `TestRelaunchOpenReportsAFailedClose`,
  `TestUpClosesTheOldPaneOfAProvenRelaunch`.
- Spec of the relaunch itself: [startup and relaunch](../../superpowers/specs/2026-10-20-startup-relaunch-design.md).

## Limits / what would invalidate it

A snapshot whose pane id herdr later reuses for another pane would point at the wrong pane. The
re-read limits the damage: only a plain shell is ever closed. An agent that takes more than 60 s
to bind leaves its old pane open; the next `up` does not close it either, because that `up`
finds the agent alive.
