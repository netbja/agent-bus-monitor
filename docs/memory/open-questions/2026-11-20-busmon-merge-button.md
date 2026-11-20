---
id: 2026-11-20-busmon-merge-button
project: agent-bus-monitor
scope: project
kind: question
status: proposed
created: 2026-11-20
last_verified: unknown
author: architect
confidence: unknown
---
# Merge a PR from busmon's merge popup, showing the exact command first (the person's idea, waiting for the person)

## Finding/decision

### 1. The request

The person asked for this on 2026-11-20, relayed by master. In the popup busmon shows for a
change waiting for their merge:
- offer to **merge it from busmon**, through the forge's own CLI;
- **show the exact command that will run**;
- run it as the person's own action, with their own forge credentials, never an agent.

They would stop typing the command or opening the forge's page.

What busmon does today, on `dev` as of f018542c:
- **The popup.** `mergePage` (`cmd/busmon/mergequeue.go`) shows the PR, its branch at the
  approved head, the reviewers and what they checked. Its copy target is the PR's URL, or
  `git switch <base> && git merge --no-ff <head>` in local mode.
- **No forge commands.** busmon reads the merge queue's cache. The merge-queue spec says it
  "never runs git or `gh` itself". This idea would change that for one action.
- **GitHub only.** The queue reads only GitHub (`gh pr list`). GitLab and Bitbucket PR readers
  are on the roadmap.

### 2. The requirement for any future spec

**The merge must be bound, at the moment it runs, to the approved head AND to the project's
base (`delivery.base`), or the button is not built.** Without the button, busmon keeps copying
the exact command, as today.

This note states that requirement only. It does not claim any mechanism meets it.

### 3. How to guarantee it: the open design question at spec time

These are candidates found while writing the note. None of them is promised, and the spec must
establish and verify whichever it uses against the installed tools and the forge's settings:
- **Head.** `gh pr merge --match-head-commit <head>` makes GitHub refuse a moved head. For
  GitLab, `glab mr merge --sha <head>`. Both flags are taken from the CLIs' documentation and
  were not run.
- **Base.** A live read of the PR just before the merge (`gh pr view --json baseRefName,…`).
  The queue's cache holds no PR base: `bus.MergeItem` has none, and the record's `Base` is the
  configured one. A read alone leaves a window before the merge. A forge rule refusing PR merges
  into every branch but `delivery.base` is one candidate for that window. Whether it can be set
  and checked is open.
- **A merge that would not happen now.** `gh pr merge` may queue the PR or enable auto-merge,
  on a repository that requires GitHub's merge queue or while required checks are pending.
  Which fields would prove an immediate merge is open.
- **The repository.** The `--repo` value and the PR number come from the cached URL. Matching
  that against the project's own `origin` (`forge.Resolve`) is a candidate check.
- **Who presses.** busmon refuses `--delete` and `--reset` under an agent (`refuseDestructive`,
  `agentcaller.Ancestor`). The forge-delivery decision calls such a check "defence in depth, not
  a boundary". When and whether to apply it to this action is open.

Recommendation: worth a spec, GitHub first, if the spec can meet section 2. If it cannot,
busmon keeps the copy action.

### 4. Questions for the person

1. **May busmon run the forge's CLI for this one action?** The alternative is that busmon only
   copies the exact command. That keeps the "busmon never runs gh" rule and leaves the typing
   to the person.
2. **The merge method: merge commit, squash or rebase?**
   - The team's written test says a PR is merged when its head is an ancestor of `origin/dev`
     (CLAUDE.md, AGENTS.md and the dev-branch decision).
   - A squash or rebase never satisfies it. This already happened with #642: it landed as the
     single-parent commit 8faba668, and its approved head 1196a630 is not an ancestor of `dev`.
   - The queue copes by patch-id (`mergequeue/delivered.go`).
   - So the choice is merge commits only, or a merged-test rule that also accepts the patch-id
     match.
3. **The branch after the merge:** delete it, or keep it?
4. **Where busmon runs when the person merges:** on the laptop, through the tunnel, or on the
   host? That decides whose forge login is used, and whether the CLI is installed there.
5. **The report on the bus:** should a merge, or a refusal, be reported to master, and under
   which name?
6. **A merge the forge would defer** (queued or auto-merged later): refused, or accepted?
7. **Forge rules:** may the repository's settings be changed, for example a rule that refuses
   PR merges into other branches, if the spec needs that to meet section 2?
8. **Running under an agent:** is a refusal that is defence in depth enough? Or must the action
   need something an agent cannot supply?

## When it applies

- **Nothing is built before the person answers.** A spec follows their answers, and it must meet
  section 2.
- **GitLab** waits for its PR reader.
- **Bitbucket, none and local** keep today's link or copied command.

## Evidence

- The popup today: [cmd/busmon/mergequeue.go](../../../cmd/busmon/mergequeue.go) (`mergePage`,
  its copy target).
- The queue's reader, GitHub only:
  [mergequeue/forge.go](../../../mergequeue/forge.go) (`ReadGitHub`, `gh pr list`), and the
  merged-item drop in [mergequeue/delivered.go](../../../mergequeue/delivered.go).
- Today's agent refusal in busmon: `refuseDestructive` in
  [cmd/busmon/main.go](../../../cmd/busmon/main.go), with `agentcaller.Ancestor`.
- The item's fields: `MergeItem` in [bus/mergequeue.go](../../../bus/mergequeue.go), which has
  no PR base.
- The forges known: [forge/forge.go](../../../forge/forge.go) (`Kind`, `Resolve`), and
  `delivery.forge` / `delivery.base` in [config/keys.go](../../../config/keys.go).
- The rules it touches:
  - [the merge-queue spec](../../superpowers/specs/2026-11-05-merge-queue-design.md) ("never
    runs git or gh");
  - [forge delivery](../decisions/2026-11-02-forge-delivery.md) (the person keeps the merge;
    defence in depth, not a boundary);
  - [the dev branch flow](../decisions/2026-11-16-dev-branch-flow.md) (PRs into `dev`, `main`
    by master).
- The person's request: master's directive of 2026-11-20 (bus cmd `1795169686518-0`).

## Limits / what would invalidate it

- **None of section 3 was run or verified.** The CLIs' flags and the forge's settings come from
  their documentation. If no mechanism meets section 2, there is no button.
- **The forge's own rules** (protection, required reviews) may refuse a merge the bus approved.
