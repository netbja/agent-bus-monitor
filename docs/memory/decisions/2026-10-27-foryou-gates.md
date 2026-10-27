---
id: 2026-10-27-foryou-gates
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-27
last_verified: 2026-10-27
author: coder
confidence: verified
---

## Finding/decision

**For you lists a 4-eyes gate only when it concerns the person, and a reviewer's verdict closes
the gate it answers.** Bernard asked for it on 2026-10-27, after busmon showed "74 for you" in
yellow: 58 gates on foureyes and 16 on ui-kimi, all opened by master over a batch and never
closed. None of them was for him.

- **What concerns the person.** A gate is a row in For you, and counts in the `for you` badge,
  when the person opened it or when it holds the person. In the first case they asked for that
  review, so the verdict is theirs to read. In the second, someone waits on them. A review between
  agents is theirs to finish, and master already follows it on the bus. The default identity
  `pilot` is no person, so it never matches. The agent's row still shows its `gate N` flag.
- **Why gates leaked.** The team challenges the *reviewer* (`challenge foureyes …`), which gates
  the reviewer, but `verdict --ref` only closed the *author's* gate. Reviewers rarely passed the
  `--ref` anyway, and the gate's meta (`challenger|text`) held no subject to match.
- **The verdict now closes the reviewer's own gates.** With `--ref`: the gate it names, plus the
  gates of the same subject opened *before* it. Without `--ref`: every gate whose stored subject
  is the verdict's subject. The first rule came the same day (task gate-subject-close): foureyes
  answered an older commit by subject and the sweep also closed the gate of a newer delta of that
  subject it had not read yet (`dmfwxrbb3o04`). "Before" is read from the challenges' own `:cmd`
  entries: the anchor is the first challenge of `--ref`, a gate is dated by its *last* challenge
  (a re-challenge under the same ref overwrites the gate with a newer delta), a gate whose
  challenge has left the capped stream is older than any retained one; a
  `--ref` with no retained challenge closes only its own gate. A self-verdict closes nothing, by
  this path or by the older author's-gate one (reviewer-codex found that bypass in review). A
  conductor's mission gate is left to the conductor, which closes it after reading the ledger.
  Nobody else's verdict closes a gate on an agent. The subject is stored at challenge time
  (`challenge --subject S`) as `challenger|[subject S] text`. That is additive: old readers see a
  longer text, and old gates simply have no subject.
- **A stale gate is closed without a verdict.** A verdict written to close a gate would count as
  an approve in the roll-up, which is exactly what must not be needed. `agentbus gate close`
  closes one gate, or sweeps by `--subject`, `--mine` or `--by`, with `--dry-run`. Only the gate's
  challenger, or a person named with an explicit `--as`, may close it. An inherited
  `AGENT_BUS_HUMAN` does not count. Each close deletes the gate only if it still holds what was
  read, and in the same script writes a `wake=no` reply on the gate's thread to the gated agent.
  That reply is the audit trace; nothing is written to the ledger.

## When it applies

Every gate opened with `agentbus challenge`. Mission gates keep their own life cycle under the
conductor.

## Evidence

- Master's directive `1793128927917-0` (task foryou-gates); directive `1793134518927-0` (task
  gate-subject-close, the live case `dmfwxrbb3o04`).
- Related: the [shared-memory decision](2026-09-19-shared-memory.md) (the verdict ledger is the
  audit, Redis holds operational state such as gates).
- Code: `bus/gates.go` (`ParseGateMeta`, `FormatGateMeta`, `CloseReviewGates`, `CloseGate`,
  `AllGates`), `cmd/agentbus/gatecmd.go` (`verdictGates`, `runGateClose`),
  `cmd/busmon/tea_foryou.go` (`personGateItems`), `cmd/busmon/poll.go` (`personGates`).
- Tests: `TestCloseReviewGates`, `TestCloseReviewGatesWithRefSparesNewerDeltas`,
  `TestCloseReviewGatesDatesAReopenedGateByItsLastChallenge`,
  `TestVerdictWithRefSparesANewerDelta`, `TestCloseGate`, `TestGateMetaRoundTrip`,
  `TestVerdictClosesTheReviewersGates`, `TestGateClose*`, `TestForYouIgnoresAgentToAgentGates`,
  `TestPollReadsThePersonsGate`.

## Limits / what would invalidate it

- A gate opened without `--subject` and answered without its `--ref` still stays open until its
  challenger closes it.
- A verdict by subject without `--ref` still sweeps every gate of that subject, a newer delta's
  included: the reviewer protects an unread delta only by passing the challenge's `--ref`.
- The CLI cannot prove who runs it: like `answer --as`, `gate close --as` trusts the caller.
- An agent literally named `close` can still be listed (`gate close` is the subcommand), but not
  through `gate <agent>`.
