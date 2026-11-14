- **Project health (v0.19)** — the dispatcher's separate bounded health loop writes
  `{p}:health`, transition-only `{p}:health:log`, and `{p}:health:notes` through fenced scripts;
  Redis `TIME` owns persisted timestamps. Health keys do not discover a project or make
  `HasKeysBesidesDispatch` true. Missing/stale evidence is `unknown`, never `ok`; remediation text
  is static guidance. A new `fail` episode wakes sentinel once, plus one separate daily directive.
  Sentinel may restart an identified failed project service once within its budget, or refresh the
  local model table; everything else is escalated. Usage refresh is at most every 15 minutes with
  a fenced writer; model refresh is daily under a host-local lock. `redis-version` (redis-watch,
  `cmd/agentbus/redislatest.go`) compares the broker's INFO version with the latest Redis release
  **of its own major** (the major it runs, read from INFO; no claim about the compose tag): the dispatch
  service's `RedisRefresh` chore reads GitHub's recent `redis/redis` releases (5 s, drafts and
  prereleases skipped) once a day under a host-local lock into `~/.cache/agentbus/redis-latest.json`
  (major -> newest release, 0600; a failed fetch moves only `checked_at`, silently); warn when behind
  in its major; a newer major is never a warning, only named in the detail (changing major is the
  person's decision); unknown when the broker is unread, no release of its major is found or no check
  succeeded within 48 h. The person updates the broker; sentinel never restarts or upgrades it. `disk` (disk-check,
  `health/disk.go` pure, `cmd/agentbus/diskcheck.go` the reader behind injected `diskDeps`, so no test touches a real disk;
  5 min): the filesystems agent-bus depends on (docker's root from `docker info` when docker is on PATH and the daemon runs
  on this host (`dockerNotLocal`: the endpoint Docker itself reports, `docker context inspect` with no name, which resolves
  `DOCKER_CONTEXT` and `DOCKER_HOST` in its own order (checked on Docker 29; never reimplemented), a local `unix://` socket): not
  measured when docker is absent, the daemon remote or its endpoint unreadable (the note names the scheme, never the host),
  or its root in a VM, on macOS or Docker Desktop, never a host path of that name; `HOME`; `go env GOCACHE` when go is on PATH, a failed or empty answer not measured, `off` no path; the
  services' working directory, the repository, and its
  `.worktrees`; `os.TempDir()`), each device once (`st_dev`), judged by statfs's free-to-unprivileged bytes and %: warn under
  `health.disk_warn_pct`/`_gib` (10 %, 5 GiB), fail under `health.disk_fail_pct`/`_gib` (2 %, 1 GiB); the detail names each
  path, its `major:minor` and free space, the worst first; a path not measured (or a filesystem reporting no size) is
  said, unknown only when nothing was. For you on warn and fail with a command that only looks (`du -sh
  ~/.cache/go-build ~/.npm ~/.cache/pip`); `go clean -cache` is named in the detail as the person's choice
  (`DiskCleanChoice`, kept whole: the paths are cut to leave it room within the 300-rune detail), never in the copyable line; a fail wakes sentinel, which never deletes anything. `github`
  (busmon-github-check, `health/github.go` pure, `cmd/agentbus/githubcheck.go` the runner; 15 min, 10 s) runs only
  when a remote is on github.com (origin first): `ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com` and
  `git -c credential.helper= -c core.askPass= ls-remote https://github.com/<o>/<r>` with `GIT_TERMINAL_PROMPT=0`, in parallel, every command
  under `LC_ALL=C` and without any askpass program (`probeEnviron`: `GIT_ASKPASS`/`SSH_ASKPASS` removed, `SSH_ASKPASS_REQUIRE=never`;
  github-askpass: the terminal switch alone left KDE's ksshaskpass asking for a username every 15 minutes) (the answers are read by git's English messages); any answer from GitHub is reached (auth unknown),
  matched before the network errors since it may quote a repository named like one (TLS by its own phrases), only a network error failed, an unrecognised answer or a missing binary not
  judged. Both reached ok, one warn (for you), neither unknown then warn after `GitHubNeitherWarnAfter` (1 h, tracked
  in the check's memory: a restart starts it over); never fail, so it never wakes sentinel. The stall guard (spec
  `docs/superpowers/specs/2026-11-01-stall-guard-design.md`; judgement pure in `health/stall.go`, readers in
  `cmd/agentbus/stallchecks.go`; `dispatch.stall_after`/`clear_stall_after`/`quiet_after`, 15 min, 0 = off):
  `team-stall` fails (waking sentinel, which sends master one waking directive with the next ready task) when every
  non-dormant agent (`bus.DormantAfter`) is idle that long, each pane seen not busy by a fresh observation (none =
  unknown), nothing is open (request, accepted task, ask, gate, pause, swap, obligation, valid hand-off, mission) and
  the plan has an agent task ready whose owner is not held; person tasks only = warn `waiting-person` (for you); it
  reads `quiet-agent`'s record (warn suppresses it, unknown is unknown, `QuietOff` when quiet_after = 0).
  A `waiting` the person answered (stale-waiting, spec `docs/superpowers/specs/2026-11-06-stale-waiting-design.md`:
  `bus.WaitAnswered`, no open question and, strictly after the declaration, a `human=yes` cmd to the agent or a
  reply on its thread, or one of its asks `answered`, at its latest answer not withdrawn) is read by team-stall and
  review-queue as idle since the answer (`answerWaits`, after `readTeam` and `readOpenWork`; it reads the asks and
  2000 cmd entries only while a non-dormant agent waits, a failed read then unknown), named in the detail
  (`master's wait answered 22:30`): the 2026-11-05 stall, master's declaration never lifted after the person
  answered, is `TestAnsweredWaitNeverBlocksTheGuards`. Nothing is written: only the agent lifts its `waiting`. The plan's
  verdicts are read forward (`plan.ReadWithVerdicts`). `quiet-agent` warns (master) on a waking directive or
  escalation from master, the dispatcher or a person, delivered with its payload (an expired request never counts),
  and nothing written since by the agent on status, report or cmd (same millisecond = written), not working, held or
  seen busy; silent since before the retained cmd history = not judged; collected per agent. `clear-stall` warns on
  a valid hand-off older than its threshold, naming the clear pass's last reason (`dispatch.ClearDecisions`; reason
  `pane-focused` is for you). A warning alone woke nobody (chain-reliability): after each stored `clear-stall` or
  `quiet-agent` record the health loop's `StallWake` hook (lease token) sends the wakes the judged input owes
  (`health.ClearStallWakes`/`QuietAgentWakes`), each one waking directive from `dispatch` under `FencedCmdOnce`:
  a stalled hand-off whose reason is `health.NotIdleReason` wakes the agent with the exact command to re-declare it,
  `agentbus handoff <a> --report <id>` (never `status idle`: the wake is a prompt, and auto-state's turn-start flip
  voids the record as working since, while a new declaration is newer than that flip; `ClearHandoff.Report`), with
  no report known, or any other reason (auto-clear off included), master, once per hand-off and target (mark `stall-clear:<a>`,
  episode `<at>|<target>`); a quiet agent wakes master once per directive it went quiet on (mark `stall-quiet:<a>`,
  episode the directive's id). The health loop's `StallNotify` hook (lease token, after each stored `team-stall`)
  tells the person once per fail episode (`FencedNotifyOnceForPerson`, mark `stall` = the record's since, kept across an
  unknown pass and deleted once the check reads ok or warn): at once when master cannot act (`dispatch.CannotAct`, the escalation rule), else at twice
  `stall_after` from the episode's first since (kept in the reader's memory across an unknown pass). `review-queue` (spec
  `docs/superpowers/specs/2026-11-04-review-queue-design.md`; `health/reviewqueue.go` pure, `cmd/agentbus/reviewqueue.go`
  the reader) warns (master) when the same idle team (`teamIdle`, shared with `team-stall`), nothing else open (gates
  aside), owes a review: an open gate whose challenge the reviewer read, older than `stall_after` (a challenge no longer retained is
  never proved read, a group skipping trimmed entries: not judged, unknown when nothing is owed); or a subject approved
  within 72 h (fixed) and not done in the plan, where a reviewer who engaged with it (a verdict on it, an open gate on
  it, or a challenge under a ref a verdict on it carries) has no approve of the deciding head; the `StallWake` hook
  wakes master once per owed set (mark `stall-review`, episode a hash of the owed keys); never fail. `idle-implementer`
  (spec `docs/superpowers/specs/2026-11-08-idle-implementer-design.md`; `health/idleimplementer.go` pure,
  `cmd/agentbus/idleimplementer.go` the reader; `dispatch.idle_after`, 15 min, 0 = off) warns (master) for each
  implementer (a manifest role with the `implement` skill, `rolesWithSkill`) idle past the threshold, its pane not busy,
  its provider not held, with a board request addressed to it in state `requested`; it needs no idle team. Its clock is
  the `{p}:agents` state time, which a turn end restarts where turn hooks run (a short turn answering a wake restarts
  it: intended). It wakes master once per implementer and requested set (mark `stall-implementer:<agent>`, one
  `{p}:quota` field each, so two implementers never alternate one field; episode a hash of the name and the sorted ids):
  idle, working, then idle again on the same set wakes nobody twice. Requests only, never ready plan tasks
  (`team-stall` names those). Long warns (sentinel-warn-digest): `agentbus health warns` is sentinel's daily digest,
  one message to master, of the checks whose effective status is warn (a stale record is unknown) since more than 24 h,
  each with its age and detail (`health.WarnDigest`). `agentbus health accept <check> --as <person> <reason>` is the
  person's accept, kept in the notes hash under `accept:<check>` (so a sentinel note never replaces it and
  `HealthNotes` never reads it), Redis `TIME`: it counts only for the current warn (written at or after its since),
  holds it out of the digest for a week (counted, not repeated), then the digest lists it again with the accept. It
  changes no measured status. An agent identity refuses the accept, `--as` is required (unproved, as reload's).
  `plan-drift` (the architect's design, reports 1794586011340-0 and 1794586021722-0;
  alert only, the person approved; `health/plandrift.go` pure, `cmd/agentbus/plandrift.go` the read) warns master,
  never fails, on a merge of origin/main since the plan's load that no task links and no `agentbus plan ignore <pr>`
  covers, a task whose merge came from another branch than its board entry records, and a task linked to a PR while a
  later PR of its branch stem (`feat/x-2` for `feat/x`) merged after it (its own merge in the list does not hide one). It reads local git only, in the dispatch service's
  working directory, through the bounded exec (own process group, killed on its deadline, output capped at 256 KiB
  while git writes, the group killed past it,
  `-c core.fsmonitor=false -c core.pager=cat`): `rev-list --first-parent --format=%s --max-count=500
  <plan commit>..origin/main`, the plan commit refused unless it is a commit id. Only GitHub's merge-commit subject
  (`Merge pull request #N from owner/branch`) is a signal. Nothing fetches: origin/main's freshness is the newest FETCH_HEAD
  of the repository (the common directory's and each linked worktree's: FETCH_HEAD is per worktree, and the
  agents fetch in theirs) that records `branch 'main' of <origin's URL>` at the very commit origin/main
  holds; a fetch of another branch or remote, or one that saw main elsewhere, is no evidence. None, or older
  than an hour, makes the check unknown, never ok. Load the plan from a commit on main: from a
  branch, the merges of main since its fork point would warn as not in the plan. It wakes master once per drift (mark `plan-drift`, episode a hash of the drift). It writes
  nothing to the plan; a derived Done is a later step. `{p}:plan:ignore` (a set, a record key) is dropped by a new
  `plan load` and by `plan clear`. busmon shows the health pill, F5
  details and retained transitions in its R recap. Each check has a place per status (`Definition.Place`: `Person`
  statuses with their `Command` are for you, any other warn or fail is in hand by its `Handler`, ok is fine, unknown
  not measured; `PlaceOf` adds the evaluator's fixed `Outcome.Reason` code, stored as `Record.Reason` only when the check
  declares it in `Reasons` (code → the person's command, `CommandOf`): services' spent budget (per kind against
  `ServiceBudgetStarts`, read before inactive since a spent kind exits; its command restarts only that kind) and
  `waiting-git` are for you, an inactive service or a recent error in hand, reviewer-codex and foureyes); busmon pages spec `docs/superpowers/specs/2026-10-31-busmon-pages-mockups.md`): the pill counts only
  what is for you (`health ▲ 2 to do (F5)`, `▲2` below the wide tier), else `?N`, else a muted `●` for in hand, else
  `✓`; F5 is the one list pattern (`listrow.go`: one line per check by place, the fine ones folded, `a` lists them,
  Enter opens a row in place, `y` copies a For-you row's command; the selection follows its check across polls). Runaway is warning-only. `cmd-delivery` judges only the consumer groups of agents registered in
  `{p}:agents` (A9, `deliveryEvidence`: a dead group's lag tripped `fail` for nobody; `agents forget`
  destroys a forgotten agent's group). Scanner, canary, key
  inventory and project-defined checks belong to later releases (password rotation shipped as
  `./setup.sh rotate-redis-password`, the person's act); health is not a security boundary.
