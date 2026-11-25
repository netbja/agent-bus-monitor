# Bounded auto-approve (bounded-auto)

Status: IDEA spec r1, for review (subject `bounded-auto-spec`). Not built. Base: origin/dev
`af527c19`. Author: architect, from master's directive (cmd `1795559518593-0`, task
`specs-from-person-docs`). This is idea 2 of 4 from the person's note "ideas borrowed from
PiBot", item 3 there. The note's reference is PiBot `packages/policy/src/core/yolo.ts`
(Apache-2.0), used for the idea only; no code is translated. No new dependency. The questions
for the person are last (§5).

## 1. Today's auto-approve switches

| Switch | Where | Scope |
|---|---|---|
| A client's own auto mode: Claude `auto` or `bypassPermissions`, Codex `approval=never`, Kimi `yolo` or `auto`, Grok `bypassPermissions`, agy `skip` or `auto` | the profile in `roles.toml`; `elevated` names it (`rolecfg/launch.go:1018`) | one role's session |
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

   Each one is an `ask` in every role, whatever its mode. The person may add entries
   (`policy.danger_extra`, PersonOnly, additions only). No setting removes one.
4. **Never persistent.** An auto-approve is never written where it outlives its scope. agentbus
   writes rules only into the launch's own `--settings`, never into `~/.claude/settings.json`,
   `~/.codex/config.toml`, or a project's `.claude/settings.local.json`.

## 3. What the build does

- **The danger class in the shared rules.** The launch adds the §2.3 list to every role's
  `ask` rules. The source is one Go table (`rolecfg`), so `roles.toml` cannot drop it. An
  explicit ask rule still prompts in Claude's `auto` mode (`roles.toml`, header), and prompt-notify
  rings the person. These are text matches: defence in depth, not a boundary. The delivery path
  is the boundary: `agentbus deliver` refuses `single_approval` and `same_provider_ok` for a PR
  whose base is the default branch, or which deletes or force-updates a branch.
- **Denies must hold.** `roles doctor` gets one line per profile: `denies hold` or `denies not
  enforced`. Codex, for one, has no per-command deny for agentbus to write. A profile in an
  auto mode whose client cannot enforce the role's denies is **refused at launch**, and the
  refusal names the deny rules that would be lost (Q1).
- **Persistent grants are shown, not removed.** A person who clicks "don't ask again" in a
  pane makes the client write an allow into its own settings file. agentbus never edits those
  files. `roles doctor` lists the allows it finds there, so the person can decide (Q3).

## 4. Tests

- `rolecfg`: every launched role's `--settings` carries the whole danger list as `ask`, also
  when `roles.toml` lists an `allow` that covers one of them (the ask wins). That allow is dropped, and
  `roles doctor` says so.
- A Codex profile with `approval=never`, on a role with deny rules, is refused at launch with the
  rules named. The same profile on a role without denies launches.
- `agentbus deliver` with `single_approval` on: a PR to the default branch, a branch delete,
  and a force update each need both approvals.
- No launch writes outside its generated settings file. The test fakes `$HOME` and checks the
  client config files are unchanged.
- `policy.danger_extra` adds an entry. A setting that tries to remove a fixed entry fails
  validation.

**Not doing:** no new approval UI, no classifier of our own, no change to Claude Code's own auto
mode, and no grant that crosses machines (that is idea 4, remote approvals).

## 5. Questions for the person

**Q1. A client whose auto mode cannot keep the role's denies.**
- **(A) recommended: refuse the launch,** naming the lost rules. You switch the profile to a
  prompting mode or drop the denies on purpose.
- (B) launch, with a warning in `roles doctor` and busmon.

**Q2. The danger list.**
- **(A) recommended: fixed in code, with your additions** (`policy.danger_extra`).
- (B) fixed in code, with no additions.

**Q3. Allows the clients saved from "don't ask again".**
- **(A) recommended: list them in `roles doctor`,** and leave the files alone.
- (B) refuse to launch a role while one of them covers a danger-class command.
