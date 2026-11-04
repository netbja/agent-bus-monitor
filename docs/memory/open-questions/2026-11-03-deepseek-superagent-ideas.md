---
id: 2026-11-03-deepseek-superagent-ideas
project: agent-bus-monitor
scope: project
kind: question
status: active
created: 2026-11-03
last_verified: 2026-11-03
author: coder
confidence: verified
---
# Three ideas taken from DeepSeek's Super-Agent prototype, waiting for the person's answer

## Finding

DeepSeek published `dsh-superagent`, an open-source (MIT) research prototype of a proactive
coding agent for one GitHub repository, released 2026-11-01 according to the articles the person
cited (Bloomberg, TheNextWeb); the person kept a checkout. It shares this project's principle: **take as little
human attention as possible, and give as much value as possible**; it acts only when asked or
when a rule the team wrote allows it, and speaks only when someone must act or can use a result.

From it the v0.27 plan carries three ideas, as person tasks with no code until the person answers
(`docs/plans/v0.27.json`: `idea-agent-led-setup`, `idea-github-source`, `idea-observe-mode`):

- **Agent-led setup**: an agent installs with the person following one file, as its `SETUP.md`
  does: a silent probe first, one question per message with a recommended answer, one full plan
  confirmed before anything runs, never a secret asked in the chat (the person runs those steps in
  their own terminal).
- **GitHub as a source**: an `@mention` by an allowed account opens a request (here: to master),
  and later comments on a pull request reach the agent that owns it.
- **Observe mode**: a new automatic rule first only records what it would have done (its accept
  mode `observe`), before it is allowed to act.

And two things we deliberately do **not** take:

- **Full user rights without isolation.** Its workers run with `danger-full-access` as the
  operator's OS user; its own documentation says the account wrappers do not isolate the process
  and recommends a dedicated OS account for isolation. agent-bus keeps per-role tool rules, a
  single pane writer and the person's approval for risky roles.
- **Reading team chats.** Its Feishu source listens to subscribed chats (mentions or every
  message); agent-bus reads no chat, only the bus and the person's own answers.

## When it applies

When the person answers one of the three `idea-*` tasks, or when a later plan proposes an
installer assistant, a GitHub source or a dry-run stage for an automatic rule: start from here,
and from the person's answer, not from this summary alone.

## Evidence

- [The v0.27 plan](../../plans/v0.27.json), where the three `idea-*` person tasks stand; the
  [single pane writer](../decisions/2026-10-10-event-driven-dispatch.md) and the
  [role security rules](../decisions/2026-09-27-provider-neutral-security.md) are what the two refusals keep.
- The local checkout `/data/AI-Docs/dsh-superagent-prototype` at commit `8c32a9d`
  (2026-10-31 20:06 +08:00): `README.md` (research prototype, the attention principle, MIT),
  `SETUP.md` (§ How to run the setup: one question per message, a recommendation, one plan, never a
  secret in the chat; the accept mode `observe` that records proactive work without doing it),
  `docs/000-philosophy.md`, `docs/001-domain.md` (Sources GitHub and Feishu, Channels),
  `docs/006-deployment.md` § Permissions and isolation (`danger-full-access` as the operator's OS
  user) and § GitHub/Feishu sources.
- The press coverage the person cited: Bloomberg and TheNextWeb, early November 2026 (not re-read
  for this note; the repository is the source for every technical claim above).

## Limits / what would invalidate it

The prototype is research code; its README says it differs from DeepSeek's internal version and
that some designs it discusses are not implemented. Nothing here is decided for agent-bus: each
idea is open until the person answers, and an answer belongs in a decision note that supersedes the
matching part of this one.
