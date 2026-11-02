# Forge delivery: GitHub, GitLab, Bitbucket or local git

Status: spec, not built; the open questions answered by Bernard on 2026-11-02 (end of this file).
Plan v0.26 task `forge-delivery`, review subject `forge-delivery-spec`.

## Why

agent-bus's core knows no forge: Redis and Git. What assumes GitHub is the agents' flow:
master and the docs agent run `gh pr create`, master records `plan link <task> --pr <n>`, and
the guides say "open the PR". A team on GitLab, Bitbucket or a plain local repository has to
improvise each delivery.

Bernard's decision (2026-10-30, option A): a `delivery` setting in `config.toml`, and
`agentbus deliver <branch>`, which uses the forge's own CLI when it is there (`gh`, `glab`;
Bitbucket in link mode, answer 2) and otherwise works in local mode (no PR; the verdicts on the bus are the review; the
person merges with git; `plan link --ref` beside `--pr`). No native per-forge API (option B,
rejected as heavy). The two `api.github.com` URLs (busmon's update check, redis-watch) are
about our own releases and Redis's, not the person's repository: they stay.

## The setting

```toml
[delivery]
forge = "auto"   # auto | github | gitlab | bitbucket | local
base  = "main"   # the branch a delivery targets
```

Two registry keys (`config/keys.go`, group Services), env `AGENT_BUS_DELIVERY_FORGE` and
`AGENT_BUS_DELIVERY_BASE`, read through `config.Resolver` like every other key; `agentbus
config` shows them. `base` must be a valid Git branch name (`git check-ref-format --branch`).

**One setting for the machine** (answer 1): the person's `config.toml` (or the env), never a
per-project value. A project's services env file is not read for these two keys, and `up` writes
neither there. Two projects on two forges need nothing as long as `auto` reads both remotes; a
project whose remote `auto` cannot place is local until the person sets `forge` for the machine,
and the line that says so names the key. The cost, said plainly (foureyes): a `forge` set for the
machine wins over `auto` for **every** project on it, so setting `forge = "gitlab"` to place one
project's self-hosted GitLab makes a GitHub project deliver as GitLab too. The fix for one
unrecognised remote is a recognisable host name for it (a `gitlab.` first label, or the forge's
own domain), not the machine's setting; deliver's line for an unknown host says both.

**`auto`** (the default) decides per repository, from its `origin` remote at each delivery:

| `origin` host | Forge |
|---|---|
| `github.com` | github |
| `gitlab.com`, or a host whose first label is `gitlab` (`gitlab.example.org`) | gitlab |
| `bitbucket.org` | bitbucket |
| no `origin` remote | local |
| any other host | local, with the line `origin is on <host>: not a known forge; set delivery.forge` |

Both URL forms are read (`git@host:o/r.git`, `https://host/o/r(.git)`, `ssh://git@host[:port]/o/r`);
user info in a URL is dropped before anything is printed. A set value other than `auto` wins
over the remote, so a self-hosted GitLab under another name is `forge = "gitlab"`.

## `agentbus deliver`

```
agentbus deliver <branch> [--task <plan-task>] [--subject <s>] [--base <b>] [--title <t>] [--dry-run]
```

Run by master, or the docs agent for its `pr/<change>` branch (answer 3: the person allows
these two roles to deliver, push included, with the 4-eyes proof below; no other role). The tool
rules carry it (below, "skills and roles"). It needs the project (the verdicts) and the
repository it is run in.
Every external command goes through one runner (as `setup.Runner`), so the tests replace it.

### The checks, before anything leaves the machine

1. **The head.** `<branch>` exists locally; its head is resolved once to a full SHA, `H`, and
   every later step names `H`, never the branch, so a commit added while deliver runs is not
   what leaves.
2. **Ahead of the base.** In a forge mode, `git fetch origin <base>` first (it updates
   `refs/remotes/origin/<base>`: a write, listed below), then `git rev-list --count
   origin/<base>..H` > 0; a stale local `<base>` never decides. In local mode, against the local
   `<base>`.
3. **The 4-eyes proof, bound to `H`.** The subject is `--subject`, else the plan task's review
   subject (`--task`), else the branch name. `bus.RollUp` over it reads `APPROVED`, **and** its
   newest independent approve names `H` in its new `head` field, with no reject after it.
   Otherwise deliver refuses, names the subject, the head it found approved (or none) and `H`,
   and exits 3 (`verdicts`' code for not approved). There is no `--force` and no fallback on
   times: committer dates are set by the committer (`GIT_COMMITTER_DATE`, an amend, a skewed
   clock), and the broker's clock is not the committer's, so no time comparison proves that a
   head was reviewed.
4. The forge, from the setting, and for GitHub and GitLab their CLI: on PATH, and logged in by
   its own status command's **exit code** (`gh auth status`, `glab auth status`; its output is
   not read). A CLI absent or not logged in is **link mode** (below), never a failure.
   Bitbucket is always link mode (answer 2): no third-party CLI is looked for or run, even one
   on PATH.

### The verdict names its head (a change to `verdict`)

`agentbus verdict … --head <sha>` records `head` (the full SHA, 40 or 64 hex characters,
resolved with `git rev-parse --verify <sha>^{commit}` in the reviewer's checkout; a short or
unknown one refused) in the `{p}:verdicts` entry: one additive field, read by `Bus.Verdicts`
like the others; `verdicts --subject` shows it. The roll-up's rule does not change; deliver adds
the head condition on top. A verdict without `--head` still counts for the roll-up, the plan and
busmon as today, and never authorises a delivery. The reviewers' skill (agent-bus, the review
part) says to pass `--head` with the SHA they read; a challenge already names it in its text.

### The steps per forge

| | Push | Open | Read back |
|---|---|---|---|
| github (`gh`) | `git push origin H:refs/heads/<branch>` | `gh pr create --base <base> --head <branch> --title <t> --body-file <f>` | `gh pr view <branch> --json number,url` |
| gitlab (`glab`) | same | `glab mr create --source-branch <branch> --target-branch <base> --title <t> --description <body> --yes` (see below) | `glab mr view <branch> --output json` (`iid`, `web_url`) |
| bitbucket (link mode always, answer 2) | same | nothing: deliver prints the new pull request page | none |
| link mode | same | nothing: deliver prints the forge's own new-request page | none |
| local | **nothing** | nothing | none |

Link mode's page, built from `origin` (never fetched): GitHub
`https://github.com/<o>/<r>/compare/<base>...<branch>?expand=1`, GitLab
`https://<host>/<o>/<r>/-/merge_requests/new?merge_request[source_branch]=<branch>&merge_request[target_branch]=<base>`,
Bitbucket `https://bitbucket.org/<o>/<r>/pull-requests/new?source=<branch>&dest=<base>`, each
part URL-escaped.

The push names the checked commit (`H:refs/heads/<branch>`), so what leaves is what was
approved, and sets no upstream (no `-u`: no write to the repository's config). A remote branch
already ahead of `H` makes git refuse the push: deliver stops there.

`glab mr create` takes the description as an argument; if the installed glab offers a file or
stdin form (pinned at implementation), deliver uses it. Otherwise the description is the one
argv exception: it holds only the title, commit subjects and verdict first lines, each through
`health.SafeText`, and an argv is readable only by the person's own processes on this host.

The exact flags are pinned at implementation against each CLI's own `--help` of the version
installed then, and held by the fake CLIs' tests; a CLI that refuses them is reported with its
exit code, and deliver falls back to link mode for that run.

### The body

Written to a temporary file (0600, removed after), never on an argv:

```markdown
<the plan task's title, else the branch's first commit subject>

## Commits
- `a1b2c3d` <subject>
- …

## 4-eyes
- approved by foureyes at 2026-11-01 14:02 on `<H>` (`<verdict ref>`): <its first line>
- approved by reviewer-codex at 2026-11-01 14:05 on `<H>` (`<verdict ref>`): <its first line>

🤖 Generated with agent-bus (`agentbus deliver`)
```

Every line that reaches the forge goes through `health.SafeText` (a line that looks like a
credential is `[redacted]` whole): the title, each commit subject and each verdict line; the
body is capped at 60 000 characters (GitHub's limit is 65 536).
`--title` overrides the title; the title is one line, 256 characters at most.

### After a delivery

- A PR or MR opened (or found already open for that branch): `plan link <task> --pr <n>` when
  `--task` is given, and one line on stdout: `delivered <branch>: <url>`.
- Link mode: `plan link <task> --ref <branch>@<H>`, and the page to open.
- Local: `plan link <task> --ref <branch>@<H>`, and the command for the person:
  `git switch <base> && git merge --no-ff <H>` (the approved commit, not whatever the branch
  holds by then).

What deliver writes in the repository: `refs/remotes/origin/<base>` (the fetch) and, by the
push, `refs/remotes/origin/<branch>`. Nothing else: no config, no local branch moved, no tag. Deliver never merges, never pushes in
  local mode, never touches `<base>`.

In every mode deliver also writes `agentbus report <self> --to master` when run by the docs
agent, so master learns the URL or the ref.

## Local mode, end to end

1. coder commits on `coder/<task>`; foureyes and reviewer-codex give verdicts on the subject.
2. master runs `agentbus deliver coder/<task> --task <task>`: the proof is checked, nothing is
   pushed, the plan records `ref coder/<task>@<sha>` (busmon's Plan view: **waiting for you**).
3. The person runs the printed `git merge`, then tells master, who records `plan done <task>`
   as today. Nothing on the bus claims the merge before the person does.

## The plan: `--ref` beside `--pr`

`agentbus plan link <task> --ref <branch>@<sha>` records a ref fact (`bus/plan.go`: `Ref string`
beside `PR int`, same script, one or the other replaced by the newer link). `plan.Derive` reads
a ref exactly as it reads a PR for the task's state (**in review**, then **waiting for you**
once approved); the review roll-up stays the task's subject, with no `pr:<n>` subject added. busmon's
plan page shows `PR #12` or `ref coder/x@a1b2c3d`.

## What changes in the skills and roles

- **agent-bus-master**: "open its PR" becomes `agentbus deliver <branch> --task <task>`; the
  `plan link --pr` line names `--ref` for local mode; the 4-eyes check before a PR is deliver's
  own, so master reads its refusal instead of checking by hand.
- **agent-bus-docs**: step 5's `gh pr create` and its "No GitHub?" paragraph become
  `agentbus deliver pr/<change> --base <base>` (the PR body it writes today becomes `--title`
  and the commits; its validation section moves into the change's own commit or report).
- **agent-bus** (every reviewer: foureyes, reviewer-codex, …): a verdict on a branch names its
  head, `agentbus verdict --subject <s> --head <sha> …`, the SHA the reviewer read; a new
  commit means a new verdict on its SHA.
- `roles.toml` (answer 3): `Bash(agentbus deliver *)` allowed in master's and docs' own
  `tool_rules`; every other shipped role carries in its own rules the deny
  `Bash(agentbus deliver *)` and `Bash(agentbus * deliver *)` (the global-flag spelling, as
  busmon-reload-deny does for `reload --as`). Per role, never in the top-level `[tool_rules]`:
  a deny merged into every role would also deny master and docs. Defence in depth, not a
  boundary: the client matches command text, and a person's own shell has no such rule.
  A role added later carries the deny too (foureyes): `roles draft` writes the two denies into
  every draft's `tool_rules`, and `roles adopt` refuses a role that lacks them, with its name, unless
  the person's answer to the adopt ask names it a delivering role (none but master and docs today).
  A test on the draft and one on adopt hold it, since `TestShippedManifestRules` sees only the
  shipped roles.
- A decision note, `docs/memory/decisions/<date>-forge-delivery.md`, records answer 3 (who may
  push, and that the 4-eyes proof bound to the head is the condition), so master's skill can cite
  it.
- The guides (EN, FR) and the README: "open the PR" says how per forge, and local mode.

## Credentials: never read

Deliver never opens a CLI's credential files (`~/.config/gh/`, `~/.config/glab-cli/`, …), never
reads or passes a token (`GH_TOKEN`, `GITLAB_TOKEN` and the like are neither set nor read by
deliver; the CLI reads its own environment), and reads a CLI's auth only by the status
command's exit code. `git push` uses the person's own Git credentials. A remote URL with user
info is printed without it.

## Failure paths

| What | What deliver does |
|---|---|
| Not approved, or approved on another head (a commit since, or a verdict without `--head`) | refuses, exit 3, names the subject and the gap; nothing pushed |
| Branch absent, or not ahead of base | refuses, exit 1 |
| `origin` absent with a forge set | refuses: `delivery.forge is <f> but there is no origin remote` |
| Fetch of the base fails | stops before the push: `cannot read origin/<base>` |
| Push refused (non-fast-forward, hook, permission) | stops, prints git's own message, records nothing |
| CLI absent or not logged in | link mode, with the CLI's login command (`gh auth login`) |
| CLI fails on create | its exit code; link mode for that run |
| A PR/MR already open for the branch | found by the view command, reused, linked (idempotent) |
| Network down | the push fails: as above |
| Plan link fails (no plan, unknown task) | the delivery stands; deliver says the link was not recorded and exits 2 |

A second `deliver` on a delivered branch pushes nothing new, finds the open PR and records the
same link: it changes nothing.

## Tests (fake CLIs, never a real forge)

- **Forge from `origin`**: a table of URLs (each form, self-hosted GitLab, user info dropped,
  an unknown host, no remote) and the set value winning over `auto`.
- **Checks**: not approved; approved on an older head; **a commit made after the approval with
  `GIT_COMMITTER_DATE` set before it** (the backdated case: refused, since its SHA was not
  approved); an approval without `--head`; a self-approval only; a branch not ahead of
  `origin/<base>` while ahead of a stale local base: each refuses before any push (the fake
  runner records no `git push`).
- **Push**: the argv is `git push origin <H>:refs/heads/<branch>`, no `-u`; a branch moved
  between the checks and the push still pushes `H`.
- **verdict --head**: recorded full, a short or unknown SHA refused, absent = no field.
- **Per forge**, with fake `git`, `gh` and `glab` first on PATH (as `tests/installer_test.sh`
  stubs docker): the exact commands in order, the body file's content and mode, the PR number
  read back, `plan link --pr` recorded; an existing PR reused.
- **Link and local modes**: the printed page per forge (escaped), no push in local, `plan link
  --ref` recorded.
- **Credentials**: the runner's recorded environment for every command carries no token the
  test environment holds (`GH_TOKEN=tok-SECRET` set in the test, absent from every argv and
  never printed); no path under the fake HOME's CLI config dirs is opened.
- **Plan**: `--ref` derives like `--pr`; the newer link replaces the other.
- **Setting scope** (answer 1): `delivery.forge` set in a project's services env file is not read
  (`agentbus config --project` shows the machine's value, `auto` by default).
- **Bitbucket** (answer 2): with a fake `bb` (and any other name) first on PATH, a bitbucket
  remote runs no CLI (the fake runner records none), pushes `H`, prints the escaped new pull
  request page and records `plan link --ref`.
- **Who may deliver** (answer 3): `TestShippedManifestRules` holds the allow on master and docs
  and the two denies on every other shipped role, none in the top-level rules; a `roles draft`
  carries the two denies, and `roles adopt` refuses a role without them.
- **Local mode** (answer 4): with an `origin` remote and `forge = "local"`, nothing is pushed or
  fetched (the fake runner records no `git push` and no `git fetch`).

## Answers (Bernard, 2026-11-02)

The four open questions, as asked and as answered (relayed by master):

1. **One setting for the machine, or per project?** One machine-wide setting in `config.toml`,
   with `auto` (the forge read from `origin`) as its default. No per-project value ("The
   setting").
2. **Bitbucket's CLI.** None: Bitbucket is link mode. agentbus pushes `H` and prints the new pull
   request page; no third-party CLI is supported or run ("The steps per forge").
3. **Who may deliver?** master and the docs agent, push included, with the 4-eyes proof bound to
   the head; no other role. The tool rules hold it, and a decision note records it ("What
   changes in the skills and roles").
4. **Local mode with a remote**: push nothing. Local means the person's repository is the only
   one; nothing is fetched either, since the base is the local one ("The checks", step 2).
