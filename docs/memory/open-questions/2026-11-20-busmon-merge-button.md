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

**The idea (the person, relayed by master on 2026-11-20).** In the popup busmon shows for a
change waiting for their merge, offer to **merge it from busmon**, through the forge's own CLI,
**showing the exact command that will run**. They would stop typing it or opening the forge's page.

**What busmon does today, on `dev` as of f018542c.**
- `mergePage` (`cmd/busmon/mergequeue.go`) shows the PR, its branch at the approved head, the
  reviewers and what they checked.
- Its copy target is the PR's URL. In local mode it is `git switch <base> && git merge --no-ff
  <head>`.
- busmon reads the merge queue's cache, which the dispatch service writes. The merge-queue spec
  says "it never runs git or `gh` itself".
- The forge-delivery decision says "the person keeps the merge: deliver never merges". This idea
  keeps that: the person still merges, but from busmon.

**The proposal (not specced, not built).**
1. **A Merge action in the merge popup**, for an item the queue lists. Pressing it opens a
   confirm step that shows the exact argv, the base it merges into and the head it is bound to.
   The two buttons are Merge and Cancel, and **Cancel is the default**. Nothing runs without
   that second press.
2. **The person's own action.**
   - **Who runs it.** busmon runs the command itself, as the person's process, with their own
     forge login (`gh auth` / `glab auth`).
   - **busmon refuses it under an agent.** The action is refused when busmon was started by an
     agent: the same `agentcaller.Ancestor` check that refuses `--delete` and `--reset`
     (`refuseDestructive`).
   - **That check is not a boundary.** As the forge-delivery decision says, it is defence in
     depth. An agent running as the same local user, with the same `gh` login, could still run
     `gh pr merge` itself, so the button adds no new power. The real limits are the person's
     forge login and the forge's own branch protection.
   - **No remote trigger.** Nothing on the bus can trigger the merge: no message, ask or
     directive does.
   - **No new agentbus command.** That would let an agent in a pane run it too.
3. **Bound to the proof.**
   - **Head match.** The command carries the approved head, so the forge itself refuses the
     merge if the PR's head has moved. On GitHub that is `gh pr merge <n> --repo <owner/name>
     --match-head-commit <head> --<method>`. A pushed commit after the review cannot be merged
     by the button.
   - **Only this project's repository.** The `--repo` / `-R` value and the number come from the
     item's URL in the queue's cache, in Redis. The button is refused when that owner/name
     differs from the project's own forge remote (`origin`, as `forge.Resolve` reads it). So a
     cache entry cannot point the merge at another repository. The security role judges this
     trust boundary in the spec.
   - **Fresh check first.** Just before running, busmon re-reads the queue record and the
     verdict ledger. If the item is gone, or its head no longer has both approvals, the button
     is refused with the reason, and nothing runs.
4. **Only into the project's base, read live.**
   - **The cache doesn't hold the base.** It does not record a PR's base: `bus.MergeItem` has
     no base, and the record's `Base` is the configured one.
   - **A live read first.** So just before the merge, busmon reads the PR from the forge:
     `gh pr view <n> --repo <owner/name> --json baseRefName,headRefOid,state,isDraft`. It
     refuses unless all of these hold:
     - the base is `delivery.base` (`dev` here);
     - the head is still the approved head;
     - the PR is open and not a draft.
   - **A race is left.** `--match-head-commit` binds only the head, and no `gh` flag binds the
     base. A retarget to another base between the live read and the merge (about a second)
     would not be caught. Branch protection on `main` would close it, but only once that policy
     is established (question 7; to design at spec time, below).
   - **`main` is never merged here.** Promotions to `main` stay master's fast-forward, on the
     person's yes (the dev-branch-flow decision).
5. **What is shown and logged.**
   - **In the popup.** The command, then the forge's answer: its first lines, cleaned, never a
     token.
   - **On the bus.** One report from the person to master: "merged PR `<n>` at `<head>` into
     `<base>` (gh)", or "merge of PR `<n>` refused: `<reason>`". Master learns it at once instead
     of at the queue's next pass.
   - **The queue.** Its next pass drops the item as it does today (`mergequeue/delivered.go`).
6. **A failed merge.**
   - **Nothing is retried.** The popup stays open with the forge's reason (head moved, conflict,
     a required check, branch protection, no login), and the item stays in the queue.
   - **Head moved.** The fix is a new review of the new head, not a second press.
   - **A merge that would not happen now.** On a repository that requires the forge's merge
     queue, or while required checks are pending, `gh pr merge` may enable auto-merge or queue
     the PR instead of merging it (GitHub's manual for `gh pr merge`). The proposal refuses
     that case:
     - the live read also asks the PR's merge state;
     - busmon runs the merge only when the forge says it can merge now;
     - otherwise it shows why and runs nothing.
     Which `gh` field proves an immediate merge is not specified here. It is to be designed at
     spec time (below, and question 6). Until it is, this refusal is a requirement, not a
     guarantee.
7. **The forge matrix.**

| Forge | Merge from busmon | Command (approved head bound) |
|---|---|---|
| GitHub | yes, first | `gh pr merge <n> --repo <owner/name> --match-head-commit <head> --<method>` |
| GitLab | after its PR reader lands (roadmap: the queue reads only `gh pr list` today) | `glab mr merge <iid> -R <group/project> --sha <head> --yes` (flag to verify against glab's version) |
| Bitbucket | no CLI: the popup keeps the link to open | none |
| none | no forge request: copy only, as today | none |
| local | copy only, as today (busmon may run on a laptop, away from the repository) | `git switch <base> && git merge --no-ff <head>` |

`--repo` / `-R` comes from the item's URL, so the merge does not need a checkout where busmon
runs: busmon can run on the laptop through the tunnel.

**Recommendation.** Build it, **GitHub only first**, with the confirm step, the head bound
through `--match-head-commit`, the base check and the report to master. GitLab follows its PR
reader. Bitbucket, none and local stay as they are. It is small: one action, one confirm, one
argv built from the item, and one report. It also removes a manual step that today is a typing
risk, a wrong number or the wrong base.

## When it applies

Only the person decides whether busmon may run the forge's CLI. That reverses the merge-queue
spec's "busmon never runs git or gh", for this one action. A spec follows their answers.

**Hard requirements for any spec.** At the moment of the merge:
- the head must be bound to the approved head;
- the base must be bound to `delivery.base`;
- a merge the forge would defer (queue or auto-merge) must be refused.

If the spec cannot prove all three with the installed CLI, the button is not built. busmon
then only copies the exact command.

To design at spec time (not specified in this note):
- **Immediate merge.** The `gh` fields and values, validated against the installed version,
  that prove `gh pr merge` will merge at once. That includes a branch whose protection
  requires GitHub's merge queue, where a mergeable PR is still queued. Without such proof, the
  guarantee narrows to "refused when the forge says it cannot merge".
- **The base race.** The guard against a retarget between the live read and the merge. Either
  branch protection on `main` that refuses PR merges, established and checked before relying on
  it, or a re-read of the base after the merge with an alert to master and the person if it
  differs.
- **Where the agent refusal runs:** at the press, or once when busmon starts for the whole
  session (foureyes).

Questions for the person (no guesses):

1. **busmon runs the forge's CLI for this one action:** may it? The alternative is that busmon
   only copies the exact command, which keeps the "busmon never runs gh" rule but leaves the
   typing to them.
2. **Merge method:** merge commit, squash or rebase? Fixed per project, chosen in the popup, or
   left to the repository's default (`gh` asks when nothing is given, so busmon must pass one)?
   - **What a squash or rebase changes.** The team's written test says a PR is merged when its
     head is an ancestor of `origin/dev`: CLAUDE.md, AGENTS.md and the dev-branch decision. A
     squash or a rebase never makes the approved head an ancestor.
   - **This already happens.** #642 went in as a single-parent commit (8faba668), and its
     approved head 1196a630 is not an ancestor of `dev`.
   - **The queue is fine.** It already matches squash and rebase by patch-id
     (`mergequeue/delivered.go`).
   - **So the choice is:** merge commits only, or a merged-test rule that also accepts the
     queue's patch-id match.
3. **The branch after the merge:** delete it (`--delete-branch`) or keep it?
4. **Where busmon runs when they merge:** on the laptop, through the tunnel, or on the host?
   That decides whose `gh` login is used, and whether it is installed there.
5. **The report on the bus:** under which name: their human identity, or "busmon"? And should it
   also be sent when they cancel? The proposal says no.
6. **A merge the forge would defer:** required checks still pending, or a repository that
   requires GitHub's merge queue. Should the button refuse, or accept that the forge queues or
   auto-merges it later? The proposal refuses: a merge happens when the person presses, not
   later.
7. **The base race:** is the second between the live read and the merge acceptable? It is
   closed if `main` has branch protection that refuses PR merges (promotions are master's
   fast-forwards). Or must busmon refuse whenever the PR was retargeted at all?
8. **Running under an agent:** busmon refuses the action when an agent started it, but that is
   defence in depth, not a boundary (above). Is that enough, or should the action need
   something an agent cannot supply, such as a separate forge login used only by busmon?

## Evidence

- The popup today: [cmd/busmon/mergequeue.go](../../../cmd/busmon/mergequeue.go) (`mergePage`,
  its copy target).
- The queue's reader, GitHub only:
  [mergequeue/forge.go](../../../mergequeue/forge.go) (`ReadGitHub`, `gh pr list`), and the
  merged-item drop in [mergequeue/delivered.go](../../../mergequeue/delivered.go).
- Today's agent refusal in busmon: `refuseDestructive` in
  [cmd/busmon/main.go](../../../cmd/busmon/main.go), with `agentcaller.Ancestor`. The item's
  fields: `MergeItem` in [bus/mergequeue.go](../../../bus/mergequeue.go), which has no PR base.
- The forges known: [forge/forge.go](../../../forge/forge.go) (`Kind`: github, gitlab, bitbucket,
  none, local), and `delivery.forge` / `delivery.base` in
  [config/keys.go](../../../config/keys.go).
- The rules it touches: [the merge-queue spec](../../superpowers/specs/2026-11-05-merge-queue-design.md)
  ("never runs git or gh"), [forge delivery](../decisions/2026-11-02-forge-delivery.md) (the
  person keeps the merge, the proof bound to the head),
  [the dev branch flow](../decisions/2026-11-16-dev-branch-flow.md) (PRs into `dev`, `main` by
  master).
- The person's request: master's directive of 2026-11-20 (bus cmd `1795169686518-0`).

## Limits / what would invalidate it

- **CLI flags.** `gh pr merge --match-head-commit` and `glab mr merge --sha` are taken from the
  CLIs' documented flags, not run here. The spec must check them against the installed
  versions.
- **The forge's own rules.** Branch protection or required reviews on the forge may refuse a
  merge that the bus approved. The popup shows that refusal; it does not work around it.
- **Two people in one busmon project.** If busmon ever serves several people in one project, the
  report's "the person" needs a name. Today there is one.
