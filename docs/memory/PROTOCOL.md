# Shared memory protocol

## Reading

1. Read INDEX.md at session start, context recovery, or task change; select the
   relevant notes rather than injecting the entire vault into every prompt.
2. Check project/scope, status, last_verified, and the cited evidence. An old
   verified note is not a fresh runtime observation. A proposed decision is not
   an accepted instruction; a superseded note is historical context only.
3. If evidence conflicts, inspect the authoritative source and report the
   conflict. Do not resolve it by trusting the more confident author or latest
   chat message. Current user instructions and applicable repository rules govern.

## Writing

After meaningful work, record only a reusable finding, a reasoned decision, or an
open question. Update a relevant note rather than producing a note for every
message, test run, or heartbeat. Keep one subject per note and a short index link.

Use the template below. Evidence should be a committed file/PR/test, preferably
at a pinned revision. Redis IDs can supplement it, but bounded stream retention
makes them insufficient as the only durable proof. Do not copy raw production
exports, credentials, personal transcripts, or other projects' data into notes.
Do not label a hypothesis verified just because another agent repeated it.

```yaml
---
id: yyyy-mm-dd-short-topic
project: agent-bus-monitor
scope: project
kind: lesson
status: active
created: yyyy-mm-dd
last_verified: yyyy-mm-dd
author: agent-name
confidence: verified
---
```

Then write: **Finding/decision**, **When it applies**, **Evidence**, and **Limits /
what would invalidate it**. `scope` is `project` or `transferable`; transferable
means a candidate lesson elsewhere, never blanket authorization in another repo.
`kind` is decision/lesson/question/journal; `status` is proposed/active/resolved/
superseded; `confidence` is verified/inferred/unknown. For an unverified proposal,
use confidence unknown and last_verified unknown; do not invent a date of proof.

## Review and maintenance

- One writer per note, isolated worktree, review the diff. Parallel agents should
  create separate notes instead of appending to a shared growing file. Integrate
  index changes deliberately; never overwrite a peer's edits to resolve a conflict.
- A proposed policy becomes active after the authorized decision, with its source.
  Record independent review when received; do not invent reviewers.
- When a decision changes, mark the old note superseded and link both directions.
  Preserve the rationale. Changing last_verified means actually checking evidence,
  not merely touching the file. No automatic deletion or TTL for reference notes.
- Sentinel may flag broken links, missing evidence, outdated statements or
  unresolved questions during an existing wake. It does not need a new polling
  loop, and it must not silently upgrade hypotheses into decisions.
- Before handoff, cite the used/changed note and say whether it is committed,
  shared, or still only in your worktree. Local files are not automatically visible
  in another machine's checkout. A lost context is recoverable only if its useful
  conclusions were written and actually made available.

## Boundaries

The board owns task/request state. The journal records dated milestones, not a
second task registry. Code graphs describe source structure, not historical
rationale. Private agent memory can hold personal preferences and links; shared
project facts belong here. Redis can later index these files for retrieval without
becoming an independently editable second source. No semantic service is required
for this first slice.

## Structural verification

Run from the repository root before sharing a memory change:

```sh
python3 scripts/check-memory.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -p 'test_memory_check.py'
```

The checker requires the memory entry points and boot references, follows local
Markdown links, checks concrete backticked .md references in skills, validates
minimal note metadata, and rejects notes unreachable from INDEX. A deliberately
optional non-memory skill file must carry a visible fallback next to its reference
and repeat that same fallback in its annotation. Memory links cannot be suppressed:

```html
Read `docs/OPTIONAL.md`. If missing, report it and continue with the index.
<!-- optional-file: docs/OPTIONAL.md | If missing, report it and continue with the index. -->
```

Used exceptions are printed as OPTIONAL and counted in the result. Dates in the
future or verification before creation fail validation.

This is a structural check, not a proof agents obey instructions or that a linked
claim is true. It does not contact external URLs, inspect Redis, parse arbitrary
shell expressions or verify runtime adoption. Existing non-memory optional
resources need their own documented fallback.
