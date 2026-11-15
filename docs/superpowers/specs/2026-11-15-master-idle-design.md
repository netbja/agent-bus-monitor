# A guard for master left idle with work in hand (idle-master)

Status: spec, for review (subject `master-idle-spec`), r8. Not built. Task `master-stall-guard`
(master's directive of 2026-11-15, at the person's request: "a reliable and durable fix").
Revisions: r1-r4 the reviewers' rounds; r5 the person's first review (forge-agnostic delivery
§3.1, postponed check-backs §4); r6 `-U0` patch-ids; r7 the rebase step skips heads with merge
commits; r8 the person's second review (residual risk and cache in §3.1, §6 sentences) and the
person's answers (§8).

## 1. What happened on 2026-11-14/15

From the bus history (`agentbus history -a master`, `-k health`):

| Time | Fact |
|---|---|
| 23:01 | coder2 reports herdr-6c2-2 approved by both reviewers at `fa2d796f`, ready to push |
| 23:32 | master asks the person "Merger 505-512"; its status becomes `waiting: 1 question(s)` |
| 00:21 | the person answers; master reports and ends its turn. It had started its own checks (k1/k2) on two approved heads in a shell job that marks a `.done` file, not a tracked background task |
| 00:33 | the checks end. Nothing addresses master, so the dispatcher has nothing to wake |
| 09:09 | the person asks why the project stopped |

No stall guard changed state all night: `team-stall`, `review-queue`, `quiet-agent` and
`idle-implementer` stayed `ok`. Each looks somewhere else:

| Guard | Sees | Why it missed this |
|---|---|---|
| `team-stall` | everyone idle and a ready **plan task** | an approved head to push, or master's own checks, is not a plan task |
| `review-queue` | a review owed | the reviews were done |
| `quiet-agent` | a directive delivered, nothing written since | master wrote a report at 00:21 |
| `idle-implementer` | an implementer idle with requests | master is not an implementer |
| `merge-queue` | approved heads **already pushed** (a PR, or a branch tip on origin) | `fa2d796f` was never pushed |
| the dispatcher's wake | a message waiting for master | there was none |

The stale `waiting` was not the cause: `bus.WaitAnswered` already reads a `waiting` that the
person answered as `idle` from the answer's time (stale-waiting). The hole is that **work only master
can move** has no measure. This spec adds that measure, and nothing more.

## 2. The check: `idle-master`

A health check in the stall guard's family (`stallChecks`, `cmd/agentbus/stallchecks.go`), run
by the dispatch service every minute. It judges one agent: **the escalation target**
(`escalateTo()`, master in this project).

**Warn** when all of these hold:

1. master is registered and its state is `idle` or `done`, or `waiting` answered since
   (`WaitAnswered`, as `team-stall` reads it), for at least `dispatch.master_idle_after` (§6),
   counted from the status entry's id or the answer's id (Redis time);
2. a fresh observation of its pane (`Observation.Fresh`) shows it not busy;
3. it has no obligation in the dispatcher's own sense, `bus.Obligation(...).Wakes()`: entries
   addressed to it, pending or unread, not `wake=no`, within the dispatcher's horizon. A `wake=no`
   entry, a stale one past the horizon, or a trimmed gap is not an obligation here either, since
   nothing would wake master for it. When it has one, the dispatcher's own wake already deals with it, and the check reads `ok`, `master has messages; the dispatcher wakes it`;
4. the project is not paused, and master's provider is not held;
5. **something is in master's hand** (§3).

Otherwise:

- `ok`, with the reason: `master working`, `master idle 6m`, `master waits for you (1 ask open)`,
  `nothing in master's hand`.
- `unknown` when a read fails, or when master's pane has no fresh observation (the stall guard's
  rule: never judge an unseen pane). The record's detail says which.
- `unknown` with the detail `off (master_idle_after = 0)` when the setting is `0`, as the other
  stall checks read off: `health.Status` has no `off`, and the health loop would coerce one.

A `waiting` that is still open (an ask unanswered) is the person's turn and reads `ok`, even with
work in hand: For you already shows the ask.

`Order` after `idle-implementer`; `Handler: "master"`; never a fail. Its detail names how long and
at most three items, then `+N`: `master idle 22m; approved, not pushed: herdr-6c2-2 (fa2d796f)`.

## 3. What counts as in master's hand

Three sources, each already on the bus or one read away. Nothing else counts.

| Item | Read from | Key (for the episode) |
|---|---|---|
| **Approved, not delivered**: a head some subject's deciding approve names (`mergequeue.ApprovedHeads`), within 72 h (`ReviewQueueWindow`), that master has not delivered (§3.1) and that is not dropped (below); a subject whose plan task is recorded done is left out (`DoneSubjects`, as `review-queue` does) | `{p}:mergequeue`, new field `undelivered` (below) | `head:<sha>` |
| **A request for master**: a board entry addressed to master in state `requested` or `accepted` | `{p}:board` | `req:<task>` |
| **A check-back due**: master said work is in flight and when to look again, and that time has passed | `{p}:checkback` (§4) | `cb:<set id>` |

### 3.1 Delivered: one git test, whatever the forge

The project runs on any forge or on none (the person's review, 2026-11-15). A squash-merge or a
rebase-merge rewrites commits, so the approved head never becomes an ancestor of the base; a test
that only asks `InBase` would list a delivered head forever. So the decision is git's alone, the
same for every forge, and a forge adapter only adds knowledge.

**Three delivery modes**, from `delivery.forge` and origin (`forge.Resolve`):

| Mode | When | Master's part is done when the head is |
|---|---|---|
| a forge (`github`, `gitlab`, `bitbucket`) | origin on a known forge, or the setting | in the base (git test below), or a branch tip on origin (pushed, PR opened: the merge is the person's) |
| `none` (new): a remote, no forge | origin on a host no adapter knows, or `delivery.forge = none` | the same: in the base, or a branch tip on origin |
| `local`: no remote | no origin, or origin is a path on this machine | never master's: nothing is pushed, `undelivered` stays empty, and the existing `Items` list the approved local tips for the person's merge |

Today `auto` maps an unknown host to `local` with a note; it maps it to `none` instead (§8,
question 4). `deliver` in `none` mode pushes the branch, as in link mode, without a page to print.

**The git test** (`mergequeue.Delivered`, new, pure over a git runner), for each approved head
within 72 h, cheapest first; a head matched by any step is delivered:

1. **Merge commit or fast-forward:** `InBase` (`git merge-base --is-ancestor <head> <base tip>`),
   as today.
2. **Squash-merge:** the zero-context patch-id of the whole branch, `git diff -U0 --no-ext-diff
   --no-textconv <fork>..<head> | git patch-id --stable`, with `<fork>` = `git merge-base <base
   tip> <head>`, is one of the base's patch-ids (below).
3. **Rebase-merge:** the zero-context patch-id of every non-merge commit of `<fork>..<head>`
   (`git log -p -U0 --no-merges --no-ext-diff --no-textconv <fork>..<head> | git patch-id
   --stable`) is among the base's patch-ids. Only for a head with no merge commit in
   `<fork>..<head>` (`git rev-list --min-parents=2 <fork>..<head>` empty): a merge commit may carry
   a conflict resolution that no single commit holds, and `--no-merges` would skip it, so a head
   that contains one is judged by steps 1 and 2 only, else dropped by hand (reviewer-codex on r6).

**The base's patch-ids**, one set per pass, not per head: the patch-ids of the non-merge commits
of `<oldest fork>..<base tip>`, the oldest fork among the heads judged. The range is the commits
the base gained since the branches forked: no date and no writer clock bound it.

**Cached by SHA.** A commit's patch-id never changes, so the dispatcher keeps, in memory, a map
commit SHA → `-U0` patch-id (and, for step 2, (fork, head) → the branch's patch-id). Each pass
lists the range (`git rev-list --no-merges <oldest fork>..<base tip>`, cheap) and computes patch-ids
only for the SHAs not yet in the map (`git show -U0 --no-ext-diff --no-textconv <new shas> | git
patch-id --stable`). An old approved branch widens the list, not the work: after the first pass,
only the base's new commits are read. Entries for SHAs no longer in any range are dropped at the
end of the pass, so the map stays the size of the range. A restart empties it; the next pass
recomputes the range once. Each command stays within the pass's per-command budget
(`mergeQueueCall`).

**Zero context** (`-U0`, foureyes on r5): `patch-id` hashes context lines, so a base commit that
changed a line within three lines of the branch's change after the fork gives a clean squash or
rebase another patch-id with the default context. With `-U0` only the changed lines count. `git
cherry` takes no diff options, so it is not used. `--no-ext-diff --no-textconv` keep any diff
driver of the repository's configuration from running: the dispatcher never runs the repository's
code. `--stable` is right here (not `--verbatim`, which the conflict-dispatch spec needs to carry
an approval): it detects a delivery, and a forge's squash may differ in whitespace only. A squash
that resolved a conflict, or that the person edited, has other changed lines and is not seen: that
is what `merge-queue drop` is for.

**Residual risk: a false "delivered".** With zero context, two unrelated changes of the same lines
to the same text share a patch-id (a version bump `1.2.3` → `1.2.4` made twice). For step 2 that
needs the base to hold one commit equal to the whole branch's change: unlikely. For step 3 a
one-commit head whose single change equals an unrelated base commit reads delivered. The cost is
bounded: the guard stays silent about that one head, master is not woken for it, and nothing is
pushed, merged or dropped on that reading; `agentbus merge-queue` still lists it while its PR is
open. A head of several commits needs every one of them matched, so a single coincidence never
delivers it. Accepted (the person, 2026-11-15), and pinned by a test (§7).

**The adapter** (`forge.Adapter`, the seam):

```go
type Adapter interface {
    OpenPRs(ctx context.Context) ([]PR, error) // Items: open PRs on an approved head
}
```

`github` reads it with `gh` (today's `ReadGitHub`); `none`, `gitlab` and `bitbucket` return
nothing yet and so use origin's tips, as the git fallback does today. A later adapter only adds
knowledge to `Items`; `undelivered` never depends on one. The `gh pr list --state closed` read
of r2 is dropped: the git test sees a merged PR, and one fewer forge call is simpler.

**The `undelivered` field.** The merge-queue pass (`cmd/agentbus/mergequeuechore.go`, every
5 min, lease held) fetches the base (`FetchBase`) and reads origin's tips (`RemoteTips`) in the
forge and `none` modes, the `gh` path included (it does neither today), then writes, in the same
fenced write of `{p}:mergequeue`, `undelivered: [{subject, head, branch, approved_at}]`: the
approved heads within 72 h that are no tip on origin, not delivered by the git test, and not
dropped. `branch` is a local branch whose tip is that head (`mergequeue.LocalTips`), or empty. A
failed or stale pass (`ItemsAt` older than 3 passes) makes this source `unknown`, and the check
names it, never `ok` on it.

**Dropping a head that will never be delivered.** A subject dropped, replaced by another one, or
squash-merged with its branch deleted leaves an approved head nobody will push (today:
restore-attach-notes, `9b7106db`, redundant after operator-notes-fragments). Master withdraws it
once:

```
agentbus merge-queue drop 9b7106db "redundant after operator-notes-fragments"
agentbus merge-queue undrop 9b7106db
```

- It writes one field of `{p}:mergequeue:dropped`: the full head, `{reason, by, at}` (`at` from
  Redis `TIME`). A short SHA is resolved against the approved heads; an ambiguous or unknown one is
  refused. Only a delivering role (`delivers = true`: master, docs) or the person may drop, as only
  they deliver; the check is `deliver`'s own.
- §3 leaves a dropped head out. The drop names a head, not a subject: a new approve on another
  head of the same subject is a new item.
- The merge-queue pass deletes a dropped field whose `at` is older than 72 h, in its fenced write,
  so the hash stays small. `agentbus merge-queue` lists the dropped heads under the queue, with
  their reasons.

**Not counted:** asks, open merge-queue items (the person's merge), gates (the reviewers'), plan
tasks (`team-stall`), requests to other agents (`idle-implementer`).

## 4. Check-back: work in flight, said once

Master's own background work (a long test run, a build) is not on the bus, so no check can see it
end. Claude Code's tracked background tasks do wake their agent when they exit, but other clients
have no such hook, and a job started outside them (the 00:21 case) wakes nobody. One command makes
it visible, for any client:

```
agentbus checkback 30m "k1/k2 checks on 7d27262c, fa2d796f"
agentbus checkback --clear
```

- It writes one field per agent in `{p}:checkback`: `{due_ms, what, set_id, count,
  first_set_id}` (`count` and `first_set_id` below). `due_ms` is
  Redis `TIME` plus the duration, `set_id` the time of the write, from the same `TIME`. It is an
  agent's write (no lease, like `status`). A new one replaces the old one; `--clear` deletes it.
  `retire --forget` deletes it too.
- **Postponed again and again.** A new check-back replaces the old one and is a new episode, so a
  master that keeps postponing would never reach the second tier. The field also keeps `count`:
  a check-back whose `what` equals the previous one's (trimmed, case folded) adds one, another
  `what` or `--clear` starts again at 1, and `first_set_id` is the `set_id` of the check-back that
  started the count (kept while `count` grows). When `count` exceeds `dispatch.checkback_max` (default 3,
  `0` = never), the stall hook sends the person **one** notice for that `what` (mark
  `checkback-person`, episode a hash of the agent, the `what` and the first `set_id`), on any pass,
  whether `idle-master` warns or not: `master has postponed "k1/k2 checks" 4 times since 00:21`.
- `idle-master` counts it once `due_ms` has passed. It never expires by itself: if master forgets
  to clear it, the next nudge says so (`check-back due 3h ago: k1/k2 checks`), and master clears it.
- Any agent may set one. Only master's is counted in this spec, since only master has this check.
  Another role's field is shown in `agentbus checkback` (no argument: list) and nothing else.
- Text: `health.SafeText`, at most 200 runes.

The master role's text gains one rule (the role file and the agent-bus-master skill): *before
ending a turn with work in flight, run it as a tracked background task, or set
`agentbus checkback`*. That is the person's point 1 and 2 of 2026-11-15, in a form every client
can follow and the guard can verify.

## 5. The nudge, and how it stays quiet

**One wake to master per episode.** While `idle-master` warns, the stall guard's wake hook
(`StallWake`, as `IdleImplementerWakes`; `idle-master` joins the check ids the health loop
hands to that hook, `dispatch/health.go:165`, and `stallWake`'s switch gains its case) sends master one waking directive through
`FencedCmdOnceAt` (below), mark `stall-master`, episode the first 8 bytes of SHA-256 of the sorted item
keys (§3). The dispatcher then types its fixed wake line in master's pane, and nothing else: the
text below is a bus message, never typed (the one-pane-writer rule).

```
Stall guard: master idle 22m with work in hand: approved, not pushed: herdr-6c2-2 (fa2d796f);
check-back due: k1/k2 checks. Move it, or say why it waits: an ask, checkback with a new time,
or merge-queue drop for a head that will never be pushed.
```

**The same facts never wake twice.** The mark (and its `:at`, below) are one field each per project, overwritten by each new
episode, so `{p}:quota` does not grow. A new item, or one gone, is a new set of keys and so a new
episode; a master who works and goes idle again with exactly the same items is not woken again by
this tier.

**Second tier: the person.** On a pass that warns with an episode already woken (the
`stall-master` mark holds that episode), and at least `master_idle_after` has passed since that
wake, the hook sends
**one** notice for the person: `FencedNotifyOnceForPerson`, mark `stall-master-person`, the same
episode:

```
Stall guard: master was woken 25m ago for: herdr-6c2-2 approved, not pushed. It is idle again and
nothing moved. Look at master's pane, or tell it in busmon.
```

The wake's time is kept with its mark, in the same script. A new `bus.FencedCmdOnceAt` is
`FencedCmdOnce` whose script, when it writes, also sets the field `<mark>:at` of `{p}:quota` to the
id `XADD` returned (Redis time), and returns that id. The stream entry itself is never the source:
the cmd stream is capped (~1000 entries) and `busmon --reset` trims it, so an entry gone says
nothing about its age. `{p}:quota` is neither trimmed nor reset.

- Mark holds this episode and `stall-master:at` is set: the wake happened at that id.
- Mark holds this episode but `:at` is missing or unreadable (written by an older release): the
  age is unknown, so no notice; the detail says `wake time unknown`. The next new episode writes
  both.
- Mark does not hold this episode: no wake was sent. A refused wake (`ErrNotHolder`,
  `ErrSwapInProgress`) writes neither field, since the script writes the entry and both fields or
  nothing; the next pass tries the wake again and the second tier waits for it.

Both fields are overwritten by each new episode, so `{p}:quota` holds two fields for this guard at
most. A master without turn hooks that stays
`idle` after the wake keeps the warn's `Since`; the pass one minute after the wake finds the wake 1
minute old and sends nothing.

So one episode costs at most one wake and one notice, whatever its length.

**Master cannot act.** When `dispatch.CannotAct` says master cannot (its provider held, or its
pane last seen blocked), the hook skips the wake and sends the person's notice at once, as
`team-stall` does.

**Interplay.** A wake hook that also fires from `quiet-agent` or `idle-implementer` for the same
night is a different fact (a directive unanswered, a coder passed over), with its own mark.
`idle-master` reads nothing from them.

## 6. Settings

| Key | Env | Default | `0` |
|---|---|---|---|
| `dispatch.master_idle_after` | `AGENT_BUS_MASTER_IDLE_AFTER` | 15m | `idle-master` off |
| `dispatch.checkback_max` | `AGENT_BUS_CHECKBACK_MAX` | 3 | never tell the person about postponements |
| `delivery.forge` | (existing) | `auto` | gains the value `none` (§3.1) |

`dispatch.master_idle_after` is a duration in the registry (`config/keys.go`), `ReadBy:
"dispatch"`, the env rule of the other stall durations. 15m (the person's answer), the default of
`stall_after` and `idle_after`, so every stall guard waits the same. Its own key all the same, as
`idle_after` is: each measures a different wait (a whole team, an implementer passed over, master
with work in hand).

`dispatch.checkback_max` is an integer, `ReadBy: "dispatch"`: how many check-backs on the same
`what` master may set before the person is told once (§4); 3 (the person's answer), `0` never
tells.

`delivery.forge` keeps its key and its machine-wide scope; it gains the value `none`, a remote
with no forge adapter: `deliver` pushes the branch and prints no page, and the git test of §3.1
alone decides delivery. `auto` resolves an origin on a host no adapter knows to `none` (the
person's answer), where it resolved to `local` before; an origin that is a path, or no origin,
stays `local`. busmon's C page lists the three.

## 7. Tests

- **Evaluation, pure** (`health/idlemaster_test.go`), one case per rule: idle past N with each item
  kind warns; `working`, `blocked`, an open `waiting`, idle under N, a busy pane, an unseen pane
  (`unknown`), an obligation, a pause, a hold: no warn; an answered `waiting` counts from the
  answer; a check-back not yet due is not an item; a stale merge-queue pass is `unknown`; `0` reads
  `unknown`, `off`. The detail caps at three items then `+N`.
- **The 2026-11-15 replay**: master `waiting` answered at 00:21, `fa2d796f` approved and not on
  origin, N = 15m: warns at 00:36 and wakes master once; the same input at 00:37 wakes nothing.
- **Obligation**: a `wake=no` entry, a stale entry past the horizon and a trimmed gap do not keep
  the check `ok`; a wakeable pending or unread entry does.
- **Episodes**: a new item makes a new episode and a new wake; a removed item too; the same items
  in another order are the same episode.
- **Second tier**: a wake refused (`ErrSwapInProgress`, lease lost) sets no mark: no notice, the
  next pass wakes again. Woken, idle again N later with the same episode: one person notice; the next
  pass sends nothing. Woken, still warning one minute later (no turn hooks, `Since` unchanged):
  nothing. The wake's time comes from `stall-master:at`, written in the wake's script; the cmd
  entry trimmed or reset changes nothing; `:at` missing with the mark set: no notice, `wake time
  unknown`. `FencedCmdOnceAt` writes the entry, the mark and `:at` together, or none of them.
  Cannot-act: notice at once, no wake.
- **Drop**: a dropped head is left out and its subject's next head is not; drop by a role without
  `delivers` is refused; an ambiguous short SHA is refused; the pass prunes drops past 72 h; with
  every item dropped the check reads `ok` and wakes nothing.
- **Delivered, `none` adapter, a disposable repository with a bare origin**: a merge commit, a
  fast-forward, a squash-merge (one commit on the base with the branch's whole diff) and a
  rebase-merge (each commit replayed) are each delivered; the same squash and rebase-merge are
  still delivered when the base changed a line within three lines of the branch's change after
  the fork; a squash that also resolved a conflict is not (drop covers it); a head holding a
  merge commit whose resolution is absent from the base is not delivered by step 3, even when all
  its ordinary commits are in the base; an unrelated commit
  with the same message is not. **Residual risk, pinned**: a one-commit head whose `-U0` diff
  equals an unrelated base commit's (the same version bump made twice) reads delivered; a head of
  two commits where only one has such a twin does not.
- **Cache**: the second pass over the same base computes no patch-id for a SHA already seen; a
  new base commit is the only one computed; an old approved branch widens the listed range but
  not the patch-ids computed after the first pass; a SHA that left every range is dropped from
  the map; a restart recomputes once. A diff driver set in the repository's config (`textconv`,
  `diff.external`) never runs in any step (a driver that would fail the test). The base's set is
  read once per pass for several heads.
- **`undelivered`**: a head that is a tip on origin, or delivered by the git test, is left out; a
  head on neither is listed with its local branch; an approve older than 72 h is left out; a done
  plan subject is left out. The same input gives the same list with the `github` adapter and with
  `none`: no forge read decides it.
- **Modes**: `local` (no origin, or a path) lists nothing; `none` (origin on an unknown host) and a
  forge list the same; `auto` resolves an unknown host to `none`.
- **Postponed check-backs**: the same `what` three times then a fourth: one person notice; a fifth:
  nothing; another `what` starts at 1; `--clear` starts at 1; `checkback_max = 0`: never.
- **`checkback`**: set, replace, clear, list; the due time is Redis time; `retire --forget`
  removes it; text over 200 runes is refused.
- **Never typed**: the wake goes through `FencedCmdOnceAt`, and the existing pane-writer test still
  sees only the wake line.
- The setting is in the registry (registry test), with its env name.

## 8. The person's answers (2026-11-15)

1. **Threshold:** 15 minutes (`dispatch.master_idle_after`).
2. **`agentbus checkback`:** yes (§4).
3. **Second tier:** yes, one notice per episode (§5).
4. **A remote on a host no adapter knows:** `auto` resolves it to `none` (§3.1, §6). This revises
   the `local` fallback of the forge-delivery decision (2026-11-02) for such a remote; the
   decision note is amended when this is built.
5. **Postponements before the person is told:** 3 (`dispatch.checkback_max`).

## 9. Not in scope

- Watching a job's process or a `.done` file: the dispatcher never runs or reads the
  repository's work.
- Pushing, merging, or dispatching for master: the guard asks; master acts.
- The `gitlab` and `bitbucket` adapters' own PR reads: the seam is here, each is a later PR that
  only adds to `Items`.
- The run budget (duration, deliveries, quota drain): its own spec, `run-budget`, counts its
  deliveries with this section's git test.
- Other roles idle with work in hand: `idle-implementer` covers implementers; the reviewers'
  queue is `review-queue`.
