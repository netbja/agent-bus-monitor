---
id: 2026-11-17-braille-graphs
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-17
last_verified: unknown
author: coder2
confidence: unknown
---
# Braille graphs in busmon's Tokens screen

## Finding/decision

The Tokens screen's bars and sparklines could use Unicode braille (2x4 dots per cell) for finer graphs, btop-style.

Recommendation (drafted by coder2 from master's directive of 2026-11-17; master to confirm or edit): Wait for coder's feasibility answer (task braille-graphs-feasibility: library or in-house encoder, colours, width, fallback) before any decision.

## When it applies

When the Tokens screen's graphs change.

## Evidence

[cmd/busmon/tokens_view.go](../../../cmd/busmon/tokens_view.go).

## Limits / what would invalidate it

Terminals or fonts without braille glyphs need the current bars as a fallback.
