# Dev branch flow: PRs into dev, dev promoted to main after the release test

Status: spec, for review (subject `dev-branch-flow`). Not built. Base: origin/dev `e6d9cfcc`
(origin/main `711ea8ef`, #556; dev is 11 commits ahead). Author: coder, task `dev-branch-flow`,
from master's directive of 2026-11-16 relaying the person's choice: **pull requests merge into
`dev`; `dev` is promoted to `main` only after the end-to-end release test passes.** One PR (this
spec); the build is §9's steps, each its own PR. The person's decisions are §10.

## 1. Why

On 2026-11-16 the herdr-exit release reached the person's machine with two bugs the suite could
not see: Claude's turn-end title printed on the holder screen (vt-osc-title) and a dirty build key
put the holder's socket over its path limit. Both are now caught by `TestReleaseEndToEnd`
(`cmd/agentbus/e2e_release_test.go`, e2e-release-test). The person wants `main` to hold only what
that test has passed, and every day's work to land somewhere else first: `dev`.

## 2. Today, measured on `e6d9cfcc`

| Where | What assumes `main` |
|---|---|
| `config/keys.go:458` | `delivery.base`, default `main`, **MachineOnly**: one value for every project of the machine (forge-delivery answer 1) |
| `cmd/agentbus/deliver.go:193` | `agentbus deliver` takes `--base`, else `delivery.base`, and opens the PR against it |
| `cmd/agentbus/plandrift.go:107-143, 244`; `health/plandrift.go` | plan-drift reads the merges of `origin/main` since the plan's load, and its freshness from a FETCH_HEAD line for origin's `main` |
| `cmd/busmon/mergequeue.go:138` | a local-forge merge item without a recorded base says `git switch main && git merge --no-ff …` |
| `mission/model.go:54` | a mission without `base` targets `main` |
| `setup/update.go:36` | `setup.sh update` is `git pull --ff-only` of **whatever branch is checked out**, then install |
| `setup/build.go` (`BuildAndInstall`) | `setup.sh install` builds the **checked-out tree** |
| `roles/coder.md`, `coder2.md`, `master.md`; `skills/agent-bus`, `skills/agent-bus-master` | "one PR per step, each from `origin/main`", and `git merge-base --is-ancestor <head> origin/main` to tell a merged PR |
| `health/health.go:118`, `health/idleimplementer.go:130` | the idle-implementer check's remediation and its text to master: "one PR per step from origin/main" |
| `health/health.go:126`, `health/plandrift.go:86-88` | plan-drift's remediation and detail name `origin/main` ("origin/main last fetched … ago") |
| `cmd/agentbus/up.go:561-565`, `holdercmd.go:168-175`, `rolecfg.ManifestPath` | the running team's files come from the repository too: `up` records `AGENTBUS_HOLDER_REPO` (`repoDir`: `AGENTBUS_REPO`, else `buildinfo.Repo` stamped by setup), the holder launches every agent through `<repo>/scripts/agent-launch`, the manifest falls back to `<repo>/roles.toml`, and the linked skills resolve under `<repo>/skills` |
| `cmd/busmon/update.go:15` | busmon's update check reads GitHub's latest **release** (tags) |

The repository's own checkout is the team's working copy: on 2026-11-16 it stood on a feature
branch (`fix/vt-osc-title`) while the installed binaries had been built from it. An install from
"the repository" is therefore an install from whatever an agent last checked out.

## 3. The two branches

- **`dev`**: every PR's base. Agents branch from `origin/dev`, open their PR against it, and master
  merges it there after the 4-eyes proof bound to its head (decision 2026-11-02, unchanged).
- **`main`**: only promotions of `dev` (§5) and nothing else. No PR targets it; no agent pushes to
  it but master's promotion. Every commit on `main` is a `dev` head that passed the release check.
- **Tags**: on `main` only, by the person (§6).

`main` never gets a commit of its own, so a promotion is a **fast-forward** of `main` to a `dev`
head: the commit that was tested is the commit that ships, sha for sha (no merge commit to
re-test).

## 4. Delivery to dev

### 4.1 `delivery.base`: the machine's, or the project's

`delivery.base` is **machine-wide by the person's own answer** (forge-delivery spec
2026-11-01, answer 1: "one machine-wide setting in `config.toml` … no per-project value"; the
registry marks it `MachineOnly`, so a project's services env file is never read for it). This
machine also runs projects that have no `dev` branch. Two ways, the person's choice (§10, Q2):

- **(a) Keep answer 1**: `delivery.base = "dev"` for the whole machine, and every project that
  delivers here gets a `dev` branch (created from its `main` once). Nothing changes in the
  registry; `agentbus deliver` already reads the setting.
- **(b) Revise answer 1 for the base only**: the project's template
  (`<projects-dir>/<project>.toml`) may set

  ```toml
  [delivery]
  base = "dev"
  ```

  and `deliver` reads `--base`, then the project's `delivery.base`, then the machine's (default
  `main`); `agentbus config --project <p>` says which applies and from where. The template, not
  the services env file: the template is the person's (bootstrap writes it, `up` never rewrites
  it), the env file is `up`'s. `delivery.forge` stays machine-wide either way.

### 4.2 Everything that names the base reads it

One function, `deliveryBase(project)`, used by:

- `agentbus deliver` (as today, from the new resolution);
- plan-drift (§7.1), the merge queue's local fallback (§7.2), missions without `base` (`mission`
  takes the project's base, not `main`);
- the role and skill text (§8): "from `origin/<base>`", the base named once at the top of the role
  (`dev` for this project).

### 4.3 Open PRs against `main`

At the switch, every open PR against `main` is retargeted to `dev` (`gh pr edit <n> --base dev`)
by master, its approvals kept: `deliver`'s proof binds the head sha only (`deliver.go:295`,
`approvalsOn`: `v.Head == h`), never the base, so a retargeted PR needs no new review and no new
`deliver` run; master merges it on the forge as before. A PR merged into
`main` after the switch is a mistake to undo by reverting it on `main` and landing it on `dev`.

## 5. Promotion: dev → main

### 5.1 Who

**master** prepares and executes it; **the person** decides when (§10, Q3). A promotion is a push
to `main`, which only master and docs may do (decision 2026-11-02); docs never promotes.

### 5.2 The check

On a **clean worktree at the `dev` head being promoted** (`git worktree add <tmp> <sha>`, never the
team's checkout), with an isolated disposable Redis started for it:

1. `go build ./...`, `go vet ./...`, `GOOS=darwin go build ./...`;
2. `go test ./... -count=1` with `AGENTBUS_TEST_REDIS_URL` set (no Redis case skips);
3. the release test under its gate, verbose:
   `AGENTBUS_RELEASE_GATE=1 AGENTBUS_TEST_REDIS_URL=<disposable> go test ./cmd/agentbus -run '^TestReleaseEndToEnd$' -count=1 -v`;
   **proof that it ran**: its output holds `--- PASS: TestReleaseEndToEnd`; a `--- SKIP` or no
   such line fails the promotion (under the gate a missing broker already fails the test);
4. `go run ./cmd/agentbus mem check`.

Any failure stops the promotion; nothing is pushed.

### 5.3 The act

The check passed on sha `S`: master pushes `S` to `main` **fast-forward only**
(`git push origin S:refs/heads/main`, refused by git if `main` is not an ancestor of `S`), then
reports to the person: the sha, the promoted PRs (`git log --first-parent origin/main..S` before the
push), and the check's lines (the test's PASS line, the suite's package count, mem check).

### 5.4 Its record

A promotion leaves a record master and busmon can read: `agentbus promote` (built in §9, step 4)
runs §5.2 and §5.3 and writes one ledger entry `{p}:promotions` (sha, from, the PRs, the check's
outcome and its PASS line, by, Redis TIME). Until that command exists, master runs the steps by
hand and the report is the record.

## 6. Tags and releases

- A tag is set **by the person, on `main` only**, on a promoted sha. busmon's update check reads
  GitHub's latest release: tags stay where it looks.
- The release-notes PR (CHANGELOG, `agentbus mem notes` assembling `docs/operator-notes.md`, the
  `herdr-plugin.toml` version) lands on **`dev`** like any PR and reaches `main` by the promotion
  that the person then tags. A tag is never set on a `main` sha whose promotion record is missing.
- A tag is never moved; a fix is a new patch release (as today).

## 7. Plan-drift and the merge queue with two bases

### 7.1 Plan-drift

A plan's tasks merge into `dev`, so plan-drift reads **`origin/<base>`** (`dev` here): the merges of
`origin/dev` since the plan's load, and the freshness of a FETCH_HEAD line for origin's `dev`. A
promotion is not a plan merge: plan-drift never reads `main`. Its health text and the master
skill's line ("unknown means origin/main was not fetched") name the base.

### 7.2 The merge queue

A merge item already carries its PR's base (`rec.Base`, written by `deliver`). busmon shows it on
the item ("into dev"), and the local-forge fallback says `git switch <base>` from the project's
base instead of `main`. A promotion is **not** a merge-queue item: it is master's act, shown in
busmon as a For-you line only when it fails (§10, Q6).

### 7.3 "Is my PR merged?"

The roles' rule (an unmerged PR never leaves a coder idle) asks
`git merge-base --is-ancestor <head> origin/<base>`, i.e. `origin/dev`: merged means merged into
`dev`, not shipped.

## 8. Install and update: from main only

### 8.1 One tree for the running team: `<repo>/.release`

The binaries are not all the team runs from the repository (§2): the launcher
(`scripts/agent-launch`), `roles.toml` and the skills are read from it too, through
`buildinfo.Repo` and `AGENTBUS_HOLDER_REPO`. Building the binaries from `main` while the rest came
from the checkout would keep the mix §2 names. So **everything the running team reads comes from
one tree, `main`'s**:

- `setup.sh install` and `update` keep a dedicated worktree **`<repo>/.release`** (fixed path,
  owned by setup) at `origin/main` (or its newest tag, §10 Q4): `update` runs `git fetch origin
  main` and moves `.release` there, never touching the checkout's branch or files;
- they build both binaries **in `.release`** and stamp `buildinfo.Repo = <repo>/.release`, so
  `repoDir`, hence `AGENTBUS_HOLDER_REPO`, the holder's launcher (`.release/scripts/agent-launch`),
  the default manifest (`.release/roles.toml`) and the linked skills (`.release/skills`) all name
  `main`'s tree;
- the skills are linked from `.release/skills`: setup links them today through
  `<in.Repo>/scripts/link-role-skills.sh` (`setup/skills.go:96`), `in.Repo` being the checkout;
  step 5 runs it from `.release`;
- the templates' `ROLES_TOML=` and launcher paths: `up` rewrites a tab whose command names
  `<repo>/roles.toml` or `<repo>/scripts/agent-launch` to the `.release` path once, and says so
  (an existing template is the person's: nothing else in it changes);
- both say what they build: `installing v0.31.0 (origin/main 711ea8ef) in <repo>/.release`.

**The consequence, stated plainly:** a change to a role, a skill, `roles.toml` or the launcher that
merges into `dev` reaches the running team only after a promotion and an `update`, exactly as a
code change does. The agents keep working **on** the checkout (their worktrees from `origin/dev`);
they no longer run **from** it. Whether a role or skill change may skip that wait is the person's
(§10, Q10).

`.release` gets its `.gitignore` line, and every tree walker must skip it: the go tool already
skips dot directories (`./...`), and step 5 checks `internal/scrub`, `agentbus mem check` and the
backup's gather, adding the skip where one walks it. The untracked-files fix (build-id-untracked)
stays; building in `.release` also keeps the person's notes out of the build key for good.

### 8.2 Testing dev on the machine

`setup.sh install --from dev` (or `--from <ref>`) builds that ref instead, says so in yellow, and
stamps the build so `agentbus version` and busmon's About read `dev <sha>` (never a release).
Whether the person wants this at all is §10 Q5.

## 9. Build order (each step its own PR, into dev)

1. **The base** (§4.1 as the person answers Q2, §4.2): with (b) the template's
   `[delivery] base` and its resolution in `deliver` and `config --project`; with (a) only the
   machine's setting and a `dev` branch per project; missions take the base either way.
2. **Text**: roles, skills, `CLAUDE.md`, `AGENTS.md` (§11), the decision note.
3. **plan-drift and the merge queue** on the base (§7).
4. **`agentbus promote`** (§5): the check on a clean worktree, the gate's PASS proof, the ff push,
   the record; `--dry-run` runs the check and pushes nothing.
5. **setup from main** (§8): the `.release` worktree, `buildinfo.Repo` stamped to it, the skills
   linked from it, `up`'s one-time rewrite of the templates' paths, the message, `--from`, the
   `.gitignore` line and the walkers' skip.

Steps 1 and 2 make the flow real by hand (master promotes with §5.2's commands); 3 to 5 automate
and guard it.

## 10. Questions for the person

1. **The switch's moment.** `dev` already holds 11 PRs `main` does not. Promote them now (after
   the release test on dev's head), or wait for the next batch?
2. **Per-project base, or not** (your forge-delivery answer 1 made `delivery.base` machine-wide,
   §4.1): (a) keep that, set `dev` for the machine and give every project here a `dev` branch; or
   (b) let a project's template set its own base (`dev` for this one), the others staying on
   `main`?
3. **Who decides a promotion, and when?** (a) master promotes as soon as the check passes, after
   each merged batch; (b) master proposes it in For you and you answer; (c) only on your word.
4. **What update installs.** The newest **tag** on main (what a release is), or **`origin/main`'s
   head** (every promotion, tagged or not)?
5. **Testing dev on your machine.** Do you want `setup.sh install --from dev` (§8.2), or does the
   machine only ever run main?
6. **A failed promotion.** Shown to you in For you, or only reported to master, who fixes `dev` and
   tries again?
7. **Hotfixes.** A bug on a released `main`: always fixed on `dev` and promoted (the release test
   on everything merged meanwhile), or a `hotfix/<x>` branch from `main`, promoted on its own and
   merged back into `dev`?
8. **GitHub settings.** Make `dev` the repository's default branch (outside PRs then target it by
   default), and protect `main` (only master's push, fast-forward only)? Both are your settings on
   GitHub, not code.
9. **The tag's check.** Is the promotion's check enough for a tag, or do you also want a live check
   on your machine (install `--from main`, start a project) before you tag?
10. **Role and skill changes.** With §8, a change to a role, a skill or `roles.toml` reaches the
    running team only after a promotion and an update, like code. Is that what you want, or should
    those files (not the binaries) follow `dev` as soon as they merge?

## 11. Documentation changes (step 2)

- `CLAUDE.md`, "Rules that bite every agent": PRs target the project's delivery base (`dev`
  here); `main` takes promotions only; worktrees from `origin/dev`.
- `AGENTS.md`: the same line; then `agentbus mem sync` for its index copy.
- A decision note, `docs/memory/decisions/2026-11-16-dev-branch-flow.md` (active), linked from
  `docs/memory/INDEX.md`; `decisions/2026-11-02-forge-delivery.md` gains a line: delivery targets
  the project's base, promotion is master's.
- `roles/coder.md`, `coder2.md`, `master.md`, `skills/agent-bus/SKILL.md`,
  `skills/agent-bus-master/SKILL.md`: "from `origin/<base>`" and the is-ancestor check on the
  base; the master skill gains "Promotion" (§5) and its plan-drift line names the base.
- `health/health.go` (idle-implementer and plan-drift remediations), `health/idleimplementer.go`
  and `health/plandrift.go` (their texts to master): `origin/main` becomes the base.
- `docs/OPERATIONAL-GUIDE.md` and `.fr.md`: install and update build `main` (§8).
- An operator-note fragment per step, never `docs/operator-notes.md`.
