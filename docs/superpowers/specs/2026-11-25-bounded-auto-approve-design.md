# Bounded auto-approve (bounded-auto)

Status: **decided spec r2** (the person's answers to r1's §5), for review (subject
`bounded-auto-spec-r2`). Not built. Base: origin/dev `535e50d6`. Author: architect, from master's
directive (cmd `1795606012814-0`). r1 is PR #771 (merged, revisions r1-r5 there, idea 2 of 4 from
the person's note "ideas borrowed from PiBot", item 3; reference PiBot
`packages/policy/src/core/yolo.ts`, Apache-2.0, idea only). No new dependency.

**The person's answers** (relayed by master):

- **Q1 A:** build §3.1, bounded relaxations.
- **Q2 A:** the danger list is fixed in code, with the person's additions (`policy.danger_extra`).
- **Q3, changed:** `roles doctor` lists every permission a client saved, read-only, with the file
  and line to remove. **And** a launch is refused while a saved permission matches the danger list
  (built-in and `danger_extra`), unless a bounded waiver covers it, under the same rules as a
  relaxation: at most 30 days, bound to the settings, written only by the person (§3.3).
- **Q4 A, with a requirement:** the person turns on branch protection for `main` at the forge (a
  PR required, no force push, no deletion), `roles doctor` warns when it is off, on every
  supported forge, and master's promotion and a person-run promotion keep working (§3.4).

r2 changes only what those answers decide: §3.1 is decided, §3.3 to §3.5 are new, the build steps
are §5, and §6 holds the one choice left to the person.

## 1. Today's auto-approve switches

| Switch | Where | Scope |
|---|---|---|
| A client's own auto mode: Claude `auto`, `bypassPermissions` or `dontAsk`, Codex `approval=never`, Kimi `yolo` or `auto`, Grok `bypassPermissions`, agy `skip` or `auto` | the profile in `roles.toml`. `elevated` (`rolecfg/launch.go:1018`) names all of them except Claude `auto`, plus Claude allow rules and Codex's full-access sandbox | one role's session |
| The role's tool rules (`allow`, `ask`, `deny`) | `roles.toml [tool_rules]` and per role; written per launch with `--settings` (`rolecfg/launch.go:696`) | one role's session |
| One approval for a small change | `delivery.single_approval`, `delivery.same_provider_ok`, both PersonOnly (`config/keys.go:674`) | one PR |
| Automatic update | `updates.auto`, PersonOnly | the machine |

Every Claude Code role but security and infra now runs in `auto` (#764). The shared denies
"apply to every role, whatever its permission mode" (`roles.toml`, header). That holds for
Claude Code. Nothing checks it for the other clients.

## 2. The idea: four rules every auto-approve obeys

1. **One scope.** An auto-approve names what it covers: one role's session, one task or one
   PR. Never a whole project, never every project.
2. **`ask` may become `allow`; `deny` never does.** A deny rule holds under every auto mode.
3. **Never the danger class.** A fixed list, written in the code and not in a setting:
   - force push (`git push --force`, `-f`, `--force-with-lease`);
   - deleting a branch, local or remote (`git branch -D`, `git push --delete`, `git push <r> :<b>`);
   - a merge or push to the default branch, or to `main` (master's promotion stays the person's
     yes, decision 2026-11-16);
   - reinstalling the binaries (`./setup.sh`, `go install` into the installed path);
   - spending: any command the person marks as paying.

   Each one is an `ask` in every role, whatever its mode. **The person approves a danger act
   at the client's prompt in the agent's pane** (prompt-notify rings). That is the one
   approval this spec relies on. The person may add entries (`policy.danger_extra`,
   PersonOnly, additions only; Q2 A). No setting removes one. `agentbus promote` is on the list
   (§3.4).

   **What is enforced, and what is not.** The prompt holds only where the client honours an
   ask rule in its mode, which the launch checks (§3). Even then, an ask rule matches the
   command's text, so a command that is spelled differently, or run inside a script or a test,
   is not caught. That is defence in depth, not a boundary. The only boundaries outside the
   client are these:
   - a push or merge to `main`: the forge's branch protection, which the person sets (§3.4), with
     one bypass for the promotion identity, and the prompt on `agentbus promote`;
   - everything else on the list (a force push to a work branch, a branch delete, a reinstall,
     spending): the client's prompt only.
   This spec claims no more than that.
4. **Never persistent.** An auto-approve is never written where it outlives its scope. agentbus
   writes rules only into the launch's own `--settings`, never into `~/.claude/settings.json`,
   `~/.codex/config.toml`, or a project's `.claude/settings.local.json`.

## 3. What the build does

- **The danger class in the shared rules.** The launch adds the §2.3 list, built-in entries and
  `policy.danger_extra`, to every role's `ask` rules. The built-in entries are one Go table
  (`rolecfg`), so `roles.toml` cannot drop them. An explicit ask rule still prompts in Claude's
  `auto` mode (`roles.toml`, header), and prompt-notify rings the person. These are text matches:
  defence in depth (§2.3).
- **The danger asks and the denies must hold, or the launch is refused.** One Go table,
  `rulesHold(profile)`, judges the effective profile and not only its mode name. Rules hold only
  where agentbus writes them and the client enforces them per command, which is Claude Code in
  `manual`, `acceptEdits` or `auto` (an explicit ask still prompts and a deny blocks, as the
  `roles.toml` header documents). Claude `dontAsk` holds too, because an ask becomes a refusal,
  which is stricter. Everything else does not hold:
  - a client for which agentbus writes no per-command rules: Codex, Kimi and Grok. agy keeps only
    the denies `agyDeny` can translate (`rolecfg/agy.go:62`), so it does not hold either.
    Whatever their mode, a sandbox is not a rule. Inside it a command runs without asking, a
    deny-matched read of `.env` included. A sandbox that allows writes or the network lets a
    danger act run inside it;
  - an escalation approved by anyone but the person: Codex `reviewer=auto_review`;
  - Claude `bypassPermissions`, and every non-Claude mode on the §1 list (`approval=never`,
    `yolo`, `skip`, …). Any client or mode the table does not know does not hold either.

  A profile that does not hold is **refused at launch, for every role**, unless a relaxation
  covers it (§3.1). **Today this concerns three roles:** reviewer-codex (`codex-review`: a
  read-only sandbox, but `reviewer = "auto_review"`), ui-kimi (`kimi`) and ui-grok (`grok`).
- **Two reviews are a review gate, not the person's approval.** `agentbus deliver` stops offering
  `single_approval` and `same_provider_ok` for a PR whose base is the default branch. Such a PR
  then needs both reviews, which is a check on the code. The person's approval of the act stays
  the prompt.

### 3.1 Bounded relaxations (decided, Q1 A)

A relaxation lets one role run on a profile whose rules do not hold. It is not part of the policy,
and complies with nothing above. It is bounded:

- **One entry per role and profile,** in `config.toml`, written only by the person (§3.5).
- **Bound to the exact effective profile:** the entry holds a fingerprint of its client, model,
  sandbox, approval, reviewer, network and other options, and of the role's **merged** tool
  rules: the shared denies, the danger list (`danger_extra` included) and the role's own rules.
  Any change voids the entry, and the launch is refused until the person renews it. So an
  agentbus update that changes the shared rules voids every relaxation, and the person looks at
  each one again. That is intended.
- **It expires:** at most 30 days, with no default. Afterwards the launch is refused again.
- **It claims no enforcement, not even of itself.** The relaxed role is one whose rules do not
  hold. As the same OS user, it can write `config.toml` and change its own entry, expiry and
  fingerprint included. A role is kept from that file only by a Claude Code deny
  (`Edit(~/.config/agentbus/config.toml)`, `roles.toml:161`, whose comment says a shell can still
  write it) and by `config set`'s PersonOnly refusal (`config/write.go:72`). The expiry and the
  fingerprint bind the person's honest configuration, not the relaxed agent. `roles doctor`,
  busmon and the launch line show the role as `rules not held`, with the denies it loses, the
  danger acts it can run unprompted, the expiry date, and this line: "the relaxed agent could
  extend this itself".

### 3.2 A later route for Codex (not in this build)

Codex enforces per-command rules of its own: `prefix_rule` entries with `decision = "forbidden"`
in the rules files under `CODEX_HOME`. A launch could write them into a per-launch `CODEX_HOME`,
as it writes `--settings` for Claude. They match commands, not file reads, so `Read(**/.env)`
would still not hold. They could make the danger asks and the `Bash(...)` denies hold for
`codex-review` without breaking rule 4. That is a later spec.

### 3.3 Saved permissions: listed, and refused on the danger list (Q3)

A person who answers "don't ask again" in a pane makes the client write an allow into its own
files. agentbus **never edits those files**.

- **Where it reads.** One Go table per client names the files that hold saved permissions:
  Claude Code's `~/.claude/settings.json`, `~/.claude/settings.local.json`, and the worktree's
  `.claude/settings.json` and `.claude/settings.local.json` (`permissions.allow`); Codex's rules
  files under `CODEX_HOME` (`prefix_rule` with `decision = "allow"`). Kimi, Grok and agy are added
  once their files are known; until then `roles doctor` says `saved permissions: not inspected`
  for them, never `none`.
- **`roles doctor` lists them all,** per role: the file, the line, the rule, and which danger
  entries it matches. It prints what to remove and removes nothing.
- **A match refuses the launch.** A saved allow matches a danger entry when it could allow a
  command of that entry: a bare tool (`Bash`, `Bash(*)`), or a pattern whose literal part before
  the first wildcard is a prefix of the entry's command, or the reverse (`Bash(git push:*)` and
  `Bash(git *)` both match `git push --force`; `Bash(git push origin feat-x)` does not). The
  match leans toward a refusal: a false match costs the person a removal or a waiver, a missed
  one costs nothing new. A user-level file concerns every role on that client; a worktree file
  concerns the roles launched there. The refusal names the file, the line and the entry.
- **What it adds, honestly.** In Claude Code the order is deny, then ask, then allow, and "a
  matching ask rule prompts even when a more specific allow rule also matches" (Claude Code
  permissions doc). So for a Claude role a saved allow does not remove the danger prompt today.
  The refusal guards a client whose order differs, a relaxed role, and a future change of that
  order. It is the person's decision, and it is cheap.
- **A waiver** lets one role launch despite one saved rule: bound to the role, the file and the
  rule's exact text, and to the same fingerprint as a relaxation, at most 30 days, written only
  by the person (§3.5). A changed rule text, file or profile voids it. `roles doctor` shows each
  waiver with its expiry, and the same line as a relaxation: a role that can write `config.toml`
  could extend it itself.

### 3.4 Branch protection on `main`, and how promotion keeps working (Q4)

**What the person turns on.** On `main`: a pull request required, no force push, no deletion.
Today the repository has none (`gh api repos/<o>/<r>/branches/main/protection` answers "Branch not
protected").

**The problem.** Master promotes by a fast-forward push of a tested `dev` head `S`: `git push
origin S:refs/heads/main` (agent-bus-master skill, "Promotion"). A PR-required rule refuses that
push. A forge merge does not replace it on GitHub: its three merge methods all write new commits
(a merge commit, a squash, or a rebase that "creates new commit SHAs", GitHub docs, "About merge
methods"). Then `main` no longer holds the sha the release check ran on, and the tag lands on a
commit no promotion carried, which the dev branch flow decision forbids.

**The route: one promotion identity with a bypass of the PR rule only** (§6 Q5 A). The force-push
and deletion rules have no bypass, for anyone.

| Forge | Setting the person makes |
|---|---|
| GitHub | Two rulesets on `main`. "main-safety": block force pushes, restrict deletions, **no bypass actors**. "main-pr": require a pull request, with one bypass actor, a deploy key `agentbus-promote` with write access (`bypass_mode: always`). A ruleset's bypass covers only its own rules, so the key can push a fast-forward and nothing else. |
| GitLab | `main` protected: allowed to merge, Maintainers; allowed to push and merge, the deploy key `agentbus-promote` only; allowed to force push, off. A protected branch is not deleted by a push. The merge method may be fast-forward only. |
| Bitbucket Cloud | Branch restrictions on `main`: require a pull request; `push`, exempting only the promotion user; `force` and `delete`, with no exemption. Bitbucket exempts users, not keys, so the identity is a dedicated user. |

**Who uses the identity.** The promotion key, `~/.config/agentbus/promote.key` (0600), is read
only by `agentbus promote` (dev branch flow, step 4), through `GIT_SSH_COMMAND`. Every Claude role
gets a `Read` deny on it, as for `notify.toml`. `agentbus promote <S>` checks that `S` is an
ancestor of `origin/dev`, that `origin/main` is an ancestor of `S`, and that the check's PASS
record names `S`. It then pushes `S:refs/heads/main` with the key, and nothing else: no tag, no
other ref, no `--force`.

- **Master's promotion.** The ask in For you stays the proposal: the sha, the PRs, the check's
  lines. The enforced approval is the danger prompt: `agentbus promote` is on the danger list, so
  master's pane prompts and prompt-notify rings. One approval in the pane; the ask carries the
  evidence. The tag is pushed as today, on the person's word (a tag is not `main`).
- **A person-run promotion.** The person runs `agentbus promote <S>` in their own shell. It runs
  the same checks and the same push. No prompt and no ask: the person is not an agent. The forge's
  merge button is not a promotion on GitHub (it writes a new commit); `roles doctor` says so when
  `main` holds a commit that is not on `dev`.
- **The merge queue** delivers into `dev` (`bus/mergequeue.go`, the PR's base). Protection on
  `main` does not touch it. An outside PR that targets `main` is still retargeted to `dev`.

**The limit.** The key is a file of the person's user. Any process of that user can read it, so
a role can push a fast-forward of `main` with it: through a shell for a Claude role, through any
command for a role whose rules do not hold. The deny and the danger prompt are defence in depth.
The forge still refuses a force push and a deletion from everyone, and a push that is not a
fast-forward from the key. Idea 3's sandbox keeps a sandboxed role away from the file.

**`roles doctor` checks the forge,** for the project's `delivery.forge`, with the credentials the
forge CLI already has:

- GitHub: `GET /repos/{o}/{r}/rules/branches/main` (the effective rules) and the rulesets'
  `bypass_actors`;
- GitLab: `GET /projects/:id/protected_branches/main` (`allow_force_push`, the push and merge
  access levels);
- Bitbucket Cloud: `GET /2.0/repositories/{w}/{r}/branch-restrictions`, by `kind`.

It warns when the PR rule is off, a force push or a deletion is allowed, a bypass covers the force
or deletion rule, or the PR rule's bypass names anyone but the promotion identity. A forge it
cannot read is `unknown`, also a warning. `local` and `none` say `no forge protection`. The exact
field names are checked against each forge's live API in step 5's first commit.

### 3.5 Who writes a relaxation or a waiver

Only the person, by the person's own act at the time:

- `agentbus policy relax <role> --days <n>` and `agentbus policy waive <role> --file <f> --rule
  '<text>' --days <n>`, in the person's shell. Both refuse from an agent's process tree
  (`agentcaller.Refuse`), and refuse more than 30 days or no `--days`.
- or busmon, the person's process: a launch refused for a relaxation or a waiver opens an item in
  For you, and the person types the role's name to confirm. busmon writes the entry at that
  moment. The answer it records carries `typed=yes`, like every typed answer. busmon writes on
  the keypress, never on reading a reply from the stream, which any data role could write.

`roles doctor` lists every relaxation and waiver with its expiry and fingerprint state.

## 4. Tests

- `rolecfg`: every launched role's `--settings` carries the whole danger list, `danger_extra`
  and `agentbus promote` included, as `ask`, also when `roles.toml` lists an `allow` that covers
  one of them (the ask wins). That allow is dropped, and `roles doctor` says so.
- A Codex profile with `approval=never` is refused at launch on a role with deny rules, and also
  on a role with none (the danger asks). So is each mode on the "does not hold" list, and any
  client or mode the table does not know.
- **A sandbox is not a rule:** a Codex `on-request` profile with a read-only sandbox, on a role
  whose denies include `Read(**/.env)`, is refused. The reason names the in-sandbox read the
  deny would not stop. A `workspace-write` or network-enabled Codex profile is refused, the
  reason naming a danger act (`git push --force`) that could run inside its sandbox.
- **Auto review is not the person:** `on-request` with `reviewer=auto_review` is refused, its
  reason `auto review`.
- The Claude profiles in `roles.toml` today launch. `codex-review`, `kimi` and `grok` are refused
  without a relaxation.
- **Relaxations:** one for reviewer-codex lets that role launch on `codex-review`. It does not
  cover another role on the same profile. A changed sandbox, network, reviewer, tool rule or
  `danger_extra` voids it. It ends at its expiry. `policy relax` refuses from an agent's process
  tree, refuses more than 30 days and refuses no `--days`. `roles doctor` shows a relaxed role as
  `rules not held`, with the line "the relaxed agent could extend this itself".
- **Saved permissions,** under a faked `$HOME` and worktree: `Bash(git push:*)` in
  `~/.claude/settings.json` refuses every Claude role, naming file, line and entry; the same rule
  in one worktree's `.claude/settings.local.json` refuses only roles launched there; `Bash(git
  push origin feat-x)` refuses nothing; a `danger_extra` entry is matched like a built-in one; a
  Codex `prefix_rule(pattern=["git"], decision="allow")` refuses the Codex roles. `roles doctor`
  lists all of them with file and line. A Kimi role shows `not inspected`. After the run, every
  client file is byte-identical.
- **Waivers:** one waives exactly one role and one rule text in one file. A changed text, another
  file, another role or a changed profile is refused again. It ends at its expiry. `policy waive`
  refuses from an agent's process tree. busmon writes a waiver on the typed confirmation, and a
  reply entry with `typed=yes` written to the stream by a test agent writes nothing.
- **Promotion,** against a local bare repository with a pre-receive hook standing in for the
  forge (PR rule bypassed only by the promote key, force and delete refused for everyone):
  `agentbus promote S` pushes `S` and `main` equals `S`; a non-fast-forward `S`, an `S` not on
  `origin/dev`, or one without a PASS record is refused before any push; the push names only
  `refs/heads/main`, without `--force`. Under `AGENT_BUS_AGENT=master` the launch's settings
  carry the `agentbus promote` ask.
- **Forge check,** on recorded API answers per forge: protected as §3.4 → no warning; PR rule
  off, force allowed, deletion allowed, a bypass on the safety ruleset, a second bypass actor, or
  an unreadable answer → one warning each, naming it; `local` → `no forge protection`.
- `agentbus deliver` with `single_approval` on: a PR whose base is the default branch needs both
  reviews.
- No launch writes outside its generated settings file. The test fakes `$HOME` and checks the
  client config files are unchanged.
- `policy.danger_extra` adds an entry. A setting that tries to remove a fixed entry fails
  validation.

**Not doing:** no new approval UI beyond busmon's confirm, no classifier of our own, no change to
Claude Code's own auto mode, no edit of a client's files, no forge setting changed by agentbus (the
person sets protection; agentbus only reads it), and no grant that crosses machines (that is idea
4, remote approvals).

## 5. Build steps

Each step is one PR into `dev`, with both reviews.

1. **The danger list and the launch check.** The Go table, `policy.danger_extra` (PersonOnly,
   additions only), the asks in every launch's `--settings`, `rulesHold` and the refusal at
   launch. On its own this step stops reviewer-codex, ui-kimi and ui-grok, so it ships with
   step 2, or behind a setting that warns instead of refusing until step 2 is in.
2. **Relaxations and waivers: the entry, its writers, its display.** The `config.toml` entry
   shared by both (role, profile, fingerprint, expiry, and for a waiver the file and rule), `policy
   relax` and `policy waive` (§3.5), busmon's typed confirm, and `roles doctor`'s display.
3. **Saved permissions.** The per-client file table, the matcher, `roles doctor`'s list, and the
   launch refusal unless a waiver covers it.
4. **deliver:** no single approval for a PR into the default branch.
5. **Promotion under protection.** The push part of `agentbus promote` with the promotion key
   (the dev branch flow's step 4 provides its check record), `agentbus promote` on the danger
   list, the `Read` deny on the key, `roles doctor`'s forge check, and an operator-notes fragment
   with each forge's setting from §3.4. Then the person turns protection on, and doctor's warning
   clears.

## 6. Question for the person

**Q5. How a promotion reaches a protected `main`.**
- **(A) recommended: a promotion key with a bypass of the PR rule only.** `main` keeps the tested
  sha, and its tag the promoted commit. Force push and deletion stay refused for everyone. The key
  is readable by your user's processes (§3.4, the limit).
- (B) merge through the forge's API, no bypass. On GitLab (fast-forward only) and Bitbucket
  (fast forward) the sha is kept. On GitHub every merge writes a new commit, so `main` would not
  hold the tested sha, which changes the dev branch flow decision.
- (C) a bypass for your own forge account. No key to set up, but every role pushes with your
  credentials, so the bypass is every role's.
