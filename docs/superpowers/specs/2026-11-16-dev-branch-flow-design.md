# Dev branch flow: PRs into dev, dev promoted to main after the release test

Status: spec, the person's answers in (§10, asked 2026-11-16; Q10 still open). Not built. First
written on origin/dev `e6d9cfcc` (origin/main `711ea8ef`, #556), merged as #570; revised on
origin/dev `510244c9` with the answers. Author: coder, task `dev-branch-flow`,
from master's directive of 2026-11-16 relaying the person's choice: **pull requests merge into
`dev`; `dev` is promoted to `main` only after the end-to-end release test passes.** One PR (this
spec); the build is §9's steps, each its own PR. The person's answers are §10.

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
- **Tags**: on `main` only, on the person's word (§6).
- **GitHub settings** (the person's, not code; §10 Q8): `main` is **protected** (no PR merges
  into it; only master's fast-forward push) and **stays the default branch**. So a pull request
  opened from outside the team targets `main` by default: master retargets it to `dev`
  (`gh pr edit <n> --base dev`, §4.3) before any review. `agentbus deliver` names its base
  explicitly and is not affected.

`main` never gets a commit of its own, so a promotion is a **fast-forward** of `main` to a `dev`
head: the commit that was tested is the commit that ships, sha for sha (no merge commit to
re-test).

## 4. Delivery to dev

### 4.1 `delivery.base`: the machine's, or the project's

`delivery.base` is **machine-wide by the person's own answer** (forge-delivery spec
2026-11-01, answer 1: "one machine-wide setting in `config.toml` … no per-project value"; the
registry marks it `MachineOnly`, so a project's services env file is never read for it). This
machine also runs projects that have no `dev` branch. **The person's answer (§10, Q2): per
project**, `dev` for this one, the others stay on `main`. So answer 1 is revised for the base only:
the project's template (`<projects-dir>/<project>.toml`) may set

```toml
[delivery]
base = "dev"
```

and `deliver` reads `--base`, then the project's `delivery.base`, then the machine's (default
`main`); `agentbus config --project <p>` says which applies and from where. The template, not
the services env file: the template is the person's (bootstrap writes it, `up` never rewrites
it), the env file is `up`'s. `delivery.forge` stays machine-wide. A project without the line
keeps `main`, as every project does today: nothing changes for them.

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

**master** prepares and executes it; **the person** decides (§10, Q3): master **proposes** each
promotion in For you (an ask: the `dev` sha, the PRs it carries, the check's outcome) and promotes
only on the person's yes. A promotion is a push to `main`, which only master and docs may do
(decision 2026-11-02); docs never promotes.

The order is: master runs the check (§5.2) on the `dev` head; it passes; master opens the ask; the
person answers yes; master pushes (§5.3). If `dev` moved meanwhile, the ask still names its own
sha: master promotes that sha (fast-forward, still an ancestor of `dev`), never a newer untested
one.

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

Any failure stops the promotion; nothing is pushed, and no ask is opened. **A failed promotion is
reported to master only** (§10, Q6), who fixes `dev` (a task to coder, a PR into `dev` like any
other) and runs the check again on the new head. It is never shown to the person in For you.

### 5.3 The act

The check passed on sha `S` and the person said yes: master pushes `S` to `main` **fast-forward only**
(`git push origin S:refs/heads/main`, refused by git if `main` is not an ancestor of `S`), then
reports to the person: the sha, the promoted PRs (`git log --first-parent origin/main..S` before the
push), and the check's lines (the test's PASS line, the suite's package count, mem check).

### 5.4 Its record

A promotion leaves a record master and busmon can read: `agentbus promote` (built in §9, step 4)
runs §5.2 and §5.3 and writes one ledger entry `{p}:promotions` (sha, from, the PRs, the check's
outcome and its PASS line, by, Redis TIME). Until that command exists, master runs the steps by
hand and the report is the record.

### 5.5 The switch (§10, Q1)

**Now**, not at the next batch: once the e2e release test (`TestReleaseEndToEnd`) passes on
`dev`'s head under §5.2, master proposes that first promotion in For you, and on the person's yes
pushes it to `main` and tags the promoted sha **v0.29.0** (the newest tag today is v0.28.0). Until
step 4 (`agentbus promote`) exists, master runs §5.2's commands by hand and the ask and the report
are the record (§5.4).

### 5.6 Hotfixes (§10, Q7)

**The default: a bug on a released `main` is fixed on `dev` and promoted**, like any change: a PR
into `dev`, then the check on everything merged meanwhile, then the promotion and a patch tag. It
is the one flow to know, and the release test runs on what actually ships.

For developers who prefer it, a **hotfix branch** flow may be switched on per project, in the same
template table as the base (§4.1):

```toml
[delivery]
base = "dev"
hotfix = "branch"   # default "dev": fix on dev and promote
```

With `hotfix = "branch"`, a fix may be branched as `hotfix/<x>` from `origin/main`, delivered
with `deliver --base main`, checked by §5.2 on its own head (a fast-forward of `main`, so `main`
gains no commit of its own), promoted and tagged, and then merged back into `dev` by a PR into
`dev` that master opens at once. With the default, `deliver --base main` is refused for that
project (`main takes promotions only: deliver to dev, or set [delivery] hotfix = "branch"`).
Building it is step 6, after the rest; until then only the default exists.

## 6. Tags and releases

- A tag is **on `main` only**, on a promoted sha, **on the person's word**: the promotion's ask may
  carry it ("promote and tag v0.29.0"), and master sets it then. **The promotion's check is
  enough to tag** (§10, Q9): no separate live check on the person's machine first. busmon's update
  check reads GitHub's latest release: tags stay where it looks.
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
base instead of `main`. A promotion is **not** a merge-queue item: it is master's ask in For you
(§5.1); a failed one never reaches For you (§5.2, Q6).

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
  owned by setup) at **the newest tag on `main`** (§10, Q4; §8.3): `update` runs
  `git fetch --tags origin main` and moves `.release` there, never touching the checkout's branch
  or files;
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
- both say what they build: `installing v0.29.0 (main, tag) in <repo>/.release`.
- `.release` is setup's and only setup's. It is a worktree of the same repository, so an agent's
  `git worktree prune` or `git worktree remove` could reach it: setup **locks** it
  (`git worktree lock --reason "agentbus release tree: setup owns it" <repo>/.release`), which
  both commands refuse to touch, and `install`/`update` re-lock it each run. The roles' worktree
  cleanup text (step 2) names it as never theirs.
- **What stays on the checkout**: `agentbus mem` (the notes the agents edit and review, read at the
  checkout's `docs/memory`), and every agent's own git work (their worktrees, branches, tests).
  Only what the team **runs** moves to `.release`: binaries, launcher, `roles.toml`, skills.

**The consequence, stated plainly:** a change to a role, a skill, `roles.toml` or the launcher that
merges into `dev` reaches the running team only after a promotion and an `update`, exactly as a
code change does. The agents keep working **on** the checkout (their worktrees from `origin/dev`);
they no longer run **from** it. Whether a role or skill change may skip that wait is the person's
(§10, Q10).

`.release` gets its `.gitignore` line, and every tree walker must skip it: the go tool already
skips dot directories (`./...`), and step 5 checks `internal/scrub`, `agentbus mem check` and the
backup's gather, adding the skip where one walks it. The untracked-files fix (build-id-untracked)
stays; building in `.release` also keeps the person's notes out of the build key for good.

### 8.2 Testing a branch on the machine: `--from`, in development mode

**`main` is the default** (§10, Q5): without a flag, `install` and `update` build the newest tag
on `main` (§8.3). **In development mode**, `setup.sh install --from <branch>` builds **any**
branch (`dev` or a feature branch, not only `dev`) in `.release` instead, says so in yellow, and
stamps the build so `agentbus version` and busmon's About read `<branch> <sha>` (never a release).
Outside development mode, `--from` is refused with the reason and the setting's name:
`--from needs development mode: set dev = true in config.toml (busmon: Settings, Services,
Development mode)`.

**How setup reads the setting.** Development mode is the registry's key `dev`
(`config/keys.go`, `AGENT_BUS_DEV`, top-level `dev = true`), the one busmon's Settings page shows
under Services as "Development mode". setup is the machine's installer, not a project's, so it
reads `dev` as a machine key: `config.Machine(getenv)`, i.e. `AGENT_BUS_DEV` in setup's own
environment, then `dev` in `config.toml`, then the default (off). A project's services env file
(`services/<p>.env`) is **not** read, even if it carries `AGENT_BUS_DEV`: it reaches that one
project's services, while a build replaces the binaries every project runs.
`--from` changes nothing else that `dev` gates (self-repair's shadow is unchanged).

The next plain `update` returns `.release` to the newest tag on `main`; `--from` is a one-time
choice, never remembered.

### 8.3 Which commit "the newest tag on `main`" is

`git describe --tags --abbrev=0 origin/main` after `git fetch --tags origin main`: the newest tag
reachable from `main`, so a tag on any other branch never installs. **Before `main` has a tag**
(no tag reachable), `install` and `update` build `origin/main`'s head and say so
(`main has no tag yet: installing origin/main <sha>`). With Q1's answer this lasts only until the
first promotion is tagged v0.29.0; tags v0.28.0 and older are already on `main`, so on this
repository the case is for other projects only.

## 9. Build order (each step its own PR, into dev)

1. **The base** (§4.1, §4.2): the template's `[delivery] base` and its resolution in `deliver`
   and `config --project`; missions take the project's base.
2. **Text**: roles, skills, `CLAUDE.md`, `AGENTS.md` (§11), the decision note.
3. **plan-drift and the merge queue** on the base (§7).
4. **`agentbus promote`** (§5): the check on a clean worktree, the gate's PASS proof, the ff push,
   the record; `--dry-run` runs the check and pushes nothing.
5. **setup from main** (§8): the `.release` worktree at the newest tag on `main` (§8.3), locked;
   `buildinfo.Repo` stamped to it, the skills linked from it, `up`'s one-time rewrite of the
   templates' paths, the message, `--from <branch>` gated on `dev` read as a machine key (§8.2),
   the `.gitignore` line and the walkers' skip.
6. **The hotfix setting** (§5.6): `[delivery] hotfix` in the template, `deliver --base main`
   refused unless it is `"branch"`.

Before step 1, master runs the switch's first promotion by hand (§5.5). Steps 1 and 2 make the
flow real by hand (master promotes with §5.2's commands, on the person's yes); 3 to 6 automate
and guard it.

## 10. The person's answers (asked 2026-11-16)

Relayed by master on 2026-11-16; each is applied in the section named.

1. **The switch's moment: now.** Promote after the e2e release test passes on `dev`'s head, and
   tag **v0.29.0** on the promoted `main` (§5.5).
2. **Per project.** `dev` for this project; the others stay on `main` (§4.1: the template's
   `[delivery] base`).
3. **master proposes each promotion in For you; the person answers** (§5.1).
4. **update installs the newest tag on `main`** (§8.1, §8.3).
5. **`main` is the default.** The existing `dev` setting (development mode), when on, makes
   `setup.sh install --from <branch>` available for **any** branch, not only `dev`; setup reads
   it as a machine key (§8.2).
6. **A failed promotion is reported to master**, who fixes `dev`; it is not shown in For you
   (§5.2).
7. **Hotfixes: by default fix on `dev` and promote** (good practice for a non-developer); a
   `hotfix/<x>` flow may be offered as a setting for developers who prefer it (§5.6).
8. **Protect `main`, keep `main` the default branch** (§3).
9. **The promotion's check is enough to tag** (§6).
10. **Still open: role and skill changes.** With §8, a change to a role, a skill or `roles.toml`
    reaches the running team only after a promotion and an update, like code. Is that what you
    want, or should those files (not the binaries) follow `dev` as soon as they merge? Until the
    answer, step 5 builds §8.1 as written (everything from `.release`); the other answer would keep
    `roles.toml`, `skills/` and the launcher read from a `dev` tree, a change to §8.1 only.

## 11. Documentation changes (step 2)

- `CLAUDE.md`, "Rules that bite every agent": PRs target the project's delivery base (`dev`
  here); `main` takes promotions only; worktrees from `origin/dev`.
- `AGENTS.md`: the same line; then `agentbus mem sync` for its index copy.
- A decision note, `docs/memory/decisions/2026-11-16-dev-branch-flow.md` (active), linked from
  `docs/memory/INDEX.md`; `decisions/2026-11-02-forge-delivery.md` gains a line: delivery targets
  the project's base, promotion is master's.
- `roles/coder.md`, `coder2.md`, `master.md`, `skills/agent-bus/SKILL.md`,
  `skills/agent-bus-master/SKILL.md`: "from `origin/<base>`" and the is-ancestor check on the
  base; `<repo>/.release` is never an agent's worktree to prune or remove; the master skill gains
  "Promotion" (§5: the check, the ask in For you, the push on yes, a failure kept to master,
  retargeting an outside PR from `main` to `dev`) and its plan-drift line names the base.
- `health/health.go` (idle-implementer and plan-drift remediations), `health/idleimplementer.go`
  and `health/plandrift.go` (their texts to master): `origin/main` becomes the base.
- `docs/OPERATIONAL-GUIDE.md` and `.fr.md`: install and update build `main` (§8).
- An operator-note fragment per step, never `docs/operator-notes.md`.
