# Independent final review of Claude's busmon changes

Reviewer: Codex. Outcome: no remaining blocking finding in the reviewed scope.
This is a code/integration review, NOT authorization to merge or deploy.

## Exact reviewed source

Claude branch `feat/busmon-coordination-v1`, worktree
`/data/projects/agent-bus-monitor-busmon`, clean through `5c10cb7`:
- `6ca67d9`: messages, states, follow-up view.
- `e87339d`: rendering tests, demo and documentation.
- `0b2cc54`: Claude's independent review of Codex's transport.
- `43eabbe`: outage snapshots and demo ownership correction.
- `5c10cb7`: typed Board/RequestInfo and Event.TextComplete integration.

Transport prerequisite: Codex branch `codex/coordination-v1`, commits
`d3a1092` and `1b5326b`, base `1e5f789`.
Codex did not edit Claude's cmd/busmon files. Integration was tested with Go's
file overlay; no branch was merged, rebased, or deployed. The installed binaries
were not replaced. The initial repository still has only its original untracked
Todo_Kimi.txt change.

## Findings resolved through independent review

1. Historical text ending in an ellipsis was being classified as truncated.
   It now remains unknown unless text_complete explicitly says yes/no; event
   rendering passes the shared Event.TextComplete through rather than guessing.
2. Empty history lookups were attributed to trimming. The detail/follow-up
   views and legend now leave the cause unknown (deletion, retention, nonexistent
   ID cannot be distinguished). Long caveats wrap instead of being clipped away.
3. Custom challenge/thread references were treated as missing root entry IDs.
   The missing-root check now requires a stream-like ID.
4. Ordinary board tasks were counted as tracked requests, and tracked commands
   could also appear as untracked unanswered threads. These are filtered apart.
5. The monitor delayed its outage indication behind further failing reads and
   replaced failed snapshots with empty data. It now draws canary failure
   immediately, skips the rest of that failed poll, shares a poll deadline and
   preserves each snapshot on its own read error. The shared client now honors
   deadlines on established connections too; an isolated blackhole proxy test
   reproduced the old failure and verifies its fix.
6. The demo script could reuse an unverified container and FLUSHALL it. It now
   checks its ownership label before starting/seeding/removing an existing demo
   container, scopes reset to its disposable demo namespace and clears REDIS_URL
   for demo runs/captures. Stubbed shell tests independently pass; no demo/reset
   script was run by Codex against a real or preexisting project.
7. The legend overstated group lag, pane identity, and missing text. It now
   explains that lag includes other targets and excludes pending entries,
   absence of a lease is not proof of a dead agent, pane identity is declared
   rather than verified, and missing text has an unknown cause.

The typed follow-up reader now consumes Bus.Board's derived Availability, not a
parallel HGETALL/hand-maintained wire model. The board remains the sole registry.
The monitor distinguishes declared state, observed bus activity, subscriber
lease, transport output, explicit acceptance, and explicit completion.

## Independent validation

Transport at 1b5326b:
- `go build ./...`: PASS.
- `go vet ./...`: PASS.
- `go test ./... -count=1`: PASS, 186 cases/subtests, zero skip/fail.

Combined Claude 5c10cb7 + transport 1b5326b, via
`/tmp/agentbus-coordination-overlay.json` mapping only bus/*.go and
cmd/agentbus/*.go to the Codex worktree:
- `go build -overlay=/tmp/agentbus-coordination-overlay.json ./...`: PASS.
- `go vet -overlay=/tmp/agentbus-coordination-overlay.json ./...`: PASS.
- `go test -overlay=/tmp/agentbus-coordination-overlay.json ./... -count=1`:
  PASS, 225 cases/subtests, zero skip/fail. All four packages pass.
- `go test -race -overlay=/tmp/agentbus-coordination-overlay.json ./... -count=1`:
  PASS (bus 3.812s, agentbus 7.502s, busmon 1.158s, usage 1.183s).
- `bash tests/busmon_demo_test.sh`: 12 passed, zero failed; Docker is stubbed.
- Worktree diff checks: clean.

All Redis integration tests used ONLY the isolated Redis 8.6.3 Unix socket.
The normal and combined JSON test logs were inspected for explicit skip/fail
records, not merely a zero process exit status. Shell tests were limited to the
newly changed demo scope. Rendering tests use simulation screens. Claude also
reports a live outage/recovery capture of his labelled isolated demo broker;
that observation is his evidence, not an independently repeated Codex test.

An additional real CLI smoke test on a fresh isolated project proves exact
3600-rune multiline Unicode round-trips for cmd, report and verdict. The request
remains requested after output_written, then changes only on explicit accept/done.
See coordination-v1-cli-smoke.json.

## Boundaries and later integration

No exactly-once agent execution promise. A completed stdout write does not
prove the agent read it. Recovery identifies the same ID and possible duplicate;
accepted work is a declaration. Retained-history-only views cannot prove that an
old unanswered thread never received a reply. Legacy content completeness is
unknown, and already-discarded content cannot be reconstructed.

Do not run old/new subscriber binaries concurrently for the same agent: separate
receiver fencing survives legacy Arm/Disarm, but legacy group readers do not
participate in mutual exclusion. The board has no automatic expiry/GC. Redis
persistence/replication and service restart behavior were not changed.

A future integrator must take the transport prerequisites AND the entire Claude
range from 1e5f789 through 5c10cb7. Cherry-picking 5c10cb7 alone is insufficient:
it depends on the preceding busmon feature/test/fix commits and the new bus API.
Until combined, Claude's typed busmon HEAD intentionally does not compile against
its old base bus package; this is why the overlay was used for validation.
Nothing was integrated automatically.

Rollback before adoption: leave the isolated branches unapplied. After a later
approved deployment, restore the prior binaries together while preserving an
export of new board/cmd/verdict evidence and a compatible reader. Old board
writers may erase request metadata; old subscriber binaries restore the known
loss window. Never reset/delete a live project as rollback. See the contract and
validation record for the full compatibility boundaries.
