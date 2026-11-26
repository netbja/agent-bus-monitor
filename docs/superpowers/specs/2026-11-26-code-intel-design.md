# Code intelligence helpers for coding agents (code-intel)

Status: spec r7, for review (subject `code-intel-spec`). Not built. Base: origin/dev `f53e4fca`.
r7, reviewer-codex's reject of r6: the watchdog has no `Pdeathsig` and kills through
`cgroup.kill` in a hard mode (§5). In scope mode the agent's own scope is the root and each
backend a sub-cgroup of it, with no scope per backend (§6). foureyes' r5/r6 build notes are in.
r6, the person's Q7 answer: each backend's budget is a hard cgroup bound through resource-limits'
shared cgroup-root interface (unit, or scope fallback); the soft bound remains only in soft mode,
gated (§6). foureyes' r4 build notes: the /tmp wording, and step 0 checks for no shell wrapper.
r5, reviewer-codex's rejects of r3/r4: the soft bound is gated by a person-only key, off by
default, until Q7 is answered, and busmon shows it (§6, §8). Every backend runs under a
watchdog that kills its group when the relay dies, `SIGKILL` included (§5).
r4, foureyes' reject of r2: a path belongs to the deepest listed worktree containing it, since
worktrees nest (§4).
r3, reviewer-codex's reject of r2: each backend reserves a budget before it starts and is held
to it (hard with cap B, a 2 s watch before; Q7 asks the person, §6). A branch has one owner,
enforced on the board (§7). The relay checks role and agent against the verified snapshot, and
says what that does not protect (§7).
r2 folds in the person's answers to ask `1795733274522-0` (Q1–Q6, all typed, with their
additions; §12) and the two r1 rejects:
- foureyes: cap B is not built, so the relay enforces its own memory guard (§6); and it routes
  only the project's own worktrees (§4).
- reviewer-codex: the person's requirements were missing. Worktree mapping fails closed (§4),
  backends die with their worktree and with the relay (§5), and every answer carries HEAD (§3).
  The gopls daemon is evaluated in step 0 (§11).

Author: architect, from the person's typed request relayed by master (cmd `1795733140669-0`,
task `code-intel-spec`). The person may send a wider summary. The shared dev-index rule
(`abm-origin-dev`) goes to docs separately, not here.

## 1. Goal

Coder and reviewer roles in every project agent-bus drives get **exact, branch-local** code
navigation: symbols, definition, references, callers, implementations, and (coders only) a
rename. It works in any project language, through one MCP helper the launcher gives the agent's
client. Today each agent greps, or uses an MCP server its user configured by hand: a shared
index, built from another tree, that may not match the agent's branch.

## 2. What exists today

- **The launcher** builds each client's argv (`rolecfg/launch.go:298`, `build`). Both clients
  take MCP servers per launch, with no file in the user's home:
  - Claude Code: `--mcp-config <json|file>`. It is additive to the user's servers unless
    `--strict-mcp-config` is given (`quota/claudeprobe.go:88` uses the strict form).
  - Codex: `-c mcp_servers.<name>.command=…` and `.args=[…]`.

  `preflight` (`rolecfg/launch.go:411`) checks each client's `--help` for the flags a launch
  passes.
- **`ExtraHelpers`** lets the background judgement ignore an MCP helper's CPU
  (`agentstate/background.go:113-120`, `:219`). Its hook, `HostRuntime.ExtraHelpers`
  (`dispatch/hostruntime.go:50`, `:303`), is declared but has no caller.
- **Memory:** an MCP stdio server is a child of the client. **Per-agent cap B is not built yet;
  the person's process model (Q7) replaces its design with one systemd unit per agent**
  (agent-resource-limits §4 and step 4, still waiting on the person's answer there). Only the
  holder's cap A (`limits.holder_memory_max`, 70 %) contains it today. `limits.agent_memory_max`
  does not exist yet on dev. §6 is the relay's own guard until then.
- **Worktrees:** coders work in one worktree per task (for example
  `/data/projects/<p>-wt/<task>`) and change worktree between tasks without relaunching. The board
  records a request's branch only at `request review` (`bus/requests.go:465-473`). It never
  records a worktree path.
- **Settings** live in `config/keys.go`. `roles.toml` gains no key: a new key there broke the
  previous release's update once (2026-11-25).

## 3. One relay per agent, one backend per worktree

```
client (claude / codex)
 └─ agentbus code-intel mcp --project <p> --agent <a> --role <r>     the relay, one per agent
     ├─ backend for worktree A   gopls mcp                                     (go.mod)
     └─ backend for worktree B   mcp-language-server + pyright-langserver      (pyproject.toml)
```

**The relay**, `agentbus code-intel mcp`, is the one MCP server the launcher declares, named
`code-intel`. It is Go, in this repository, with no new dependency: MCP over stdio is JSON-RPC 2.0,
one message per line.

**Allowlist, not trust (the person, Q2).** The relay offers only its own tools and forwards each
to one known backend tool. Every other backend tool is never listed and never called:
mcp-language-server's `edit_file`, its own `rename_symbol` outside §7's rule, and any shell or
write tool.

| Relay tool | Arguments | Forwarded to |
|---|---|---|
| `symbols` | `path`, optional `query` | the backend's symbol search or document symbols |
| `definition` | `path`, `line`, `column` | definition |
| `references` | same | references |
| `callers` | same | call hierarchy (incoming) |
| `implementations` | same | implementations |
| `rename` | same, `new_name` | the backend's rename that applies edits, under §7; **coders only** |

There is no rename preview: no backend offers one natively. The edit lands uncommitted in the
coder's own worktree, and `git diff` is the preview. Building one in the relay (an LSP
`textDocument/rename` returning the WorkspaceEdit unapplied) is left out (the person, Q2: build it
or drop it).

**Every answer says which revision it read (the person, Q1).** Each result carries
`{worktree, branch, head, dirty}`: `git rev-parse HEAD`, and whether the tree has uncommitted
changes. A branch moves; a head does not.

**Language detection per worktree**, first match from `code_intel.servers` (§9):

| Marker at the worktree root | Backend |
|---|---|
| `go.mod` | `gopls mcp` |
| `pyproject.toml`, `setup.py`, `requirements.txt` | `mcp-language-server` + `pyright-langserver --stdio` |
| `package.json` and `tsconfig.json` | `mcp-language-server` + `typescript-language-server --stdio` |
| `Cargo.toml` | `mcp-language-server` + `rust-analyzer` |

If nothing matches, the tools answer `no code-intel backend for this worktree (markers: …): use
grep`. Serena is a later mapping entry (the person, Q2: mcp-language-server first).

## 4. Which worktrees: the project's own, resolved fail-closed

- **The project's worktrees** are `git -C <project repo> worktree list --porcelain`, read at
  each backend start and every 30 s. The project repo is the relay's `--project` repo from the
  projects registry. A `path` argument is resolved (`filepath.EvalSymlinks`, every listed root too)
  and must lie inside a listed worktree. **The worktree is the deepest listed root that contains
  the path** (the longest root, compared path element by element, never as a string prefix, so
  `/a/b` does not contain `/a/bc`). Worktrees nest: this project has over a hundred under
  `.worktrees/`, inside the main checkout. A first match would route a coder's file to the main
  checkout's backend on `dev`, silently (r4, foureyes). Otherwise the call is refused, `not a worktree of <p>: <path>`, and no
  backend starts. Another project's tree, an untrusted clone and any unlisted directory (in `/tmp` or elsewhere) never get a server; a review worktree in `/tmp` that `worktree list` names is the project's own (the
  person, Q1; foureyes r1).
- **From a request's branch to its worktree** (the person, Q1): the board knows a branch, not a
  path. The relay maps a branch to the worktree that has it checked out, through the same
  `worktree list`. With none, or more than one, it fails with
  `branch <b> is checked out in no worktree of <p>` (or `in several: …`). It never falls back to
  the launch directory or any other tree.
- **A backend's environment** sets `GOTOOLCHAIN=local`, so gopls never downloads a Go toolchain
  (foureyes r1). Modules resolve as `go test` does in that worktree: the agent already built it.

## 5. Lifetime: no orphan servers

- **Children of the relay, dying with it, even when the relay is killed (the person, Q1):**
  every backend starts under a watchdog, `agentbus code-intel watch --parent <pid>:<pid_start>
  -- <backend argv>`. It is the same binary, with no new dependency.
  - The watchdog makes a new process group, starts the backend in it (and, in a hard mode, in
    the backend's own cgroup `ci-<n>`, §6), and waits on two things: the backend's exit and the
    relay's death.
    - Linux: a `pidfd` on the relay, opened at start, then the relay's `(pid, start)` checked
      again, so a relay already dead before the open is seen (foureyes r5).
    - macOS: kqueue `EVFILT_PROC`/`NOTE_EXIT` on the relay's pid, registered the same way, plus
      a 1 s `(pid, start)` check as backstop.
    - **No `Pdeathsig` on the watchdog** (r7, reviewer-codex): a `SIGKILL` on the relay's death
      would kill the watchdog before it could act. The watchdog has to outlive the relay to
      clean up.
  - When the relay dies, `SIGKILL` and crashes included, the watchdog kills the backend, then
    exits:
    - **in a hard mode:** it writes `1` to `ci-<n>/cgroup.kill` (Linux 5.14+). That kills every
      process in the cgroup, including a child that left the process group with `setsid` or a
      daemon the backend forked (foureyes r5).
    - **in soft mode:** it kills the process group. A descendant that left the group escapes it.
      This is stated, and the backends are configured not to daemonize (gopls without
      `-remote`, §11 step 0).
  - The relay's graceful exit kills each backend itself first.
  - **What this does not cover:** a `SIGKILL` of the watchdog itself together with the relay.
    Nothing in agent-bus sends that. In a hard mode, the backend's `ci-<n>` is still inside the
    agent's unit or scope, so it ends with the agent: `systemctl stop` of the unit, or the
    holder's end of the pane, kills the agent's whole cgroup (resource-limits §12.5).
- **The relay itself ends when its client ends.** Its stdin closes, and the relay kills its
  groups and exits. A park (cleaning-up-agents), a relaunch, a reload or a holder restart all end
  the client, so they end the relay too.
- **A removed worktree stops its backend (the person, Q1).** The 30 s sweep (§4) and every call
  check that each backend's worktree is still listed. A worktree gone from the list has its
  backend's group killed at once.
- A backend unused for `code_intel.idle_stop` (default 15 min) is stopped. At most
  `code_intel.max_backends` (default 2) run per agent, and starting one more stops the least
  recently used first.

## 6. Memory: counted against the agent's cap, refused before an OOM

The person (Q5) counts the backends in the agent's 30 % cap. Cap B is not built (§2), so the relay
enforces the budget itself:

- **The agent's cap** is `limits.agent_memory_max` once resource-limits step 4 adds it. Until
  then it is 30 % of `MemTotal`, the default that spec names (§8 there), as a named constant
  read in one place.
- **The agent's usage** is the RSS sum of the client's process tree (the relay's parent and its
  descendants, each pid once), read from `/proc` (Linux) or `ps` (macOS). It is the figure
  resource-limits §5 publishes without cap B.
- **Each backend has a budget, reserved before it starts:** `code_intel.backend_memory`
  (person, PersonOnly, default `2G`). A backend starts only when **usage + budget ≤ cap**.
  Otherwise the call is refused: `memory cap reached (<used> + <budget> > <cap>): code-intel
  unavailable, use grep`, and nothing starts (the person, Q5). Each running backend's budget
  counts as reserved, so two backends never both fit in the same headroom (r3, reviewer-codex:
  r2 checked usage only, so a backend could still cross the cap after the check).
- **Each backend is held to its budget:**
- **Each backend is held to its budget through the shared cgroup root** (the person, Q7,
  2026-11-27). Resource-limits' process-model amendment specifies one interface for the agent's
  cgroup root, and `agentbus run --cap`, cap B and code-intel all use it. The relay asks it for a
  sub-cgroup per backend and writes `memory.max` = budget, `memory.high` = 90 % of it and
  `memory.swap.max` = 0 there itself. The backend's watchdog (§5) starts it in that cgroup
  (`UseCgroupFD`). The kernel ends the backend, never the agent's other processes. By the mode
  the interface reports (the same one `roles doctor` shows):
  - **user unit or system unit** (`agentbus-agent@<role>.service`, `Delegate=yes`): a hard
    bound, in the agent's own delegated subtree, under the agent's cap B (the unit's
    `MemoryMax`).
  - **scope** (before the units exist, the person's `systemd-run --user --scope` fallback):
    the agent's own scope is the root. The holder starts the pane in it, in `agentbus.slice`,
    with `Delegate=yes` (resource-limits §12.5). The backend's `ci-<n>` is a sub-cgroup under
    it. There is no scope per backend and no `systemd-run` per backend start (r7,
    reviewer-codex: the person's "no systemd-run per spawn"). The scope is delegated, so the
    relay owns its children's files and writes their limits itself. The scope sits in the
    slice, so cap A covers the backends too (foureyes r6).
  - **soft** (macOS, or no delegated memory controller): the soft bound below, gated.
- **The soft bound, only in soft mode:**
    - Runtime limits: `GOMEMLIMIT` = budget for Go backends, `--max-old-space-size` for the
      Node ones.
    - A watch: the relay reads each backend's RSS every **2 s**, and a backend above its budget
      has its process group killed at once. Later calls to that worktree say so.
    - `RLIMIT_AS` is not used: it breaks Node and `-race`, as resource-limits §6 found.
    - **Gated:** in soft mode the relay starts **no backend** unless `code_intel.soft_memory_ok`
      (person, PersonOnly, default `false`) is true. It answers: `code-intel has no hard memory
      bound here (mode soft): set code_intel.soft_memory_ok, or use grep`.
    - **The residual, said plainly:** a backend that grows faster than its budget within 2 s,
      while the rest of the agent is also near the cap, can still push the agent over. §12 Q7
      asks the person whether to ship with this soft bound or wait for cap B.

In every hard mode the kernel enforces each budget. The relay's reservation stays as the early,
clear refusal.

## 7. Who gets which tools: from the launch, never from the environment

- **Role and agent come from the launch, and are checked against verified state.** The launcher
  writes `--agent` and `--role` into the client's MCP config. They are fixed for the relay's
  life, and the relay never reads `AGENT_BUS_AGENT` or another variable (the person, Q3). At
  start, before offering any tool, the relay checks them (r3, reviewer-codex: plain argv could
  be forged):
  1. Its parent process is the agent's client. The parent's `(pid, pid_start)` must equal
     the verified identity `{p}:agents` holds for `--agent` (`AgentSnapshot.PID`/`PIDStart`,
     `bus/stream.go:81`, written only through `putSnapshot`).
  2. `--role` equals `--agent`, and it is a role in the project's `roles.toml`. An agent is
     launched under its role's name (`agentbus pop <role>`), and the snapshot has no role field
     (`bus/stream.go:60-100`). An agent whose name is not a role gets no tool.

  A mismatch, or no verified snapshot, starts the relay with no tool and the error `code-intel:
  launch identity not verified`, shown in busmon.
  **What this is not:** a security boundary against the agent itself. Any agent with a shell can
  edit files directly, and a reviewer's "never write" rests on its prompt and tool rules
  (`roles.toml`: foureyes and reviewer-codex deny only `agentbus deliver`), not on code-intel.
  The checks stop a misconfigured or copied relay from offering `rename` to the wrong role.
  They do not stop a deliberate bypass, which needs no relay at all.
- **`rename` only for roles outside `code_intel.read_only_roles`** (default
  `foureyes,reviewer-codex`). For a read-only role it is not even listed.
- **A rename applies only in the coder's own task worktree (the person, Q3: one writer per
  file).** "Own" means the worktree's branch is the declared branch of a request this agent holds
  (`accepted` or `review`). The relay reads it from the board, maps it by §4, and refuses
  otherwise: `rename refused: <worktree> is not one of your task worktrees`.
  - Because the board learns a branch only at review today, `request accept` gains
    `--branch <b>`. `request branch <task> <b>` (target only, while accepted) records it later.
  - It goes in a new field, `request.work_branch`. The `branch` field that review and
    close-on-merge read is untouched.
  - **One owner per branch, enforced on the board** (r3, reviewer-codex: the target supplies
    the value). The script that records `work_branch` refuses a branch already declared by
    another agent's open request (`accepted`, `review` or `blocked`), as `work_branch` or as
    the review `branch`: `branch <b> belongs to <agent>'s request <task>`. It reads the
    board in the same call, so two agents cannot both claim one branch. The relay checks
    ownership again at each rename: the worktree's branch is this agent's declared branch,
    and no other open request names it. A branch nobody declared cannot be renamed in by
    anyone. A coder declares at accept, so its worktree is claimed before any other agent can
    claim it.
  - The read tools work on any of the project's worktrees: a reviewer reads the branch under
    review.

## 8. Declared by the launcher, seen by doctor and busmon

- **Opt-in:** `code_intel.enabled` (project, PersonOnly, default off). `code_intel.roles` (project,
  PersonOnly, default `coder,coder2,foureyes,reviewer-codex`).
- **Clients (the person, Q4):** Claude Code and Codex. For a listed role, `build` adds:
  - Claude Code: `--mcp-config` with a JSON string. The server is
    `"code-intel": {"command": "<agentbus full path>", "args": ["code-intel", "mcp", "--project", "<p>", "--agent", "<a>", "--role", "<r>"]}`.
    It is not strict: the person's own servers stay.
  - Codex: the same command and args through `-c mcp_servers.code-intel.command=…` and
    `-c mcp_servers.code-intel.args=[…]`.

  `preflight` checks `--mcp-config` in claude's help. kimi, grok, agy and adapters are unchanged
  until each MCP option is verified.
- **`ExtraHelpers` is filled:** for an agent whose role gets the helper, the relay and its
  backends' binaries, so their CPU is never background work.
- **Grep stays the fallback (the person, Q4).** The agent-bus skill says: when the `code-intel`
  tools are absent or refuse (no backend, cap reached, not your worktree), use grep and the
  files, as today. An agent on another client behaves as today.
- **`agentbus roles doctor`** (the person, Q4 and Q6):
  - per role, whether code-intel is active: enabled, role listed, client supported;
  - per pinned binary: present in its versioned directory, with the pinned version and checksum,
    or missing or wrong.

  `--publish` carries it as the other checks do.
- **busmon, per agent:**
  - On the Agents row and the agent's page, `code-intel ✓`, or `✗ <reason>` (no relay, cap
    reached, last start failed, identity not verified, soft bound not allowed).
  - Where cap B does not run, `✓ soft memory bound` (§6), so the person sees the weaker bound.
  - The page also lists each backend's worktree, head and age.

  The relay writes one hash, `{p}:helpers`, field `<agent>`. The value is
  `{state, backends:[{worktree, branch, head, server, pid, started}], error, at}` (`at` from
  Redis `TIME`), written at each start, stop and refusal, and at exit. It never touches
  `{p}:agents` (`putSnapshot` stays its only writer). A field older than 10 minutes shows
  `✗ stale`. The broker ACL gains this key for agent users, in the same step.

## 9. Settings (`config/keys.go`)

| Key | Default | Owner | Applies |
|---|---|---|---|
| `code_intel.enabled` | `false` | person, project | each launch |
| `code_intel.roles` | `coder,coder2,foureyes,reviewer-codex` | person, project | each launch |
| `code_intel.read_only_roles` | `foureyes,reviewer-codex` | person, project | each launch |
| `code_intel.servers` | §3's table | person, project | each backend start |
| `code_intel.idle_stop` | `15m` | person (PersonOnly, Q5) | live |
| `code_intel.max_backends` | `2` | person (PersonOnly, Q5) | live |
| `code_intel.backend_memory` | `2G` | person (PersonOnly) | each backend start |
| `code_intel.soft_memory_ok` | `false` | person (PersonOnly; per Q7) | each backend start |

No agent can raise any of them. Settings PRs conflict on the registry's shared lists
(merge-conflicts measure, 2026-11-26). If hot-files step 2 has not merged when this is built, the
settings step runs alone.

## 10. Install: a dedicated command, pinned, on the person's yes (Q6)

- **`agentbus code-intel install`**, per host, installs nothing until the person types yes. From
  an agent that is an ask; from the person's own shell it is y/n. It shows each binary, its pinned
  version, its source and its checksum first.
- **Checksums come from the repository's pins, never from the download site.** One file,
  `codeintel/pins.toml`, holds each binary's version, source and SHA-256. Each install method
  checks against it:
  - Go tools (`gopls`, `mcp-language-server`): `go install <module>@<tag>`, verified by the Go
    checksum database, then the built binary's SHA-256 is checked against the pin.
  - Node servers (`pyright`, `typescript-language-server`): `npm install --ignore-scripts` of the
    exact version, with the pinned `integrity`.
  - rust-analyzer: the tagged release asset, with the pinned SHA-256.

  A mismatch installs nothing. A version bump is a reviewed PR.
- **A dedicated versioned directory, called by full path:**
  `~/.local/share/agentbus/code-intel/<pins version>/bin/`. The relay runs backends and language
  servers from there by full path, never through `PATH`. Another version on the machine is never
  picked up.
- **`agentbus update` never downloads them.** It only reports changed pins: `code-intel pins
  changed: run agentbus code-intel install`. `roles doctor` flags missing or wrong versions.
- **The LSP servers are pinned too (the person, Q2)**, not only the wrappers. doctor checks their
  versions, not just their presence.

## 11. Build: steps in series

0. **Facts check**, no code. Each check gets one line in the PR or a note:
   - `gopls mcp` (gopls v0.20 or later) and its tool names.
   - mcp-language-server's tool names and which of them write, at the pinned commit.
   - Codex's `mcp_servers` override syntax.
   - Claude's `--mcp-config` accepting a JSON string.
   - Both clients start an MCP stdio server directly, with no shell wrapper, so the relay's
     parent is the client (§7's identity check; foureyes r4).
   - **gopls's shared daemon (`-remote=auto`)** (the person, Q1). Measure the RSS of two
     worktrees with and without it. A host daemon shares caches but sits outside every agent's
     tree, so it escapes §6's accounting and the per-agent cap. Decide from the numbers.

   A fact that does not hold comes back to architect.
1. **The relay with the gopls backend** (after resource-limits' cgroup-root interface, for §6's
   hard modes; until then only soft mode, gated): the tools and the allowlist, §4's resolution, §5's
   lifetime, §6's guard, §7's role and rename rules, `request accept --branch`,
   `request branch`, `work_branch`, and `{p}:helpers` with its ACL.
   - Tests use a fake MCP backend (a Go test binary):
     - a path outside the project's worktrees is refused;
     - a path in a worktree nested inside the main checkout routes to the nested worktree's backend, not the main one; `/a/bc` is not inside `/a/b`;
     - a branch in no worktree or in two is refused;
     - each answer carries HEAD;
     - a removed worktree kills its backend within one sweep;
     - the relay's exit, and its stdin closing, kill every backend group;
     - the relay `SIGKILL`ed: within 2 s no backend and none of its children is alive. The fake backend forks one grandchild that stays in the group and one that calls `setsid`. In a hard mode both die (`cgroup.kill`); in soft mode the one in the group dies and the `setsid` one is documented as escaping. Run on Linux in the suite, on macOS in step 2's live check;
     - the relay dead before the watchdog opens its `pidfd`: the `(pid, start)` re-check kills the backend;
     - idle stop and the least-recently-used limit;
     - without cap B and with `soft_memory_ok` false, no backend starts and the message names the key;
     - a cap-reached refusal when usage + budget > cap, with the running backends' budgets reserved;
     - a backend above its budget killed within one 2 s watch, the agent untouched;
     - `rename` absent for a read-only role, and refused outside the coder's own worktree;
     - an unlisted backend tool is never called;
     - role and agent taken from argv, with `AGENT_BUS_AGENT` set to something else;
     - a relay whose parent is not the agent's verified client, or whose `--role` differs from `--agent` or is no role, offers no tool;
     - `work_branch` refused when another agent's open request holds that branch, and two concurrent claims leave one owner.
   - One integration test runs a real `gopls mcp` on a scratch module when it is installed, and
     skips otherwise.
2. **The launcher and settings:** `--mcp-config`, Codex's `-c`, `preflight`, `ExtraHelpers`, the
   six keys, and the skill's grep fallback.
   - Tests: each client's argv with code-intel on and off, a role not listed, and the dry run.
   - **A live check per client, recorded in the PR:** a coder and a reviewer launched on each
     client list the tools; `rename` is absent for the reviewer (the person, Q4: tested, not
     assumed).
3. **Install and doctor:** `codeintel/pins.toml`, `agentbus code-intel install`, the doctor
   section, and `update`'s pin notice.
   - Tests stub `go`, `npm` and the network:
     - nothing is installed without yes;
     - a checksum mismatch installs nothing;
     - the relay ignores a same-named binary on `PATH`.
4. **busmon:** the helper cell and the page section. Goldens regenerated.
5. **The non-Go servers** (pyright, typescript-language-server, rust-analyzer) with their pins.

Each step is its own PR and review, in series (merge-conflicts §3, sibling-enforce).

## 12. The person's answers (ask `1795733274522-0`, all typed)

1. One relay, one backend per worktree. Plus:
   - only the project's worktrees;
   - stop on worktree removal;
   - children die with the relay, on park and restart too;
   - HEAD in every answer;
   - branch to worktree through `git worktree list`, failing clearly;
   - one line on gopls `-remote=auto`.

   §3, §4, §5, §11.
2. mcp-language-server first. Plus an allowlist of tools, the rename preview built or dropped
   (dropped), and the LSP servers pinned and checked. §3, §10.
3. Coders only rename. Plus the role taken from the launch identity, never the environment, and
   a rename only in the coder's own task worktree. §7.
4. Claude Code and Codex now, MCP config and enforcement tested per client. Plus the grep
   fallback in the skill and doctor showing per role. §8, §11.
5. 15 minutes and 2 servers, counted in the 30 % cap. Plus a clear refusal before an OOM, and
   both values person-only. §6, §9.
7. **Answered (ask `1795734341064-0`, 2026-11-27, typed): the process model.** One systemd
   unit per agent (`agentbus-agent@<role>.service`, cap B = its `MemoryMax`), `Delegate=yes`
   so the relay makes one sub-cgroup per language server itself, user units with linger first,
   system units later. One cgroup-root interface is shared by code-intel, `agentbus run --cap`
   and cap B, with the `systemd-run --user --scope` fallback until the units exist. `roles
   doctor` reports the mode and whether the memory controller is delegated. It is specified in
   resource-limits, ahead of its step 4. Here: §6 uses that interface, and the soft bound is
   left only for soft mode, gated by `soft_memory_ok`.
6. A dedicated install command. Plus:
   - update only reports changed pins;
   - checksums from the repository's pins;
   - a versioned directory called by full path.

   §10.

Item 6 of the request (fuzzy or semantic search through one shared read-only index,
codebase-memory-mcp or Zoekt) is not in this spec. It is evaluated separately, later.
