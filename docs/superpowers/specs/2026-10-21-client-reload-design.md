# Client reload after an update — design

Status: draft for review (coder, 2026-10-21). Task `sentinel-reload-spec`, branch
`docs/sentinel-reload-spec`. Spec only; no code in this change.

## The problem

A client updates itself on disk while the agent keeps running the old binary. Claude Code then
shows `Update installed · /restart to apply`; Codex and others have their own banner. Nobody on
the team sees it unless they look at that pane, so agents run old clients for days.

Observed on this machine on 2026-10-21: `~/.local/bin/claude` resolves to
`~/.local/share/claude/versions/2.1.306`, while the four agents of `agent-bus-monitor` (master,
coder, foureyes, sentinel) run `/proc/<pid>/exe` = `…/versions/2.1.305`.

Bernard asks that sentinel **propose** reloading the client after an update. Nothing restarts
anything by itself.

## Detecting an installed-version change

### What can be known without running the client or reading a credential

| Client | Running process (Linux) | Installed now |
|---|---|---|
| Claude Code (native install) | `/proc/<pid>/exe` → `…/claude/versions/<v>` | the `claude` the launcher resolved, a symlink → `…/versions/<v>` |
| Codex (npm) | pane process is `node …/@openai/codex/bin/codex.js`; the shared app-server runs `~/.codex/packages/app-server-daemon/releases/<v>-…/bin/codex` | `package.json` `version` next to the script; it is rewritten in place by `npm i -g` |
| Grok | `/proc/<pid>/exe` → `…/grok-<v>-linux-x86_64` | the `grok` symlink's target |
| Others (Kimi, agy, Gemini) | no stable layout verified | — |

A version string is not available the same way for every client, but one fact is: **whether the
entry point the launcher started has changed since**. That is what the design measures.

### Options

| | How | Verdict |
|---|---|---|
| A | **Launch fingerprint.** At launch, `rolecfg` already resolves the client on `PATH` (`lookPath`). It records that path and a fingerprint of what it resolves to: the final symlink target, its device, inode, size and mtime, and for a script (`#!` or `.js`) the same for the `package.json` beside it. `bind` stores both in the agent's verified identity. Later, the dispatcher re-resolves the same path and compares. | **Recommended**: one rule for every client, no exec, no version parsing; a version label is added only where a parser is proven |
| B | Run `<client> --version` and compare with the running version | Runs client code from the dispatcher, which must never run code (CLAUDE.md "Things that bite"); some clients start slowly or touch their config. Rejected for the dispatcher. A person may still run it by hand |
| C | Read the banner in the pane (`Update installed`) | The dispatcher already reads each verified pane's recent screen for its ready/busy signatures, so it costs nothing. But the wording changes between releases and languages, and it only shows while the banner is on screen. Kept as a **secondary** signal, per client, only for agents with no fingerprint (bound by `status`, not by the launcher) |
| D | Compare `/proc/<pid>/exe` with the resolved target | Exact for native binaries (Claude, Grok), useless for Node clients, where the exe is `node` and the script is rewritten in place. A's fingerprint covers it |

### Recommended detection (A, plus C as fallback)

- **Launcher** (`rolecfg/launch.go`): after `lookPath(cmd[0])`, compute `ClientFingerprint{Path,
  Target, Dev, Ino, Size, MtimeNs, Pkg *FileFP}` and export it as `AGENT_BUS_CLIENT_FP` (JSON),
  next to `AGENT_BUS_CLIENT`. `agentbus bind` stores it in the snapshot as `client_fp`, set only with a
  proof, like `model` (`bus/ident.go`). Additive field; old snapshots have none.
- **Version label** (display only, never a decision): Claude `versions/<v>`, Grok
  `grok-<v>-linux…`, npm `package.json` `version`. Missing parser → no label, the flag still works.
- **Dispatcher** (the observe pass, which already inspects every verified pane every 15 s; it runs
  only in the dispatch **service** — `Config.Observe` is 0 by default, so a foreground
  `agentbus dispatch` and the existing tests observe nothing, and neither detect updates): for each
  verified agent with a `client_fp`, re-resolve `Path` at most once a minute (stat and readlink
  only), and mark the agent `client_update` when the fingerprint differs. With no `client_fp`,
  a client signature `update` (C) may set the same mark, flagged `banner`.
- **Layout fallback for agents without a fingerprint**, Claude Code only, Linux: when
  `/proc/<pid>/exe` is `…/claude/versions/<v>` and that `versions` directory holds a higher
  version, or the link reads `… (deleted)`, the agent is marked `client_update` with source
  `layout`. That covers every agent launched before this change (today's whole team) without a
  relaunch. Version order is the numeric dotted order; anything else in the directory is ignored.
  It misfires on a deliberately pinned or downgraded install (a newer version left on disk): for a
  proposal that only asks, that is acceptable, and `skip this version` silences it.
- `/proc` is Linux only, like `snapLife`. On macOS the fingerprint (stat only) still works; the
  running label is omitted.

Nothing is read beyond file metadata, a `package.json` version field and symlink targets: no
transcript, no configuration, no credential.

## Where it surfaces

1. **`{p}:observed`** (fenced, replaced every pass, expires after 120 s) gains an additive
   per-agent field: `update: {running, installed, since}` (labels may be empty; `since` is the Redis
   TIME of the first observation of this fingerprint change, carried forward like `Since`).
2. **busmon**: the agent row gets the flag `update`. The agent detail gets a row
   `Client  2.1.305 running · 2.1.306 installed · reload when idle`, plus the reload command (below).
   Observed, never declared: the state column is untouched.
3. **Health**: a new check `client-updates` (interval 5 min): `warn` when one or more agents run an
   outdated client, detail `N agent(s): <client> <running>→<installed>`; `ok` when every verified
   agent can be judged (fingerprint or layout fallback) and none is outdated; `unknown` when no
   observation is fresh, or when some verified agent has neither a fingerprint nor a layout match. Warn never wakes sentinel (v0.19 rule), so it does not on its own.
4. **One proposal per update episode.** An episode is `(client, installed fingerprint)`, across
   agents: one update of Claude Code is one episode, however many agents run it. When an episode
   starts, the dispatcher sends **sentinel** one directive, written with its once-mark in one fenced
   script (`FencedCmdOnce`, mark `update:<p>:<client>:<fp-hash>`). A second update starts a new
   episode; a restarted dispatcher sends nothing twice.

## What sentinel does with it

Sentinel stays one-shot (skill `agent-bus-sentinel`, new section "Client update"):

1. Read `agentbus agents` and `agentbus health detail client-updates`.
2. Sort the agents into **ready** (declared `idle`, not observed busy, no `accepted` board task, no
   open challenge on either side, no open ask, **not on a fallback profile**: no record in
   `{p}:fallback`, `Bus.Fallbacks`) and **later** (anything else). Master is always last. A swapped
   role is listed as `after agentbus swap <role> --back`: the reload path below relaunches the
   template's command, which carries the role's own profile, so reloading a swapped role would
   silently undo the swap.
3. Send master **one** directive with the proposal (text below), and a health note
   (`agentbus health note client-updates …`).
4. Do nothing else. It never closes a pane, never types into one, and never runs `swap` or
   `retire`.

Master puts the decision to the person with `agentbus ask` (choices: `reload the ready ones now`,
`later`, `skip this version`) and acts only on the answer, one agent at a time.

### The reload path: `/exit`, then `agentbus up <p> --resume`

No new mechanism. v0.21's relaunch already does the right thing once the client has exited:

1. **The person** types `/exit` in the agent's pane (it is idle: that is why it is listed as ready).
   The client exits and the pane returns to its shell.
2. **The person, or master on the person's answer**, runs `agentbus up <p> --resume` from a
   terminal, after `agentbus up <p> --resume --dry-run` shows `<role>: relaunch (resumed)` and
   nothing else. `up` sees the tab's command gone (dead pid, every pane a plain shell), and runs the
   tab's **template command** in a **new pane split from that tab**, with `--resume <session>`
   appended (`resumeCommand`, `cmd/agentbus/relaunch.go`). The template command is
   `env ROLES_TOML=<manifest> <scripts>/agent-launch <role> <p>` (`rolecfg/template.go`), so the
   manifest is the project's own, and the launcher re-applies the role rules, the profile's model
   and the identity (`bind`). No one types into an existing pane: the person types `/exit`, and `up`
   runs the command in a pane it has just created, as v0.21 decided.

Limits, stated in the proposal:

- **Swapped roles** are not ready (above): the template carries no `--profile`, and `agent-launch`
  takes `--profile` or `--resume`, not both.
- **The session id is the one the agent last declared.** `AgentSnapshot.Session` is updated by the
  agent's `status`; if it ran `/clear` and has not declared since, the reload resumes the earlier
  conversation. The proposal shows the session's declaration age. Once v0.21.1 lands, the launcher's
  reported resume command follows `/clear` and can replace this source.
- **Clients without launcher resume** (Codex today): `up --resume` relaunches them fresh, and the
  proposal says the conversation is not kept.

### The proposal text (sentinel → master, master → person)

```
Claude Code 2.1.306 is installed; 4 agents still run 2.1.305.
Ready to reload now (idle, no work in flight): coder, foureyes, sentinel.
Later: none. Master last. Swapped roles: none.
Reload keeps the conversation (as last declared), the role rules and the identity:
  1. in the agent's pane: /exit
  2. agentbus up agent-bus-monitor --resume --dry-run   (expect: <role> relaunch (resumed))
  3. agentbus up agent-bus-monitor --resume
Reload now, later, or skip this version?
```

## The rules

- **Propose only.** No process of ours restarts a client, exits it, or types `/restart`.
- **Never into a busy pane.** An agent observed busy, declared `working`, holding an `accepted`
  task, under an open challenge or with an open ask is listed as *later*, never *ready*.
- **Only the existing safe path:** `/exit` typed by the person, then `agentbus up <p> --resume`,
  which runs the template command in a new pane. Nothing of ours types into an existing pane.
  `agentbus swap` is refused for the same profile by design and is not a reload path; `retire` is
  not needed, since `/exit` already ends the client.
- **Once per update episode.** Proposals are keyed by client and installed fingerprint, not by
  agent or time; a person's `skip this version` is recorded as a health note and nothing is
  proposed again for that fingerprint.
- **After a reload**, the agent's new launch records a fresh fingerprint. The flag clears on the
  next pass; if it does not, the health check stays `warn` and says which agent.
- **Claude Code's own `/restart`** is not used until it is proven to keep the argv (`--settings`),
  the environment (`AGENT_BUS_*`) and the conversation. If the live test below shows it does, the
  proposal may name it as the shorter path; identity then re-proves itself on the agent's next
  `status`, since Claude's commands descend from the pane.

## Test plan

### Unit (no herdr, no broker unless marked)

- `ClientFingerprint`: a symlink switched to a new target → changed; a file rewritten in place
  (new mtime/size) → changed; the same file → unchanged; a script with `package.json` rewritten →
  changed; an unreadable path → unknown, never "changed".
- Layout fallback: a temp `versions/{2.1.305,2.1.306}` tree with a fake exe link → update; only
  `2.1.305` → none; `(deleted)` suffix → update; a non-version entry (`.lock`) → ignored.
- Version labels: `versions/2.1.306`, `grok-1.0.62-linux-x86_64`, `package.json` → labels; unknown
  layout → empty.
- Launcher: the dry run prints `AGENT_BUS_CLIENT_FP`; `bind` stores `client_fp` only with a proof;
  an unverified `status` never erases it (`mergeSnapshot` rule).
- Dispatcher (test broker): observe pass with a fake runtime and a temp client tree → `update` in
  `{p}:observed`; re-resolution throttled to once a minute; paused project → nothing.
- Sentinel sorting (pure function): idle / working / accepted task / open challenge / open ask /
  fallback record / master → ready or later, master last.
- Episode: two agents on the same new fingerprint → one directive to sentinel; dispatcher restart →
  none; a second update → a second directive.
- Health `client-updates`: ok / warn with count / unknown without fingerprints.
- busmon: flag `update` and the detail row, in `TestAlignmentSuite` fixtures (all sizes, palettes).
- Sentinel skill: `tests/skills_test.sh` asserts the new section forbids restart, typing,
  `swap` and `retire`.

### Live checks (this machine, read-only first)

1. Today's state already reproduces it: with the branch's dispatcher pass run against the test
   broker and the real pids, the four agents read `update 2.1.305 → 2.1.306` through the layout
   fallback (they have no fingerprint). Read-only: stat and readlink of `/proc/<pid>/exe`.
2. `/restart` semantics, in a throwaway Claude Code on a separate herdr session (Bernard, or coder in
   its own pane, never in a team pane): note `/proc/<pid>/cmdline`, the environment's
   `AGENT_BUS_AGENT`, and the conversation before and after `/restart`; record whether the pid
   changes. The result decides whether the proposal may name `/restart`.

## Review

foureyes on `b36d08c`: detection, propose-only, once per episode and the test plan confirmed;
changes requested on the reload path (missing `ROLES_TOML` and `--profile`, and typing into an
existing pane), now `/exit` + `agentbus up --resume` with swapped roles excluded. Also addressed:
the session-age limit, the layout fallback's misfire, the health `unknown` rule, and the observe
pass running only in the service. Open questions 1–3 were answered as proposed.

## Open questions for review

1. Episode per client across agents (one message) rather than per agent? (Proposal: per client.)
2. Sentinel → master → person, or the dispatcher straight to the person with a `notify`?
   (Proposal: sentinel → master, as asked, since master owns `ask`.)
3. Banner fallback (C) in this change, or later? (Proposal: later. The fingerprint covers new
   launches and the Claude layout fallback covers older ones; for other clients launched before
   the change the health check says `unknown` until their next launch.)
