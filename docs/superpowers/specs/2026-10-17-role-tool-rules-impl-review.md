# v0.17 role tool rules implementation review

## rev 5 — CHANGES

### Important — the ask is still described as a safe fallback

The scope reduction is reflected in the code diff: it contains no changes under `bus/`, `cmd/agentbus/`, or `scripts/agent-spawn`; the bus approval, answer refusal, spawn gate, and swap spend are absent. The role configuration and launch changes look consistent with the reduced scope, including deriving per-role allow/ask rules, rejecting invalid declarations, and adding `--settings` only when rules exist.

The documentation is not yet consistently candid about the `ask` being a convenience rather than a lock. In the spec, §4.3 lines 116–117 calls the classifier fallback for a non-matching command form “the safe direction,” but the live result recorded in §9 says `agent-spawn "deploy"` passed and ran without asking. The master skill lines 87–90 similarly says the person decides after the prompt, without explaining that alternative command forms can reach the classifier and run. Please state that the prompt applies to the exact matching command form and that other forms are not guaranteed to prompt or be denied. The release notes, changelog, and `CLAUDE.md` do disclose this limitation.

The spec also leaves obsolete, unstruck active requirements in §§2–3 (bus approval and `agent-spawn` refusal/spend), despite the rev 5 header saying those sections were dropped and abandoned sections are struck. Mark those requirements as historical/not built or remove them so the retained design is unambiguous.

### Checks

- `AGENTBUS_TEST_REDIS_URL=unix:///tmp/agentbus-test/redis.sock go test ./... -count=1` — passed against the isolated test Redis.
- `bash tests/run.sh` — passed; Go emitted a non-fatal read-only stat-cache warning.
- `git diff --check origin/main...feat/v017` — passed.

No production Redis was accessed. This rev 5 review addition is uncommitted.

Reviewed `origin/main..feat/v017` at `55cc38b` against spec rev 4 and the plan's Review Focus.

## Verdict: CHANGES

### Important — approval answers can be carried across a changed reason

`Ask` computes carried question IDs with `sameQuestion`, which compares the header, question and option labels but ignores `Context` (`bus/asks.go:208`). For an approval, the context is the reason Bernard is being asked to approve. A master with an open approval can use the ordinary `ask --replace` path to replace it with the same `spawn-<role>` ID and unchanged header/question/options but a different context. `askCreate` then records that ID in the old ask's `|c` field and moves `approval:<agent>:<role>` to the replacement. A response submitted from the stale old panel is followed by `askAnswer` through `|c` to the new ask (`bus/asks.go:321-324`), so an approval shown for the old reason can authorize the changed reason. The dedicated `ApprovalAsk` replacement correctly sets `|c` empty, but it does not close this generic replacement path.

Approval asks must not carry an answerable question ID across replacement. For an approval-index replacement, clear the old `|c` (and do not carry any answer state), or reject generic replacement of approval asks; add a test that replaces an open approval with the same question identity but a different context and verifies an answer from the old ID is refused and cannot authorize the new index.

## Checks

- `AGENTBUS_TEST_REDIS_URL=unix:///tmp/agentbus-test/redis.sock go test ./... -count=1` — passed against the isolated test Redis.
- `bash tests/run.sh` — passed. Go emitted a non-fatal warning that its module download stat cache was read-only.
- `git diff --check origin/main...HEAD` — passed.

No production Redis was accessed. This report is uncommitted.
