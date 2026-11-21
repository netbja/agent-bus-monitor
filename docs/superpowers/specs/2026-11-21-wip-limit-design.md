# A work-in-progress limit per agent (wip-limit)

Status: spec r2, for review (subject `wip-limit`). Not built. Base: origin/dev `d1760436` (r1
merged as #734). r2: the person's answers (cmd `1795289356825-0`): Q1 no, Q2 2, Q4 the person's
high priority, Q5 queued, as recommended; Q3 differs: the person alone sets `board.wip_limit`,
but from busmon's Shift+C as well as `config.toml`, with a message saying what the value
implies (§3.3, §3.5). The questions are now answers (§7).
Author: architect, from master's directive (cmd `1795288231080-0`, plan v0.34 task
`wip-limit-spec`). No new dependency. Questions for the person are last (§7).

## 1. Today

A tracked request (`bus/requests.go`) is `requested`, `accepted`, `blocked` or `done`. The board
entry holds it (`BoardEntry.Request`, `bus/board.go:26`), with `accepted_at` and `responded_at`.

- **Nothing limits how much an agent holds.** `request accept` is refused only for the request's
  own state: unknown, another target, done, expired, or its body gone (`requestTransitionSrc`,
  `bus/requests.go:253`). An agent may hold any number of `accepted` requests.
- **`request next`** (`bus.NextRequest`, `bus/priority.go:63`) answers `finish first` while one
  request is `accepted`. A `blocked` one does not hold the agent, and a `done` one is "in review".
  That is advice: nothing refuses a second accept.
- **`done` means delivered, in review** (the agent-bus skill: "a delivered request in review
  does not hold you"). But a **multi-step** request (`triage-dedupe-build`,
  `single-approval-build`, `conflict-owner-build`) is one request for several PRs. It stays
  `accepted` while each step's PR is reviewed, because `done` would end it. So `accepted` mixes
  "being worked" with "a step in review".
- **The reviews do not name the task.** An open challenge names a subject
  (`triage-dedupe-step2`, `busmon-project-badge-1`), not the request (`triage-dedupe-build`,
  `busmon-project-badge`). "In review" cannot be derived from the gates reliably.
- **busmon already flags a silent request:** `board.no_accept_after` (30 min) and
  `board.no_reply_after` (2 h) (`config/keys.go:206`).
- **A precedent for refusing a request:** a draining run refuses a new one (`ErrRunDraining`,
  `bus/requests.go:47`), except the person's own.

## 2. The measure: the board, last 72 hours

Source: `agentbus board --json` at 2026-11-21 ~20:15, `accepted_at` to `responded_at` per request.
This is architect's measure on the live board.

**Now:**

| Agent | accepted | requested (queued) |
|---|---|---|
| coder | 5 | 1 |
| coder2 | 1 | 3 |
| docs | 0 | 6 |

coder's 5 `accepted`, read against the open challenges and `origin/dev`:

| Request | Open for | What it is |
|---|---|---|
| `conflict-owner-build` | 0.9 h | being worked |
| `triage-dedupe-build` | 1.4 h | step 2 in review (challenge `triage-dedupe-step2`) |
| `busmon-project-badge` | 0.3 h | part 1 in review (challenge `busmon-project-badge-1`) |
| `busmon-cli-flags` | 10.1 h | items 1, 3, 4 and 5 merged (`2a7e0360`, `cffcef72`); never closed |
| `language-picker` | 10.3 h | never closed |

So one is in hand, two wait on a review, and two are probably finished but still open.

**Over 72 hours,** the "9 at once" that a naive count gives coder (and coder2) is not real work.
Nine coder requests accepted between 11-16 and 11-18 (`context-escalation-build`,
`holder-hardening-step1`… `language-setting-spec`), held 29 to 72 h, were all closed in one sweep
at 11-19 07:13. coder2's nine closed the same way at 11-19 00:55 and 07:15.

The number that matters is not the peak: **`accepted` lags reality.** A limit counted on it
today would refuse work because of requests nobody closed. So the design first makes the count
honest (`review`, §3.2), then limits it (§3.3).

## 3. The design

### 3.1 What is in flight

**In flight for an agent** = its tracked requests (`request.target` = the agent) in state
**`accepted`**. Not in flight:
- `requested`: queued, not started;
- `review` (new, §3.2): a PR or a step waits on reviewers. This is master's "a PR in review is
  not counted";
- `blocked`: waiting on someone else (the person, Q1);
- `done`.

Only tracked requests count. A plain `cmd` directive is not recorded anywhere, so it cannot be
counted. Every agent is limited the same way: an implementer (coder, coder2) in practice, but
docs or architect too, so there is no list of implementer roles to keep.

### 3.2 A new request state: `review`

```
agentbus request review <task> [--branch <b>] [<note>]
```

- **From `accepted` only**, by the target. It writes the board entry (`state = review`, the
  branch if given, `updated`), in the same script as the other transitions. No stream entry: it
  informs no one, the review's own challenge does that.
- **Back to work:** `request accept <task>` (a reject to answer, the next step), as after a block.
  This is a **resume**, never refused by the limit (§3.3).
- **`request done`** is allowed from `review` (the last step merged or approved).
- **`request block`** is allowed from `review`.
- **`NextRequest`**: a `review` request does not hold the agent, like `blocked`. Its "finish
  first" stays as it is.

The agent-bus skill (coder, docs): after sending a step's challenge, `request review <task>`;
after a reject, or to start the next step, `request accept <task>`. A single-PR request may go
straight to `done`, as today.

### 3.3 The limit

**Setting** (`config/keys.go`; group Services, beside `run.*` and `delivery.*`, `config/group.go:25`):

| Name | Kind | Default | Meaning |
|---|---|---|---|
| `board.wip_limit` | Int | 2 (the person, Q2) | Requests an agent may hold `accepted` at once when it starts a new one; 0 = no limit |

It is a `Project` key, with **no env variable**: config.toml's machine value
(`board.wip_limit`), overridden per project in `[project.<name>]` (`config.ProjectValue`,
`config/file.go:250`).

**The person alone sets it, from Shift+C or `config.toml`** (Q3). Today a `PersonOnly` key
(`backup.after_*`) is refused by `config.Set` and `Unset` (`config/write.go:60`, `:98`), and
busmon shows it without editing it (`cmd/busmon/settings_ui.go:402`). That is too strict here,
so the registry gets one more flag:

- **`PersonEdit`** (`config.Key`): `config.Set` and `config.Unset` refuse it with `board.wip_limit:
  the person's own setting: set it in busmon (Shift+C) or config.toml`, so `agentbus config set`
  and `unset` refuse it for everyone. A new `config.SetAsPerson` and `config.UnsetAsPerson`
  write it, with `Set`'s checks otherwise unchanged; busmon's Shift+C is their only caller.
  A test pins that no other package calls them.
- busmon is the person's own tool: agents do not drive its pages. The agents' launch settings
  already deny `Edit(~/.config/agentbus/config.toml)` (`roles.toml`'s shared `[tool_rules]`,
  pinned by `rolecfg/configdeny_test.go`), though a shell can still write the file. A
  `PersonEdit` key is a discipline, defence in depth, not a security boundary, like §3.3's
  limit itself.
- Shift+C writes the machine value. When `[project.<name>]` holds a value for the shown
  project, the row says so and is not edited there, as busmon already does for a value set
  above the file ("set by …: unset it there").

**Where it is enforced:** in `requestTransitionSrc`, on a **first accept** only (the request has
no `accepted_at`). The caller reads the setting and passes it as an argument. The script counts
the board entries whose `request.target` is the agent and whose state is `accepted` (this task
apart). At or above the limit it refuses:

```
wip limit reached for coder (2 of 2 accepted: conflict-owner-build, language-picker): finish one,
or mark it review (agentbus request review <task>) or blocked; then accept triage-x
```

The new refusal joins `requestRefusals`, so it is `ErrRequestRefused`.

- **A resume is never refused:** re-accepting a request that has an `accepted_at` (back from
  `review` or `blocked`). Answering a reject is finishing, not starting.
- **The person's high priority passes** (`request.priority = high`, set only by a person through
  `request priority … --as`; the person, Q4).
- The count is read in the same script as the write, so two accepts at once cannot both pass.
- The board is one hash of about 300 entries: one `HGETALL` and a decode per first accept.
- The limit is a discipline, not a security boundary. An old binary that passes no limit is not
  refused. The setting's caller is `agentbus request accept`, alone.

### 3.4 What master does

- **`request send`** to an agent at its limit is **not refused** (the person, Q5). It is queued as today
  (`requested`). `send` prints one line on stderr: `coder is at its wip limit (2/2:
  conflict-owner-build, language-picker): queued; coder2 0/2`. It names the other agents under
  their limit that already hold a request of the same project, so master can route.
- **The master skill:** at a limit, route to an agent under it, or wait. Never tell an agent to
  go over it; only the person's high priority does. When an agent's in-flight list holds a
  request past `board.no_reply_after`, ask the agent whether it is done or in review.
- **The dispatcher does not change.** It wakes for messages as today. An agent at its limit
  drains, `request next` answers `finish first`, and a new accept is refused with §3.3's text.
  It does not read the limit, and it runs no repository code.

### 3.5 What busmon shows

- **TEAM / agent rows:** `wip 1/2` next to the state; at the limit, `wip 2/2` in the warn role.
  With `board.wip_limit` = 0, nothing.
- **The agent's page:** its in-flight requests, each with its age. One older than
  `board.no_reply_after` is marked as the board already marks it.
- **Board and Requests views:** `review` is shown as a state (the board does not hardcode states,
  `bus/board.go:23`). A queued request whose target is at its limit reads `waits: coder at wip
  limit`.
- **For you:** nothing. A limit is master's routing, not the person's action.
- **Shift+C:** the `board.wip_limit` row, in the Services group, **editable** (Q3). Its detail
  zone, shown when the row is selected and again in the edit, says what the value implies:

  > Each agent may hold this many requests accepted when it starts a new one. At the limit a new
  > accept is refused until the agent finishes one or marks it review or blocked; your high
  > priority passes. Lower: work finishes before more starts. Higher: more in parallel, more
  > PRs in review at once and more merge conflicts. 0: no limit. Agents cannot change it.

  After a save, the flash says when it applies: `wip limit 2: applies at each agent's next
  accept`. Requests already accepted are not touched.
- **`agentbus config`** lists the key with its value and source, and its refusal (above) names
  where the person sets it.

### 3.6 Rollout

When the build lands, the board still holds what it holds: coder at 5 `accepted` would be at
the limit at once. That is the clean-up §2 asks for. The build's PR body lists each agent's
in-flight requests, and master asks each agent once to mark them `review`, `done` or `blocked`.
Nothing is migrated automatically.

## 4. Steps (in series)

1. **bus:** the `review` transition, the first-accept limit and the high-priority pass in
   `requestTransitionSrc`; the new refusal in `requestRefusals`; `NextRequest` skips `review`;
   `AcceptRequest` takes the limit.
2. **CLI and skills:** `request review`, the limit read in `request accept`, `send`'s stderr
   line; `board.wip_limit` in the registry with the new `PersonEdit` flag, `SetAsPerson` and
   `UnsetAsPerson`; the agent-bus, agent-bus-docs and agent-bus-master
   skills; an operator-notes fragment `docs/operator-notes.d/wip-limit.md`.
3. **busmon:** the TEAM `wip n/m`, the agent page's list, the `waits:` tag, the Shift+C row with
   its detail text and flash, written through `SetAsPerson`.

## 5. Tests

- **Script** (disposable broker only):
  - a limit of 2: a first accept with 2 others `accepted` is refused, with the text naming them;
    with 1, it passes;
  - `review` and `blocked` entries are not counted (Q1);
  - another agent's `accepted` entries are not counted;
  - a resume from `review` or `blocked` passes at the limit;
  - a high-priority request passes at the limit, and a normal one does not;
  - 0 means no limit;
  - two concurrent first accepts at limit − 1: one passes, one is refused;
  - `review` from `requested`, `done` or by another agent is refused; `done` and `block` from
    `review` pass; `review` writes no stream entry.
- **`NextRequest`:** a `review` request does not hold the agent; `finish first` still names an
  `accepted` one.
- **CLI:** `send` to an agent at its limit queues it and prints the line; under it, nothing.
- **Settings:** `board.wip_limit` in the registry, `Project`, `PersonEdit`, no env variable, and
  its invalid text; a `[project.<name>]` value beats the machine value for that project only;
  `config.Set` and `Unset` refuse it with the text above; `SetAsPerson` writes it and reads back;
  no package but busmon calls `SetAsPerson` or `UnsetAsPerson`; the `backup.after_*` keys
  (`PersonOnly`) stay not editable in busmon.
- **busmon:** goldens for `wip 1/2`, `wip 2/2` in the warn role, none at 0; the `waits:` tag; the
  Shift+C row's detail text, its edit saved through `SetAsPerson`, the flash; a
  `[project.<name>]` value shown as set there and not edited.

## 6. What this does not do

- It does not detect "in review" from the gates (§1: subjects do not name tasks).
- It does not close stale requests: busmon shows them and master asks (§3.4).
- It does not limit how many requests master sends, or how many PRs wait for review.

## 7. The person's answers (r2)

1. **A blocked request is not in flight.** As recommended.
2. **The default limit is 2.** As recommended.
3. **The person alone sets `board.wip_limit`, from Shift+C or `config.toml`,** with a message on
   what the value implies. Not the recommended "shown only": §3.3's `PersonEdit` and §3.5's
   detail text.
4. **The person's high priority passes the limit;** nothing else does. As recommended.
5. **A request sent to an agent at its limit is queued,** with a line to master. As recommended.

No question is left open.
