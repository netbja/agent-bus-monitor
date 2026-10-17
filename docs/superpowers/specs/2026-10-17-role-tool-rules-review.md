# Review of 2026-10-17-role-tool-rules-design.md (rev 1)

Reviewer: Claude Code (abm-claude session, w1:p1), 2026-10-17. Not committed. Sources: the
Claude Code docs read today (code.claude.com/docs/en/permissions and /permission-modes), and
`scripts/agent-spawn`, the live solana master pane.

Verdict: **CHANGES**. The `allow` half is sound and worth shipping. The `ask` half, as written,
does not deliver "Bernard approves deploy": Claude Code documents that an ask rule is not a
security boundary, and the proposed pair of rules can be passed by the very command the allow rule
admits.

## §9.1 is answered: master runs in auto mode

The solana master's pane (hyperliquid session, w3:p1) reads `⏵⏵ auto mode on`. It was launched
with `--permission-mode acceptEdits`, then switched in the session (Shift+Tab, or the prompt's "Yes,
and switch to auto mode"). In auto mode an action no rule covers goes to the classifier, which
blocked `agent-spawn deploy` as "Production Deploy". `acceptEdits` itself never classifies. So the
design must hold **in auto mode**, the mode masters actually end up in.

## §5.3 precedence: right, with one consequence the spec misses

Confirmed by the docs: "Rules are evaluated in order: deny, then ask, then allow. The first match
in that order determines the outcome", across every settings scope and command-line source; "a
matching ask rule prompts even when a more specific allow rule also matches". In auto mode, rules
resolve before the classifier ("Actions matching your allow, ask, or deny rules resolve
immediately"), and "Ask rules that match on a command's content, such as `Bash(git push *)`, fall
back to a permission prompt". A narrow allow like `Bash(agent-spawn *)` is not one of the rules
dropped on entering auto mode (blanket `Bash(*)`, wildcarded interpreters, package-manager runs,
`Agent`, `Monitor`). So in auto mode: `agent-spawn architect p` runs without the classifier, and
`agent-spawn deploy p` prompts. Good.

The consequence: **the ask only fires when the command text matches it, while the allow fires on
anything that starts with `agent-spawn `.** The docs say it plainly: "a deny or ask rule covers the
invocation Claude usually produces and isn't a security boundary around the program." With
`allow = ["Bash(agent-spawn *)"]` and `ask = ["Bash(agent-spawn deploy *)"]`, each of these runs
deploy **unasked and unclassified**, because the allow matches and the ask does not:

- `agent-spawn "deploy" ai-tradex-solana` or `agent-spawn 'deploy' …` (quoting)
- `agent-spawn $ROLE ai-tradex-solana` (a variable)
- `agent-spawn  deploy …` (two spaces), `agent-spawn deploy` with no project (prints usage, harmless)

`agent-spawn` itself takes the role as `$1` strictly, so no flag-before-role form exists; the
quoting and variable forms are enough. A model that was just denied, or one that "helpfully"
quotes its arguments, finds them without trying to evade anything.

**Fix (cheap, keeps the spirit):** never allow by prefix. Generate one allow rule per role that does
not need approval, and no allow at all for the others:

```toml
[roles.master.tool_rules]
allow = ["Bash(agent-spawn architect *)", "Bash(agent-spawn coder *)", "Bash(agent-spawn docs *)",
         "Bash(agent-spawn security *)", "Bash(agent-spawn sentinel *)", "Bash(agent-spawn foureyes *)"]
ask   = ["Bash(agent-spawn deploy *)"]
```

Then every unusual form (quoted, variable, path, `bash -c`) matches no allow and falls to the mode:
a prompt in acceptEdits, the classifier in auto — the safe direction. Better still, the launcher
**derives** this list from `roles.toml` (every role without `approval = "person"`, §below), so a new
role needs no rule edit and a typo cannot widen it. Validation then rejects any allow ending in a
bare `agent-spawn *`.

## The approval itself belongs on the bus, not in a pane prompt

Even fixed, a Claude Code prompt in master's pane has three weaknesses for this job:

1. **It is per client.** §6 has to forbid swapping master to Codex or Kimi because the ask would
   vanish. The fallback profiles of v0.15 exist precisely so master can move when Claude is out of
   quota — the moment risky work most often waits.
2. **It is in the wrong place.** Bernard's cockpit is busmon; a prompt in master's tab is seen only
   when he happens to look at that tab. busmon has the waiting state, the bell and the question
   panel for exactly this.
3. **It is not recorded.** A pane prompt leaves no trace of who approved what, when.

Proposal, small and reusing v0.14 asks: a role may carry `approval = "person"` in `roles.toml`
(deploy ships with it). `agent-spawn <role> <project>` for such a role refuses to start unless the
bus holds an answered ask from master for that role — question id `spawn:<role>`, answered "yes" by
a person (`human=yes`), within the last N minutes, not yet used — and marks it used in the same Lua
script (one approval, one pop). master's flow becomes: open the ask ("Pop deploy to move /tmp to
/mnt/aitradex?"), Bernard answers in busmon (bell, panel), master runs `agent-spawn deploy …`.
This works for every client, survives a swap, leaves the decision in `{p}:asks`, and does not
depend on matching command text. The `ask` rule of §4 can stay as a belt-and-braces second prompt
for Claude, but it is no longer what the guarantee rests on, and §6's swap refusal can go.

## §6

- **`ask` with `bypassPermissions` rejected:** right — the docs: allow rules "have no effect in
  bypassPermissions", and nothing prompts there.
- **`ask` on a non-Claude client fails the launch:** right as long as the ask rule is the
  guarantee. With the bus approval above, it degrades to "no second prompt", and the swap check is
  no longer needed.
- **`allow` ignored on other clients with a note:** fine (fewer permissions, more prompts).
- **`dontAsk` allowed:** fine.

## Smaller points

- §4.2: also reject an allow whose pattern is a prefix wildcard of a script that takes a role
  (`Bash(agent-spawn *)`, `Bash(agent-spawn*)`), or more simply: do not accept free-form allow
  rules for `agent-spawn` at all, generate them (above).
- §5.1 `--settings` JSON: fine, and it is merged as one more source, so a user or project deny
  still wins (confirmed). Check `claude --help` for `--settings` in preflight, as planned.
- §5.4: keep the sentence "call agent-spawn by its bare name" — with per-role allows it becomes a
  convenience (path forms prompt), not a hole.
- §8 step 1: also test the quoted form `agent-spawn "deploy" x` against the rev 1 rules; it should
  show the hole, and the per-role list should close it.
- §9.3 (`agentbus swap`/`retire` without the classifier): same treatment — allow exact forms
  (`Bash(agentbus --project * swap *)` is too wide; prefer leaving them to the mode until there is a
  need).
- §9.4 version: **v0.17**. It changes the launch command line of every master, adds a manifest key
  and a role property, and (with the bus approval) an `agent-spawn` refusal path. v0.16.x should
  stay for fixes to the compose window.
