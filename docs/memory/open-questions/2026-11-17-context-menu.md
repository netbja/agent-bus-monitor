---
id: 2026-11-17-context-menu
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# A right-click context menu in busmon

## Finding/decision

busmon handles clicks and the wheel, but every action is a key; a right-click menu would show the actions of the row under the mouse.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): Spec first, as master asked: which rows get a menu, which actions (each one an existing key's action, nothing new behind it), and how it behaves in terminals that do not report right-clicks.

## When it applies

When busmon's mouse handling or a row's actions change.

## Evidence

[cmd/busmon/tea_model.go](../../../cmd/busmon/tea_model.go) (the mouse messages).

## Limits / what would invalidate it

Not every terminal reports a right-click; the keys stay the reference.
