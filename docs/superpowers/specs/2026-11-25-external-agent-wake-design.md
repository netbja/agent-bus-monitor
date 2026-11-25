# Waking an external agent (external-wake)

Status: IDEA spec r1, for review (subject `external-wake-spec`). Not built. Base: origin/dev
`04c1755f`. Author: architect, from the person's idea (directive `1795607315032-0`, thread of the
same id), sent through the external agent claude-ao with the person's approval. No new dependency.
The questions for the person are last (§7).
r2: foureyes's reject of r1 (report `1795608657532-0`): deliver already counts any name's approve,
so the reviewer filter is a change and ships with step 1; every run is a try; the command runs off
the dispatcher's pass; the wake command is only as safe as `config.toml`; the decision note to
amend is named. reviewer-codex's reject of r1 (verdict on `dn49qfft4ol8`): the path check
resolves symlinks and checks every parent directory, with a fixed `PATH`; the same-user limit is
stated. claude-ao's note (thread `1795607315032-0`): an external agent never falls back to
the person's identity.

## 1. Today

Since the herdr exit, the dispatcher wakes an agent only through its terminal on the holder
(`dispatch/runtime.go`, `Runtime.Prompt`). `Decide` (`dispatch/decide.go`) needs a verified
snapshot (rule 6: `Snap.PID == 0` is "unverified snapshot"), a pane it can inspect, and a
supported client (rule 7). An agent running elsewhere, such as a Claude Code session in the
person's desktop app, has none of these. It can use the CLI. But a message sent while it is not
armed waits until its next drain, and after `T` the dispatcher escalates it to master as a stall.
It has no entry in `roles.toml` either, so master cannot tell it from any name a process chooses,
and had to ask the person to confirm it in busmon.

Two facts bound what this spec can promise:

- **`from` is what the writer says.** That is true for every agent today, not only an external
  one. A verified identity exists only for the snapshot: the holder's child, its pid and start
  time (`bus/ident.go`, `Bind`).
- **The broker's ACL users are per kind of caller, not per agent,** and not on yet
  (`bus/acl.go`: admin, master, dispatcher, agent, busmon, ping; broker-hardening step 4). Every
  agent other than master is `agent`.

## 2. The idea

**A declared external agent, and a wake command the person owns.**

- **The declaration, in the repository:** a role in `roles.toml` with `kind = "external"`. It has
  no profile, client or launch line. The holder never starts it, and `pop` refuses it. Its tool
  rules are not written anywhere, because agentbus launches nothing: they do not apply, and
  `roles doctor` says so.
- **The wake command, in the person's config only:** `config.toml`, PersonOnly, per project and
  agent: `external.<agent>.wake = ["/abs/path/to/script"]`. It is not in `roles.toml`, because
  that file is in the repository: a PR that set a command there would make the dispatcher run it,
  and the dispatcher never runs repository code (CLAUDE.md). A declared role with no wake
  command is a role the dispatcher cannot wake (§3).
- **Registration, by the person, once:** `agentbus external add <agent>`, from the person's shell
  (`agentcaller.Refuse` from an agent's tree). It records that the person vouches for this name on
  this project. busmon shows the row as `external · declared by the person`. Master reads that and
  does not ask the person again, per session or per message.

What the registration proves: that the person declared this name. It does not prove that a
message whose `from` is that name came from it. No agent has that proof today (§1). The external
agent is at the level of every other agent's messages, not below it.

**It never writes as the person.** claude-ao's own shell had `AGENT_BUS_PROJECT` but no
`AGENT_BUS_AGENT`, so `agentbus` wrote as the person's `identity.as` without a word: the identity
guard refuses only inside a holder terminal (`cmd/agentbus/identityguard.go:63`). The person sets
`AGENT_BUS_AGENT=<its name>` in the session's environment, and the person-name spec's step 1
refuses the person's name to any process with a client among its ancestors.

## 3. The wake

**Exec only, in this build** (§7 Q1). The dispatcher runs the command with:

- **a fixed argv:** the configured argv, then `--project <p> --agent <a> --count <n> --nonce <x>`.
  `n` is the obligation's count, `x` a fresh random value. **Never a message's text**, as the pane
  wake types only the fixed line;
- **no shell,** `exec` of an absolute path, checked at each run, not only at config time:
  - the path is resolved with every symlink followed (`filepath.EvalSymlinks`); the **resolved**
    path is the one checked and the one run;
  - it is refused when it is relative, not a regular file, not owned by the person, writable by
    group or others, or inside a registered project's repository or worktree (after resolution,
    so a symlink into a worktree is refused);
  - **every parent directory** of the resolved path, up to `/`, must be owned by the person or
    root and not writable by group or others (a sticky `/tmp` is refused too), so nobody else can
    swap the file between the check and the run;
- **a clean environment:** a fixed `PATH=/usr/bin:/bin` (plus `/opt/homebrew/bin` on macOS), not
  the person's or the dispatcher's, `HOME`, `LANG`, and `AGENTBUS_WAKE_NONCE`. Not the
  dispatcher's own environment, which holds the broker's credentials;
- **a timeout of 10 s,** and one command at a time per agent. Its output is discarded. The wake
  record keeps the exit code and the duration;
- **off the dispatcher's pass.** The pass starts the command in its own goroutine and moves on;
  the next pass reads the result. A running command counts as a wake in flight for its agent, so
  the pass does not start another. A slow or hung command never delays another agent's decision,
  quota or context check. The lease's renewal has its own ticker (`dispatch/loop.go:228`).

A webhook is an exec of `curl`. So exec covers it, and **agentbus holds no secret** for the
target. The clean environment passes none, so the person's script reads its own token or endpoint
from a file of its own.

**Same user, same limits.** These checks stop another user and a careless path. They do not stop
a process of the person's own user: it owns the file and its directories, so it can replace the
script after the check, or edit `config.toml`. **The wake command is only as safe as `config.toml`.** PersonOnly binds `config set`, not the
file. A role's shell can write `config.toml` (the comment at `roles.toml:161` says so) and point
`external.<a>.wake` at a 0700 script it wrote in `/tmp`, which passes every path check above. The
dispatcher would then run it outside that role's tool rules and outside idea 3's sandbox. It
still needs a declared external role and the person's `external add`. The sandboxed-launch spec's
bases must keep `config.toml` out of every role's writable paths.

**The rules are the pane wake's,** with the pane rules left out. `Decide` takes the external case
as a new input, `External`:

| Rule | Pane agent | External agent |
|---|---|---|
| 1 no obligation | none | same |
| 2 receiver lease present | none | same: never woken while armed |
| 3, 4 escalated, tries exhausted | escalate | same: **every run is a try,** whatever its exit; the exit code is kept in the wake record and named in the escalation |
| 5 retry window | none | same |
| 6 snapshot, identity | wait | skipped: no pid to verify |
| 7 inspection, client | wait | skipped: no pane; a role with no wake command waits and escalates after `T`, reason `no wake command` |
| 8 pane state | wait | skipped |
| 9 ready | prompt | run the wake command |

An exit of 0 means the command ran, not that the agent woke. As for a pane, only progress proves
it: the agent drains and its obligation moves (`Progressed`). The woken agent still runs
`subscribe --drain`.

**Never for an external agent:** `/clear`, `/compact`, self-repair, `PopDone` and a pop's end,
prompt-notify, context fill and the background judgement. They all need a pane.

## 4. The board, and reviews

- **Requests:** an external agent may accept a request addressed to it, report, block and finish
  it, like any agent. A wake channel without tasks would serve little.
- **Delivery:** never. `delivers` is refused on an external role.
- **Reviews, today:** `deliver` counts the latest approve of **any** reviewer name that is not the
  author and names the head (`currentApprovers`, `cmd/agentbus/deliver.go:549`). A name only has
  to pass `ValidName`. So an external agent's approve counts today, as does one from any name a
  process picks. That is Q3 (C).
- **Reviews, with this spec:** a new filter. `deliver` counts only the team's reviewer roles and the
  external roles the person marks as reviewers (§7 Q3). It ships **with step 1**, so no external
  role exists before the filter does.

## 5. Build steps

0. **The person's first target, by hand.** For the desktop app, can a script put a line into the
   session? Without that, the build wakes nothing useful. The script is the person's; the spike
   only says whether one exists.
1. `kind = "external"` in `rolecfg`: no launch, `pop` refuses, `delivers` refused, `roles doctor`
   shows it. `agentbus external add/list/remove`, person-only. **With it, the reviewer filter:**
   `deliver` counts an approve only from a role in `roles.toml` that is not external, or from an
   external role the person marked a reviewer. That also stops an approve from a name no role
   declares, which counts today.
2. `external.<agent>.wake` (PersonOnly), its path checks, and the exec with its argv, environment
   and timeout, off the pass. The event-driven dispatch decision (2026-10-10) and CLAUDE.md's rule
   on what the dispatcher does are amended, as the self-repair decision (2026-11-09) did: the
   dispatcher also runs the person's wake command for a declared external agent, with fixed
   arguments, never in a pane.
3. `Decide`'s `External` input and the loop's branch: no inspection, the wake command for
   `Prompt`. The wake record keeps exit code and duration; busmon shows them on the row.

## 6. Tests, and what this does not do

**Tests:** `Decide` with `External`: an armed agent is not woken; a ready obligation runs the
command; within the retry window it does not; every run counts a try, so three runs that exit 0
with no progress escalate with the reason "tries exhausted" and the last exit code (`MaxTries` 3,
`dispatch/loop.go:117`); a role with no wake command escalates after `T` with its reason. Rules 6-8 never
apply. The exec, under a fake command recording its argv and environment: the argv ends with the
four fixed flags, no message text appears in argv or environment, and no broker credential is in
the environment. A path that is relative, group-writable, not the person's, or under a worktree is
refused; so is a symlink in `~/bin` that resolves into a worktree, a script in a directory
writable by group, and one under `/tmp`; the resolved path is the one run, and the command sees
`PATH=/usr/bin:/bin` whatever the dispatcher's. A command that sleeps past 10 s is killed and counted a try, and while it sleeps another
agent's prompt in the same pass is not delayed; a second wake for the same agent is not started. `external add` refuses from
an agent's tree. `pop` refuses an external role. `deliver`: an unmarked external approve plus one
team approve is refused; an approve from a name no role declares does not count; a marked
external approve plus one team approve passes.

**Not doing:** no webhook built in, no secret held by agentbus, no per-agent broker user, no proof
of `from`, and no pane-based act on an external agent.

## 7. Questions for the person

**Q1. The wake channel.**
- **(A) recommended: exec only.** Your script reaches the target; a webhook is `curl` in it.
- (B) exec, and a built-in webhook: an HMAC-signed POST to a loopback URL, its key in a 0600 file
  of yours.

**Q2. What makes master trust it.**
- **(A) recommended: your registration, once** (`agentbus external add`). Its messages are then at
  the level of every other agent's.
- (B) a per-agent broker credential that binds `from` to its user. It needs broker-hardening
  step 4 and per-agent users: a later spec.

**Q3. May its verdicts count as one of the two reviews?** Today any name's approve counts (§4).
- **(A) recommended: only once you name it a reviewer,** with the filter of step 1.
- (B) never.
- (C) always, as today.
