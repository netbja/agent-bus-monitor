# Bounded auto-approve (bounded-auto)

Status: IDEA spec r3, for review (subject `bounded-auto-spec`). Not built. Base: origin/dev
`af527c19`. Author: architect, from master's directive (cmd `1795559518593-0`, task
`specs-from-person-docs`). This is idea 2 of 4 from the person's note "ideas borrowed from
PiBot", item 3 there. The note's reference is PiBot `packages/policy/src/core/yolo.ts`
(Apache-2.0), used for the idea only; no code is translated. No new dependency. The questions
for the person are last (§5).
r2: reviewer-codex's reject of r1 (verdict `1795575214730-0`). The launch now refuses a profile
whose mode cannot keep the danger-class asks, not only one that would lose denies, even for a role
with no deny rule (§3). The guarantee is narrowed to what is enforced: the person approves a danger
act at the client's prompt, and two reviews on `deliver` are a review gate, not that approval
(§2, §3). `elevated` is cited for what it names; Claude `auto` is not in it (§1).
r3: reviewer-codex's reject of r2 (verdict `1795575323186-0`). A prompting mode is a sandbox
boundary, not the enforcement of agentbus's rules. Inside its sandbox Codex runs a command
without asking, a deny-matched read included, and `auto_review` approves an escalation without
the person. So rules are held only where agentbus writes rules the client enforces, which is
Claude Code today. Every other profile is refused unless the person accepts it by name. Three
team roles are concerned, so this is the person's Q1 (§3, §4, §5).

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
   PersonOnly, additions only). No setting removes one.

   **What is enforced, and what is not.** The prompt holds only where the client honours an
   ask rule in its mode, which the launch checks (§3). Even then, an ask rule matches the
   command's text, so a command that is spelled differently, or run inside a script or a test,
   is not caught. That is defence in depth, not a boundary. The only boundaries outside the
   client are these:
   - a push or merge to `main`: the forge's branch protection, which the person sets (Q4), and
     master's promotion on the person's yes, an ask answered `typed=yes`;
   - everything else on the list (a force push to a work branch, a branch delete, a reinstall,
     spending): the client's prompt only.
   This spec claims no more than that.
4. **Never persistent.** An auto-approve is never written where it outlives its scope. agentbus
   writes rules only into the launch's own `--settings`, never into `~/.claude/settings.json`,
   `~/.codex/config.toml`, or a project's `.claude/settings.local.json`.

## 3. What the build does

- **The danger class in the shared rules.** The launch adds the §2.3 list to every role's
  `ask` rules. The source is one Go table (`rolecfg`), so `roles.toml` cannot drop it. An
  explicit ask rule still prompts in Claude's `auto` mode (`roles.toml`, header), and prompt-notify
  rings the person. These are text matches: defence in depth (§2.3).
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
  - every mode on the §1 list (`bypassPermissions`, `approval=never`, `yolo`, `skip`, …), and any
    client or mode the table does not know.

  A profile that does not hold is **refused at launch, for every role**, because the danger asks
  are mandatory. The one exception is the person's own: a profile named in
  `policy.rules_not_held_ok` (PersonOnly, in `config.toml`, which no role may edit) launches.
  `roles doctor` and busmon then show it as `rules not held`, with the denies it loses and
  the reason (sandbox only, auto review, mode). **Today this concerns three roles:**
  reviewer-codex (`codex-review`: a read-only sandbox, but `reviewer = "auto_review"`), ui-kimi
  (`kimi`) and ui-grok (`grok`). Without the person's listing they stop launching (Q1).
- **Two reviews are a review gate, not the person's approval.** `agentbus deliver` stops offering
  `single_approval` and `same_provider_ok` for a PR whose base is the default branch. Such a PR
  then needs both reviews, which is a check on the code. The person's approval of the act stays
  the prompt, or master's promotion ask.
- **Persistent grants are shown, not removed.** A person who clicks "don't ask again" in a
  pane makes the client write an allow into its own settings file. agentbus never edits those
  files. `roles doctor` lists the allows it finds there, so the person can decide (Q3).

## 4. Tests

- `rolecfg`: every launched role's `--settings` carries the whole danger list as `ask`, also
  when `roles.toml` lists an `allow` that covers one of them (the ask wins). That allow is dropped, and
  `roles doctor` says so.
- A Codex profile with `approval=never` is refused at launch on a role with deny rules, and also
  on a role with none (the danger asks). So is each mode on the "does not hold" list, and any
  client or mode the table does not know.
- **A sandbox is not a rule:** a Codex `on-request` profile with a read-only sandbox, on a role
  whose denies include `Read(**/.env)`, is refused. The reason names the in-sandbox read the
  deny would not stop. A `workspace-write` or network-enabled Codex profile is refused, the
  reason naming a danger act (`git push --force`) that could run inside its sandbox.
- **Auto review is not the person:** `on-request` with `reviewer=auto_review` is refused, its
  reason `auto review`.
- `policy.rules_not_held_ok = ["codex-review"]` lets that profile launch. `roles doctor` then
  lists its lost denies and the reason. A role cannot set the key.
- The Claude profiles in `roles.toml` today launch; `codex-review`, `kimi` and `grok` launch only
  when listed.
- `agentbus deliver` with `single_approval` on: a PR whose base is the default branch needs both
  reviews.
- No launch writes outside its generated settings file. The test fakes `$HOME` and checks the
  client config files are unchanged.
- `policy.danger_extra` adds an entry. A setting that tries to remove a fixed entry fails
  validation.

**Not doing:** no new approval UI, no classifier of our own, no change to Claude Code's own auto
mode, and no grant that crosses machines (that is idea 4, remote approvals).

## 5. Questions for the person

**Q1. Profiles whose rules do not hold** (today reviewer-codex, ui-kimi and ui-grok).
- **(A) recommended: refused unless you list them** in `policy.rules_not_held_ok`. Each one you
  list is shown as `rules not held`, with what it loses. You would likely list `codex-review`, so
  the second reviewer stays.
- (B) refused, with no exception. Reviews then run only on Claude.
- (C) launched, with a warning in `roles doctor` and busmon.

**Q2. The danger list.**
- **(A) recommended: fixed in code, with your additions** (`policy.danger_extra`).
- (B) fixed in code, with no additions.

**Q3. Allows the clients saved from "don't ask again".**
- **(A) recommended: list them in `roles doctor`,** and leave the files alone.
- (B) refuse to launch a role while one of them covers a danger-class command.

**Q4. Branch protection on `main` at the forge** (the only boundary outside the clients for a push
to `main`).
- **(A) recommended: you turn it on** (a PR required, no force push, no delete). `roles doctor`
  says when the forge reports it off.
- (B) leave it off, and rely on the prompt and master's promotion ask.
