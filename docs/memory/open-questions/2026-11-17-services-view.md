---
id: 2026-11-17-services-view
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# A services view with restart buttons and readable logs (the person's item 18)

## Finding/decision

busmon's F3 shows each service as a card; Enter or l opens its log tail. The cards offer no restart: their footer only names `agentbus --project <p> service status|logs|restart` to type in a shell, and the log is a raw tail.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): Spec first: a restart through the existing `agentbus service` commands only (never a new process path), refused while a service hosts a terminal, and a log page that is filtered and readable (levels, time, one entry per line).

## When it applies

When the person has to restart a service or read why it failed.

## Evidence

[cmd/busmon/services_cards.go](../../../cmd/busmon/services_cards.go) (the F3 cards, their footer), [cmd/busmon/services.go](../../../cmd/busmon/services.go) (`serviceLogBody`, the log page).

## Limits / what would invalidate it

Restarting the holder ends every agent's terminal: the spec must keep that out of a single click.
