# Independent review — Coordination v1 delivery/request layer

Reviewer: Claude Code (owner of `cmd/busmon`). Subject: `bus/delivery.go`,
`bus/requests.go`, `bus/board.go`, `cmd/agentbus/subscribe.go` in
`/tmp/agent-bus-monitor-coordination`, against `docs/coordination-v1-contract.md`.
Read-only review: no file of Codex's was modified, nothing was run against the
live broker, nothing was merged or deployed.

The brief's question was whether the layer keeps **technical delivery**,
**acceptance** and **execution** apart. It does. The findings below are about
what happens around that separation.

## What holds

- **No transport signal ever becomes acceptance.** `deliveryRecord` writes only
  `attempts`, `duplicate_possible`, `delivery` and `output_written_at`. Nothing
  in the delivery path touches `accepted_at`, `response_id` or `responded_at`;
  those are written exclusively by `requestTransition`, which additionally
  refuses a completion that was never explicitly accepted, and refuses to accept
  a request whose body has been trimmed (`XRANGE` check) or whose deadline has
  passed. The three axes are genuinely independent, including the deliberate
  case where a request reads `accepted` while delivery is still `queued`.
- **ACK follows output, not precedes it.** `deliver` returning an error leaves
  the entry in the PEL; `XACK` happens inside the same fenced script as the
  metadata write. The audit's finding #1 is fixed rather than papered over.
- **Skip-ACKs are confined to the agent's own group**, so acknowledging an entry
  addressed to somebody else cannot consume it from their group.
- **Gap evidence is preserved.** `claimRetained` checks the body and claims it
  atomically, so a tombstone cannot be auto-removed between the two, and a
  missing body stays pending until the caller has actually written the gap out.
  `XAUTOCLAIM` was correctly avoided.
- **Recipients are never invented.** A missing body recovers target/task only
  from board metadata and otherwise leaves the target unknown.

## Findings

### 1. "subscriber already armed" hot-loops the wake model — operational, high

`WatchCmdDelivery` fails `SetNX` and returns an error; `runSubscribe` routes it
to the `default` branch, which emits `event:error` with `rearm:true` and exits
75.

In this architecture the exit *is* the wake: a subscriber that exits re-invokes
the Claude session that armed it. So a subscriber that died without releasing
its lease (up to 30s of TTL), or a stale duplicate subscriber for the same
agent, produces a loop of arm → refuse → exit → wake → arm. Each turn of that
loop costs a session wake-up, not just a process spawn.

Suggestion: treat a held lease as "nothing for you yet" rather than an error —
wait for it within the idle window and emit the ordinary heartbeat (exit 64),
which the re-arm loop already handles. Failing that, a distinct event carrying a
backoff, so the caller does not re-arm immediately.

### 2. The delivery path bumps `board.updated` — correctness, moderate

`deliveryRecord` sets `e.updated` for both `attempt` and `finish`. `updated` is
what `agentbus board` and busmon's BOARD pane sort and age by, so a purely
technical event — a redelivery of a three-hour-old request — makes the task look
freshly worked. That re-merges, in the one field the operator reads as work
activity, the axis the rest of the design keeps apart.

`output_written_at` already records the transport timestamp, so the bump buys
nothing. Suggestion: leave `updated` to explicit transitions.

(busmon is insulated: the follow-up view ages tracked requests from
`created_at`. The BOARD pane and the CLI are not.)

### 3. Expiry is judged on two different clocks — cross-host, moderate

`requests.go` derives every timestamp from `redis.call('TIME')`, including the
expiry check inside the accept transition. `delivery.go:172` compares
`e.ExpiresAt` against `time.Now().UnixMilli()` — the subscriber host's clock.

This project deliberately spans a laptop and the VDR over an SSH tunnel, so skew
between a subscriber and the broker is a realistic condition, not a theoretical
one. With a fast subscriber clock a request is delivered as `expired` and its
body blanked while `AcceptRequest` on the server still accepts it; with a slow
one the reverse. Both make the record disagree with what the agent saw.

Suggestion: take "now" from the server in the delivery path too, so one clock
decides expiry everywhere.

### 4. `Arm`/`Disarm` are dead, and their doc now contradicts the code — low

`bus/stream.go:631/640` have no remaining non-test callers. `Arm`'s comment
states the armed key is "observability only; callers must not gate command
delivery on it", while `delivery.go` now uses that exact key as the delivery
mutex. Two contradictory statements about one key is how the next reader gets it
wrong. Either delete the pair or rewrite the comment to describe the lease.

Note also that the stored value changed shape (`consumer` → `consumer:token`).
busmon only reads presence, so the 👂 badge is unaffected, but anything that
prints the value now prints a token.

### 5. A mixed-version window breaks the fence — rollout, moderate

The installed `agentbus` still calls the old `Arm`, a plain `SET` on
`{p}:armed:{agent}`. If any old binary is still running while a new subscriber
holds the lease, that `SET` overwrites the token: renewal fails, the subscriber
cancels mid-flight, and `finishDelivery` is refused *after* output was already
written — manufacturing precisely the uncertain/duplicate case the design exists
to bound.

The contract's rollback section covers old readers and old board writers but not
this interference. Suggestion: give the lease its own key (e.g.
`{p}:receiver:{agent}`) so the two generations cannot share it; then old and new
subscribers merely fail to exclude each other, instead of corrupting a fence.
Otherwise this is a hard cutover, and the contract should say so.

## Note on the busmon side

Until `bus.BoardEntry.Request` reaches busmon's tree, `cmd/busmon/tracking.go`
parses the board hash JSON directly, so the contract's snake_case names are a
load-bearing interface for it. `Availability` is derived by `Board()` and not
persisted, so busmon renders "availability unknown" until it switches to the
typed reader — one function, `boardRequests`, changes then.

`Event.TextComplete` landed as the agreed tri-state string and is parsed in
`ParseEntry`, so busmon's `eventFidelity` needs only to pass it through to
`classify`, which already handles yes/no/empty.
