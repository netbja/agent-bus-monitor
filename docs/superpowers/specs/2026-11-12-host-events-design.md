# Host events: network drops and reboots, recorded and told (self-repair watcher addendum)

- Status: draft, for review (coder, foureyes, reviewer-codex) and for the person's answers (§7)
- Date: 2026-11-12
- Author: architect
- Task: `selfrepair-host-events`
- Base: `origin/main` at 394a7261
- Companion of: [the self-repair watcher](2026-11-09-self-repair-watcher-design.md) (answered,
  not built). This spec adds a light **system view** to the same loop. It changes none of the
  watcher's rules; it gives R3 (network) and R4 (reboot) a better fact and a record.

## 1. Why

On the evening of 2026-11-11 the WiFi dropped for about 18 s and nothing recorded it. The watcher's
network fact today is indirect: `quota-freshness` (the provider's quota reader reaches the
provider about once a minute) and `llm-endpoint`. A drop shorter than a minute leaves no trace,
and a reboot is noticed only as dead agents. When agents then show API errors or a stall, nobody
can say whether the host lost its network or rebooted at that time.

Constraint (the person): simple, fast, reliable; no heavier project.

## 2. What it is

Two facts, measured inside the dispatch service's existing loop (the watcher's loop: no new
daemon, no new dependency, nothing that needs root), recorded with the existing health
mechanism, and told to master on the bus.

### 2.1 Network: down and up

Two probes, cheapest first:
- **Link, every 2 s (Linux).** Read `/proc/net/route` (is there a default route?) and
  `/sys/class/net/<iface>/operstate` of that route's interface. Two small file reads; this is
  what catches an 18 s WiFi drop. macOS has neither file: there, only the upstream probe runs
  (§7 Q3).
- **Upstream, every `host.probe_every` (default 30 s), skipped while the link is down.** A TCP
  connect, 3 s timeout, to port 443 of the API host of each provider the project's roles use
  (from the launch profiles: e.g. `api.anthropic.com`, `api.openai.com`), at most three hosts. No
  TLS, no request, nothing sent: a connect proves the route, DNS and the provider's edge answer.
  Uses Go's `net` package only.

**Down** = the link has no default route or its interface is not `up`, or every upstream host
failed twice in a row. **Up** = the link is back and one upstream connect succeeded (on macOS:
one connect). The duration is measured on the monotonic clock; the times written come from Redis
`TIME`, which still answers during a drop because the broker is local (`127.0.0.1:6380`).

### 2.2 Machine start and resume

- **Reboot.** The dispatcher already reads the boot id (`dispatch.BootID`,
  `/proc/sys/kernel/random/boot_id`). It keeps `{p}:host` (a small hash: `boot_id`, `last_seen`),
  written fenced each pass (Redis `TIME`). At start, a boot id different from the stored one is a
  **reboot**: the host was down at most from `last_seen` to the new boot. macOS: the boot id is
  `kern.boottime` read through `golang.org/x/sys/unix` (already a dependency).
- **Resume from suspend.** Same boot id, and the wall clock moved more than 30 s further than the
  monotonic clock between two passes: the host slept. (The watcher already uses this to restart
  R1's grace, §4.4 there.)

### 2.3 What is recorded

- A health check **`host-network`**: `ok` while up, `warn` while down (never `fail`: a network
  drop is not something sentinel can repair). Its transitions go to `{p}:health:log` through
  `fencedHealthPut`, as every check's do: `ok → warn` at the drop, `warn → ok` at the return,
  each with its time. That is the record that was missing, with no new key. The detail names
  the cause: `link wlan0 down`, or `no default route`, or `upstream unreachable (3/3 hosts)`.
- A health check **`host-boot`**, `ok` with the detail `booted 07:42, previous last seen 02:10`
  after a reboot (`warn` for one hour after it, so it shows in busmon's health and is logged as a
  transition), and `resumed 08:15 after 6h12m asleep` after a suspend.
- When the herdr-exit spec's `{p}:events` stream exists (its §6.5, not built yet), the same
  events also go there: `net_down`, `net_up`, `boot`, `resume`. Until then the health log is
  the record.

### 2.4 What is told, and what is done

| Event | Told to master (one report from `dispatch`, fenced) | Action |
|---|---|---|
| **Network down** | nothing at the drop (master's own provider is likely unreachable then) | none. R3 already holds relaunches while the network is down (watcher §4.3) |
| **Network up** | one line per episode: `network: down 18s (21:04:12–21:04:30), link wlan0 down; API errors in that window: coder (claude-code) 1` | none: agents whose processes survived need nothing; R1 handles any that died (watcher §4.3) |
| **Reboot** | one line at the first pass after start: `host rebooted: last seen 02:10, up since 07:42; agents dead: 4; self-repair: on (relaunching per R1) / off` | the existing paths only: R1 relaunch-with-resume when self-repair is on and the workspace is open, else the watcher's existing notice to run `agentbus up <p> --resume` (watcher §4.4) |
| **Resume from suspend** | one line: `host resumed after 6h12m asleep` | none beyond R1's grace restart (watcher §4.4) |

Drops shorter than `host.report_min` (default 5 s) are recorded in the health log but not
reported: a 2 s blip is noise for master.

**Linking API errors.** At the `up` report, the watcher counts API errors that clients recorded
between 30 s before the drop and 60 s after the return. The only verified source today is Claude
Code's transcript: entries with `isApiErrorMessage: true` and a `timestamp` (the field
`quota/claude.go` already parses). The watcher reads only the tail of each live Claude Code
agent's transcript (the last 256 KB), once per `up` event. Other clients show `unknown` until a
verified source exists for them; no new parser is written for this spec.

### 2.5 What it never does

Changes network or system settings, restarts the network, Docker, the broker, herdr or any
service on a host event, relaunches anything beyond the watcher's existing R1, or sends anything
to a provider beyond a TCP connect.

## 3. Several projects on one host

Each project's dispatch service runs the probes itself (they cost two file reads every 2 s and a
connect every 30 s), records in its own project's health and tells its own master. With the
one-dispatcher-per-host decision (2026-10-26, decided, not built), one loop will probe once and
record in each project. §7 Q4 asks whether one master should hear it for all.

## 4. Settings (`config/keys.go`)

`host.events` (`on`/`off`, default `on`), `host.probe_every` (default 30 s), `host.report_min`
(default 5 s). The link check's 2 s period is fixed: it is the point of the feature.

## 5. Delivery

One PR, independent of the watcher's R1/R2 steps, after its step 6 (facts, acting on nothing):
the probes as pure functions over injected readers (`/proc` and `/sys` paths, a dialer, two
clocks), the `{p}:host` hash and its fenced write, the two health checks, the reports, the
transcript tail count, the settings, and one line in the master skill on reading
`host-network` and `host-boot`.

## 6. Tests

- Link probe: tables over fake `/proc/net/route` and `operstate` files (default route present,
  absent, interface `down`, `dormant`, `unknown`).
- Upstream probe: a fake dialer; two failures needed for down; skipped while the link is down.
- An 18 s drop on fake clocks: one `warn` and one `ok` transition in the health log, one report
  at `up` with the right duration; a 2 s drop: two transitions, no report.
- Reboot: a different stored boot id → `host-boot` warn and one report; the same id → nothing;
  a wall/monotonic jump with the same id → the resume line.
- API error linking: a transcript tail with errors inside, before and after the window counts
  only those inside; a non-Claude client reads `unknown`.
- Every write fenced (a test with a lost lease writes nothing); Redis tests on a disposable
  broker only.

## 7. Questions for the person

1. **Report threshold.** Report network drops of 5 s or more to master, and only record shorter
   ones? Or report every drop?
2. **Down notice.** Tell master only at the return (recommended: during the drop master's
   provider is probably unreachable too), or also at the drop?
3. **macOS.** With no `/proc`, the Mac relies on the 30 s upstream probe and can miss a drop
   shorter than 30 s. Accept that, or add the macOS route socket (more code) to watch the link?
4. **Several projects.** Should every project's master be told of a host event (today's model,
   one report per project), or only one master chosen by you?
5. **Desktop.** Should a reboot or a drop longer than a minute also ring the desktop (`notify`),
   or stay on the bus only?
