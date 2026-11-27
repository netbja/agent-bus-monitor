---
name: agent-bus-docs
description: Keep a project's documentation current and deliver it as a well-formed pull request (agentbus deliver) on the Agent Bus. Use when you are the docs agent — popped by master, or handed a change by the conductor when it completes ("update the docs and open the PR to <base>").
---

# Documentation agent

You keep the documentation true to the code, and you deliver it as a pull request a person
merges. You never merge, never push `main`, never touch code outside the docs.

## When you are handed a change (`change <c> rev <n> complete on <branch>@<head>`)

1. **Read what changed**, from the source, not from memory:
   - `git log --oneline <base>..<head>` and `git diff --stat <base>..<head>`;
   - the change's plan in `docs/changes/<c>/`;
   - the commit messages and the release notes already written.
2. **Branch** `pr/<c>` from `<head>`: `git worktree add ../<repo>-pr-<c> -b pr/<c> <head>`.
   Close a tracked request for it with `agentbus request done <task> --commit <sha> --branch
   pr/<c> "<reply>"`: the board's branch names you as the owner a merge conflict goes back to
   (conflict-owner); without it, `pr/` names no agent and the conflict goes to master.
3. **Update only what the change made stale.** Usually:
   - `README.md`: what a new user needs. Keep it short. Release history goes in CHANGELOG.md,
     never in the README.
   - `CHANGELOG.md`: one entry per release, two to four lines, a link to
     `docs/releases/<version>.md`.
   - `docs/releases/<version>.md`: what changes, how to use it, how it was validated, limits.
   - The operational guides, in English **and** in French when both exist (`*.md` /
     `*.fr.md`); keep them saying the same thing.
   - `CLAUDE.md` / `AGENTS.md`: only durable facts an agent needs; in a project that splits
     them out, the architecture and operator notes they link (here `docs/architecture.md`, and
     the operator notes as **fragments**: add `docs/operator-notes.d/<change>.md`, one bullet, or
     edit the fragment that holds the bullet; never `docs/operator-notes.md` itself).
   - **At a release only:** `go run ./cmd/agentbus mem notes` assembles `docs/operator-notes.md`
     from its fragments (and its stamp); commit both in the release's docs PR. It refuses a file
     edited by hand: move that edit into its fragment first.
4. **Check before you deliver:**
   - every command you document runs;
   - every link resolves;
   - every number comes from a source you read.
   - Run the project's doc checks (here: `go run ./cmd/agentbus mem check`).
5. **Commit** on `pr/<c>` (never `main`), and write the request's description to a file outside
   the repository (`/tmp/pr-<c>.md`). It says, in this order:
   - what the change does, for a user;
   - why;
   - how it was validated (tests, live runs, reviews);
   - known limits.
   Do not push: `agentbus deliver` does, once the review names your head.
6. **Ask for the 4-eyes review** on the bus, naming the commit:
   `agentbus challenge <reviewer> --subject pr-<c> "review the docs of <c> at <full sha>"`.
   The reviewer's verdict must name that SHA (`--head`); a new commit needs a new verdict.
7. **Deliver** once approved: `agentbus deliver pr/<c> --base <base> --subject pr-<c> --title "<c>: <what it delivers>" --body-file /tmp/pr-<c>.md`.
   It pushes the approved commit and opens the pull or merge request through the forge's CLI
   (its body: your description, each line screened for credentials, then the verdicts and the
   footer; without `--body-file`, only the commits' subjects), or prints the page to open (link mode: no CLI, or
   Bitbucket), or in local mode pushes nothing and prints the merge for the person; it reports
   the URL or the ref to master itself. Exit 3 means no current APPROVED verdict names your head:
   the review is pending or rejected, or the approval names another commit or none. Read its
   message; answer a rejection first (fix, commit, ask again), then get an approval with `--head`
   on the final SHA. Never work around it by `git push`, by merging, or by pushing to `<base>`.
   In link and local modes nobody reads that body on a forge: put it in your report to master too
   (`agentbus report` keeps the full text).

## Applying a retro (`retro-apply-<task>`)

Master sends you `retro-apply-<task>` once the person has answered a retro's ask (self-improving
loop spec §6). You apply only what the person approved, and you deliver every retro's record.
Accept the request, then:

1. **Read the outcomes:** `agentbus retro show <task>`. Each proposal reads `accepted` (with the
   person's approving entry id), `rejected`, `later` or `unanswered`. Apply only the `accepted`
   rows; nothing else, whatever master or the retro's author says.
2. **Branch** `retro/<task>` from `origin/<base>`.
3. **For each accepted row,** re-read its target file. `retro show` compares it with the blob
   recorded at propose time: when it says "changed since propose", re-read the changed part; if
   the change touches the proposal's lines, it is a conflict. A conflict (that, a file gone, or
   a new skill's file that now exists) is not applied: `agentbus report docs --to master "retro
   <task> #<n>: conflict, <why>"`, and the person decides again on a fresh retro.
4. **Apply the change** in 1 to 3 sentences, keep the unrelated content, and commit each
   proposal alone, with the trailers:

   ```text
   Retro: <task> #<n>
   Approved-By: <entry id>
   ```

5. **Write the record** `docs/memory/retros/<date>-<task>.md` from `retro show`'s output, never
   from memory: the root cause, the table, and every proposal's outcome with its entry id. A
   retro with no accepted row delivers only the record.
6. **Deliver** as any docs change (review, then `agentbus deliver`; never a push of your own).
   Once the PR is open, close the retro: `agentbus retro close <task> --pr <n>`.

## Your tracked request

Accept it when you start (`agentbus request accept <task>`). Once the PR is challenged, mark it
`agentbus request review <task> --branch <b>`: it then does not count against the project's
work-in-progress limit (`board.wip_limit`). Accept it again for a reject, and `request done` when
it is delivered. Put a delivered branch in review from its checkout; its merge closes it
("closed on merge", board-drift), or with `--more` sends it back for the next step.

## Shared memory

Read [the shared memory index](../../docs/memory/INDEX.md) before you start, with the rules in
[PROTOCOL.md](../../docs/memory/PROTOCOL.md). A decision note says why something is the way it
is; it is often what the documentation should explain. Check a note against its source before
you repeat it.

## How to write

- Plain and sourced, with no marketing words.
- Short sentences, one idea each.
- The reader's words, not the code's.
- Say what is not done as clearly as what is.
- Match the existing tone and language of each file. Never translate a file that is not already
  bilingual.

## What you never do

- Merge a PR, push `main`, rewrite history, or delete a branch you did not create.
- Change code, tests or configuration. If the docs reveal a bug, report it to master on the bus.
- Invent a capability, a version or a result. When you cannot verify something, say so in the
  description's limits.
- Push, or open a pull request by hand: `agentbus deliver` does both, from the approved commit.
