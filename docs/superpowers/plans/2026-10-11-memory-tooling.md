# Shared-memory tooling in Go (`agentbus mem`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `scripts/check-memory.py` with `agentbus mem check|new|sync` in Go, so the shared memory has one toolchain, a cheap way to write a note, and an index copy in `AGENTS.md` that cannot drift.

**Architecture:** A new package `memory/` holds all logic (check, sync, new) as pure functions over a repository root, with the clock injected. `cmd/agentbus/mem.go` only parses arguments and maps results to output and exit codes; `mem` runs before the project check and never touches Redis. The migration task wires the real repository (markers in `AGENTS.md`, the INDEX label fix) and deletes the Python.

**Tech Stack:** Go 1.26 standard library only (`regexp`, `path/filepath`, `os`, `time`); `bus.ValidName` for names.

**Spec:** `docs/superpowers/specs/2026-10-11-memory-tooling-design.md` (rev 1)

## Global Constraints

- No Python, no SQLite, no new store; `agentbus mem` never commits, never pushes, never touches Redis or the network.
- Exit codes: `0` ok, `1` check failed / refused, `2` usage.
- `--root <dir>` on every subcommand; default = the nearest ancestor of the current directory holding `.git` (file or directory, so worktrees work).
- Output lines: `OPTIONAL: …`, `WARN: …`, `FAIL: …`, then the final `PASS: …` line on success.
- Ported rules keep the Python messages verbatim (listed in Task 1).
- Markers: `<!-- memory-index:begin -->` / `<!-- memory-index:end -->`; template marker `<!-- mem:fill -->`.
- INDEX budget: 60 lines, 4096 bytes. Default `--stale-after 60d`.
- Slug: `^[a-z0-9][a-z0-9-]{0,47}$`. Author: `AGENT_BUS_AGENT`, required, `bus.ValidName`.
- User-facing text in English. Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Tests need no Redis: nothing in `memory/` or `mem.go` may read `AGENTBUS_TEST_REDIS_URL`.

## Review Focus

1. Old journal checkpoints and `resolved`/`superseded` notes must never raise a stale WARN — otherwise every daily sentinel run reports noise forever (Task 3).
2. A `--title` that is empty, multi-line, or contains `[` / `]` would corrupt the INDEX link — refuse it before any file is written (Task 4).
3. `agentbus mem` run where there is no `docs/memory/INDEX.md` (the sentinel in another project) must exit 1 with one clear line, not panic, and must not require `--project` or a broker (Task 5).
4. A hand edit inside the AGENTS.md block is reported by `check` and undone by `sync`; text outside the markers is preserved byte for byte (Task 2).
5. INDEX links with an anchor (`PROTOCOL.md#writing`) or a `./` prefix are rebased correctly, and external URLs are left alone (Task 2).

---

## File Structure

| File | Responsibility |
|---|---|
| `memory/doc.go` | package comment; shared regexps (`linkRE`, `codeFileRE`, `optionalRE`), `Fields`, `splitFrontmatter` |
| `memory/check.go` | `Options`, `Result`, `Check` — the ported rules |
| `memory/rules.go` | the new rules (label, budget, marker, stale) called from `Check` |
| `memory/sync.go` | `RenderBlock`, `Sync`, block drift rule |
| `memory/new.go` | `NewNote` |
| `memory/root.go` | `FindRoot` |
| `memory/*_test.go`, `memory/testdata/new-lesson.golden` | tests |
| `cmd/agentbus/mem.go`, `cmd/agentbus/mem_test.go` | CLI |

---

### Task 1: Port the checker (`memory.Check`)

**Files:**
- Create: `memory/doc.go`, `memory/check.go`, `memory/root.go`, `memory/memtest/tree.go`
- Test: `memory/check_test.go`

**Interfaces:**
- Produces:
  - `type Options struct { Root string; Today time.Time; StaleAfter time.Duration }`
  - `type Result struct { Errors, Optional, Warnings []string }`
  - `func Check(o Options) Result`
  - `func FindRoot(dir string) (string, error)` — walks up to the first directory containing `.git`.
  - `func splitFrontmatter(text string) (fields map[string]string, body string, ok bool)` — Python `text.split('---', 2)` semantics: `ok` only with three parts and blank part 0; fields from `^([a-z_]+):\s*(.*?)\s*$` (multiline, last wins).
  - `func memtest.WriteTree(t testing.TB) string` in `memory/memtest/tree.go` (a non-test package so `cmd/agentbus` tests can use it): the Python `setUp` tree in a `t.TempDir()`, with a `.git` directory at its root. Its INDEX is the Python one followed by three empty sections, `## Decisions`, `## Lessons`, `## Open questions`, which Task 4 needs and the ported cases ignore. Tests in `memory/` are in package `memory_test` (memtest imports memory, so no cycle).

Port `scripts/check-memory.py` rule for rule. Decisions the implementer cannot infer from the Python:
- Go has no lookbehind: match `\[[^\]]*\]\(([^)]+)\)` and skip a match preceded by `!`.
- The "visible nearby fallback" window is `[pos-400, pos+600)` in **runes** (Python string indices), comments `<!--.*?-->` (dot matches newline) removed.
- Paths: `filepath.Clean(filepath.Join(...))`, then `filepath.EvalSymlinks` when the target exists; "escapes repository" = not under the (symlink-resolved) root.
- Docs are visited in sorted path order; errors keep the Python order (required, boot, per document, unreachable).
- When `created` does not parse, skip the `last_verified predates created` comparison (Python reported it as an extra `invalid last_verified date`; the rule is unchanged).
- Dates are `YYYY-MM-DD` only; "future" means after `o.Today`'s date.

Messages (verbatim from the Python, `<rel>` = slash path relative to root):
`missing required entry point: <name>` · `boot path does not reference memory index: <rel>` · `<rel>: optional-file <t> needs a visible nearby fallback instruction` · `<rel>: optional missing file <t>; fallback: <why>` (Optional) · `<rel>: local link escapes repository: <t>` · `<rel>: broken local link: <t>` · `<rel>: missing skill file reference: <t>; provide optional-file fallback` · `<rel>: missing YAML frontmatter` · `<rel>: missing metadata <key>` · `<rel>: invalid <key>` · `<rel>: <key> is in the future` · `<rel>: last_verified predates created` · `<rel>: invalid <key> date` · `<rel>: no linked evidence/context` · `memory note unreachable from INDEX: <rel>`.

- [ ] **Step 1: Write the failing tests** — `memtest.WriteTree` builds exactly the Python `setUp` tree (same files, same contents). The spec asks for `testdata/` fixture trees; building the tree in code, as the Python did, is the same fixture and keeps each case a three-line diff. One Go test per Python case, same names in Go style:
  `TestCompleteStructureAndBoot` (no errors), `TestMissingEntryPointFails`, `TestMissingSkillReferenceFails`, `TestOptionalReferenceRequiresExplicitFallback` (no errors, exactly one Optional containing `docs/OPTIONAL.md`), `TestBootWithoutMemoryFails`, `TestMissingKnownRoleIsNotSilentlyIgnored`, `TestBrokenRelativeLinkFails`, `TestUnindexedNoteFails`, `TestVerifiedNoteNeedsVerificationDate`, `TestOptionalCommentCannotHideBrokenMemoryLink`, `TestOptionalSkillCommentNeedsVisibleFallback`, `TestOptionalSkillCannotHideMissingMemoryNote`, `TestFutureAndInvertedDatesFail` (two subtests). Each asserts the same substring the Python asserts, with `Today` fixed at 2026-10-11. Add `TestImageLinkIsNotALink` (`![x](missing.png)` in INDEX → no error) and `TestFindRootInWorktree` (`.git` as a file is found from a nested dir).
- [ ] **Step 2: Run** `go test ./memory/ -count=1` — Expected: FAIL (`Check` undefined).
- [ ] **Step 3: Implement** `doc.go`, `check.go`, `root.go` as above.
- [ ] **Step 4: Run** `go test ./memory/ -count=1` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(memory): port check-memory.py to Go (identical rules)`.

---

### Task 2: AGENTS.md index block (`RenderBlock`, `Sync`, drift rule)

**Files:**
- Create: `memory/sync.go`, `memory/sync_test.go`
- Modify: `memory/check.go` (call the drift rule), `memory/memtest/tree.go` (`WriteTree` gains the markers and runs `Sync` last, so the ported cases stay green)

**Interfaces:**
- Consumes: `Options`, `Result`, `memtest.WriteTree`.
- Produces:
  - `func RenderBlock(index string) string` — the full block, markers included.
  - `func Sync(root string) (changed bool, err error)` — rewrites the block in `<root>/AGENTS.md`; error (exit 1) when either marker is missing; writes nothing when unchanged.
  - `const BeginMarker = "<!-- memory-index:begin -->"`, `EndMarker = "<!-- memory-index:end -->"`

Block layout, exactly:

```
<!-- memory-index:begin -->
<!-- Generated by `agentbus mem sync` from docs/memory/INDEX.md. Edit the index, not this block. -->

<INDEX body>
<!-- memory-index:end -->
```

INDEX body = INDEX.md minus its first `# ` heading line and the blank lines right after it, trailing whitespace trimmed, links rebased: a link whose target has no scheme, does not start with `#` or `/`, becomes `path.Clean("docs/memory/" + p)` with any `#fragment` kept; images are rebased the same way. Nothing else changes.

Drift rule in `Check`: missing markers → `AGENTS.md: memory index block is missing; run agentbus mem sync`; block ≠ `RenderBlock(INDEX)` → `AGENTS.md: memory index block is out of date; run agentbus mem sync`.

- [ ] **Step 1: Write the failing tests:**
  - `TestRenderBlockRebasesLinks`: INDEX `[P](PROTOCOL.md)`, `[W](./PROTOCOL.md#writing)`, `[L](lessons/a.md)`, `[U](https://x.y/z.md)`, `[A](#top)` → `docs/memory/PROTOCOL.md`, `docs/memory/PROTOCOL.md#writing`, `docs/memory/lessons/a.md`, URL and `#top` unchanged; the `# ` title is absent.
  - `TestSyncIsIdempotent`: second `Sync` returns `changed == false` and the file bytes are identical.
  - `TestSyncKeepsOutsideText`: bytes before the begin marker and after the end marker are unchanged after a sync that rewrote a hand-edited block.
  - `TestSyncMissingMarkers`: error, file unchanged.
  - `TestCheckBlockDrift`: append a line to INDEX → Check has the `out of date` error; after `Sync` → no error. Removing the markers → `missing` error.
- [ ] **Step 2: Run** `go test ./memory/ -run 'Sync|RenderBlock|BlockDrift' -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement** `sync.go`; update `memtest.WriteTree`.
- [ ] **Step 4: Run** `go test ./memory/ -count=1` — Expected: PASS (ported cases included).
- [ ] **Step 5: Commit** `feat(memory): generated index block in AGENTS.md (mem sync)`.

---

### Task 3: New check rules (label, budget, marker, stale)

**Files:**
- Create: `memory/rules.go`, `memory/rules_test.go`
- Modify: `memory/check.go`

**Interfaces:**
- Consumes: `Options.Today`, `Options.StaleAfter` (0 = 60 days), `splitFrontmatter`, `memtest.WriteTree`.
- Produces: `const FillMarker = "<!-- mem:fill -->"`; Errors/Warnings below, appended after the ported errors.

Rules (`<rel>` as in Task 1):
- **Label:** an INDEX link whose text ends in `(proposed)`, `(active)`, `(resolved)` or `(superseded)` and whose target note exists with a different `status` → `docs/memory/INDEX.md: link labelled (<label>) but <rel> has status <status>`.
- **Budget:** INDEX over 60 lines (count of `\n`, plus one if the file does not end in one) or over 4096 bytes → `docs/memory/INDEX.md: <n> lines, <b> bytes; the index is loaded at every boot and must stay within 60 lines and 4096 bytes`.
- **Marker:** any `.md` under `docs/memory/` containing `FillMarker` → `<rel>: template not filled (<!-- mem:fill --> remains)`.
- **Stale (Warnings):** only notes with valid frontmatter whose `kind` is not `journal`:
  - `status: active`, `confidence: verified`, `last_verified` strictly before `Today − StaleAfter` → `<rel>: verified on <date>, older than <N>d; re-check its evidence`.
  - `status: proposed`, `created` strictly before `Today − StaleAfter` → `<rel>: proposed since <date>, older than <N>d; decide or supersede it`.

- Tests that edit INDEX call `memory.Sync` afterwards, so the block-drift error never masks the rule under test.

- [ ] **Step 1: Write the failing tests:**
  - `TestIndexLabelMismatch`: a decision note with `status: active` linked as `[Dispatch (proposed)](decisions/d.md)` → error containing `labelled (proposed)` and `has status active`; relabel `(active)` → no error; no label → no error.
  - `TestIndexBudget`: 61 lines → error; 60 → none; 4097 bytes on 10 lines → error.
  - `TestFillMarkerFails`: marker in a lesson → error containing `template not filled`.
  - `TestStaleWarnings`: `Today` 2026-10-11, `StaleAfter` 60 days: verified active note last verified 2026-08-11 → one warning; 2026-08-12 → none (boundary); proposed note created 2026-08-01 → warning; **journal** note verified 2026-01-01 → none; **resolved** note verified 2026-01-01 → none. Warnings never appear in `Errors`.
- [ ] **Step 2: Run** `go test ./memory/ -run 'Label|Budget|FillMarker|Stale' -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement** `rules.go`; call it from `Check`.
- [ ] **Step 4: Run** `go test ./memory/ -count=1` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(memory): label, budget, template and staleness rules`.

---

### Task 4: `memory.NewNote`

**Files:**
- Create: `memory/new.go`, `memory/new_test.go`, `memory/testdata/new-lesson.golden`

**Interfaces:**
- Consumes: `Sync`, `Check`, `FillMarker`, `bus.ValidName`, `memtest.WriteTree`.
- Produces:
  - `type NewOptions struct { Root, Kind, Slug, Title, Author, Scope, NoteProject string; Today time.Time }`
  - `func NewNote(o NewOptions) (rel string, err error)` — `rel` is the created file, slash path relative to root. All validation happens before any write.

Kind → directory → INDEX section (heading matched by prefix): `lesson` → `lessons/` → `## Lessons`; `decision` → `decisions/` → `## Decisions`; `question` → `open-questions/` → `## Open questions`. The line `- [<title>](<dir>/<file>)` goes right after the last `- ` line of that section (or after the heading's blank line when the section has none). Then `Sync`.

Refusals (each an error, nothing written): unknown kind; slug not matching the constraint; empty title, a title with a control character, `[` or `]`; `Author` empty or not `ValidName`; `Scope` other than ``/`project`/`transferable`; file exists; INDEX section missing; no `docs/memory/INDEX.md`; project undecidable — distinct `project:` values among existing notes ≠ 1 and `NoteProject` empty (`notes disagree on project (<a>, <b>): pass --note-project` / `no existing note names a project: pass --note-project`); `NoteProject` not `ValidName`.

Generated file (frontmatter values from the spec §3.2; `scope` defaults to `project`):

```
---
id: <YYYY-MM-DD>-<slug>
project: <project>
scope: <scope>
kind: <kind>
status: proposed
created: <YYYY-MM-DD>
last_verified: unknown
author: <author>
confidence: unknown
---
# <title>

## Finding/decision

<!-- mem:fill --> What was found or decided, in two or three sentences.

## When it applies

<!-- mem:fill --> The situation in which the next agent needs this.

## Evidence

<!-- mem:fill --> Link at least one committed file, PR or test as a Markdown link.

## Limits / what would invalidate it

<!-- mem:fill --> What this does not prove, and what would make it false.
```

- [ ] **Step 1: Write the failing tests:**
  - `TestNewNoteGolden`: lesson `pel-floor`, title `PEL floor`, author `codex`, Today 2026-10-11 on `memtest.WriteTree` plus one existing note with `project: test` → file `docs/memory/lessons/2026-10-11-pel-floor.md` equals `testdata/new-lesson.golden`; INDEX's `## Lessons` gains `- [PEL floor](lessons/2026-10-11-pel-floor.md)` as its last item; AGENTS.md block contains `docs/memory/lessons/2026-10-11-pel-floor.md`.
  - `TestNewNoteSections`: a decision lands under `## Decisions`, a question under `## Open questions…`, never in another section.
  - `TestNewNoteRefusals`: table over every refusal above; for each, the tree's bytes are unchanged (compare a hash of all files before/after).
  - `TestFreshStubFailsCheck`: after `NewNote`, `Check` has `template not filled` and `no linked evidence/context` for the new file; after replacing the markers with text and adding `[x](../PROTOCOL.md)` under Evidence, those two errors are gone.
- [ ] **Step 2: Run** `go test ./memory/ -run 'NewNote|FreshStub' -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement** `new.go` and write the golden file.
- [ ] **Step 4: Run** `go test ./memory/ -count=1` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(memory): mem new writes a fillable note and its index line`.

---

### Task 5: `agentbus mem` CLI

**Files:**
- Create: `cmd/agentbus/mem.go`, `cmd/agentbus/mem_test.go`
- Modify: `cmd/agentbus/main.go` (dispatch `mem` **above** the project-required check, next to `limits`/`version`; add `mem` to the usage list)

**Interfaces:**
- Consumes: `memory.Check`, `memory.Sync`, `memory.NewNote`, `memory.FindRoot`, `extractFlag`, `extractBool`.
- Produces: `func runMem(args []string, getenv func(string) string, cwd string, today time.Time, stdout, stderr io.Writer) int`; `main` calls `os.Exit(runMem(args[1:], os.Getenv, wd, time.Now(), os.Stdout, os.Stderr))`.

Usage (printed to stderr with exit 2 on no subcommand, an unknown subcommand, a leftover `--flag`, a missing positional, or a bad `--stale-after`):
`usage: agentbus mem check [--stale-after 60d] [--strict] | mem new <lesson|decision|question> <slug> --title "<title>" [--scope transferable] [--note-project P] | mem sync   (all: [--root DIR])`

- `--stale-after` accepts `<N>d` with N ≥ 1 only.
- `check`: print Optional (`OPTIONAL: `), Warnings (`WARN: `), Errors (`FAIL: `) in that order; exit 1 on any error, or on any warning with `--strict`; otherwise print `PASS: memory entry points, boot references, local links, metadata, reachability and index block; <o> optional missing file(s), <w> warning(s)` and exit 0.
- `new`: author from `getenv("AGENT_BUS_AGENT")`; on success print the relative path and exit 0; a refusal prints `error: <msg>` to stderr, exit 1.
- `sync`: print `AGENTS.md: index block updated` or `AGENTS.md: index block already up to date`, exit 0; error → exit 1.
- Root: `--root`, else `FindRoot(cwd)`; no root or no `docs/memory/INDEX.md` → `error: no docs/memory/INDEX.md under <root>`, exit 1.

- [ ] **Step 1: Write the failing tests** (trees from `memtest.WriteTree`):
  - `TestMemCheckPassAndFail`: clean tree → exit 0, stdout ends with a line starting `PASS: `; broken link → exit 1, a `FAIL: ` line, no `PASS`.
  - `TestMemCheckStrict`: stale note only → exit 0 without `--strict` (one `WARN: ` line), exit 1 with it.
  - `TestMemUsage`: `[]`, `[bogus]`, `[check --typo]`, `[check --stale-after 2w]`, `[new lesson]` → exit 2.
  - `TestMemNeedsNoProject`: `runMem` with a `getenv` returning "" for everything except `AGENT_BUS_AGENT` succeeds for `new` and `check` (no project, no Redis).
  - `TestMemNoMemoryDir`: a git dir without `docs/memory` → exit 1, stderr `error: no docs/memory/INDEX.md under …`.
  - `TestMemNewThenSync`: `new lesson x --title X` → exit 0 and prints `docs/memory/lessons/<today>-x.md`; `sync` then prints `already up to date`.
- [ ] **Step 2: Run** `go test ./cmd/agentbus/ -run Mem -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement** `mem.go` and the `main.go` dispatch.
- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./cmd/agentbus/ ./memory/ -count=1` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(agentbus): mem check|new|sync`.

---

### Task 6: Migrate the repository and delete the Python

**Files:**
- Modify: `AGENTS.md` (markers + generated block, placed right after the "Shared memory" reading rule, §4), `CLAUDE.md` (an `@docs/memory/INDEX.md` import line in "Shared memory"; `go run ./cmd/agentbus mem check` in Commands; `mem` in the agentbus command list; a "Things that bite" line: `mem` must stay above the project check in `main()`), `docs/memory/INDEX.md` (dispatch decision label `(proposed)` → `(active)`), `docs/memory/PROTOCOL.md` ("Structural verification" → `agentbus mem check` and `go test ./memory/ -count=1`; new notes via `agentbus mem new …`, the manual template still valid), `skills/agent-bus-sentinel/SKILL.md` (Duty 1 step 5, below), `README.md` (one `agentbus mem` paragraph under "Use it"), `docs/memory/VALIDATION.md` (one line: the checker is now `agentbus mem check`; the Python commands above are historical)
- Delete: `scripts/check-memory.py`, `tests/test_memory_check.py`, `scripts/__pycache__/`, `tests/__pycache__/` (and `tests/` if empty)
- Create: `memory/repo_test.go`

Sentinel Duty 1 step 5 gains, before the stale-note review: run `agentbus mem check`; if it prints `error: no docs/memory/INDEX.md`, say so in the summary and skip; on any `FAIL:` or `WARN:` line, `agentbus notify "memory check: <n> FAIL, <m> WARN — run agentbus mem check"`. The sentinel never edits a note's status and never promotes a note.

- [ ] **Step 1: Write the failing test** `TestRepositoryPasses` in `memory/repo_test.go`: `Check(Options{Root: "..", Today: time.Now()})` has no Errors (Warnings allowed, logged with `t.Log`).
- [ ] **Step 2: Run** `go test ./memory/ -run TestRepositoryPasses -count=1` — Expected: FAIL (no markers in AGENTS.md, INDEX label drift).
- [ ] **Step 3: Edit** the files listed; run `go run ./cmd/agentbus mem sync` to generate the block; delete the Python files.
- [ ] **Step 4: Run** `go run ./cmd/agentbus mem check` → `PASS: …`; `go build ./... && go vet ./... && go test ./... -count=1` → all `ok`; `git grep -n "check-memory\|test_memory_check" -- ':!docs/releases' ':!docs/superpowers' ':!docs/memory/VALIDATION.md'` → no output.
- [ ] **Step 5: Commit** `chore(memory): agentbus mem replaces check-memory.py; AGENTS.md carries the index`.

---

## After the plan (manual, not a task)

Spec §7.5 acceptance, after merge and once the user allows `go install`: in a fresh session of each client (Claude, Codex, Kimi, Grok, agy), ask "what does the shared memory say about the PEL?" without naming a path; record client, revision and result in `docs/memory/open-questions/2026-09-19-adoption.md`.
