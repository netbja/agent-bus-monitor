---
id: 2026-11-16-dev-branch-flow
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-11-16
last_verified: 2026-11-16
author: coder
confidence: verified
---
# PRs merge into the project's base (dev here); main takes only master's promotions, on the person's yes (active)

## Finding/decision

**Every pull request of agent-bus-monitor merges into `dev`; `main` moves only by master's
promotion of a `dev` head that passed the release check, on the person's yes.** The person decided
it on 2026-11-16, after the herdr-exit release reached their machine with two bugs the suite could
not see (vt-osc-title, the holder socket's dirty build key); `TestReleaseEndToEnd` now catches both,
and `main` is to hold only what it has passed. The person's answers, as the
[dev branch flow spec](../../superpowers/specs/2026-11-16-dev-branch-flow-design.md) §10 records them:

- **The base is per project**: a project template's `[delivery] base` (`dev` for this one) is
  above the machine's `delivery.base` (default `main`); the other projects stay on `main`.
- **Promotion**: master runs the check (build, vet, darwin build, the full suite with a disposable
  Redis, the release test under `AGENTBUS_RELEASE_GATE=1` with its PASS line as proof, mem check)
  on a clean worktree at the `dev` head, proposes it in For you, and pushes that sha to `main`
  fast-forward on the person's yes. A failed check goes to master only, who fixes `dev`.
- **Tags** are on `main` only, on the person's word; the promotion's check is enough to tag.
  `update` installs the newest tag on `main`; with development mode (`dev = true`) on,
  `setup.sh install --from <branch>` may build any branch.
- **Hotfixes** go into `dev` and are promoted, by default; a `hotfix/<x>` flow may be a setting for
  developers who prefer it.
- **GitHub**: `main` protected, and kept as the default branch; master retargets an outside PR to
  `dev`.
- **Roles and skills like code** (Q10): a change to them reaches the running team after a promotion
  and an update, as the binaries do.

It **amends** the [forge delivery decision](2026-11-02-forge-delivery.md)'s "one machine-wide
setting, never a per-project value" for the base only: `delivery.forge` stays the machine's.

## When it applies

agent-bus-monitor, from the switch (spec §5.5: the first promotion, tagged v0.29.0 on the person's
word). Any project whose template sets `[delivery] base`; the others keep `main` and nothing changes
for them. Agents branch from `origin/<base>` and call a PR merged when its head is an ancestor of
`origin/<base>`; master alone promotes.

## Evidence

- Spec: [dev branch flow](../../superpowers/specs/2026-11-16-dev-branch-flow-design.md), §10 (the
  person's answers relayed by master, 2026-11-16; Q10 in master's directive for step 1).
- The release test: `cmd/agentbus/e2e_release_test.go` (`TestReleaseEndToEnd`, its gate).
- Step 1 (the per-project base): `rolecfg/deliverybase.go`, `cmd/agentbus/deliverbase.go`.
- Step 2 (this text): `roles/coder.md`, `roles/coder2.md`, `roles/master.md`, the agent-bus and
  agent-bus-master skills ("Promotion").

## Limits / what would invalidate it

Built in steps (spec §9): until step 3 plan-drift and the merge queue still read `main`; until step
4 the promotion is master's commands by hand, its report the record; until step 5 `setup.sh` still
builds the checked-out tree, not `main`'s newest tag. A promotion that is not a fast-forward, a
commit made on `main` itself, or a tag on a sha that no promotion carried breaks the rule rather
than extending it.
