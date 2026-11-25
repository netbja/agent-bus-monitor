# Sandboxed agent launch (sandboxed-launch)

Status: IDEA spec r1, for review (subject `sandboxed-launch-spec`). Not built. Base: origin/dev
`af527c19`. Author: architect, from master's directive (cmd `1795559518593-0`, task
`specs-from-person-docs`). This is idea 3 of 4 from the person's note "ideas borrowed from
PiBot", item 1 there. The note's references are PiBot
`packages/external-agents-contract/src/agents/catalog.ts` (`AgentAllowances`) and
`packages/execution/src/sandbox/` (Apache-2.0), used for the idea only. **New dependency, named by
the note:** `@anthropic-ai/sandbox-runtime` (`srt`), or bubblewrap directly on Linux (§5 Q1).
The questions for the person are last (§5).

## 1. Today

A role's client runs as the person's OS user, with the person's whole home directory and
network. Two narrower things exist:

- **The client's own rules.** For Claude Code, agentbus writes per-launch `--settings`. Its
  denies (`roles.toml [tool_rules]`) keep the Read and Edit tools off `.env`, keys and
  `~/.ssh`, but `roles.toml`'s header says it plainly: "processes an agent starts … can still
  read env". A shell command is not a Read tool.
- **The client's own sandbox,** where it has one: Codex `sandbox = "read-only"` for
  `codex-review`, and agy `sandbox = true`. Claude Code can sandbox its Bash tool. None of them
  wraps the client process itself.

So nothing stops a role's process, or what it starts, from reading another worktree, another
project, or a key file, or from reaching any host.

## 2. The idea

The holder starts each role's client inside an OS sandbox. The launch line becomes
`srt --settings <per-launch file> <client argv>`. The holder still owns the pane and the PTY:
only the client's process tree is confined.

**A fixed base policy in Go** (`rolecfg`), which a role's table can only narrow:

| | Base: the widest any role gets |
|---|---|
| Read | everything but `~/.ssh`, `~/.gnupg`, the secret globs of `[tool_rules] deny`, `~/.config/agentbus/notify.toml`, and other projects' trees |
| Write | the role's worktree; its client's own config dir (`~/.claude`, `~/.codex`, `~/.kimi`, `~/.grok`, …); its temp dir; the Go and npm caches |
| Network | the client vendor's API and auth hosts; the forge's API host; the broker |

**A per-client allowance table** (the PiBot `AgentAllowances` idea) names each client's config
dir and vendor hosts. **A per-role narrowing** in `roles.toml` may remove a path or a host. It may
never add one: the launch takes the intersection of the role's table and the base, and refuses a
role entry that names something outside the base. For example, a reviewer's worktree can be
read-only.

**Delivering roles** (master, docs) need to push. Push credentials are in `~/.ssh` or a
credential helper that the base refuses. So deliver gets a second fixed base, `deliver`, which
adds only the forge's git host and the one credential path the forge setting names (§5 Q2).

**Violations reach the bus as hints.** `srt` reports a denied write or connection. agentbus
forwards each one to `{p}:events` as `sandbox-denied` (agent, line, time). That is a new kind in
`AppendEvent`'s closed list (`bus/events.go:28`). It is rate-limited to
one per minute per agent and path or host. busmon shows it on the agent's row. srt's own source
says its Linux reports are read from the untrusted process's memory and "must never gate a policy
decision". So an event is a hint for the person, never an input to any judgement: not the
dispatcher's, not `PopDone`'s, not sentinel's.

## 3. What must be proven first: a spike, before any build

The base relies on facts that only a run can prove. **Step 0 is a spike on this host and on
macOS. It writes no product code.** Its report decides whether step 1 happens:

1. **The broker.** On Linux, `srt` removes the network namespace, and TCP goes through its SOCKS
   proxy. go-redis does not read `ALL_PROXY`. So either `agentbus` dials through the proxy, a
   small `bus` dialer option, or the broker's unix socket is bind-mounted and allowed. Which one
   works with `apply-seccomp`'s block on new `AF_UNIX` sockets?
2. **The clients.** Do Claude Code, Codex, Kimi and Grok start, authenticate, refresh their
   tokens and resume a session inside `srt`, with only their vendor hosts allowed? Which hosts
   are those, measured from the spike's denied list and not guessed?
3. **Git.** Fetch over HTTPS through the proxy, and push over SSH for the `deliver` base: `ssh`
   ignores SOCKS without a `ProxyCommand`.
4. **The holder.** A client under `srt` keeps its PTY, resize, `/clear` typed by the dispatcher,
   and the holder's process identity check (the verified pid becomes `srt`'s child).
5. **Cost.** Start time, and memory per agent.
6. **Where violations surface.** The `srt` CLI's reports must reach the holder without being
   written into the pane, where they would garble the client's screen. Can they go to a file
   or a descriptor? Or is a small wrapper on srt's library API needed, which is more Node code?

If any of 1, 2 or 4 fails for a client, that client launches unsandboxed and shows `sandbox off`
in `roles doctor` and busmon (§5 Q3).

## 4. Build steps (after the spike), tests, and what this does not do

1. `rolecfg`: the base, the allowance table, narrowing by intersection, and the per-launch srt
   settings file. A `sandbox.mode` setting (`off`, `on`), PersonOnly, default `off`.
2. The holder launches through `srt`, and identity follows the client's pid.
3. `sandbox-denied` events, and busmon's mark on the agent's row.

**Tests:** a role entry naming a path or host outside the base is refused at launch. The settings
file for each team role is checked against a golden file. Under a fake `srt`, the launch argv
wraps the client and the identity record names the client's pid. A `sandbox-denied` event never
changes a `PopDone` or stall judgement (a test feeds one and compares). A disposable-host
integration test runs only when explicitly enabled: inside `srt`, a read of `~/.ssh`, a write
to another worktree, and a connection to a host off the list are each refused.

**Not doing:** no container per agent, no ACP, and no change to the clients' own sandboxes. No
policy decision rests on a violation report. The person's own shell stays unconfined.

## 5. Questions for the person

**Q1. The sandbox tool.**
- **(A) recommended: `srt`.** One tool on Linux and macOS, with a domain allowlist through its
  proxies. It costs a Node dependency and, on Linux, bubblewrap and socat.
- (B) bubblewrap directly, from Go. No Node, but Linux only, and the network is all or nothing
  (no domain allowlist).

**Q2. Push credentials for master and docs.**
- **(A) recommended: a second fixed base, `deliver`,** that adds only the forge's git host and the
  one credential path the forge setting names.
- (B) master and docs run unsandboxed.

**Q3. A client that fails the spike.**
- **(A) recommended: it runs unsandboxed,** shown as `sandbox off`, until a fix.
- (B) it is refused while `sandbox.mode = on`.

**Q4. The default once built.**
- **(A) recommended: `off`, then `on` after a week of `sandbox-denied` events without a false
  refusal.**
- (B) `on` at once.
