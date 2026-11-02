---
id: 2026-11-02-forge-delivery
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-11-02
last_verified: 2026-11-02
author: coder
confidence: verified
---

## Finding/decision

**Only master and the docs agent deliver a branch, push included, and only once the 4-eyes proof
names the exact commit that leaves.** The person decided it on 2026-11-02 (answers to the
forge-delivery spec, relayed by master): `agentbus deliver <branch>` pushes and opens the pull or
merge request through the forge's own CLI, else prints the forge's page (link mode), else, in local
mode, pushes nothing and leaves the merge to the person.

- **Who.** master, for any delivered task, and the docs agent, for its `pr/<change>` branch. No
  other role pushes or delivers. **deliver itself enforces it**, before any git call and whatever
  the command's spelling: only a role with `delivers = true` in `roles.toml` (master and docs) may
  run it, so a role added without the field never delivers; the caller is `AGENT_BUS_AGENT`, which
  the identity guard requires inside a herdr pane, and a person outside the agents' panes is no
  role and may. The tool rules only echo it: `Bash(agentbus deliver *)` allowed in master's and
  docs' own rules; every other role denies `Bash(agentbus deliver *)` in its own rules (never in
  the shared ones, which would deny master and docs too), and the launch adds the spellings after
  the project's own `--project`/`-p` (and a following `--host`/`-H`) with the project's literal
  name. Never `agentbus * deliver *`: the wildcard swallows a message, and agents write
  `--project <p>` on every command, so every report mentioning "deliver" would be refused (the
  reviews of step 6). `roles draft` writes the deny into every draft, and `roles adopt` refuses a
  role without it unless the person's answer names it a delivering role.
- **The condition: the proof bound to the head.** deliver resolves the branch to one full SHA `H`
  once, and pushes `H:refs/heads/<branch>` only when the subject's roll-up is approved **and** its
  newest independent approve names `H` (`agentbus verdict --head <sha>`), with no reject after it.
  No time comparison and no `--force`: a committer date is the committer's to set, so only the SHA
  proves what was reviewed. A verdict without `--head` counts for the roll-up, the plan and busmon,
  never for a delivery.
- **Why the person's yes covers the push.** The review already happens on the bus; what the person
  asked for is that nothing unreviewed reaches the forge, and that a push needs no hand on every
  delivery. Binding the push to the reviewed SHA gives both. The person keeps the merge: deliver
  never merges, never touches the base, and in local mode pushes and fetches nothing.
- **Defence in depth, not a boundary.** The bus is no boundary against the local user: the caller
  is the `AGENT_BUS_AGENT` an agent's environment states, and an agent that sets another name, or
  runs `git push` itself, is held by neither check. The client's rules match command text only
  (another project's name, a wrapper, the person's own shell). The proof check inside deliver is
  the condition; the role check and the rules keep other agents from reaching for it.

## When it applies

Every project on a machine with this release. The forge comes from one machine-wide setting,
`delivery.forge` (`auto` reads `origin`), never a per-project value; `delivery.base` is the target
branch.

## Evidence

- Spec: [forge delivery](../../superpowers/specs/2026-11-01-forge-delivery-design.md), its "Answers"
  section (the person, 2026-11-02, answers 1 to 4).
- Related: the [dogfood team decision](2026-10-20-dogfood-team.md) (4-eyes on the bus).
- Master's directive `1793653379935-0` (task forge-delivery, this note).
- Code (built with this decision): `agentbus deliver`, `verdict --head`, `plan link --ref`, the
  roles' rules and `roles draft`/`adopt`.

## Limits / what would invalidate it

A forge whose CLI pushes by itself, or a branch protection that rejects the person's own Git
credentials, is outside it: deliver uses `git push` with the person's credentials and never reads a
token. A team that wants the forge's own review as the gate would replace the bus proof, which this
decision does not cover.
