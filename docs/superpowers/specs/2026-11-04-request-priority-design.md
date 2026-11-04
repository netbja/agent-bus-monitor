# Request priority: high, normal, low, set by the person

Status: spec, for review (subject `request-priority`). Not built. Request: the person, 2026-11-04,
through master. Decided by the person: three levels (high, normal, low), normal by default, tracked
requests first, the plan later.

## 1. Why

Today the order in which work is taken is the order it arrived. When master queues several
tracked requests for one agent, that agent takes the oldest. busmon's Requests tab sorts each
section by unread, then age. The person has no way to say "this one first" except writing a
directive, which is text an agent has to interpret.

A priority is a small structured fact. The person sets it, and every reader orders ready work by it
the same way.

## 2. What it is, and what it is not

- **One of three levels per tracked request:** `high`, `normal` or `low`. Absent means `normal`.
- **Set by the person only, never by an agent** (§4).
- **It orders ready work and nothing else** (§5). A request is ready when its board state is
  `requested` and `Bus.Board`'s derived `Availability` is `retained`: its body is still in the cmd
  stream and its deadline has not passed. The other two values are `missing` (body trimmed) and
  `expired` (`now >= expires_at`). Neither can be accepted: `requestTransition` refuses both, so
  neither is ever next.
- **Untouched:**
  - dependencies, both a plan task's `after` and a mission's order;
  - one task at a time: an agent with an accepted request finishes it first;
  - the review: a `done` request is delivered, so it is in review. It is out of the ready set, and
    a priority never reopens, reorders or pre-empts it. "Never overtakes a request in review" means
    exactly that, and nothing more. It does **not** add a review barrier: an agent with a delivered
    request under review may take its next ready request, as today (the review queue). A barrier
    ("no new work while my delivery is in review") would be a new policy: open question 6, for the
    person;
  - delivery: the cmd stream and the drain keep id order, at least once.
- **Not in this spec:** plan tasks (phase 2, §10) and missions.

## 3. Data model

There is no new key. The priority lives in the request's own record on the board, `{p}:board`
(task → `BoardEntry` JSON), inside `request`:

| field | type | meaning |
|---|---|---|
| `priority` | `"high"` or `"low"` | absent means normal; `normal` is never stored |
| `priority_by` | ValidName | the person who set it |
| `priority_at` | Unix ms | Redis `TIME` at the write |

`bus.RequestInfo` gains `Priority`, `PriorityBy` and `PriorityAt` (all `omitempty`). It also gains
`bus.PriorityRank(p string) int`: high 0, normal 1 (absent or unknown), low 2.

Why on the board:

- The board is the request's record already. Every board writer that touches a request is a Lua
  script that decodes the whole entry with `cjson` and re-encodes it: `publishRequest`,
  `requestTransition`, `deliveryRecord`, the conductor's fenced writes and `boardMutation`. So a
  field it does not know survives an accept, a block, a delivery record or a done.
- One Go helper does write a whole entry from a struct: `boardSet` (`bus/board.go`,
  `json.Marshal` of a `BoardEntry` then `HSET`), called today only by `delivery_review_test.go`. A
  struct round trip would drop a field the struct does not know. This change moves `boardSet` into
  a `_test.go` file of package `bus`, so production code has no struct writer of the board, and §9
  step 3 keeps it that way.
- `board` is a `KeyRecord` in `bus/keykinds.go` and already in `projectKinds`. A backup and a
  restore carry the priority with the request. `TestEveryWrittenKeyKindIsClassified` and
  `projectKinds` do not change.

**End of life:**

- Only `board drop` ends a tracked request: it deletes the entry, priority included.
  `boardMutation` refuses claim and state on any entry carrying `request`, done or not ("tracked
  request: use request accept/block/done or explicit board drop"). `publishRequest` refuses a new
  request while the entry exists. So a task name's next request starts at normal only after a drop.

## 4. Who may write, and how

### Bus

`Bus.SetRequestPriority(ctx, task, level, by string) (changed bool, err error)` is one script,
`requestPriority`, on `{p}:board`:

1. The entry exists and has `request` (refusals: `unknown request`, `board task is not a tracked
   request`, the same words `requestTransition` uses).
2. Its state is not `done` (`request already completed`). A delivered request is in review, and its
   priority would order nothing.
3. Setting `high` or `low` writes `priority`, `priority_by` and `priority_at` from Redis `TIME`.
   Setting `normal` removes all three. The same level as stored writes nothing and returns
   `changed = false`, so a repeated key press makes no churn.
4. `e.updated` does not change: a priority is not progress on the work.

No stream entry is written (open question 1). The new refusals join `requestRefusals`, so
`TestEveryScriptRefusalIsListed` holds them.

The bus is no boundary against the local user, as with every `by` on this bus. `by` records who set
the priority; it does not prove it.

### CLI

`agentbus request priority <task> high|normal|low --as <person>` (`cmd/agentbus/requests.go`):

- **`--as` is required** and must pass `bus.ValidName`. This is the reload and `gate close` form: a
  person names themself explicitly, which also passes the identity guard in a herdr pane.
- **Refused to an agent caller** with the same two checks as `suspend` and `resume --all`
  (`refuseAgentCaller`): `AGENT_BUS_AGENT` set, or a process under a registered agent's verified pid
  (`agentAncestor`; it needs the broker, which this command dials anyway). This check is the guard.
  The tool rule below is defence in depth.
- **Prints the outcome:** `<task>: high (was normal)`, or `<task>: already high`.
- **Help:** a fourth usage line in the `request` row of `commands.go`, and `--as` in its flags.
  `TestEveryFlagLiteralIsInTheTable` covers that.

**Tool rule:** a shared deny on `Bash(agentbus request priority*)` in the shipped `[tool_rules]`.
There is deliberately no `agentbus * request priority*` form: like `agentbus * suspend*`, it would
deny every report or message that merely mentions a request priority.

### busmon

busmon writes as the person through `busWriter.SetRequestPriority`, with `humanIdentity()` as `by`,
in a command. The answer comes back as a notice (§6).

## 5. The ordering rule

There is one pure function in `bus`, so the CLI and busmon cannot disagree:

```go
// RequestLess orders ready requests: priority rank, then created_at, then id.
func RequestLess(a, b RequestInfo) bool
// NextRequest is the request an agent should take next, or why none.
func NextRequest(board map[string]BoardEntry, agent string) (task string, why string) // board as Bus.Board returns it
```

`NextRequest(agent)` works in this order:

1. If the agent has an `accepted` request that is not done, it answers that one with the reason
   `finish first`. Nothing overtakes work in hand.
2. Otherwise it takes the agent's ready requests (state `requested` and `Availability ==
   "retained"`, the values `Bus.Board` derives with one Redis `TIME`) and answers the first by
   `RequestLess`. `NextRequest` is pure over those derived values and takes no `now` of its own, so
   it can never call ready what `Bus.Board` called `expired`.
3. A `done` request, delivered and in review, is neither in hand nor ready. With a done A under
   review and a high requested B, the answer is B (no review barrier, §2 and open question 6).
4. If there are none, it answers `nothing ready`.

A `blocked` request does not hold the agent: it waits on someone else. Its priority applies again
once it is accepted.

**Who reads it:**

- **The agent:** the new `agentbus request next [<agent>] [--json]` prints the task, its level and
  its request id. With no argument it uses the caller itself; with nothing ready it exits 64, as
  the drain does when empty. The `agent-bus` skill says: when more than one tracked request waits,
  run `agentbus request next` and accept the task it names. The drain itself is unchanged.
- **Master:** the `agent-bus-master` skill names `request next <agent>` as how to see what an agent
  takes next before sending more. What master hands out next comes from the plan, which is §10.
- **busmon:** the Requests tab (§6).
- **The stall guard:** unchanged in this phase. `team-stall` fails only when nothing is open, and an
  open request already counts as open work. Its "next ready" is a plan task (§10).

## 6. busmon

### Requests tab, on the tracked section's rows

- **Keys:** `+` raises (low → normal → high) and `-` lowers. Both are free in the shell's table.
  They get two new actions, `ActPriorityUp` and `ActPriorityDown`, in the panel table, with
  `Panels: Requests` only. A row that is one tracked request writes at once; there is no `y`, since
  the change is undone by the opposite key.
- **Other rows:**
  - a group row of several requests says `open the group to set one request's priority`;
  - any other row (a thread that is no tracked request, an ask, the notices line) says
    `only a tracked request has a priority`;
  - a `done` request says `delivered: in review, its priority orders nothing`.
- **Mark:** a column before the kind. `↑` (`GlyphUp`) in the attention role for high, `↓`
  (`GlyphDownArrow`) muted for low, a blank cell for normal. No new glyph, so the closed set is
  unchanged. A group shows the mark of its highest member.
- **Sort,** in the tracked section only:
  - within a group: priority rank, then today's age order;
  - groups: unread first, as today, then the group's best rank, then the oldest.

  Accepted, blocked and done threads sort as normal. The display never claims to reorder work in
  hand.
- **The request's page:** a row `Priority:  high · set by <person> HH:MM` (through `fields.go`)
  when one is set. On that page `+` and `-` work as `OnPage` bindings, offered by `pageOffers` only
  on a tracked request that is not done.
- **Outcome:** a notice, `rq-42: high`, or the refusal as the script words it. busmon re-reads the
  board on the next `requestPoll` tick. It never edits its own copy.

### Help, footer, tour

- Help rows come from the key table.
- The footer shows `+/- priority` only while the Requests tab has the focus on a tracked request.
- The tour's Requests page names the keys through `tourKeys(ModeShell, ActPriorityUp)`, and
  `tourVersion` is bumped so the changed page shows once. `TestTourKeysExist` and
  `TestTourMentionsNoBareKey` hold it.

### Goldens

A Requests fixture with one high, one low and one normal request, in wide, medium and narrow, in
every palette. `TestShellAlignmentSuite` runs on it.

## 7. Configuration

None. Three fixed levels, as the person decided.

## 8. Migration and compatibility

- **Absent means normal,** so every existing request reads normal and nothing is rewritten.
- **An older binary** ignores the field. Go's `json.Unmarshal` drops it on read, and every write is
  a Lua script that keeps it.
- **An older busmon** shows no mark and sorts as today.
- **An older agent** without `request next` takes the oldest, as today. The skill update is part of
  this change.
- `subscribe`'s JSON is untouched, so there is no `v` bump.

## 9. Test plan

Test-first, one step each:

1. **`bus`, pure:**
   - `PriorityRank`, and `RequestLess` with every pair of levels, equal levels by `created_at`, and
     equal times by id;
   - `NextRequest`: an accepted request wins whatever its level; a blocked one does not hold the
     agent; `missing` and `expired` requests are skipped whatever their level (a high missing one
     never beats a normal retained one); a done A in review plus a high requested B gives B;
     another agent's requests are ignored; `nothing ready`.
2. **`bus`, Redis (disposable broker):**
   - set high, set low, set normal (fields removed), the same level twice (`changed = false`, entry
     byte for byte equal);
   - refused on unknown, untracked and done;
   - the priority survives `AcceptRequest`, `BlockRequest`, `deliveryRecord`, the conductor's
     fenced board state and `CompleteRequestAt`;
   - `priority_at` comes from Redis `TIME`;
   - availability for real: a request whose cmd entry is trimmed (`XTRIM`) reads `missing` through
     `Bus.Board` and is never `request next`, while an accepted request whose body has since been
     trimmed still holds the agent (in hand needs no body); an expired deadline reads `expired` and
     is skipped.
   - **Mutants:** remove the done refusal; store `normal` instead of deleting.
3. **The no-struct-writer test:** an AST walk over the module's non-test files that fails on:
   - any `json.Marshal` of a `BoardEntry` or `RequestInfo` value or pointer (type-checked with
     `go/types`, so a parameter or a variable is caught, not only a literal);
   - any `HSet`, `HSetNX` or `HMSet` call whose key argument's data flow (call arguments,
     assignments and parameters, followed to a fixpoint) reaches `BoardKey`.

   It keeps §3's survival argument true. It fails today on `boardSet` (key passed as a parameter),
   so it is written before `boardSet` moves to a test file (red, then green).
4. **CLI:**
   - `request priority` without `--as`, with `AGENT_BUS_AGENT`, and under an agent ancestor (the
     injected `AgentCaller`): all refused;
   - with `--as`: the outcome lines;
   - `request next`: output, `--json` and exit 64;
   - the command table and flag tests;
   - the shipped deny present in `roles.toml` (`TestShippedManifestRules`).
5. **busmon:**
   - `+` and `-` on a tracked row, on a group, on a non-request row and on a done request (the
     writer is faked, so the calls and notices are checked);
   - the mark and the sort in `requests_view`, kept pure;
   - the page row;
   - `pageOffers`;
   - help, tour and goldens.
6. **Full suite plus the Redis suite** before each challenge.

## 10. Phase 2, later: plan tasks

The person said the plan comes later. Recorded here so phase 1 does not close the door:

- The plan's priority would be a **plan fact**: `{p}:plan:facts`, master's facts today (PR, done,
  note). The person would write it, with the same CLI rule and a `plan priority <task> …` form.
  It would not live in `docs/plans/*.json`, which agents edit.
- `plan.Derive` would order `AgentReady` and `PersonReady` by the same `PriorityRank` and then plan
  order. `team-stall`'s "next ready" (`health/stall.go`, the first of `AgentReady`) and master's
  next dispatch would follow it with no change of their own.
- When master turns a plan task into a request, the request would inherit the task's priority.

## 11. Open questions

1. **Tell the target?** Should a priority change write a `wake=no` notice to the target agent, so a
   change reaches an agent that already drained? The proposal is no: `request next` reads the board
   at the moment of choice.
2. **Group rows:** should `+` and `-` on a group row of several requests apply to all of them? The
   proposal is no, to avoid a mass change by one key.
3. **Drain:** should `subscribe --drain --brief` show `prio=high|low` on a tracked request's line?
   That needs a board read in the drain. The proposal is no; `request next` is the place.
4. **States:** allow a priority on an `accepted` or `blocked` request, where it orders nothing
   today, or refuse anything but `requested`? The proposal is to allow it, since it applies again
   once a blocked request is accepted.
5. **Phase 2 storage:** plan facts (the proposal) or a field in the plan file.
6. **Review barrier:** should an agent with a delivered request still in review take no new
   request until the review closes? Today it may, and the proposal keeps that: the review queue
   runs in parallel. A barrier would need a source (the verdict roll-up over the request's subject)
   and a release (approved, or rejected and re-opened). It is a person's decision, not part of
   priority.
