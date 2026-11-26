# Code intelligence helpers for coding agents (code-intel)

Status: spec r1, for review (subject `code-intel-spec`). Not built. Base: origin/dev `f53e4fca`.
Author: architect, from the person's typed request relayed by master (cmd
`1795733140669-0`, task `code-intel-spec`). The person may send a wider summary. The shared
dev-index rule (`abm-origin-dev`) goes to docs separately, not here.

## 1. Goal

Coder and reviewer roles in every project agent-bus drives get **exact, branch-local** code
navigation: callers, references, implementations, symbols, and (for coders) a safe rename. It
works in any project language, through an MCP helper the launcher gives the agent's client.
Today each agent greps, or uses whatever MCP servers its user configured by hand: a shared
index, built from another tree, that may not match the agent's branch.

## 2. What exists today

- **The launcher** builds each client's argv (`rolecfg/launch.go:298`, `build`). Claude Code gets
  `--settings <json>`. Codex gets `-c key=value` overrides. Both clients take MCP servers per
  launch, with no file in the user's home:
  - Claude Code: `--mcp-config <json|file>`. It is additive to the user's servers unless
    `--strict-mcp-config` is given. `quota/claudeprobe.go:88` uses the strict form.
  - Codex: `-c mcp_servers.<name>.command=…` and `-c mcp_servers.<name>.args=[…]`.

  `preflight` (`rolecfg/launch.go:411`) already checks a client's `--help` for every flag a
  launch passes.
- **`ExtraHelpers`** is declared and read, but never filled. `Facts.ExtraHelpers`
  (`agentstate/background.go:113-120`) lets the background judgement ignore an MCP helper's CPU
  (`:219`). `HostRuntime.ExtraHelpers` (`dispatch/hostruntime.go:50`, `:303`) is the hook that
  would supply it. No caller sets it today.
- **Memory:** an MCP stdio server is a child of the client, so it sits in the agent's terminal
  cgroup, cap B (`2026-11-25-agent-resource-limits-design.md` §4,
  `limits.agent_memory_max`, 30 %). It is counted in the agent's per-agent figure (§5 of that
  spec). Nothing new is needed for the cap itself.
- **Worktrees:** coders work in one worktree per task, outside the launch directory (for
  example `/data/projects/<p>-wt/<task>`), and change worktree between tasks without
  relaunching. A helper rooted at the launch directory would see the wrong branch.
- **Settings** live in `config/keys.go`. `roles.toml` gains no key: a new `roles.toml` key broke
  the previous release's update once (release check, 2026-11-25).

## 3. The design: one proxy per agent, one backend per worktree

```
client (claude / codex)
 └─ agentbus code-intel mcp          one per agent, stdio MCP, started by the client
     ├─ backend for worktree A        gopls mcp            (go.mod found)
     └─ backend for worktree B        mcp-language-server  (pyproject.toml found, pyright)
```

**`agentbus code-intel mcp`** is the one MCP server the launcher declares, named `code-intel`.
It is Go, in this repository, with no new dependency: MCP over stdio is JSON-RPC 2.0, one message
per line. It offers a fixed, small tool set, the same in every language:

| Tool | Arguments | For |
|---|---|---|
| `symbols` | `path`, optional `query` | the symbols of a file or package, or matching a name |
| `definition` | `path`, `line`, `column` (or `symbol`) | where it is defined |
| `references` | same | every use |
| `callers` | same | the functions that call it |
| `implementations` | same | the types that implement an interface, and the reverse |
| `rename` | same, `new_name` | a safe rename across the worktree, **coders only** (§6) |

Each call carries a path. The proxy finds the path's worktree (`git rev-parse --show-toplevel`
from the path's directory). It finds or starts that worktree's **backend** and forwards the call,
translated to the backend's own tool. The answer is the backend's, with paths relative to the
worktree. A path outside every git worktree is refused.

- **Branch-local by construction.** A backend runs with its worktree as its root and sees exactly
  that tree, uncommitted edits included. There is no persistent or shared index. The language
  servers keep their own caches in memory only, and `gopls` its module cache.
- **Started and stopped with the agent.** The client starts the proxy and ends it when the client
  ends. The proxy ends its backends when it ends (its process group). A backend unused for
  `code_intel.idle_stop` (default 15 min) is stopped. At most `code_intel.max_backends` (default
  2) run per agent; starting a third stops the least recently used.
- **Language detection per worktree**, first match, from a mapping in config
  (`code_intel.servers`, §7), checked from the worktree's root:

  | Marker | Backend |
  |---|---|
  | `go.mod` | `gopls mcp` |
  | `pyproject.toml`, `setup.py`, `requirements.txt` | `mcp-language-server` + `pyright-langserver --stdio` |
  | `package.json` with `tsconfig.json` | `mcp-language-server` + `typescript-language-server --stdio` |
  | `Cargo.toml` | `mcp-language-server` + `rust-analyzer` |

  The mapping is extensible: one entry is a marker, a backend command and its arguments. No
  match: the tools answer `no code-intel backend for this worktree (markers: …)`, and the agent
  greps as today.
- **Serena** (oraios/serena) is the alternative backend for non-Go languages: more languages,
  real LSP servers, more tools. It is Python, run through `uv`, and much heavier. Its editing and
  shell tools must be switched off for this use. §9 Q2 asks which backend goes first. The proxy
  makes it one mapping entry either way, so the other one can follow.

## 4. Declared through the launcher, opt-in per project and per role

- `code_intel.enabled` (project, PersonOnly, default **off**). Off: nothing changes for anyone.
- `code_intel.roles` (project, PersonOnly, default `coder,coder2,foureyes,reviewer-codex`): the
  roles that get the helper once it is enabled. Other roles never get it.
- At launch, for a listed role, `build` adds the server:
  - Claude Code: `--mcp-config` with a JSON string
    `{"mcpServers":{"code-intel":{"command":"<agentbus path>","args":["code-intel","mcp","--role","<role>"]}}}`.
    It is not strict: the person's own servers stay.
  - Codex: `-c mcp_servers.code-intel.command=<agentbus path>` and
    `-c mcp_servers.code-intel.args=["code-intel","mcp","--role","<role>"]`.

  `preflight` adds `--mcp-config` (claude) to the flags it checks. kimi-cli, grok, agy and
  adapter roles are not given the helper in this spec (§9 Q4). Their launch is unchanged.
- **`ExtraHelpers` is filled:** the dispatcher's `HostRuntime.ExtraHelpers(agent)` returns
  `agentbus` plus the backend command names (`gopls`, `mcp-language-server`, the language
  servers) for an agent whose role gets the helper. Their CPU is then a helper's, never
  background work (`background.go:219`).

## 5. Seen: roles doctor and busmon

- **`agentbus roles doctor`** gains a `code-intel` section when it is enabled. For each mapping
  entry, it checks that the backend and language server binaries exist on `PATH` and that their
  versions equal the pinned ones (§8). The result is ok, missing or other version, per binary.
  `--publish` carries it as the other checks do.
- **busmon, per agent:** a helper cell on the Agents row and the agent's page.
  - `code-intel ✓` (the proxy is up, with its backends and their worktrees on the page).
  - `code-intel ✗` (the role gets it, but the proxy is not running or its last backend start
    failed, with the reason).
  - Nothing for a role without it.

  The proxy writes its state to one hash, `{p}:helpers`, field `<agent>`. The value is
  `{state, backends:[{worktree, server, pid, started}], error, at}` (`at` is Redis `TIME`),
  written at start, at each backend start or stop, and at exit. It runs as the agent
  (`AGENT_BUS_AGENT`), like the agent's own commands. It does not touch `{p}:agents`
  (`putSnapshot` stays the only writer there). A field older than 10 minutes, or whose
  writer's pid is gone, shows `✗ stale`. The broker ACL gains this key for agent users, in the
  same step.

## 6. Safety

- **Reviewers never edit.** `--role` tells the proxy which tools to offer. `rename` is offered
  only to roles not in `code_intel.read_only_roles` (default `foureyes,reviewer-codex`), and the
  backend's own edit tools are never forwarded. A rename returns the edits it applied: the
  agent's own worktree changes, as any edit the agent makes, and nothing is committed.
- **The proxy runs no repository code itself.** The backends do run the project's toolchain, as
  `go test` does (`gopls` runs `go list`, a language server reads the project's config). That is
  in the agent's process tree, under cap B, never in the dispatcher (CLAUDE.md: the dispatcher
  never runs repository code). For a repository the person did not write, the folder-trust
  ask's context (`cmd/agentbus/trust.go:112-113`, "settings, hooks and MCP servers: … look at it
  first") also names code-intel's language servers.
- **Nothing installed without the person's typed yes** (§8).

## 7. Settings (`config/keys.go`)

| Key | Default | Owner | Applies |
|---|---|---|---|
| `code_intel.enabled` | `false` | person, project | at each launch |
| `code_intel.roles` | `coder,coder2,foureyes,reviewer-codex` | person, project | at each launch |
| `code_intel.read_only_roles` | `foureyes,reviewer-codex` | person, project | at each launch |
| `code_intel.servers` | the §3 table | person, project | at each backend start |
| `code_intel.idle_stop` | `15m` | person | live |
| `code_intel.max_backends` | `2` | person | live |

The settings registry's shared lists conflict between parallel settings PRs (merge-conflicts
measure, 2026-11-26). If hot-files step 2 has not merged when this is built, the settings step
runs alone.

## 8. Install: pinned, checksummed, on the person's yes

- **`agentbus code-intel install`** shows what it would install, then installs nothing until the
  person types yes. That is an ask in busmon when run from an agent, or a y/n when the person
  runs it.
  - What it shows: each binary, its pinned version, its source, and its checksum.
  - Where it installs: one host-wide directory, `~/.local/share/agentbus/code-intel/<name>@<version>/`.
    The proxy runs binaries from there first, then from `PATH`.
- **Pinned to tagged releases with checksums, never `curl | bash`, never a branch head:**
  - Go tools (`gopls`, `mcp-language-server`): `go install <module>@<tag>`. The Go checksum
    database verifies the module. The pinned tag and the module's `go.sum` line are recorded in
    this repository.
  - Node language servers: `npm install --ignore-scripts` of an exact version into the install
    directory, with the `integrity` hash recorded.
  - rust-analyzer: its tagged release asset, with the SHA-256 recorded.
  - Serena, if chosen: `uv tool install` of a tagged release, hashes pinned.
- The pinned versions and checksums live in one file in this repository, `codeintel/pins.toml`.
  A version bump is a reviewed PR. `roles doctor` reads the same file.
- Step 0 of the build checks the facts this spec assumes before any code: `gopls mcp` (gopls
  v0.20 or later), its tool names, mcp-language-server's tool names, and Codex's
  `mcp_servers` override syntax. A fact that does not hold goes back to architect, not around.

## 9. Questions for the person (ask `1795733274522-0`, in French)

1. **One proxy, a backend per worktree?** Recommended: yes, the design above. The alternative is
   one server per launch, rooted at the launch directory. That is simpler, but wrong whenever the
   agent works in another worktree, which coders do for every task.
2. **Which backend first for non-Go languages?** Recommended: mcp-language-server. It is Go, one
   pinned `go install`, light, and wraps the same LSP servers. Serena comes later as one more
   mapping entry if its extra tools are wanted. Alternative: Serena first. It covers more
   languages, but it is Python/uv, heavier, and has edit and shell tools to lock down.
3. **Who may rename?** Recommended: coders only. Reviewers get the read-only tools.
4. **Which clients?** Recommended: Claude Code and Codex now. kimi, grok and agy later, each once
   its MCP launch option is checked.
5. **Limits per agent?** Recommended: a backend stops after 15 minutes unused, and at most 2 run
   per agent. Both are your settings.
6. **Install path?** Recommended: `agentbus code-intel install` per host, on your typed yes,
   versions and checksums shown first, pins in the repository. Alternative: part of
   `agentbus update`, still on your yes.

Item 6 of the request (fuzzy or semantic search through one shared read-only index,
codebase-memory-mcp or Zoekt) is not in this spec. It is evaluated separately, later, as the
person said.

## 10. Build: steps in series

0. **Facts check** (§8, last bullet). No code.
1. **The proxy with the gopls backend:** `agentbus code-intel mcp`, the tool set, worktree
   routing, idle stop and the backend limit, `{p}:helpers` and its ACL. Tests use a fake backend
   (a Go test binary that speaks MCP): routing by worktree, a path outside a worktree refused,
   `rename` absent for a read-only role, idle stop, the LRU limit, the proxy's exit ending its
   backends, the `{p}:helpers` fields. One integration test runs a real `gopls mcp` on a scratch
   module when gopls is installed, and skips otherwise.
2. **The launcher and the settings:** `--mcp-config` and the Codex `-c` overrides for listed
   roles, `preflight`'s flag, `ExtraHelpers` filled, the six keys. Tests: the argv for each
   client with code-intel on and off, a role not listed, the dry-run output.
3. **Install and doctor:** `codeintel/pins.toml`, `agentbus code-intel install`, `roles doctor`'s
   section. Tests stub `go`, `npm` and the network: nothing is installed without yes, a checksum
   mismatch is refused.
4. **busmon:** the helper cell and the page section. Goldens regenerated.
5. **The second backend** (Q2's answer), its mapping entries and pins.

Each step is its own PR and review, in series (merge-conflicts §3).
