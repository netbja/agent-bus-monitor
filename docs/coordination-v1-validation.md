# Coordination v1 validation and review

Worktree: `/tmp/agent-bus-monitor-coordination`, branch
`codex/coordination-v1`, base `1e5f789`. This record accompanies the contract;
all changes are unmerged and undeployed. Todo_Kimi.txt and the initial working
tree were not edited. Claude's busmon branch is reviewed separately below.

## Observations and demonstrated causes

Installed PATH binaries both report build revision
`924ef599f48e7fc0a86840d33da993b98655f9ef`, `vcs.modified=true`, module
`v0.5.1+dirty`, Go `go1.26.5-X:nodwarf5`. Agentbus reports protocol v1.
SHA-256 at audit time:
- agentbus: `45ce204e5437722dff219f7e0de666e98f8075687b002262d806ea8a104871de`
- busmon: `69265abe26f272545096ff437b00b3c64210f9a01581a4965a2584695539145c`

No processes named agentbus/busmon were visible to the read-only process probe
at that instant; this is not proof about past runs or other hosts. Herdr showed
Claude and Codex active separately. Binary modified source cannot be identified
from the revision stamp alone. The committed diff to HEAD is only a guide file.

The installed binary, run ONLY against a newly-created isolated server, did:
1. Publish two commands to dev, both returning IDs.
2. First subscribe (`--since 0`) returned the first command, exit 0.
3. Second subscribe returned heartbeat, exit 64, leaving the second command
   unreturned. Source explanation: COUNT 16 leaves it pending; only `>` is read.
4. A 1000-rune multiline Unicode verdict was accepted, but its ledger view had
   only a flattened 501-rune preview ending in ellipsis. The live cmd copy and
   ledger are distinct: the ledger's SanitizeReportMessage caused this loss.
Raw probe output is in `coordination-v1-binary-audit.json`.

Source regression tests failed BEFORE the fix:
- `TestCoordinationOneShotDoesNotStrandBatch`: second rearm timed out.
- `TestCoordinationAckAfterCallback`: pending count was 0 INSIDE the callback.
  Source ACK occurred before delivery; CLI wrote only after WatchCmd returned
  and ignored writer errors. This was a real loss window, not a Redis outage.

Presence and counters:
- Old busmon replaces the displayed state after 2m/10m without bus activity;
  its offline label is passive age, not observed herdr process death.
- Agent snapshots carry explicit status, timestamp, pane/session identifiers.
  Pane identifiers are not a live health check and can become stale/reused.
- Reports renew bus activity, not an explicit declared work state.
- Armed is a subscriber lease, not agent availability or accepted work.
- CmdLag comes from XINFO GROUPS lag for the ENTIRE shared cmd stream per group.
  It includes entries for other targets and excludes messages already pending.
  It is neither addressed requests awaiting action nor acceptance/completion.
- Thread results, activity and ledger are retention-bounded; an absent entry
  cannot distinguish never existed, explicit deletion, or automatic trim.

## Test isolation

Initial server: local `redis-server` is actually Valkey 9.0.5, port 0, private
Unix socket `/tmp/agentbus-coordination-redis/redis.sock`, persistence disabled.
A second independent container ran official `redis:8-alpine`, Redis 8.6.3,
image `sha256:5068a1b35387fae8c8d5c2b30da50eacbb519b45e97a2524ae4758078fbc77a8`,
`--network none`, port 0, user 1000, only a fresh /tmp directory mounted.
Redis socket: `/tmp/agentbus-coordination-redis8/redis.sock`.
No configured production REDIS_URL was used by tests. Each integration test
uses its own unique throwaway project. Cleanup only affects those test keys.
Sandbox socket denial initially produced skipped tests; those runs are NOT
counted as validation. Tests were rerun with actual isolated socket access.

## Coverage and results

New regression/integration cases in bus/coordination_test.go,
bus/requests_test.go and cmd/agentbus/coordination_test.go cover:
- original two-message one-shot batch stranding and pre-callback ACK;
- pending takeover across consumer names, including an old 16-entry read;
- independent per-agent routing for two recipients;
- complete multiline Unicode across report/cmd/verdict, rejection before
  publication of oversize/invalid UTF-8 bodies, legacy completeness unknown;
- closed writer, short writer, and cancellation after successful output before
  ACK; retry has the same ID and duplicate_possible/attempt metadata;
- real subprocess exits inside Write before AND after stdout write, bypassing
  cleanup, then a fresh process recovers the pending body; a successful ACK
  prevents redelivery on the next process. The test accelerates ONLY its own
  isolated lease's expiry instead of waiting 30 seconds;
- receiver exclusion and fencing: old process cannot ACK or delete a new lease;
- expiration, pending-body trimming, failed gap output followed by retry,
  missing legacy target left unknown, trimming before any consumer read;
- atomic concurrent board claims/request publication; metadata preserved;
- explicit target acceptance, block/resume, completion in original thread,
  duplicate publication rejection and idempotent completion.

`go build ./...`, `go vet ./...`, `go test ./... -count=1` passed on Redis 8.6.3.
The race detector first found a preexisting global SetLogger mutation race;
initialization moved before any clients. Final rerun results are recorded below.
Shell scripts/bootstrap/skills were not modified, so their shell tests were not
expanded; CLI/subscribe changes are covered by Go tests and real subprocesses.

## Limits and rollback

No exactly-once action guarantee. Recovery flags possible duplicates, not a
proof of duplicate execution. Technical output does not prove the agent read
it. Explicit acceptance is a self-declared fact, not external attestation.
Older publications have unknown completeness; already-truncated text cannot
be recovered. Untracked old cmd delivery state is not retroactively invented.
Tracked metadata survives body retention; it cannot reconstruct missing bodies.
No automatic re-execution of accepted or merely output work occurs after ACK.

Request publication and response transitions use Redis Lua atomically; broker
persistence, replication, failover and disk loss were not changed or guaranteed.
Tests restart subscribers, not a production broker. The supplied Compose broker
configuration remains unchanged. Stream caps are approximate, board unbounded
until explicit cleanup, and configured limits describe each writer process.

Before deployment, rollback is simply not adopting these isolated branches.
For any later approved rollout: retain an export of board/cmd/verdict evidence,
revert the binaries together, stop mixing subscriber versions for a given agent,
and keep a compatible reader for added request metadata. Old board writers can
erase metadata and old subscribers reintroduce the demonstrated loss window.
Do NOT reset/delete a project as rollback. No rollback was run against live data.

## Independent review of Claude's busmon change

Worktree `/data/projects/agent-bus-monitor-busmon`, branch
`feat/busmon-coordination-v1`; no busmon files edited by Codex.
Initial review findings sent to Claude through herdr:
1. Legacy trailing ellipsis is not proof of truncation: require TextComplete
   marker, otherwise label unknown.
2. Missing XRANGE result is not proof of automatic trimming: leave cause unknown.
3. A custom ref/challenge correlation key need not name a Redis root entry;
   avoid a false missing-root warning.
4. Ordinary board tasks must not count as tracked requests; tracked cmd threads
   must not be counted twice in the untracked section.
Final review status will be recorded after Claude stabilizes its changes.

## Cross-review corrections and final transport validation (2026-09-18)

Claude independently reviewed the bus/CLI layer in his committed
`docs/coordination-v1-busmon-review.md` (0b2cc54). All five findings were handled:
- contended receiver waits within the one-shot idle deadline, heartbeat/64 on
  timeout; takeover tested after expiry (no immediate error wake loop);
- delivery attempt/ACK leaves board.updated unchanged; only explicit work
  transitions update it;
- Redis TIME determines both subscribe expiration and derived Availability;
  a hooked broker clock differing from the test host proves both paths follow it;
- legacy Arm/Disarm remain documented presence-only APIs;
- ReceiverKey is separate from legacy ArmedKey; legacy SET/DEL cannot corrupt
  its fence. ArmedAgents reads both, returning consumer names without tokens.

After these changes, on isolated Redis 8.6.3:
- `go build ./...`: PASS.
- `go vet ./...`: PASS.
- `go test ./... -count=1`: PASS (bus 2.494s, CLI 2.474s, busmon 0.004s,
  usage 0.016s). This busmon is the original unchanged package in Codex's tree.
- `go test -race ./bus ./cmd/agentbus -count=1`: PASS (bus 3.507s, CLI 7.803s).
- `git diff --check`: PASS.

Independent tests of Claude's three-commit tree through 0b2cc54:
`go test ./cmd/busmon -count=1` PASS (0.020s). Initial four semantic findings
are corrected (legacy unknown, absent cause unknown, custom refs, duplicate
tracked/untracked counts). Two additional findings were sent for correction:
- demo shell script reused an unverified container name and executed FLUSHALL;
  require explicit ownership and unique throwaway namespaces, no FLUSHALL;
  capture must clear inherited REDIS_URL, and shell regression tests are needed.
- monitor health update was delayed by later failing poll reads; refresh failure
  immediately and preserve snapshots on unavailable/partial polls.
API typed integration and final review still pending at this checkpoint.
