# Shared-memory tooling in Go (v0.10)

- **Status:** rev 1 — direction approved by the user on 2026-10-11 ("ok, écris la mini-spec pour
  la v0.10 mais pas de python").
- **Base:** `main` @ 0baba13 (v0.8.0)
- **Inputs:** the `busmem` proposal (`docs/busmem-brainstorm.md`, untracked); independent
  opinions from Grok and agy (`.superpowers/brainstorm/busmem/{grok,agy}.md`); the active decision
  [`2026-09-19-shared-memory`](../../memory/decisions/2026-09-19-shared-memory.md).
- **Ships with:** v0.10 (alongside the ACP/CLI drivers, which have their own spec).

## 1. Problem

The shared memory (`docs/memory/`) exists and is the right source. Two things fail in practice:

1. **The boot read is not guaranteed.** Grok's own session (2026-10-11) loaded `AGENTS.md` and
   `CLAUDE.md` but never `docs/memory/INDEX.md`. It opened the index only because the brief named
   it. `AGENTS.md` tells the agent to read the index, but nothing enforces that instruction.
2. **Writing a note costs too much.** Since 2026-09-19, no agent has written a lesson on its own
   initiative. Git shows only two new decisions, both written by the controller at release time.
   A note needs a worktree, nine frontmatter fields from memory, four sections and an index line.

A third, smaller one: **drift goes unnoticed.** `INDEX.md` still labels the dispatch decision
"(proposed)" while the note says `status: active`, and `scripts/check-memory.py` does not see it.

## 2. Decision

- **No new store.** There is no SQLite, no Redis memory, no private scope and no FTS5 index.
  `busmem` is not adopted, because it would be a second shared source that agents edit directly,
  which the active decision rules out. Grok, agy and Claude all agreed on this.
- **No Python.** The memory tooling moves into the Go module as `agentbus mem`, and
  `scripts/check-memory.py` and `tests/test_memory_check.py` are deleted. One language, one
  binary already on every host.
- **Git stays the transport.** `agentbus mem` writes files in the working tree. It never commits,
  never pushes, never touches Redis.

## 3. Commands

All three commands take `--root <dir>`, defaulting to the git top level of the current directory.
Their exit codes are 0 (ok), 1 (check failed / refused) and 2 (usage). The code lives in a new
package `memory/`; `cmd/agentbus/mem.go` only parses arguments, as the other commands do.

### 3.1 `agentbus mem check [--stale-after 60d] [--strict]`

This is a port of `check-memory.py` with **identical rules**:

- the required entry points;
- every boot file (CLAUDE.md, the skills, the roles) references `docs/memory/INDEX.md`;
- local links resolve inside the repository;
- the SKILL.md `optional-file` fallback rule;
- frontmatter: the nine fields, the allowed values, `created` and `last_verified` (not in the
  future, `last_verified` ≥ `created`, `unknown` only when not verified);
- at least one linked piece of evidence per note;
- every note is reachable from INDEX.

The output format stays `FAIL: …`, `OPTIONAL: …` and the final `PASS: …` line, so existing
habits keep working.

New rules:

| Rule | Level |
|---|---|
| The `AGENTS.md` index block (§3.3) differs from what `mem sync` would write | FAIL |
| An INDEX link whose text ends in `(proposed)`, `(superseded)`, `(resolved)` or `(active)` disagrees with the target note's `status` | FAIL |
| `INDEX.md` is longer than 60 lines or 4 KiB (it is injected at every boot) | FAIL |
| A note still contains the template marker `<!-- mem:fill -->` (§3.2) | FAIL |
| `confidence: verified` and `last_verified` older than `--stale-after` | WARN |
| `status: proposed` older than `--stale-after` | WARN |

`WARN:` lines never change the exit code unless `--strict` is given. Staleness is reported, never
acted on: nothing changes a status or deletes a note, as the protocol says (no TTL).

### 3.2 `agentbus mem new <lesson|decision|question> <slug> --title "<title>"`

This command:

- creates `docs/memory/<lessons|decisions|open-questions>/<YYYY-MM-DD>-<slug>.md`;
- adds `- [<title>](<dir>/<file>)` at the end of the matching INDEX section;
- runs `mem sync`.

The slug must match `^[a-z0-9][a-z0-9-]{0,47}$`. The command refuses an existing file.

The frontmatter is filled from facts, never from guesses:

- `id`: the file stem.
- `project`: the value every existing note shares. When they disagree, `--note-project` is
  required.
- `scope`: `project`, or `--scope transferable`.
- `kind`: the argument.
- `status`: `proposed`.
- `created`: today.
- `last_verified`: `unknown`.
- `confidence`: `unknown`.
- `author`: `AGENT_BUS_AGENT`, which is required and checked with `bus.ValidName`.

The body holds the title, then the protocol's four sections (**Finding/decision**, **When it
applies**, **Evidence**, **Limits**), each with a one-line prompt and the marker
`<!-- mem:fill -->`. `mem check` fails until the marker is gone and a piece of evidence is linked.
That way a stub cannot pass for a note. The command prints the path, and the agent edits, verifies
and commits in its own worktree.

`journal` is not offered, because the journal has its own index and cadence.

### 3.3 `agentbus mem sync`

This command rewrites, inside `AGENTS.md`, the block between `<!-- memory-index:begin -->` and
`<!-- memory-index:end -->` with the body of `INDEX.md`. Two transformations apply:

- links are rebased from `docs/memory/` to the repository root;
- the first heading is dropped.

The command is idempotent, and a missing marker pair is an error (exit 1) rather than a silent
append.

**Why a copy in AGENTS.md:** it is the one file every non-Claude client in use loads by itself.
Grok and agy confirmed this in their sessions. Codex reads it natively. Kimi is not verified (its launcher builds an
agent file), and §7.5 settles it. The copy is generated, and `mem check` fails on any drift, so it cannot become a
second source.

**Claude Code** gets the index through an import line in `CLAUDE.md`: `@docs/memory/INDEX.md`
(Claude Code expands `@path` imports in CLAUDE.md). That way it gets the real file, not a copy.

## 4. Boot and maintenance duties

- The `AGENTS.md` text keeps the reading rule, now placed right above the injected block: read
  the index, then 0–3 relevant notes, and check `status`/`last_verified`/evidence before trusting
  one.
- **Sentinel** runs `agentbus mem check` during its existing wake and notifies master of any
  `FAIL:` or `WARN:` lines. It needs no new loop. It never edits a note's status and never
  promotes a note.
- `PROTOCOL.md` "Structural verification" is changed to `agentbus mem check`. The new-note path
  is documented as `agentbus mem new …`, and the manual template stays valid.

## 5. Not built

The following are not built: a snapshot or "inject everything" command, a search command (`rg`
answers in milliseconds on ~15 files; this will be revisited when a real search fails), `promote`,
a private per-agent scope (each client keeps its own scratch), LLM extraction, deletion or TTL,
and any network or Redis access.

## 6. Testing

- **Port the 13 regression cases** of `tests/test_memory_check.py` to `memory/check_test.go`,
  using `testdata/` fixture trees. Each Python case maps to one named Go case, listed in the PR.
- **New-rule tests:**
  - the block drifts after an INDEX edit;
  - the status label mismatches (with the current real case `(proposed)` vs `active` as a
    fixture);
  - INDEX goes over budget;
  - the marker is left in a note;
  - the stale WARN cases, both with and without `--strict`.
- **`mem new`:** a golden file for the generated note, a test that the INDEX line lands in the
  right section, refusal of an existing file and of an invalid slug or author, and a check that
  a fresh stub FAILs `mem check`.
- **`mem sync`:** idempotence, link rebasing, and an error on missing markers.
- **The real repository passes `agentbus mem check`** after the fixes of §7. No Redis is involved:
  these tests need no `AGENTBUS_TEST_REDIS_URL`.

## 7. Migration (one PR)

1. Add `memory/` and `cmd/agentbus/mem.go`, along with the tests.
2. Add the markers and the first generated block to `AGENTS.md`, and the import line to
   `CLAUDE.md`.
3. Fix the real drift: in `INDEX.md`, change the dispatch decision from "(proposed)" to its actual
   status.
4. Delete `scripts/check-memory.py`, `tests/test_memory_check.py` and the `__pycache__`
   directories. Update `PROTOCOL.md`, the sentinel skill, `README.md` and `CLAUDE.md` (commands).
   `VALIDATION.md` is a dated record, so it is left as is, with one line pointing to the Go
   checker.
5. Acceptance (manual, recorded in `open-questions/2026-09-19-adoption.md`): run a fresh session in
   each client (Claude, Codex, Kimi, Grok, agy) and ask, **without naming any path**, "what does
   the shared memory say about the PEL?". The client passes if it answers from the injected index
   and cites the note. Record the client, the revision and the result.

## 8. Risks and open point

- **The copy in AGENTS.md drifts.** This is mitigated by a hard FAIL in `mem check`, and because
  `mem new` syncs automatically.
- **Some clients have no verified AGENTS.md loading** (agent files differ by client). The §7.5
  acceptance is what proves it per client. It is not inferred from the file's existence.
- **Open: the launcher.** `scripts/agent-launch` → `scripts/lib/role_config.py` is the other
  Python in the repository. Under the same "no Python" rule, porting it to Go belongs to the
  v0.10 drivers spec (it is the per-client launch layer those drivers replace). It is not part of
  this slice.
