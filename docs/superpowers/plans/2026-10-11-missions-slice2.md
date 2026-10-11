# Missions slice 2 — operations (reload, recover, skip, reassign, timeouts) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the missions conductor operable over a real multi-day change. This adds:
- reloading a revised plan, and rebuilding from Git after losing Redis;
- `skip` and `reassign`, and per-mission timeouts;
- the six minors carried over from slice 1;
- the busmon indicator, the bootstrap tab, and the agent/master skill sections.

**Architecture:** Everything extends what slice 1 built:
- the `Next` table gains three events;
- `ops.go` gains the new operator commands, each applied by the conductor through fenced writes;
- `conductor.go` gains the timeout check and the minor fixes;
- busmon, the bootstrap generator and the skills get small additive changes.

Nothing changes in the dispatcher.

**Tech Stack:** Go 1.26, go-redis v9, system git. No new dependency.

**Spec:** `docs/superpowers/specs/2026-10-11-missions-design.md` (rev 3, approved): §8 item 2, §6.4, §6.5, §5.3, §6.1, §5.5 and §10.

## Global Constraints

- Slice 1's constraints hold unchanged:
  - fenced conductor writes, Redis `TIME`, `change/<change>` only, no push, no LLM;
  - tests only on `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392`;
  - no Python added;
  - English user-facing text;
  - commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Operator-command args, as a JSON array on `{p}:missionops`:
  - `load`: `[ref]`, `[ref, "force", reason]` or `[ref, "recover"]`;
  - `retry`: `[c/id]`;
  - `reassign`: `[c/id, agent]`;
  - `skip`: `[c/id, reason]`;
  - `abort`: `[c, reason]`.
- Default mission timeout: `4h`, counted from the board's `request.accepted_at` (Redis milliseconds).
- The verdict subject becomes `mission:<c>/<id>@<full 40-hex sha>` (§10 minor). The ref is still the attempt's task name.
- `bootstrap` is a bash script with an embedded Python template generator that predates the no-Python rule. Task 7 adds three lines to that existing generator rather than a second generator. The launcher port, which is the live acceptance, removes that Python.

## Review Focus

1. **A reload while an attempt is queued but not yet read.** The superseded request must never produce a delivery that gets checked. The agent who drains it later must find the "superseded" reply in the same drain (Task 2).
2. **`--recover` on a branch that also holds merges not made by the conductor** (a hand merge, a merge from `main`). They carry no `Mission:` trailer and must be ignored, never counted (Task 3).
3. **Two attempts of the same mission naming the same task after a recover.** The board kept `chg-t1-1` while Redis lost the mission record. A fresh attempt must skip to a free name, never "reuse" the old board entry as its own (Task 3).
4. **`reassign` to the mission's reviewer, to its current agent, or to an invalid name** must be refused before anything changes (Task 4).
5. **A timeout after a `retry` or `reassign`** must be measured from the new attempt's acceptance (its own task, so its own `accepted_at`), never from the first attempt's (Task 5).

---

### Task 1: Three more events in `Next`

**Files:** Modify `mission/state.go`, `mission/state_test.go`.

**Interfaces — Produces:** `EvSkip`, `EvReassign`, `EvTimedOut`, all in `AllEvents`.

New rows of the transition table (every other pair stays refused):

| From | Event | To | Actions |
|---|---|---|---|
| pending, ready | skip | integrated | — |
| requested, accepted, delivered, checking, blocked | skip | integrated | supersede_attempt |
| in_review | skip | integrated | supersede_attempt, resolve_review |
| accepted, blocked | reassign | requested | supersede_attempt, send_request |
| accepted | timed_out | blocked | escalate |

`approved` and `integrating` refuse `skip`, because a merge is under way. `requested` refuses `reassign`: nobody has accepted the work yet, so there is nothing to take away (use `retry` after a block).

- [ ] **Step 1:** Add the rows to `expected()` in `TestNextTable`. The test already checks that every unlisted pair is refused.
- [ ] **Step 2:** Run `go test ./mission/ -run Next -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same. Expected: PASS.
- [ ] **Step 5:** Commit `feat(mission): skip, reassign and timeout events`.

---

### Task 2: Reload a revised plan (§6.4)

**Files:**
- Modify `mission/ops.go` (`load`), `mission/store.go` (`MissionRecord.IntegratedUnder string`, `json:"integrated_under,omitempty"`), `bus/conductorkeys.go` (`FencedMissionBatch`), `mission/conductor.go` (missions absent from the file still get their effects replayed), `cmd/agentbus/missions.go` (`load … --force <reason>`).
- Test: `mission/reload_test.go`, `bus/conductorkeys_test.go`, `cmd/agentbus/missions_test.go`.

**Interfaces — Produces:**
- `func (b *Bus) FencedMissionBatch(ctx, token, change string, missions map[string]string, changeRecord string) error`. It is **one** Lua script that checks the lease, `HSET`s every mission record and then the change record, so a reload is all or nothing (Codex: no hybrid revision after a crash).
- `load` with a known change and a different file hash builds revision `n+1`:
  1. Read, parse and validate the new file at the new commit, as in a first load.
  2. Classify, through the pure `planReload(old map[string]MissionRecord, f File, rev int, force bool, reason string) (map[string]MissionRecord, reloadCounts, error)`, every id of the old records union the new file. The new definition's hash is compared with `MissionRecord.Hash`.

     | Old state | Unchanged | Changed | Removed from the file |
     |---|---|---|---|
     | pending, ready | kept | `pending`, new hash | tombstone |
     | requested, accepted, delivered, checking, in_review, blocked | kept | refused unless `force`; with `force`: `pending`, new hash, the attempt moved to `History` (outcome `superseded`, `Closed` false), `Escalated` true | refused unless `force`; with `force`: tombstone with the attempt moved to `History` the same way |
     | approved, integrating | kept | refused, **even with** `force` | refused, even with `force` |
     | integrated | kept | refused unless `force`; with `force` it stays `integrated`, `IntegratedUnder = "rev <n> <old hash>"`, new hash, `Reason = "reload --force: <reason>"` | the same as changed: it stays `integrated` with `IntegratedUnder` |
     | aborted (an earlier tombstone) | kept | kept | kept |
     | (new id) | — | — | a `pending` record |

     A **tombstone** is the record in state `aborted` with `Reason = "removed in rev <n+1>"`. The record is kept rather than deleted, so its effects (closing the attempt, closing the review gate) still replay. `ReviewOpen` stays as it was, so a gate open on a removed or superseded mission is closed by the effects.
  3. Every refusal is decided before any write. The error lists every offending mission with its state.
  4. One `FencedMissionBatch` writes every record and the change record (`Rev+1`, the new `Commit`, `Path` and `FileHash`). The next pass replays the owed effects: supersede replies, board states, gate closes.
  5. Answer `reloaded <c> rev <n+1>: <k> changed, <a> added, <r> removed, <u> unchanged`. The counts are disjoint: `changed` is in both files with another hash (forced `integrated` ones included), `added` is new ids, `removed` is new tombstones, and `unchanged` is the rest.
- `passChange` runs `effects` and `notifyLateSuperseded` for missions absent from the file too (with an empty `Mission`), and never `advance`s them.
- `blocked` counts as in flight (it holds an attempt). Task 8 writes this into spec §6.4 itself, not only §9.
- An aborted change refuses `load`: "change aborted; load it under a new name".
- CLI: `mission load <path>@<commit> [--force <reason>]` sends `[ref, "force", reason]`. `--force` without a reason is a usage error (exit 2).

- [ ] **Step 1:** Write the failing tests, on the harness from `conductor_test.go`, with a second `missions.json` committed and loaded through `HandleOp`:
  - `TestReloadKeepsUnchanged`: t1 in review and unchanged, t2 pending and changed. After the reload, t1 is still `in_review` with the same attempt, t2 is pending with the new hash, the change is rev 2, and the answer counts "1 changed".
  - `TestReloadRefusesInFlightWithoutForce`: t1 `accepted` and changed. Without `force` the answer is refused, naming t1 and `accepted`, and both the change and the mission records are byte-identical to before. With `force` and a reason, t1 is `pending` right after the reload. After one pass, the old task is `superseded` and exactly one `wake=no` reply has gone to its thread.
  - `TestReloadNeverCancelsApproval`: t1 `approved` or `integrating` and changed, even with `force`, gives a refusal and nothing written.
  - `TestReloadForceKeepsIntegrated`: t1 `integrated` and changed. Without `force` it is refused. With `force` it is still `integrated`, `IntegratedUnder` names rev 1, and t2 (which depends on t1) is not reset.
  - `TestReloadAddsAndRemoves`: a new t3 gives a `pending` record; removing the pending t2 tombstones it, and the answer is exactly `reloaded chg rev 2: 0 changed, 1 added, 1 removed, 1 unchanged`.
  - `TestReloadRemovesInFlightWithForce`: t1 `in_review` is removed from the file. Without `force` it is refused. With `force` it becomes a tombstone; after one pass its gate is closed, its old task is `superseded`, and a late `done` on that task gets exactly one more reply.
  - `TestReloadIsAtomic`: make `FencedMissionBatch` fail by swapping the token just before the call (lease lost). Every record and the change record are byte-identical to before.
  - `TestPlanReloadTable`: the pure function against every row of the table above, including the counts.
  - `TestReloadSupersededQueuedRequestProducesNothing` (Review Focus 1): t1 `requested`, not accepted, and changed, reloaded with `force`. The agent then accepts and delivers the old task. After two passes, t1's new attempt is the only one checked: the old commit appears in no `Subject`. The agent's drain contains the superseded reply.
  - `TestReloadAbortedChangeRefused`.
  - `TestFencedMissionBatch` in bus: all fields are written in one call; a stale token is refused and writes nothing.
  - `TestMissionCLILoadForceNeedsReason`: exit 2.
- [ ] **Step 2:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ ./bus/ ./cmd/agentbus/ -run 'Reload|MissionBatch|LoadForce' -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement. The classification is a pure function `planReload(old map[string]MissionRecord, f File, force bool) (reloadPlan, error)`, tested through the table above.
- [ ] **Step 4:** Run the same. Expected: PASS.
- [ ] **Step 5:** Commit `feat(mission): reload a revised plan as a new revision`.

---

### Task 3: Rebuild from Git after losing Redis (§6.5)

**Files:**
- Modify `mission/ops.go` (`load` with `recover`), `mission/git.go` (`MissionMerges`), `mission/conductor.go` (`newAttempt` skips names already on the board).
- Test: `mission/recover_test.go`, `mission/git_test.go`.

**Interfaces — Produces:**
- `type MissionMerge struct{ ID, Commit, Hash string; Rev int }`
- `func (g Git) MissionMerges(ctx, branch string) ([]MissionMerge, error)`. It reads `git log --merges --format=%H%x00%(trailers:key=Mission,valueonly)%x00%(trailers:key=Mission-Commit,valueonly)%x00%(trailers:key=Mission-Hash,valueonly)%x00%(trailers:key=Mission-Rev,valueonly)`, keeps only the merges whose `Mission:` names the given change, and returns them oldest first.
- `recover` works as follows:
  - it is refused when the change is known in Redis ("already loaded; recover is for a lost store");
  - otherwise it parses and validates like a first load, then classifies each mission from the trailers:

    | Trailer | Result |
    |---|---|
    | a merge with the current hash | `integrated`, `Current.Commit` = `Mission-Commit`, `TestedOK` true |
    | a merge under another hash | `blocked`, reason "merged under another definition (rev <r>); decide with skip, retry or reload", `Escalated` false (the next pass escalates) |
    | none | `pending` |

  - the change record's `Rev` is the highest `Mission-Rev` seen, or 1;
  - **several merges of one id** (a merge under an old hash, then one under the current hash after a reload): the **most recent** merge on the branch decides. If its hash is current the mission is `integrated`; otherwise it is `blocked`;
  - `MissionMerges` skips a merge whose trailers are incomplete (no `Mission`, `Mission-Commit` or `Mission-Hash`), or whose `Mission-Commit` is not 40 hex characters;
  - it answers `recovered <c> rev <n>: <i> integrated, <b> blocked, <p> pending`.
- `newAttempt` (Review Focus 3): while the board already holds `TaskName(c, id, Attempt+1)`, it increments `Attempt`. It gives up past 99 with an error. The crash-safety path (`ready` or `requested` with `Current.Task` already set) is unchanged.

- [ ] **Step 1:** Write the failing tests.
  - `TestMissionMergesIgnoresForeignMerges` (Review Focus 2): one conductor merge, one hand merge, and one merge of `main` into the branch. Only one `MissionMerge` comes back. A merge with a hand-written incomplete `Mission:` trailer is skipped too, and two conductor merges come back oldest first.
  - `TestRecoverMostRecentMergeDecides`: t1 merged under hash A, then again under the current hash B, gives `integrated`. In the reverse order, B then A, it gives `blocked`.
  - `TestRecoverRebuilds`: integrate t1 through the normal flow, then delete every `p:change:*` and `p:missions:*` key. `recover` brings back t1 `integrated` and t2 `pending`, and the next pass requests t2.
  - `TestRecoverChangedDefinitionBlocks`: the same, but recovering from a `missions.json` where t1 changed. t1 is `blocked`, and the next pass escalates once.
  - `TestRecoverRefusedWhenKnown`.
  - `TestNewAttemptSkipsTakenNames`: the board holds `chg-t2-1` from before the loss. After recover, t2's first request is `chg-t2-2`.
- [ ] **Step 2:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ -run 'Recover|MissionMerges|SkipsTaken' -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same. Expected: PASS.
- [ ] **Step 5:** Commit `feat(mission): recover a change from its merge trailers`.

---

### Task 4: `skip` and `reassign` (§5.3)

**Files:** Modify `mission/ops.go`, `cmd/agentbus/missions.go`. Test: `mission/ops_test.go`, `cmd/agentbus/missions_test.go`.

**Interfaces — Produces:**
- `skip c/id reason` (`EvSkip`):
  - it marks the mission `integrated` without a commit, with `Reason = "skipped by <from>: <reason>"`;
  - it appends a self-verdict to the ledger with `bus.AppendVerdict`: subject `mission:<c>/<id>@skipped`, author and reviewer = the op's `from`, decision `approve`, message = the reason. A self-approval never counts in a roll-up, but it records the act;
  - dependents unlock on the next pass.
- `reassign c/id agent` (`EvReassign`):
  - `agent` must pass `ValidName`, differ from the mission's reviewer, and differ from the current attempt's agent. Otherwise it is refused before any write;
  - it resets `Rejections` to 0 and opens a new attempt for `agent`.
- `HandleOp` needs the op's `from` for `skip`. `Conductor.HandleOp` already receives the `MissionOp`.
- CLI: `mission skip <c>/<id> --reason <text>` and `mission reassign <c>/<id> <agent>`. A missing reason or agent is a usage error (exit 2).

- [ ] **Step 1:** Write the failing tests.
  - `TestSkipUnlocksDependents`: skip a blocked t1. t1 is `integrated` with no commit, the ledger has the self-verdict, and the next pass requests t2.
  - `TestSkipRefusedDuringMerge`: t1 `approved` gives a refusal.
  - `TestSkipInReviewClosesGate`.
  - `TestReassignBlocked`: t1 blocked, assigned to kimi, is reassigned to grok. Attempt 2 targets grok and the old task is superseded.
  - `TestReassignRefusals` (Review Focus 4): to the reviewer, to the current agent, to `Bad Name`, and on a `requested` mission. Each gives a refusal, with the records unchanged.
  - CLI usage cases.
- [ ] **Step 2:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ ./cmd/agentbus/ -run 'Skip|Reassign|MissionCLI' -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same. Expected: PASS.
- [ ] **Step 5:** Commit `feat(mission): skip and reassign`.

---

### Task 5: Mission timeouts (§6.1)

**Files:** Modify `mission/conductor.go`. Test: `mission/conductor_test.go`.

**Interfaces:**
- `Conductor.Now func(ctx) (int64, error)` is the clock in Unix ms. It defaults to `Bus.ServerNowMS`; tests replace it, so no test sleeps across a deadline.
- The deadline is the board entry's `request.accepted_at` plus `Timeout` (default `4h`). Each attempt is its own task, so its own `accepted_at`.
- In `advance`, for a mission in `requested` or `accepted` whose board entry is `accepted` **or** `done`:
  - if the board is still `accepted` and `Now` is past the deadline, apply `EvTimedOut`;
  - if the board is `done` but `request.responded_at` is past the deadline, it is a **late delivery** (Codex): also `EvTimedOut`. The delivery is never checked, and the attempt is superseded by the later `retry` or `reassign`.
  - The reason is `"no delivery within <timeout> of acceptance"`. The resulting `blocked` is escalated through the effects.
- For the late-delivery case from `requested`, apply `EvAccepted` first, then `EvTimedOut`, as for a normal done.

- [ ] **Step 1:** Write the failing tests.
  - `TestTimeoutBlocksAndEscalates`: accept, set `Now` to `accepted_at + 4h + 1ms`, then pass. The mission is `blocked`, with one escalation. At `+4h - 1ms` it stays `accepted`.
  - `TestLateDeliveryTimesOut`: with a `30m` timeout, accept, then `done` with a valid commit, and set `responded_at` past the deadline by rewriting the board entry in the test. After a pass the mission is `blocked`, no check ran (no log file), and there is one escalation.
  - `TestTimeoutMeasuredPerAttempt` (Review Focus 5): with a `1h` timeout, accept attempt 1, block it, `retry`, then accept attempt 2. With `Now` set to attempt 1's `accepted_at + 1h + 1ms` it does **not** time out, and with attempt 2's `accepted_at + 1h + 1ms` it does. A blocked agent cannot re-accept the same task back into flight: the mission is `blocked` until `retry` or `reassign` opens a new task, so no bus change is needed.
- [ ] **Step 2:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ -run Timeout -count=1`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same. Expected: PASS.
- [ ] **Step 5:** Commit `feat(mission): per-mission delivery timeouts`.

---

### Task 6: The six slice-1 minors (§10)

**Files:** Modify `mission/ops.go`, `mission/conductor.go`, `mission/git.go`, `mission/integrate.go`. Test: next to each.

| Minor | Change | Test |
|---|---|---|
| abort ordering | transition the missions first, then write the change as `aborted` | `TestAbortCrashLeavesChangeActive`: make the 2nd mission's transition fail (lost lease mid-abort through a token swap), and the change is still `active` |
| transient git error | `Git.ResolveCommit` returns `ErrUnknownCommit` when `rev-parse --verify` exits 1, and a wrapped infra error otherwise; `runChecks` rejects only on `ErrUnknownCommit`, and any other error goes back to `delivered` | `TestResolveCommitInfraError` (repo dir removed: not `ErrUnknownCommit`) and `TestPassUnknownCommit` unchanged |
| subject length | full sha in `Subject` | update the slice-1 tests that build subjects |
| late replies on aborted changes | `passChange` runs `notifyLateSuperseded` for aborted changes too (and nothing else) | `TestAbortedChangeAnswersLateDone` |
| at-least-once effects | the `effects` comment says "at least once", and one test asserts a duplicate escalation is tolerated (two escalation cmds after a simulated crash, mission still `blocked`, no third) | `TestEffectsAtLeastOnce` |
| dirty integration worktree | `integrate` checks `Dirty` before merging; dirty gives `integrate_failed` with "integration worktree has unrecorded changes", nothing deleted. **At startup**, a dirty worktree with no mission integrating is escalated once (`ChangeRecord.DirtyEscalated`, cleared when the worktree is clean again), and integrations stay refused until it is clean | `TestIntegrateRefusesDirtyWorktree` (an untracked unrelated file must not ride into the merge) and `TestStartupDirtyWithoutIntegrationEscalatesOnce` (the file is kept; one escalation over two restarts; cleaning the worktree clears the flag) |

- [ ] **Step 1:** Write the six failing tests.
- [ ] **Step 2:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ -count=1`. Expected: FAIL on the new tests only.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same. Expected: PASS.
- [ ] **Step 5:** Commit `fix(mission): slice-1 minors (abort order, git errors, full-sha subject, late replies, dirty worktree)`.

---

### Task 7: busmon indicator and bootstrap tab (§5.5)

**Files:** Modify `cmd/busmon/header.go`, `cmd/busmon/poll.go` (read `ConductorAlive` on the dispatcher-facts tick, extend `busReader`), `cmd/busmon/help.go`, `scripts/bootstrap` (the `missions` tab and `--no-missions`). Test: `cmd/busmon/header_test.go`, `cmd/busmon/poll_test.go` (or wherever `busReader` fakes live), `tests/bootstrap_test.sh`.

**Interfaces:**
- `headerData.missions string` takes `""`, `"up"` or `"down"`. It is shown only when it is not `""`, as `● missions up` or `○ missions down` (compact: `● miss` / `○ miss`), with prio right after dispatch.
- Poll: `missions` is `"up"` when `ConductorAlive`; `"down"` when not alive but `ChangeNames` is not empty (a loaded change with no conductor is worth showing); `""` otherwise.
- Help: one line describing `missions up/down`.
- bootstrap: a `missions` tab runs `<repo>/agentbus --project <p> missions --repo <project dir>`. `--no-missions` omits it.

- [ ] **Step 1:** Write the failing tests:
  - header cases: up, down, and absent, with absent not taking a cell;
  - the alignment suite stays green with `missions` set (add it to the hostile fixture);
  - poll: the three states;
  - bootstrap: the tab is present, `--no-missions` omits it, and the TOML stays valid.
- [ ] **Step 2:** Run `go test ./cmd/busmon/ -count=1 && bash tests/run.sh`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run the same, then `go test ./cmd/busmon/ -race -count=1`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(busmon,bootstrap): missions indicator and tab`.

---

### Task 8: Skills, docs, end-to-end

**Files:**
- Modify `skills/agent-bus/SKILL.md` (section "Missions — working one"), `skills/agent-bus-master/SKILL.md` (section "Missions — running a change"), `README.md`, `CLAUDE.md`, and the spec (status, and §9 additions: blocked counts as in flight on reload; full-sha subject).
- Test: `mission/e2e_test.go`, `tests/skills_test.sh` (only if it asserts section lists).

Skill content, in English and short:
- **Agent.** When a request body starts with `mission <c>/<id>`, the agent does the following:
  1. reads the brief at the given commit with `git show <commit>:<path>`;
  2. works in its own worktree on a branch from `base`;
  3. commits only inside the allowed paths;
  4. runs the mission's test;
  5. delivers with `request done <task> --commit <sha> --branch <b> "<summary>"`.

  A reply "superseded by …: stop" means it stops and does not deliver. As a reviewer, it answers with the exact `agentbus verdict --subject … --ref …` line given in the challenge.
- **Master.** It runs `missions` in its tab and loads a change with `mission load`. On an escalation it chooses `retry`, `reassign`, `skip` or `abort`. A revised plan is reloaded with `--force` only for in-flight missions. A lost Redis is rebuilt with `--recover`. `mission status` gives the picture.

- [ ] **Step 1:** Write the failing test `TestEndToEndReloadAndSkip`:
  - run the three-mission change;
  - after a and b are integrated, reload a revision that changes c's title (c still pending);
  - skip c;
  - all end `integrated`, the change is rev 2, and there are 2 merges.
- [ ] **Step 2:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ -run EndToEnd -count=1 -v`. Expected: FAIL until Tasks 1–4 are in, then PASS.
- [ ] **Step 3:** Write the skill sections and the docs; `go run ./cmd/agentbus mem check` gives PASS.
- [ ] **Step 4:** Run `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./... -race -count=1 && bash tests/run.sh`. Expected: all ok.
- [ ] **Step 5:** Commit `docs(missions): agent and master skills; slice 2 e2e`.

---

## After the plan (not a task)

The live acceptance (spec §7) needs `go install` (the user's call) and real agents on the production broker. A change "launcher-go" ports `scripts/lib/role_config.py` and the bootstrap template generator to Go as missions run by Kimi and Codex, with master arbitrating. Then v0.10.0 is tagged.
