# A guard for an implementer left idle while work waits (idle-implementer)

Status: spec, for review (subject `idle-implementer-spec`). Not built. It is the mechanical half
of the person's rule of 2026-11-08: "a step that depends on an unmerged PR never leaves the
implementer idle". The rule's text half is in the roles and skills (`never-idle-rule`): master
dispatches the next independent item, and the implementer takes it and says which. This spec is
the check that notices when that did not happen. §8 holds the questions for the person.

## 1. The hole

Today's guards see a whole team at rest, never one implementer:

| Guard | Fires when | Misses |
|---|---|---|
| `team-stall` (`health/stall.go`) | **every** agent idle for `stall_after`, nothing open, a plan task ready | master or a reviewer still at work while coder sits idle |
| `review-queue` (`health/reviewqueue.go`) | the team idle while a review is owed | the same: it needs the whole team idle |
| `quiet-agent` | an agent that declared `working` and fell silent | an agent that declared `idle` and stays so |

On 2026-11-08 coder sat `idle` for 20 minutes waiting for PR 355 to merge, while master worked
and a requested task (agy-client) waited on the board. None of the three could fire: the team was
not idle, and coder's idle was a declared, honest one.

## 2. The check: `idle-implementer`

A new health check, run by the dispatch service like the others:

- **Implementer:** a role whose `skills` include `implement` in `roles.toml` (today `coder` and
  `coder2`). No new manifest key: the skill is what makes a role an implementer.
- **Warn** when, for an implementer that is registered and not dormant:
  1. its declared state is `idle`, since at least `dispatch.idle_after` (§4);
  2. its pane is not seen busy (the same `PaneSeen` the stall guard reads);
  3. it is not held by a quota (a held agent is idle for a reason);
  4. the board holds at least one tracked request **addressed to it** in state `requested`
     (delivered or not, never accepted).
- A request it accepted and delivered (in review) does not count either way: work in review is
  not in hand (the rule's own reading).
- `Unknown` when the board or the agents cannot be read, `OK` otherwise, with the detail naming
  why (`coder working`, `coder idle only 6m`, `nothing requested for coder`).
- Its record's detail names the implementer, how long, and the requested tasks, by id, at most
  three, then `+N`: `coder idle 22m; requested: notify-guided-setup-impl, agy-client-impl`.

`Order` places it after `review-queue`; `Handler: "master"`; never a fail. It wakes master, not
sentinel: the move is a dispatch decision, and sentinel's narrow actions do not include one.

## 3. The nudge

While it warns, one waking directive to master per **episode**, as `ReviewQueueWakes` does:

- `Mark: "stall-implementer"`, the episode a hash of the implementer's name and its sorted
  requested ids, so a new requested task, or the same ones for another implementer, is a new
  episode and the same facts never wake twice.
- The text: `Stall guard: coder idle 22m while requested for it: agy-client-impl. Dispatch the
  next independent item (no stacked PRs: one PR per step from origin/main), or tell coder why
  it waits.`
- It goes through the bus like every stall-guard directive. The dispatcher types only the fixed
  wake line in master's pane, never this text (the one-pane-writer rule).
- The implementer is not woken by the guard. Its requested task already woke it when it was sent;
  if it then declared idle, the question is master's: dependency, order, or a forgotten
  dispatch.

## 4. The setting

`dispatch.idle_after` (`AGENT_BUS_IDLE_AFTER`), a duration in the settings registry
(`config/keys.go`), default `15m`, `0` turns the check off (its record says `off`). It is its own
key, not `stall_after`, because the two measure different waits: a whole team at rest against one
implementer passed over. busmon's C page lists it with the other dispatch durations.

## 5. Where it shows

- busmon F5 (health), as every check: the row and its detail.
- Master's pane, through the wake line and the directive above.
- Not the person's notifications: a warn handled by master is not a `for-you` event, and the
  notifications spec sends only those (unless the person adds `for-you`, which still excludes a
  master-handled check).

## 6. Tests

- Pure evaluation, one case per rule of §2: idle long enough with a requested task warns; working,
  blocked or waiting, idle under the threshold, a busy pane, a quota hold, dormant, a non-implementer
  role, only accepted or delivered requests, a request addressed to another agent: OK; a read
  error: Unknown; `idle_after = 0`: off.
- The wake: one directive to master per episode; the same facts on the next pass wake nothing; a
  new requested task wakes again; two idle implementers are two episodes.
- The implementer set comes from the manifest's `implement` skill, read the way the launcher reads
  skills.
- The setting is in the registry (the registry test), with its env name.

## 7. Not in scope

- Moving or accepting a task for the implementer: only its target accepts a request (the bus
  refuses anyone else), and only master dispatches.
- Plan tasks not yet sent as requests: `team-stall` already names a ready plan task, and a plan
  task becomes this check's matter once master sends it.
- Judging whether a requested task truly depends on an unmerged PR: the board does not know, and
  master does. The nudge asks; it does not decide.

## 8. Questions for the person

1. **The threshold.** Recommended: 15 minutes, the stall guards' own default. Or shorter (5 min),
   at the cost of nudging master while an implementer only reads its next task.
2. **Who is an implementer.** Recommended: a role with the `implement` skill. Or an explicit
   `implements = true` on the role.
3. **Ready plan tasks too.** Recommended: no, requests only (§7). Or also warn when the plan holds
   a ready task owned by an implementer that nobody sent.
