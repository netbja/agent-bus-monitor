# One busmon pane after `agentbus start` on an open project — design

Status: spec only, no code until the person's go. Task `start-busmon-pane` (v0.29).
Base: `origin/main` at 5b8eaac (v0.28.0).

## The bug

In a project already open in herdr, the person quits busmon in its pane. The pane is now a plain
shell. They run `agentbus start` in the project's folder and press Enter. `up` relaunches busmon
in a **new pane split from the busmon tab** and leaves the old shell pane beside it: the tab now
has two panes. Wanted: one busmon pane.

## Why it happens (the code today)

`start` ends in `up`'s own code path (`runStart` → `d.Up`). On an open workspace `runUp` focuses
it and calls `relaunchUp` → `relaunchOpen` (`cmd/agentbus/relaunch.go`), which judges each
template tab with `judgeTab` → `decideRelaunch`:

- The busmon tab's command is `…/busmon --project <p> --as <name>`. `launchRole` finds no
  `agent-launch` in it, so the tab is **not an agent** (`isAgent` false): it has no snapshot, no
  life, no proof.
- herdr detects no agent in it, so `judgeTab` asks `paneShell` for every pane of the tab. When
  they are all one interactive shell by argv (`idleShell`), the verdict is `verdictRelaunch`.
  When busmon still runs, it is `verdictRunning` and nothing happens.
- `verdictRelaunch` never types into the existing pane (the relaunch invariant: whether a shell
  waits at its prompt cannot be proven from outside). It runs the command in a new pane
  (`splitPane`, `--direction right`) and prints `busmon: relaunched (new pane; the old one is
  left as it is)`.
- The old pane is closed only for an **agent**: `old` is set only when `isAgent && has &&
  o.Prove != nil` and the snapshot's pane is in the tab; `closeOldPanes` then waits for
  `provenIn` (a new pid, alive, bound in the new pane). busmon never gets there. The decision
  note says so on purpose: "never a tab that is not an agent (busmon)"
  (`docs/memory/decisions/2026-10-27-relaunch-close.md`).

So each quit-then-start adds a pane: the second time busmon is quit, both panes are shells, the
next start splits again from the tab's first pane, and the tab holds three.

## Design

Extend relaunch-close to **the busmon tab**, with a proof of its own, and keep every other rule.

### Which tab

Only the template tab named `busmon` whose command's first word has base name `busmon` and
carries `--project <p>` for this project. No other non-agent tab is touched (see open question
3 for `dispatch` and `missions` in `--tabs` mode).

### Which panes close

The panes that made the verdict `verdictRelaunch`: every pane of the busmon tab (`ref.All`), all
of them one plain shell at judging time. The new pane is not among them. A tab with a pane that
is not a plain shell is never `verdictRelaunch` in the first place (it is `running`), so nothing
changes there.

Closing all of them, not only the first, also cleans a tab left with two shells by an earlier
`up` (the bug as it stands). Open question 2 asks whether only one should go.

### The proof that busmon runs

busmon has no bus identity: no snapshot, no pid recorded anywhere, no key it writes on start.
What is left is herdr's view of the **new** pane, the one the relaunch created and typed the
command into, which nobody else has touched:

- `herdr pane process-info --pane <new>` lists exactly one foreground process;
- its `argv[0]` base name is `busmon`;
- its argv holds `--project <p>`.

Read once a second, at most 10 times (busmon starts in well under a second; agents get 60 s
because a client can wait on a trust dialog, busmon cannot). The pane is new, so a busmon there
can only be the one the relaunch started. This is a weaker proof than an agent's (no pid start
time), and it is enough for what it guards: closing a plain shell that is, at that moment,
re-read as one.

A new helper, say `busmonIn(herdr, pane, project) (bool, error)`, beside `paneShell`, parsing
the same `foreground_processes[].argv`. An error is "no proof yet".

### The close

The same steps as `closeOldPanes`, in the same pass, after the agents:

1. No proof in time: every old pane is left as it is, and the line says why
   (`busmon: old pane <id> left as it is (no busmon in pane <new> after 10 s)`).
2. Proof: each old pane is re-read with `paneShell` right before. Only one that is still a plain
   interactive shell is closed (`herdr pane close <id>`); busy, gone or unreadable is left, and
   said.
3. Nothing is ever typed into an old pane. Closing is not typing (the decision's own words).

Output, as for agents:

```
busmon: relaunched (new pane; the old one w4:p1 closes once busmon is seen running)
busmon: old pane w4:p1 closed
```

`--dry-run` says `would relaunch (new pane; would close the old one w4:p1 once busmon is seen
running)`. With no `Prove` set (the workspace hook, `reload`), the busmon tab keeps today's
behaviour: `the old one is left as it is`.

### Where in the code

- `relaunchOpen`: when `v == verdictRelaunch` and the tab is the busmon tab and `o.Prove != nil`,
  record an old-pane entry per pane of `ref.All` with the new pane and a "busmon" kind instead of
  a role.
- `oldPane` gains the kind; `closeOldPanes` proves a busmon entry with `busmonIn` instead of
  `provenIn`. The proof loop for agents is unchanged; the busmon proof has its own 10 tries,
  run after the agents' (or interleaved in the same loop with a separate budget: an
  implementation choice).
- `relaunchProof` gains nothing the busmon proof needs except the herdr call it already has.
- The decision note `2026-10-27-relaunch-close.md` gets a dated amendment: "the busmon tab too,
  proven by herdr's foreground argv in the new pane" (once the person agrees).

### Tests (first, red)

In `cmd/agentbus/relaunch_test.go`, with the fake herdr already there:

- `TestRelaunchOpenClosesBusmonsOldShellOnceBusmonRuns`: busmon tab with one shell pane; after
  `pane run`, process-info of the new pane answers `["/x/busmon","--project","shop",…]`; the old
  pane is closed, nothing typed into it.
- `TestRelaunchOpenLeavesBusmonsOldShellWithoutProof`: the new pane stays a shell for 10 reads;
  old pane left, line says why.
- `TestRelaunchOpenLeavesBusmonsOldPaneThatIsBusyAgain`: proof given, re-read of the old pane is
  `vim`; left.
- `TestRelaunchOpenBusmonProofNeedsTheProject`: the new pane runs `busmon --project other`; no
  proof.
- `TestRelaunchOpenClosesEveryShellOfTheBusmonTab`: tab with two shell panes; both closed after
  proof (or only one, per open question 2).
- `TestRelaunchOpenNeverClosesAnotherNonAgentTab`: a `dispatch` tab (tab mode) relaunched; its
  old pane is left.
- `TestRelaunchOpenDryRunSaysItWouldCloseBusmonsShell`.
- In `up_test.go`, `TestUpClosesBusmonsOldShell`: through `runUp` on an open workspace.

## What `start` shows for a project already open

The person asked whether the initial configuration screen is needed again. Today, for an
existing project, `start` shows the full screen (`asker.screen`): Project, Folder, You, Team and
the client lines, then one status line, from the agents' liveness only:

```
  shop is running: 5 of 5 agents live.
  Enter: focus its workspace   1-4: change   q: quit
```

It says nothing of busmon, and "focus its workspace" is wrong when busmon's pane is a shell:
Enter relaunches busmon.

### Proposed: a short screen

When the project exists (its template names this folder), no flag changes it (`--name`, `--as`,
`--roles`) and nothing is unsettled (`p.unsettled()` is empty), start shows:

```
agentbus start — /data/projects/shop

  shop is open: 5 of 5 agents live, busmon not running (its pane is a shell).
  Enter: reopen busmon   c: change project, name or team   q: quit
```

- The status line says what Enter will do, from the same judgement `up` will use: the agents'
  life (`d.Live`, as today) and, inside herdr with the workspace open, `judgeTab` on every
  template tab, run with `DryRun` (it reads herdr, types and closes nothing). The Enter label
  lists what is not running: `reopen busmon`, `relaunch coder and foureyes`, `relaunch coder,
  reopen busmon`, or `focus its workspace` when everything runs.
- Outside herdr, or when herdr cannot be read, the busmon part is left out (as an unreadable
  liveness is today) and Enter keeps today's label.
- `c` shows today's full screen; `1`-`4` typed on the short screen still go straight to the
  same question, so a habit does not break.
- `--yes` is unchanged (no screen). `--dry-run` prints the full plan as today.
- A new project, or one with something unsettled, keeps the full screen.

The short screen changes nothing that is written or run: Enter still ends in `up`'s own path.

### Tests (first, red)

In `cmd/agentbus/start_test.go`:

- `TestStartShowsTheShortScreenForAnOpenProject`: existing template, all agents live; no
  Project/Folder/You/Team lines, the status line and `Enter: focus its workspace`.
- `TestStartShortScreenSaysBusmonIsNotRunning`: the busmon tab's pane is a shell; `Enter:
  reopen busmon`.
- `TestStartShortScreenCOpensTheFullScreen` and `…NumberStillGoesToItsQuestion`.
- `TestStartKeepsTheFullScreenForANewProject` and `…WhenSomethingIsUnsettled`.

## Out of scope

- A bus identity for busmon (a presence key with pid and start time). It would give a proof as
  strong as an agent's, but it is a protocol change for one close; open question 1.
- busmon quitting itself back into its pane (a supervisor loop in the tab's command).
- Panes of other tabs, and other workspaces.

## Open questions (for the person)

1. **Is herdr's view of the new pane enough proof?** The alternative is a busmon presence key in
   `bus/` (pid, start time, pane), checked like `provenIn`. Proposed: herdr's argv is enough,
   since only a plain shell, re-read just before, is ever closed.
2. **Close every shell pane of the busmon tab, or only one?** Proposed: every one, which also
   cleans the tabs the bug already split. Only the tab's first pane would leave older shells.
3. **The `dispatch` and `missions` tabs in `--tabs` mode** pile up the same way. Same rule for
   them (proof: the foreground argv names `agentbus dispatch` / the missions command), or busmon
   only? Proposed: busmon only now.
4. **The short screen's key for the full one**: `c`, or Enter-less "any of 1-4" only? Proposed:
   `c`, with 1-4 still working.
5. **Amend the decision note** `2026-10-27-relaunch-close.md` ("never a tab that is not an agent
   (busmon)") once this is agreed: it is a change of a decision the person made.
