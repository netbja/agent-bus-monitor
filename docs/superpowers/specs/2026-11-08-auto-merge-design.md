# Auto-merge: master merges a doubly approved PR after a window the person can cancel (auto-merge)

Status: spec, for review (subject `auto-merge`). Not built. v0.29. The person asked for it
("ok lance la spec", relayed by master, 2026-11-07). §12 holds the decisions only the person can
take; nothing is built before their answers.

Today every merge is the person's own act. `agentbus deliver` pushes and opens the request, and the
merge queue (`2026-11-05-merge-queue-design.md`) lists in For you what waits for their merge. This
spec lets the person hand **some** of those merges to master, under a setting they alone write. A
merge then happens only on a head that two independent reviewers approved, after a window in which
the person can stop it.

## 1. The setting

| Key | Values | Default | Who writes it |
|---|---|---|---|
| `delivery.automerge` | `off`, `docs`, `all` | `off` | the person only |
| `delivery.automerge_window` | a duration, at least `1m` | `10m` | the person only |

- **Both keys are `PersonOnly`** in the one registry, `config/keys.go`. That flag already exists for
  `backup.after_*`.
  - They are read from `config.toml` only, never from an env variable or a project's services env
    file (`MachineOnly`).
  - `agentbus config set` and `unset` refuse them: the person edits `config.toml` by hand.
  - So no agent can turn auto-merge on, widen its scope or shorten the window. Its tool rules
    already deny editing `config.toml` (`Edit(~/.config/agentbus/config.toml)`, `rolecfg/configdeny.go`), and the CLI refuses
    anyway.
- **`off`** is today: nothing merges by itself.
- **`docs`** auto-merges only a PR whose every changed path is documentation (§5.1).
- **`all`** auto-merges any PR not hit by a hard exclusion (§5.2).
- **An unreadable or invalid value reads `off`**, and health `config` says why. It never reads the
  default of a wider scope.
- **One machine-wide value**, as `delivery.forge` and `delivery.base`. A per-project override is out
  of scope (§11).

## 2. Who merges, and how

**Master merges, through the forge's CLI, by one command:** `agentbus automerge <pr>`.

- Never a reviewer, never the coder, never docs, never the dispatcher.
- **Who may run it**, checked by the command before any read, in this order:
  - **master holding the pilot lease**: `AGENT_BUS_AGENT` is `master` and `{p}:pilot` names it.
    `delivers = true` is not enough: docs has it too, and docs never merges (reviewer-codex);
  - **the person, outside the agents' panes**: no `AGENT_BUS_AGENT`, and on Linux no registered
    agent among the command's ancestors (`agentAncestor`, the check `agentbus notifications test`
    makes). The person needs no lease: they are not master;
  - anyone else is refused, with the reason.
- **These checks are defence in depth, not a boundary**, as the forge-delivery decision says of
  `deliver`: an identity on the bus is what the caller's environment states. The real guard is that
  the command merges **only an eligible PR** (§3), each condition re-read at that moment. So a
  process that passed the identity check wrongly could merge nothing that master could not, and
  nothing before the person's window ran out.
- **The command decides nothing new.** It recomputes every condition of §3 at that moment, from the
  ledger and the forge, then calls:

  ```
  gh pr merge <n> --merge --match-head-commit <H>
  ```

  `--match-head-commit` makes GitHub refuse the merge if the PR's head is no longer `H`. The race
  between the check and the merge is closed by the forge, not by us. The merge method is `--merge`,
  as the team's merges are today (`Merge pull request #N`); §12 Q3.
- **Who calls it.** The dispatch service's merge-queue chore (§4) sees an eligible item, opens its
  window, and when the window ends with nothing aborted, sends master a directive. It is the
  dispatcher's fixed wake line plus a bus entry, as an escalation is. The directive reads
  `auto-merge due: PR <n> head <H7> (<branch>)`. Master runs the command. The dispatcher never
  merges and never runs `gh pr merge`: it stays the one pane writer, typing only its wake line.
- **After the merge** the command reports to the person, with `report master --to <person>` and
  one line: `auto-merged PR <n> (<title>) at <H7>: approved by <r1> (<p1>) and <r2> (<p2>)`. The
  merge queue drops the item on its next pass, as for a merge by hand.

## 3. The condition: all of it, at merge time

A PR `n` with head `H` is **eligible** when every point holds. The chore checks it to open and to
keep the window; `agentbus automerge` checks it again just before `gh pr merge`.

1. **The setting allows it.** `delivery.automerge` is not `off`, and the PR's paths fit its scope
   (§5.1), with no hard exclusion (§5.2).
2. **A merge-queue item.** The PR is open, not a draft, and its head `H` is the deciding approved
   head of each subject it carries (merge-queue §2). So the roll-up is `APPROVED`, and no reject
   came after the approve.
3. **Two independent approves of `H`.** Among the item's independent approves naming `H`
   (`approvalsOn`, as `deliver` reads them), there are two verdicts that satisfy all of these:
   - from **two different reviewers**, neither of them the author;
   - **one of them `foureyes`**, the role the team reviews with;
   - from **two different makers** (the company whose model reviewed), each known (below);
   - with **no `reject` on any of the item's subjects after them**.

   **The maker of a verdict.** A verdict records `ReviewerProvider` today, from the reviewer's
   snapshot (`verdictProviders`, `cmd/agentbus/verdicts.go`). A provider is not a maker: `agy`
   (provider `google`) serves `claude-*` (maker anthropic) and `gpt-*` (maker openai) too
   (`models/readers.go`, `Row.Family`; foureyes). So:
   - The verdict gains an additive field, **`reviewer_model`**: the snapshot's `Model` (the
     launcher's `AGENT_BUS_MODEL`), written beside the provider, empty when unknown. The same goes
     for `author_model`.
   - **`models.MakerOf(provider, model)`** is the maker:
     - a provider that serves only its own models (anthropic, openai, moonshot, xai) is its own
       maker, whatever the model;
     - a provider that serves several makers (google through `agy`) takes the maker from the
       model's `Family`, by its prefix rule, so `claude-*` is anthropic;
     - with no model recorded, or a model the table does not know, the maker is **unknown**.
   - A verdict whose maker is unknown **never counts** toward the two. That includes every verdict
     written before this field. Two verdicts of one maker are one review: "independent" means
     another maker's model, not another pane or another provider.
4. **The head is unchanged.** The forge's head for `n` is `H`, read in the same pass. The merge
   call then carries `--match-head-commit H`.
5. **Not stacked.** The PR's base is `delivery.base`. A PR into any other branch never
   auto-merges: it is the PR 342 incident, a merge into `feat/busmon-queue-strip` that main never
   saw (§5.3).
6. **Mergeable.** The forge reports the PR mergeable (`mergeable: MERGEABLE`, not `CONFLICTING`;
   `mergeStateStatus` not `BLOCKED`, `DIRTY` or `BEHIND` where the base requires up to date).
7. **Checks green.** Every check the forge reports on `H` has passed (`gh pr checks <n>`). A pending
   check waits; a failed or cancelled one blocks. With **no check reported at all**, which is this
   repository today (no workflow), §12 Q2 decides. The recommendation: allowed only when the base
   branch requires no check and none exists, because the reviewers' runs of the full suite are then
   the only proof.
8. **No review still open on the item's subjects.** No gate is open whose subject
   (`ParseGateMeta(...).Subject`) is one of the item's subjects, on any agent: a security review, a
   third reviewer, or a challenge still waiting for its verdict all hold the merge, even with two
   approves (foureyes). A gate whose meta carries no subject does not match. A stale gate is closed
   by `agentbus gate close`, as always, never by auto-merge.
9. **The window ran out, unaborted, on this head** (§4). No abort names `n` at `H`.
10. **The bus reads.** The ledger, the gates, the abort entries, the setting and the forge were all
   read in the pass. Any read that fails makes the PR **not eligible**. Unknown is never "go", as merge-queue
   §3d: unknown is never "nothing waits".

`agentbus automerge <pr>` prints the first point that fails, in plain words (`not eligible: only
one provider approved 3f2a1c9 (anthropic)`), and exits 3. It never merges on a partial read.

## 4. The window and the abort

- **Opening.** When the merge-queue chore first finds a PR eligible on every point but 9, it opens
  the PR's window. It writes `{p}:automerge` (a hash, fenced like the queue record, runtime class,
  no TTL beyond 7 days) with the field `<n>` = `{"head": H, "opened_at": <Redis TIME ms>, "due_at":
  opened_at + window}`. The window is the setting's value at opening; a later change to the setting
  applies to the next window.
- **A new head is a new window.** A push to the PR leaves the merge queue. Back on a new approved
  head, the PR gets a new window, never the remainder of the old one.
- **Closing without a merge.** A PR that stops being eligible, for any reason, loses its field on
  the next pass. If it was announced, a busmon notice says why (`auto-merge of PR 351 cancelled:
  a new push`).
- **The abort is the person's.** It is a bus entry, `cmd automerge-abort` from the person, with
  `ref` = `<n>` and `head` = `H`, written by busmon (the For you row's action, §7) or by `agentbus
  automerge abort <pr>`.
  - It is the audit: `agentbus history` shows it, the ledger's way.
  - It binds to the head, so a later head is not aborted by it.
  - **Who may abort.** `agentbus automerge abort` runs only for the person outside the agents'
    panes, detected as for the merge (§2): no `AGENT_BUS_AGENT`, and no registered agent among its
    ancestors on Linux. busmon is the person's tool and refuses when it runs as an agent, by the
    same two checks. The entry's sender is the configured person (`identity.as`).
  - **Defence in depth, not a boundary.** `identity.as` can be set from the environment
    (`AGENT_BUS_HUMAN`), so an agent could write an abort entry that names the person (foureyes).
    The chore cannot tell. An abort whose sender is an agent's name is ignored, and health
    `automerge` warns. **A forged abort can only stop a merge**: the PR stays in the merge queue for
    the person, the fail-safe direction. Nothing an abort entry says can make a PR merge.
- **Notice.** On opening, the person is told on every surface they already read:
  - busmon's For you merge row turns into an auto-merge row (§7);
  - a notification goes through the existing curated path (`notify`, kind `automerge`, summary
    privacy), saying `PR 351 auto-merges in 10 min (abort in busmon, or agentbus automerge abort
    351)`. It follows notify's rules (on, targets, quiet), and ringing is not a condition: an
    auto-merge does not wait for a notification to be delivered.
- **Due.** At `due_at` the chore re-checks points 1–10 and, if all hold, sends master the directive
  (§2). If master has not merged after 30 minutes, the chore sends it again once, and busmon's
  row reads `auto-merge overdue: master has not merged`. No merge happens without master.

## 5. Scope and exclusions

### 5.1 `docs`

Every changed path of the PR, as the forge lists them (`gh pr view <n> --json files`) and compared
with `git diff --name-only <base>...H` when the repository is at hand, matches one of these:

- under `docs/`;
- a Markdown file anywhere (`*.md`).

…and none matches §5.2. A PR whose file list the forge truncates (more than its page) is out of
scope: a list we cannot read whole is not a docs-only list. Renames count by both paths.

### 5.2 Hard exclusions: never auto-merged, whatever the scope

A PR touching any of these paths waits for the person, even under `all`:

| What | Paths (repository-relative) |
|---|---|
| the merge rules themselves and this setting | `automerge/**` and every module package it depends on, the command files feeding it (below), `cmd/agentbus/automerge*.go`, `cmd/agentbus/mergequeue*.go`, `dispatch/**`, `docs/superpowers/specs/*auto-merge*`, `docs/memory/decisions/**` |
| who may do what | `roles.toml`, `roles/**`, `rolecfg/**`, `skills/**`, `AGENTS.md`, `CLAUDE.md`, `.claude/**` |
| release, tag, reinstall | `setup.sh`, `herdr-plugin.toml`, `CHANGELOG.md`, `docs/releases/**`, `install*`, `Makefile` |
| supply chain and CI | `go.mod`, `go.sum`, `.github/**`, `docker-compose.yml`, `Dockerfile*` |
| security-sensitive | `SECURITY.md`, `health/**` secret readers (`health/secrets*.go`), `notify/**`, `backup/**`, any path whose name holds `secret`, `token`, `credential`, `password` or `.env` |

- **The first row is defined by dependency, not by hand** (foureyes). The eligibility and the merge
  live in one package, `automerge/` (`Eligible`, `HardExcluded`, the scope, the merge call), and
  `cmd/agentbus/automerge*.go` is a thin command over it. Every package of this module that
  `automerge/` depends on (`go list -deps ./automerge`, module packages only) is excluded whole.
  The expected set is `bus/**`, `config/**`, `forge/**`, `models/**` and `mergequeue/**`; the test
  below, not this list, is the authority.
  - So are the files outside it that feed its facts:
    - `cmd/agentbus/verdicts.go` and `cmd/agentbus/main.go`, which record verdicts and their
      provider and model;
    - `cmd/agentbus/reviewers.go`;
    - `cmd/agentbus/identityguard.go` and `cmd/agentbus/notificationscmd.go`, which detect an
      agent caller;
    - `cmd/agentbus/deliver*.go`;
    - `dispatch/**`, the chore's runner, its fence and Redis `TIME` behind the window.
  - **A test enforces it.** `TestAutomergeDependenciesAreExcluded` runs `go list -deps ./automerge`,
    and fails when a module package it lists, or a file named above, is not excluded by
    `HardExcluded`. A new dependency then cannot slip out of the list.
- Matching is on the path, before and after a rename.
- The list lives in one place, `automerge.HardExcluded(path) (bool, why string)`, inside an excluded
  package.
- A label `no-automerge` on the PR also excludes it. Master sets it when an ask is pending on the
  PR, and the person can set it on the forge.
- **Never an act beyond the merge.** `agentbus automerge` merges and reports; it never tags,
  releases, reinstalls binaries, deletes the branch or touches another PR. A release stays the
  person's, as the release checklist says.

### 5.3 Stacked PRs are forbidden

PR 342 (`feat/busmon-triage` → `feat/busmon-queue-strip`) was merged 14 s after its base branch
had merged to main. The triage never reached main until PR 343. The rule (§3.5): only a PR whose
base is `delivery.base` auto-merges. The merge queue already shows a PR into another branch. With
auto-merge on, busmon's merge page marks it `stacked: base feat/… is not main, never auto-merged`.
Master delivers one branch from main per step (master's directive, 2026-11-07).

## 6. How it composes with forge delivery

The forge-delivery decision (`docs/memory/decisions/2026-11-02-forge-delivery.md`) says: "The
person keeps the merge: deliver never merges, never touches the base." This spec does not change
`deliver`. It adds a second, separate act:

- **`deliver` is unchanged.** It pushes `H` only on a newest independent approve naming `H`, then
  opens the request. It still never merges.
- **`automerge` is the one merging act, and the person opts into it** with `delivery.automerge`.
  With `off`, the decision holds word for word. With `docs` or `all`, the decision's clause "the
  person keeps the merge" becomes "the person keeps the merge, except what they hand to master by
  this setting, each time with a window to stop it".
  - On the person's go, a new decision note records it. Its first line: "Auto-merge: the person
    hands merges to master by a setting only they write".
  - The forge-delivery note gets one line pointing to it.
- **The same proof, stronger.** `deliver` needs one independent approve of `H`. `automerge` needs
  two, on two providers, one of them foureyes.
- **A narrower caller check than `deliver`'s.** `deliver` accepts any role with `delivers = true`.
  `automerge` accepts master with the pilot lease, or the person (§2). Roles' rules echo it:
  - `Bash(agentbus automerge *)` is allowed in master's own rules;
  - it is denied in every other role's own rules, docs included, with the same spellings after
    `--project` that `deliver`'s deny uses.

  `agentbus automerge abort` is the person's. The command refuses an agent caller (§4), so no rule
  needs to allow it.
- **Defence in depth, not a boundary**, as there: the bus is no boundary against the local user.
  The merge's real guard is the forge's `--match-head-commit` and the conditions read at merge time.

## 7. busmon

- **For you.** An eligible PR inside its window stays a merge row (`rankMerge`, the strip's `merge`
  chip). Its text gains the countdown:

  ```
  ◆ merge  PR 351  docs: operator notes for services   auto in 7m
  ```

  - Enter opens the merge page.
  - `x` on the row is **abort**, with a confirmation (`abort auto-merge of PR 351? y/n`). It is
    bound in For you only, on a row whose key is `merge:<n>` with an open window. `x` is free in
    ModeShell, and `TestNoKeyCollisionPerMode` holds it.
  - `rowHint` keeps `PR` for Enter. The footer adds `x abort` while such a row is selected.
- **The merge page** gains an `Auto-merge` block:
  - `in 7m (due 21:40)`, then `abort: x`;
  - or the first point of §3 that fails (`not eligible: checks pending`);
  - or `off (delivery.automerge)`.

  Each approval line names its provider and maker, which the page reads from the queue record (the
  additive fields `provider` and `maker` on `MergeApproval`).
- **Overdue.** Past `due_at` + 30 min, the row reads `auto-merge overdue` and is a decision, as any
  merge row is.
- **Settings page.** Two rows beside `delivery.forge` and `delivery.base`:
  - `delivery.automerge  off` and `delivery.automerge_window  10m`;
  - each a `PersonOnly` row, as `backup.after_*` are: its help reads `your own setting: edit
    config.toml yourself`, and Enter says so instead of editing.

  The Settings goldens show them.
- **Header.** Unchanged: the `merge` chip counts these rows as it counts every merge row.

## 8. CLI and health

- `agentbus automerge <pr>`: master's act (§2, §3). It exits 0 on a merge, 3 when not eligible
  (the reason printed), and 1 on a forge or read error.
- `agentbus automerge status [--json]`: the open windows, their due times, and why each other
  queue item is not eligible. Read-only, for any role.
- `agentbus automerge abort <pr>`: the person's abort (§4).
- `agentbus merge-queue` gains an `auto` column (`in 7m`, `—`, or the reason).
- **Health `automerge`** (only when the setting is not `off`):
  - warn when a window is overdue (master did not merge);
  - warn when an abort entry from an agent identity was ignored;
  - unknown when `{p}:automerge` cannot be read;
  - otherwise ok.

## 9. Failure modes

| What happens | What auto-merge does |
|---|---|
| a push during the window | the PR leaves the queue; the window closes; a new approved head opens a new one |
| a reject after the approves | roll-up `REJECTED`: not eligible; the window closes |
| checks pending at `due_at` | stays open, not merged; re-checked each pass; `waiting: checks pending` |
| a check fails | not eligible; the window closes; the notice names the check |
| conflict, or behind a base that requires up to date | not eligible (§3.6); master's work, as today |
| the forge unreachable, `gh` missing or unauthenticated | not eligible (unknown); the record keeps its windows; nothing merges |
| the ledger or `{p}:automerge` unreadable | not eligible; health `automerge` unknown |
| an abort arrives while master runs the command | the command reads aborts as its last step before `gh pr merge`; an abort written after that call starts is too late, and the report says `merged before the abort` |
| `--match-head-commit` refused by the forge | nothing merged; the command says so; the next pass re-derives |
| the setting turned `off` during a window | not eligible at the next check; the window closes |
| two masters (a stale pane) | only master holding the pilot lease may run `automerge` (or the person, §2): a check of its own, which `deliver` does not make |
| the dispatcher down | no window opens, no directive; nothing merges by itself; the merge queue reads unknown, as today |
| master's merge refused by the forge (permission) | exit 1 with the forge's message; the window stays overdue; health warns; the person merges by hand |

## 10. Tests

- **Eligibility (pure, `mergequeue.Eligible`).** One fixture per point of §3, each failing alone:
  - the setting off, out of scope, excluded;
  - not in the queue;
  - one approve; two approves from the same reviewer; two from one provider;
  - two from two providers of one maker: foureyes (anthropic) and a reviewer on `agy` running
    `claude-*`, which counts once;
  - an `agy` verdict with no model recorded (unknown, never counts); a verdict from before the
    field;
  - no foureyes among the two; a reject after;
  - an open gate on one of the item's subjects (a security review), and a gate on another subject
    (does not hold);
  - the head moved; the base not `delivery.base`; conflicting;
  - a check pending, failed, none reported (both answers of Q2);
  - the window not run out; an abort on `H`; an abort on an older head (does not count);
  - each read failing.
  Then the one fixture where all hold.
- **Scope.** `docs/x.md`, `README.md` and `docs/img.png` are docs. `cmd/x.go` is not. A rename from
  `cmd/a.go` to `docs/a.md` is not. A truncated file list is out. Every row of §5.2 is excluded
  under `all`, and `HardExcluded`'s own file is too.
- **The command.** With fake `gh` on `PATH` (as the merge-queue tests do):
  - `--match-head-commit H` is in the argv;
  - nothing is called on any non-eligible fixture;
  - `--match-head-commit H` is the one merge call;
  - every caller is refused before any call except master with the lease and the person: docs
    (`delivers = true`), master without the lease, an agent ancestor;
  - an agent's `automerge abort` is refused; an abort entry from an agent's name is ignored by
    the chore and warned about;
  - the report names both reviewers and providers.
- **The window (Redis, a disposable broker only).**
  - Opening is fenced and uses Redis `TIME`.
  - A new head opens a new window.
  - Closing removes the field.
  - The due directive goes to master once, then once more after 30 minutes.
- **Settings.** Both keys are `PersonOnly`: `config set` refuses them, and an env value is ignored.
  An invalid value reads `off`, and health `config` says so. The registry test (no literal env
  name) holds.
- **busmon.**
  - The For you row with its countdown; `x` with its confirmation writes one abort entry from the
    person.
  - The merge page's block; the Settings rows.
  - Goldens: For you with an auto row (wide, medium, narrow), the merge page, Settings' Delivery
    group. `TestNoKeyCollisionPerMode`; F1 lists `x`.

## 11. Steps, and out of scope

1. **The derivation and the setting:** the keys; the `automerge/` package with `Eligible`,
   `HardExcluded` and the scope, and its dependency test; `reviewer_model` and `author_model` on the
   verdict and `models.MakerOf`; the provider and maker on `MergeApproval`; `agentbus automerge
   status`. Nothing merges yet.
2. **The window and the abort:** `{p}:automerge`, the abort entry and command, the notice, and
   busmon's row, `x` and page.
3. **The merge:** `agentbus automerge <pr>`, the directive to master, the rules and the decision
   note, after the person's answer to Q1.

**Out of scope:**
- GitLab and Bitbucket (their CLIs' head-match flags are a later step; under them auto-merge reads
  `unsupported forge` and never merges);
- a per-project scope;
- squash or rebase merges (Q3);
- merging several PRs as one;
- deleting branches after a merge;
- the forge's own review as the gate.

## 12. Decisions the person must take

1. **The credential master merges with.** What was measured on 2026-11-08: `main` has no branch
   protection and no ruleset, and the `gh` login on this machine is the person's account
   (`netbja`), with the `repo` scope. So GitHub's branch rules do not block a merge today. The
   refusal master met must come from elsewhere. Before step 3, master states its exact message
   (its client's permission prompt, or the forge's error). The options:
   - **A. The person's own `gh` login**, as `deliver` pushes with the person's Git credentials.
     Nothing to set up. The merge is recorded as the person's, and master's client must be allowed
     `agentbus automerge` (not raw `gh pr merge`).
   - **B. A fine-grained token for this repository only** (Contents: read and write; Pull requests:
     read and write; Metadata: read), made by the person and expiring.
     - It is stored in `~/.config/agentbus/forge.env` (0600, in a 0700 folder), read only by
       `agentbus automerge` and handed to `gh` as `GH_TOKEN` for that one call.
     - Every role denies reading the file, as `notify.toml`.
     - The merge is still recorded as the person, but the token can do nothing outside this
       repository and the person can revoke it alone.
   - **C. A machine account or GitHub App** with write access to the repository. Merges are
     recorded as the bot, which is the best audit. It is also the most setup: an account or an
     app, its key, its installation.
   - **D. GitHub's own auto-merge.** `automerge` calls `gh pr merge --auto --match-head-commit H`
     at `due_at`, and GitHub merges when the required checks pass. It needs branch protection with
     required checks, which this repository has none of, and still a credential to enable it (A or
     B).

   Recommended: **B**. It is narrow, revocable and recorded, and needs no new account. A is
   acceptable while auto-merge is `docs` only.
2. **No check reported.** This repository has no CI workflow, so no PR has a check. Recommended:
   allow the merge when the base requires no check and none is reported. The two reviewers' full
   runs are the proof. Or require at least one green check, which means adding CI first.
3. **The merge method.** Recommended: a merge commit (`--merge`), as today. Or squash.
4. **Decision notes under `docs`.** `docs/memory/decisions/**` is excluded (§5.2) because a decision
   is the person's. Recommended: keep it excluded. Or allow decision notes under `docs`.
5. **The window.** Recommended: 10 minutes, and at least 1 minute. Should a window that ends
   between 22:00 and 08:00 wait for the morning? Recommended: no; the setting is the person's
   choice, and the abort stays open until the merge.
