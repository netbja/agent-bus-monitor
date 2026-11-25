# Sandboxed agent launch (sandboxed-launch)

Status: IDEA spec r3, for review (subject `sandboxed-launch-spec`). Not built. Base: origin/dev
`af527c19`. Author: architect, from master's directive (cmd `1795559518593-0`, task
`specs-from-person-docs`). This is idea 3 of 4 from the person's note "ideas borrowed from
PiBot", item 1 there. The note's references are PiBot
`packages/external-agents-contract/src/agents/catalog.ts` (`AgentAllowances`) and
`packages/execution/src/sandbox/` (Apache-2.0), used for the idea only. **New dependency, named by
the note:** `@anthropic-ai/sandbox-runtime` (`srt`), or bubblewrap directly on Linux (§5 Q1).
The questions for the person are last (§5).
r2: reviewer-codex's reject of r1 (verdict `1795575649339-0`). A default of `off` gathers no
evidence, so the trial is an explicit cohort of roles the person names (§4, Q4). A violation
report is parsed into fixed fields, and its raw line is never stored. A per-agent bound sits
above the per-target throttle (§2).
r3: foureyes's reject of r2 (verdict `1795575792907-0`). A base fixed in Go that misses daily
work breaks every role until a code change. The §2 table is now the spike's starting candidate,
and the spike measures each role's daily work (§3, item 7). The bases are written from the spike's
output.

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

**A fixed base policy in Go** (`rolecfg`), which a role's table can only narrow. Because a
role cannot widen it, a gap in the base breaks every role until a code change. So the table
below is **the spike's starting candidate, not a decision**. The base that is built is the
spike's measured output (§3, item 7). It names, per base, every path and host it holds and the
daily act that needs each one.

| | Starting candidate (to be measured) |
|---|---|
| Read | everything but `~/.ssh`, `~/.gnupg`, the secret globs of `[tool_rules] deny`, `~/.config/agentbus/notify.toml`, and other projects' trees |
| Write | the role's worktree; **the shared git dir** (`git rev-parse --git-common-dir`, the main checkout's `.git`: objects, refs, `worktrees/`), where a worktree's commits land; its client's own config: `~/.claude` **and `~/.claude.json`** at the home root, `~/.codex`, `~/.kimi`, `~/.grok`, …; its temp dir; the Go build and module caches; the npm cache only if a role's daily work needs it |
| Network | the client vendor's API and auth hosts; the forge's API host; the broker; **the Go module proxy and checksum database** (a cold module cache); the hosts of the client's web tools and of remote MCP servers (context7's, for one) |
| Local sockets | the production broker, and **the disposable test broker's unix socket** in `/tmp` (CLAUDE.md, `docs/testing-redis.md`); local MCP servers (codegraph, codebase-memory) |

**A per-client allowance table** (the PiBot `AgentAllowances` idea) names each client's config
dir and vendor hosts. **A per-role narrowing** in `roles.toml` may remove a path or a host. It may
never add one: the launch takes the intersection of the role's table and the base, and refuses a
role entry that names something outside the base. For example, a reviewer's worktree can be
read-only.

**Delivering roles** (master, docs) need to push. Push credentials are in `~/.ssh` or a
credential helper that the base refuses. So deliver gets a second fixed base, `deliver`, which
adds only the forge's git host and the one credential path the forge setting names (§5 Q2).

**Violations reach the bus as hints.** `srt` reports a denied write or connection. srt's own
source says its Linux reports are read from the untrusted process's memory and "must never gate a
policy decision". So an event is a hint for the person, never an input to any judgement: not the
dispatcher's, not `PopDone`'s, not sentinel's. A report's text is controlled by the confined
process, so:

- **Parsed, not copied.** The holder parses each report into fixed fields and drops the raw
  line. The fields are `op` (`read`, `write` or `connect`) and `target`. For a connection,
  `target` is the host and port only, with no scheme, user info, path or query. For a file, it is
  the path. Any report that does not parse is counted, never stored.
- **Redacted as a whole.** If `health.Sensitive` matches `target`, it is stored as `[redacted]`.
  A path longer than 200 runes is cut to its first three components and `…`.
- **Bounded per agent.** At most one event per minute for each agent and target, and at most 20
  per hour for each agent in all. Past that, one `sandbox-denied` event per hour says how many
  were dropped. Varying the target cannot flood the stream.
- Written to `{p}:events` as `sandbox-denied` (`agent`, `op`, `target`, time). That is a new
  kind in `AppendEvent`'s closed list (`bus/events.go:28`). busmon shows it on the agent's row.

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
7. **Each role's daily work, measured, under the candidate base.** It runs inside `srt` once per
   role kind, coder, reviewer and master/docs, and records each denial:
   - commit from a worktree. That writes the shared git dir. A fetch, and a push for the
     `deliver` base;
   - `go build` and `go test ./...` on a cold module cache, with the disposable test broker. A
     reviewer starts its own `redis-server` on a unix socket. `apply-seccomp` blocks creating
     new `AF_UNIX` sockets, so that may fail for every Redis test. Whether a path-scoped allow
     exists on Linux is measured, not assumed;
   - a WebFetch and a WebSearch, a local MCP call (codegraph, codebase-memory) and a remote one
     (context7);
   - the client's start, token refresh and resume (item 2).

   The spike's report gives each base's final list, with the act that needs each entry. A
   role kind whose daily work cannot pass stays out of the cohort.

If any of 1, 2 or 4 fails for a client, that client launches unsandboxed and shows `sandbox off`
in `roles doctor` and busmon (§5 Q3). A role in the cohort keeps that state.

## 4. Build steps (after the spike), tests, and what this does not do

1. `rolecfg`: the base, the allowance table, narrowing by intersection, and the per-launch srt
   settings file. A `sandbox.roles` setting, PersonOnly, empty by default: the roles that launch
   sandboxed (the cohort, Q4). `all` is accepted once the person decides so.
2. The holder launches through `srt`, and identity follows the client's pid.
3. `sandbox-denied` events, and busmon's mark on the agent's row.

**Tests:** a role entry naming a path or host outside the base is refused at launch. Violation
reports: a denied URL `https://user:tok@host/p?token=…` is stored as `connect host:443`. A path
that `health.Sensitive` matches is stored as `[redacted]`. A line that does not parse is not
stored. 100 reports with distinct targets in an hour store 20 events and one "dropped 80"
event. The settings
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
- (B) a role in `sandbox.roles` on that client is refused.

**Q4. How it goes live.**
- **(A) recommended: a named cohort first.** You list a few roles in `sandbox.roles` (for example
  architect and a reviewer). Each one runs sandboxed for a week. You read its `sandbox-denied`
  events for false refusals, then widen the list, or set `all`.
- (B) every role at once.
