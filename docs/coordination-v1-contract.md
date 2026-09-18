# Coordination v1 contract

Status: implementation contract, 2026-09-17. Owners: Codex owns `bus/*`,
`cmd/agentbus/*`, this contract and its validation record; Claude Code owns
`cmd/busmon/*` and its tests. Neither agent edits the other's files. Worktrees:
Codex `/tmp/agent-bus-monitor-coordination`; Claude reports its own worktree.
No merge, deployment, restart or production bus mutation is part of this slice.

## Audit baseline

Repository HEAD: 1e5f789. Installed agentbus and busmon: Go build metadata reports
924ef599f48e7fc0a86840d33da993b98655f9ef, vcs.modified=true, v0.5.1+dirty;
installed agentbus reports subscribe protocol v1. The committed difference is
only a guide change, but uncommitted binary inputs cannot be reconstructed from
metadata. Findings below are source findings, not proof of a particular live
incident or Redis failure. Binary behavior will be probed only on isolated Redis.

Existing sources of truth:
- status/report/notify/cmd: Redis Streams, XADD MAXLEN ~1000; no age expiry.
- per-agent consumer group over the shared cmd stream; target filtering happens
  after reading. XINFO GROUPS lag counts ALL stream entries, not addressed work;
  pending is separate. XREAD observers do not consume agent messages.
- thread identity = ref if nonempty, otherwise cmd ID. Thread lookup only covers
  retained cmd entries. Gate locks and the verdict ledger are separate concerns.
- agents hash: latest explicit status plus pane/session identity; reports provide
  activity in busmon, not an authoritative herdr process/session state.
- armed lease: subscriber exists recently; absence is not proof an agent is dead.
- board hash: sole ownership/work-state registry. It has no automatic retention.
- verdicts: bounded ledger (default ~10000), not permanent storage.
- reports: <=500-rune preview, optional sanitized <=8000-rune retained text,
  configurable limits. Verdict currently stores only the 500-rune preview.

Confirmed source gaps to reproduce before fixing:
1. XACK precedes the callback; CLI emits after WatchCmd returns and ignores
   writer errors. A crash/output failure can lose an acknowledged delivery.
2. XREADGROUP COUNT 16 advances over a batch; one-shot stops after one addressed
   entry; remaining pending entries are never read (only `>` is used).
3. Pending entries from interrupted consumers are not reclaimed. XACK errors
   are ignored. No distinction between technical delivery and agent acceptance.
4. Silent report full-size clipping and verdict preview-only persistence lose
   content. Raw cmd currently has no application size bound.
5. Board claim uses read-then-write and can race; updates must preserve new
   request fields and avoid overwriting concurrent transitions.
6. Offline is inferred from passive bus activity age, not observed agent death.

## Additive interface for consumers

Existing APIs/fields remain. No second request registry or new stream.
`BoardEntry` gains optional `Request *RequestInfo` (`json:"request,omitempty"`).
Existing Owner/State/Branch/Updated remain; Updated is seconds. Request times are
Unix milliseconds. New tracked requests are opt-in; historical cmds are NOT
invented as tracked tasks. Request publication atomically appends cmd and records
its metadata in the existing board. Task slug remains the board key.

RequestInfo fields (Go / JSON):
- ID/id, Thread/thread, From/from, Target/target, CreatedAt/created_at,
  ExpiresAt/expires_at (0 = no deadline).
- Delivery/delivery: `queued`, `uncertain`, `output_written`, `expired`, `missing`.
  OutputWrittenAt/output_written_at means the JSON writer accepted all bytes;
  it does NOT mean the agent read or accepted them. Attempts/attempts counts
  delivery attempts, not completed work. DuplicatePossible/duplicate_possible
  marks a recovered pending delivery; never promises exactly-once execution.
- AcceptedAt/accepted_at: explicit target-agent acceptance only.
- ResponseID/response_id, RespondedAt/responded_at: explicit completion reply.
- BlockedReason/blocked_reason: explicit target-agent blockage.
- Availability/availability: read-time `retained`, `missing`, or `expired`;
  missing cannot distinguish retention from deletion and never implies done.

Board State for tracked requests: `requested` -> `accepted` -> `done`, with
`blocked` available before/after acceptance and acceptance permitting resumption.
No automatic acceptance from XACK, output, lease, status, report or reply text.
Only explicit request transitions change acceptance/completion. Ordinary board
operations must not reset or bypass tracked request state.

Package API: `Request(ctx, task, from, target, ref, body, ttl) (string,error)`,
`AcceptRequest(ctx, task, agent) error`, `BlockRequest(ctx, task, agent, reason)
error`, `CompleteRequest(ctx, task, agent, body) (string,error)`;
`Board(ctx)` supplies the optional request data. CLI: `request send <task>
<target> [--ref T] [--ttl 5m] <body>`, `request accept <task>`, `request block
<task> <reason>`, `request done <task> <reply>`, `request [--json]`.
`board --json` also exposes all fields. Self identity is AGENT_BUS_AGENT.

Event gains Task, ExpiresAt, Delivery, Recovered, Attempt; subscribe JSON gains
`task`, `expires_at`, `delivery`, `duplicate_possible`, `attempt` where relevant.
Existing v1 fields and one-shot/rearm/exit semantics stay intact. Missing/expired
payloads use existing `event:error` with ID and explicit delivery disposition,
never `event:cmd`. Higher-level consumers must ignore unknown fields. v remains
1: no existing field is renamed/repurposed; acknowledgement moves after a
successful writer callback as a transport bug fix. `--since` remains an explicit
exclusive floor, including for pending entries; it is not proof of acceptance.

`WatchCmdDelivery` adds an error-returning writer callback. Existing WatchCmd is
a compatibility wrapper (callback completion is its delivery boundary).
Use COUNT 1, recover pending before new entries, identify possible duplicates,
check ACK errors. A compare-and-renew receiver lease serializes subscribers for an
agent; lease renewal only exists during subscribe (not an agent heartbeat).
A crash after successful output but before ACK yields uncertain redelivery.
A crash after ACK but before the agent acts is visible as unaccepted tracked
work, not automatically retried action. Redis persistence/durability remains a
broker configuration concern; producer retries without the same task can still
create duplicates. Retention can remove unread bodies: metadata cannot restore
text that Redis has removed.

## Text and retention

New report/verdict writes retain exact valid UTF-8 text up to the configured full
limit (default 8000 runes); oversize/invalid UTF-8 is rejected BEFORE publication,
not silently truncated. Preview sanitization remains for flat viewers. Verdict
adds Full/full and TextComplete/text_complete; Event adds TextComplete.
TextComplete is a string: `yes` = complete, `no` = known truncated, empty =
unknown legacy data (not a boolean). Old data
without text_complete is explicitly unknown, even if it ends without ellipsis.
Cmd rejects invalid UTF-8 and bodies above 65536 runes; no clipping. New cmd
entries mark text_complete. CLI full-detail views indicate unknown historical
completeness. `bus.Limits()` / `agentbus limits` expose resolved per-process caps
and approximate retention. Environment-dependent limits describe the current
writer, not immutable server policy. Existing historical truncation is not
recoverable. No automatic TTL purge or board garbage collection is introduced.

## Validation and rollback

All tests use a new isolated Redis instance and throwaway projects, never the
configured production broker. Required cases: long multiline Unicode, output
failure, crash before/after write, pending transfer, rearm/restart, duplicates,
two recipients, deadline, trimmed entries, explicit accept/block/done, concurrent
board updates. Run go build ./..., go vet ./..., go test ./... -count=1.
Evidence, binary probes and independent busmon review will be appended in a
separate validation record. No shell workflow is changed unless documented.

Rollback before deployment: discard these isolated branches/worktrees. After a
future reviewed deployment: revert binaries together; old readers ignore added
JSON keys, but old board writers may erase request metadata and old subscribers
retain known delivery bugs. Export board/cmd/verdict data first; do not delete or
reset a project. Keep the new reader for request audit until metadata is no
longer needed. No automatic rollback or production mutation is authorized here.

## Clarifications after implementation review

- `TextComplete` is string-valued on both Event and Verdict; do not infer
  truncation from a trailing ellipsis. A literal ellipsis may be original text.
- Availability describes the request's original cmd body, not its reply body.
  `missing` means absent at the read instant; it does not identify the cause.
  The recorded response ID/time survive response-stream retention. Use Thread
  to inspect the reply if still retained. A custom Thread/ref need not be a
  Redis entry ID and must not be displayed as an absent root entry.
- `queued` means no delivery attempt was recorded by the new transport. This
  is not a claim about an older/mixed-version subscriber's side effects.
- `--since` remains an intentional exclusive skip. It can discard pending
  entries at/below that cursor; operators must not advance it past unseen work.
  `--since 0` does not resurrect acknowledged entries of an existing group.
- The receiver lease has a 30-second TTL, renewed every 10 seconds only
  while subscribe runs; process death permits takeover after expiry. Recovered
  records can have been read by an earlier process: IDs identify duplicates,
  while the actual agent action still needs application-level idempotency.
  A user callback must return promptly/cooperate with its context; a process
  paused beyond the lease can overlap another writer. Fenced ACK prevents it
  acknowledging as the new owner, but cannot retract bytes already written.
- New subscribers are fenced against each other on receiver keys; legacy
  `Arm`/`Disarm` only touch armed presence keys and cannot overwrite the fence.
  Old binaries do not honor receiver mutual exclusion. Do not run old/new subscribers concurrently
  for the same agent. No binary rollout is performed by this task.
- Ordinary board state/claim cannot overwrite tracked metadata. Explicit
  `board drop` removes metadata, not an already-published cmd and not an action.
  A new request with an existing task is rejected with its ID. A repeated done
  returns the original response ID and never publishes a second completion;
  it does not replace the first response text. Recovery is inspect/reuse IDs,
  not blindly retry under a new task slug.
- Completion after the acceptance deadline is allowed for already accepted
  work. Expiry blocks initial acceptance and executable subscribe delivery;
  it does not erase work already accepted or its final evidence.
- Callback output is emitted before ACK; if ACK fails after output, subscribe
  exits 75 without appending a second JSON object. `delivery:uncertain` in that
  output is deliberate: a callback cannot know the later ACK result. Board's
  `output_written` is only updated together with the successful technical ACK.
- The Redis logger is initialized once, before clients start: the race detector
  demonstrated a preexisting race when each Connect replaced the global logger.
- Separate source files `bus/delivery.go` and `bus/requests.go` stay in the shared
  bus package under Codex ownership. They reuse stream.go helpers and board.go's
  existing hash; there is no relay, daemon, new registry, or new stream.

## Cross-review amendment (2026-09-18, before implementation)

Claude's independent review changes the receiver implementation without changing
subscribe v1 or request JSON: the exclusive token lives at `{p}:receiver:{agent}`,
separate from legacy `{p}:armed:{agent}` presence. ArmedAgents reads both, returning
consumer names, with receiver precedence. This is an ephemeral transport lock,
not a new request registry. Arm/Disarm remain legacy observability APIs only.
A held receiver lock is waited for within the subscribe idle window; timeout
emits ordinary heartbeat/64, avoiding immediate rearm/error wake loops.
Transport attempts/ACKs MUST NOT refresh BoardEntry.Updated (work activity).
Delivery expiry and derived Availability both use Redis TIME, as publication and
acceptance already do. Mixed old/new subscribers still cannot coordinate their
consumer-group reads; a same-agent cutover is required, but old Arm/Disarm can
no longer overwrite the upgraded receiver fence. Old busmon may miss new leases
until its bus package is upgraded; no fake presence or heartbeat is published.

Connect enables go-redis ContextTimeoutEnabled for both URL and host settings.
An isolated TCP proxy reproduced a 100ms deadline taking 3.001s on an already
established, blackholed connection. Honoring caller I/O deadlines prevents this
from defeating subscribe/poll timeouts. This changes no wire fields or keys.
