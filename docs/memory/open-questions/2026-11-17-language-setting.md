---
id: 2026-11-17-language-setting
project: agent-bus-monitor
scope: project
kind: question
status: resolved
created: 2026-11-17
last_verified: 2026-11-20
author: coder2
confidence: verified
---
# The person's language: master talks to them in it, the bus stays English (the person's item 17, decided)

## Finding/decision

The person's direction (2026-11-20, bus thread `1795188144565-0`; the spec's questions answered the same day: a peer answering the person stays English, and any ISO 639-1 code is taken, an invalid one reading `en` with a warning): not a translation of busmon. busmon, the CLI, logs and the project stay English. One setting, `person.language` (default `en`), read by master's role skill (the concierge's later): master writes to the person in it, and to every other agent in English, relaying the person's words in English. On the bus, only a message from or to the person may be in that language. Spec: [2026-11-18-language-setting-design](../../superpowers/specs/2026-11-18-language-setting-design.md).

## When it applies

When master writes to the person, or relays the person's words to another agent.

## Evidence

[config/keys.go](../../../config/keys.go) (the `person.language` row), [master's skill](../../../skills/agent-bus-master/SKILL.md) (the rule).

## Limits / what would invalidate it

Decided by the person. Built on `feat/person-language`; true once that PR merges into `dev`.
