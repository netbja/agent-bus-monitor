# Leaving herdr: a Go terminal host for the agents

- Status: draft, for review (coder, foureyes) and for the person's decisions (§10)
- Date: 2026-11-10
- Author: architect
- Task: `herdr-exit-spec`
- Base: `origin/main` at b3dd13a2

## 1. Problem

agent-bus runs every agent inside herdr (0.9.5, Rust). herdr gives us four things we do not
own: the terminals the agents run in, the reading of each agent's state (`agent get`), typing
into a pane (`agent prompt`), and the window the person looks at (workspaces, tabs, splits,
the plugin overlay). 131 Go files mention it. Every herdr release can move a field we parse,
the plugin contract binds our release to theirs, and a Rust toolchain sits in the critical path
of a Go project. The person wants agent-bus to stop depending on herdr and to stay 100 % Go.

The hard part is not running a program in a PTY. It is the **agent console**: knowing, for five
different clients (Claude Code, Codex, Kimi, Grok, Antigravity/Gemini), whether the agent is
idle, working, or waiting on a dialog; typing the wake line only when it is safe; letting the
person watch, take over and give back a pane; and bringing an agent back after a restart.

## 2. What herdr does for us today (inventory)

Read from `origin/main`. Grouped by the caller's need, not by herdr verb.

| Need | Callers | herdr calls | Notes |
|---|---|---|---|
| **Inspect an agent** | `dispatch/herdr.go` (`Runtime.Inspect`), clear, pop-done, focus notice | `agent get`, `pane process-info`, `pane get`, `pane read --source recent --lines 40 [--ansi]` | herdr supplies `Client`, `State` (idle/working/blocked/done), `Focused`. Ready/Busy/Modal/Dialog are already **ours** (`dispatch.Signatures`, fixtures in `dispatch/testdata/screens`). |
| **Type into an agent** | `Runtime.Prompt`, `Runtime.Clear` | `agent prompt`, `pane wait-output --match <nonce>` | Only the fixed wake line and `/clear`. Observed = the nonce reached the screen. |
| **Provision a project** | `agentbus up`, `start`, `restore` | `workspace create/list/focus/close`, `tab create/rename/list`, `pane split/run/list` | Layout = herdr workspace per project, tab per role. |
| **Replace an agent** | `relaunch`, `swap`, `suspend`/`resume`, self-repair (decided, not built) | `tab create`, `pane split/run/close`, `pane process-info` | "never type into an existing pane": a new pane, then close the old one once proven. |
| **Know who is in a pane** | `status`, `bind`, `agents`, identity guard | `HERDR_PANE_ID`, `HERDR_SESSION`, `HERDR_SOCKET_PATH` in the agent's env; `pane process-info` | `{p}:agents` holds pane + pid + start time (+ boot id). |
| **Resume after herdr restarts** | `roles report-resume` (SessionStart hook) | `pane report-agent-session` | herdr's native restore re-runs our `agent-launch --resume` argv. |
| **The person's window** | `busmon` in a pane, `busmon --pick`, `agentbus plugin`, `herdr-plugin.toml`, `setup.sh` link + key binding | `plugin pane open`, workspace events | Workspace events start a project's stopped services. |
| **Liveness** | dispatcher supervisor, health | `workspace list` as ping, `session list --json` | "waiting for herdr". |

Two observations drive the design:

1. **The seam already exists for the dispatcher** (`dispatch.Runtime`: Inspect/Prompt/Clear),
   and most of the intelligence behind it is already ours: the signatures. What herdr adds is
   the *rendered screen*, the *process facts*, the *focus bit* and the *state word*.
2. **Everything else calls the herdr CLI directly** from `cmd/agentbus`, with herdr's layout
   vocabulary (workspace, tab, split) leaking into `up`, `relaunch`, `start`, `restore`.

## 3. Options

### Option A — keep herdr, wrap it (status quo + seam)

Introduce the seam, keep herdr as the only adapter.
- For: no new runtime, nothing to operate.
- Against: does not meet the goal (Rust stays, plugin contract stays). Useful only as step 0.

### Option B — move to herdr-core (`hc`)

herdr-core is the maintainer's new runtime: holder + core, raw attach, OSC 7501 status.
- For: designed for exactly this (runtime, not multiplexer); a smaller API than herdr.
- Against: still Rust, still someone else's release cadence; and its rule "status comes only
  from OSC 7501, no screen detection, no vendor logic" means **none of our five clients would
  report a state** — they do not emit OSC 7501. We would keep all detection ourselves anyway.
  Read as reference only, as the directive says.

### Option C — our own Go host, pure-Go emulator (recommended)

A small Go daemon owns the PTYs; a pure-Go VT emulator gives the rendered screen; agent-state
detection stays ours and moves above the seam; busmon becomes the view, with attach passthrough.

### Option D — our own Go host, gostty (libghostty-vt) emulator

As C, but the screen model is gostty: Ghostty's VT core, best fidelity, snapshots with restore.
- For: fidelity (Kitty keyboard, wide chars, every mode), first-paint render for free.
- Against: not Go. gostty is "no cgo" but **embeds a native Zig-built shared library** loaded
  with purego and unpacked into the user's cache at init. A native blob per OS/arch in our
  binary, a pinned Ghostty revision, and a loader that writes to disk at start. Against the
  spirit of "100 % Go" (question Q3).

### Option E — tmux as the host

- For: battle-tested, `capture-pane`, `send-keys`, attach for free.
- Against: a C dependency instead of a Rust one; we would again parse someone else's CLI. Not
  pursued, listed so the review does not have to ask.

### Challenge to master's proposal

The proposal (step 0 = a `termhost` interface "list/create/run/read screen/send text+keys/close/
pane state" with herdr as first backend; step 1 = a Go holder with creack/pty or ptyx and go-vte
or gostty; busmon as view with attach) is the right shape. Three changes:

1. **"pane state" must not be in the interface.** State (idle/working/blocked) is a judgement
   from screen + hooks + process facts, and it is the part we must own and test across clients.
   If the backend returns a state word, the herdr adapter returns herdr's and the Go adapter has
   to reinvent it inside itself: two detectors, untestable against each other. The interface
   returns **facts** (screen snapshot, foreground processes, viewer/typing activity, output
   quiet time); one `agentstate` module judges, for every backend.
2. **"create/run" and "send text+keys" are too wide.** Callers never need a split, a tab or a
   raw key stream: they need "start this agent's command as a terminal named X in project P",
   "type this line and submit", and the two control sequences `/clear` and interrupt. A narrow
   interface keeps herdr's layout out of the domain and keeps the dispatcher unable to type
   anything but its fixed lines (a CLAUDE.md rule) — enforced by type, not by review.
3. **go-vte is a parser, not a screen.** It turns bytes into Print/Execute/CSI callbacks; we
   would still write the grid (cursor, erase, scroll regions, SGR, wide chars, alt screen,
   modes). The emulator choice is a bake-off on recorded agent output (§6.4), not a given.
   ptyx's extra (Windows ConPTY, console mux) is out of scope; creack/pty is the smaller choice.

## 4. Recommended design (Option C)

### 4.1 Vocabulary

- **Terminal**: one PTY running one program, owned by the host. Replaces herdr's *pane*.
  Identified by a `TermID` (opaque, unique for the host's lifetime) and named `<agent>` within
  its **project**. There are no tabs, splits or workspaces in the domain.
- **Host**: the process that owns terminals. herdr in step 0, the **holder** (`agentbus holder`)
  after.
- **Screen**: a snapshot of a terminal's rendered lines (text and, per line, the SGR-styled
  form), cursor, and modes (bracketed paste, alt screen, focus reporting).
- **Viewer**: a person's terminal attached to a Terminal (raw passthrough), or an observer
  (read-only).
- **Activity**: when a viewer last typed, whether it holds an unsent draft, and when the program
  last wrote output.
- **Agent state**: the judgement `idle | working | blocked | unknown` plus `ready`, made by
  `agentstate` from Screen + Activity + hooks + declared status. Never made by a host.

### 4.2 The seam: `termhost`

One package, one interface, two adapters (herdr, holder) plus an in-memory fake for tests.
Shape (prose first; the type sketch only fixes the decision):

```go
type Host interface {
    // Start runs spec as a new terminal. It never reuses an existing one.
    Start(ctx, Spec) (Term, error)          // Spec: project, name, cwd, argv, env, size
    List(ctx, project string) ([]Term, error) // Term: id, name, pid, pidStart, alive, viewers
    Close(ctx, TermID) error                 // SIGHUP, then SIGKILL after a grace period
    Screen(ctx, TermID, lines int) (Screen, error)
    Activity(ctx, TermID) (Activity, error)  // viewer typing/draft, output quiet, foreground procs
    Submit(ctx, TermID, Line) error          // Line: a closed set (wake line, /clear), see below
    WaitScreen(ctx, TermID, match string, d time.Duration) (bool, error)
    Ping(ctx) error
}
```

Interface rules (part of the interface, not comments):
- `Submit` takes a `Line`, a type whose only constructors are `WakeLine(nonce, …)` and
  `ClearLine()`. No free text crosses the seam from the dispatcher. It types as a paste
  (bracketed when the program enabled mode 2004), waits ~200 ms, then sends CR.
- `Submit` refuses (typed error `ErrPersonActive`) when Activity says a person is active
  (§4.5). The host enforces it at the moment of typing; the dispatcher's check before is only
  an early skip. This closes the inspect→type race the dispatcher handles today with a
  re-inspection.
- `Start` puts `AGENTBUS_TERM_ID` and `AGENTBUS_HOST` in the program's environment, and returns
  the pid it spawned. Identity no longer needs a process search: the host knows its child.
- Every call reports what it observed, never what it assumed (herdr-core's rule, adopted).

Where callers land:
- `dispatch.Runtime` keeps its three methods; its herdr implementation becomes
  `termhost` + `agentstate`. `Inspection` keeps its fields; `State`, `Ready`, `Busy` come from
  `agentstate`, `Focused` becomes "person active".
- `up`, `start`, `restore`, `relaunch`, `swap`, `suspend`, self-repair call `Start/List/Close`
  only. The herdr adapter maps a project to a workspace and an agent to a tab; that mapping
  lives in the adapter and dies with it.
- Identity: `status`/`bind` record `AGENTBUS_TERM_ID` (with the herdr adapter, the pane id) in
  the existing `pane` field; the verified pid+start rule is unchanged.

### 4.3 `agentstate`: one detector for every host

Inputs, strongest first:
1. **Declared status and turn hooks** (already on the bus: `status`, `bus.TurnState` from
   UserPromptSubmit/Stop/SessionStart). Working/idle at turn edges are exact for Claude, Grok,
   Codex.
2. **Client hooks for "waiting on the person"**: Claude Code's `Notification` hook
   (permission prompt), Codex's `notify`. Each wired in the launcher, writing an observed
   `blocked` (not a declared one). Today `blocked` comes from herdr's `agent get`; this is the
   single largest piece of herdr intelligence we must replace, and hooks replace it more
   reliably than screen text.
3. **Screen signatures** (existing `Signatures`: Ready, Busy, Modal, Dialog, plus new
   `Blocked` patterns per client for permission menus, pinned by captured screens).
4. **Activity**: output quiet for ≥ 800 ms replaces "two reads 200 ms apart agree" as the
   settle test (both kept until the trial shows one suffices).

The **client** is no longer detected: it comes from the agent's launch profile (`roles.toml`
→ client), cross-checked with the foreground process name. A client without signatures is
unsupported and never prompted, as today. Gemini CLI has none yet (Q7).

### 4.4 The holder (`agentbus holder`)

- One process **per project** (Q4), started as a project service (systemd user unit /
  launchd agent, like `dispatch` and `missions`), socket under
  `$XDG_RUNTIME_DIR/agentbus/<project>/holder-<protoversion>.sock`, mode 0600, same-uid peer
  check (`SO_PEERCRED` / `getpeereid`).
- Owns the PTYs (creack/pty), is every agent's parent, reaps them, always reads output (a slow
  viewer never blocks a program: per-viewer bounded queue, a viewer that falls behind is
  repainted, herdr-core's rule).
- Keeps, per terminal, a pure-Go emulator (screen + N lines of scrollback, setting
  `holder.scrollback`, default 5 000) and a raw tail (~1 MB) for `agentbus term dump`.
- Wire: length-prefixed frames on a unix socket; JSON control frames, raw bytes for terminal
  data; protocol version in hello/welcome.
- **Upgrade without killing agents**: a new holder build with a new protocol version listens on
  a new socket; the old one refuses new terminals and exits when its last terminal ends
  (herdr-core's "older build drains" rule). Agents are relaunched routinely (`/clear`, reload,
  relaunch), so an old holder drains within a day. Clients list every holder socket of the
  project. No fd-passing, no state migration.
- The holder runs no repository code and makes no bus writes; facts it exposes are read by the
  dispatcher, which keeps doing the fenced writes. (The dispatcher stays the one automated pane
  writer: the holder types only what `Submit` hands it.)

### 4.5 The focus rule, restated for a host without focus

Today: "never type into a focused pane" — herdr knows which pane has focus. A holder has no
window; it has viewers. Proposed rule (Q2 decides the threshold):

> The dispatcher never types into a terminal while a person is **active** in it: a viewer has
> typed within `dispatch.person_quiet` (default 3 s), or holds an unsent draft (typed bytes
> since the last CR, up to `dispatch.draft_wait`, default 60 s), or a viewer is attached with
> focus (the viewer's terminal reports focus-in via mode 1004).

Strict alternative: any attached, non-observer viewer blocks typing. The focus notice
(`dispatch.focus_notice`) keeps its meaning: a wake waiting on the person is shown in busmon.

### 4.6 The person's console: busmon + attach

- `agentbus attach <agent> [--observe]`: puts the current terminal on the agent's terminal.
  Raw bytes both ways, first paint from the emulator's screen, detach chord `ctrl+\`
  (herdr-core's; go-pty uses `ctrl+]`), outer terminal restored on detach (synchronized output,
  kitty key flags, modes, scroll region, cursor, colors — the list in herdr-core `attach.rs`).
  `--observe` never types or resizes. Works over `ssh host agentbus attach coder` with no extra
  machinery (laptop ⇄ host topology).
- **Size**: the most recently active viewer sets the size; with none, the size the terminal was
  started with (default 200×50, setting `holder.size`). Agents do not see a resize from a
  read-only observer.
- busmon (bubbletea): the Agents panel gains a **live preview** of the selected agent's screen
  (read-only, from `Screen`), and `enter`/`a` runs `agentbus attach` through `tea.ExecProcess`;
  detach returns to busmon. busmon itself is no longer a herdr pane or plugin; it runs in any
  terminal.
- Side-by-side viewing of several agents is the terminal's job (Ghostty splits, one `attach`
  each). busmon does **not** become a multiplexer (Q1).

### 4.7 Resume and restore

- Process lifetime = holder lifetime. The holder survives busmon, ssh sessions and the person's
  terminal closing; it does not survive reboot or `agentbus down`.
- The resume argv the SessionStart hook reports today to herdr is written instead to
  `{p}:agents.<agent>.resume` (fenced like the rest of the snapshot; the hook is the agent's own
  verified process).
- `agentbus up` on a project whose holder has no terminal for a role starts it with that resume
  argv when one is recorded, else fresh. Self-repair (decision 2026-11-09) uses the same path:
  `Start` a new terminal, never type into an old one.

### 4.8 What is deleted at the end

`herdr-plugin.toml`, `agentbus plugin`, `busmon --pick` as a herdr overlay (the projects page
stays), herdr parts of `setup.sh` (link, key binding block), `dispatch/herdr.go`,
`herdrbin.go`, `reportresume.go`'s herdr call, every `HERDR_*` read, the herdr stubs in tests,
and decisions 2026-10-19 (plugin) and the herdr parts of 2026-10-27 (relaunch-close) marked
superseded.

## 5. User stories

1. As the person, I want agent-bus to install and run without herdr, so that one Go toolchain builds everything I depend on.
2. As the person, I want `agentbus up <project>` to start every role's agent in its own terminal, so that a project starts with one command as today.
3. As the person, I want to see which agents run, their state and a live preview of the selected one in busmon, so that I can watch the team without opening each agent.
4. As the person, I want to attach to an agent full-screen from busmon and return with one chord, so that I can take over a conversation.
5. As the person, I want to attach to an agent from another terminal or over ssh, so that I can work from my laptop against the host.
6. As the person, I want an observe-only attach, so that I can watch without any risk of typing into the agent.
7. As the person, I want the dispatcher never to type while I am typing or have a draft in an agent, so that a wake line never lands in the middle of my sentence.
8. As the person, I want to see in busmon that a wake is waiting because I am active in that agent, so that I know to step away.
9. As the person, I want agents to keep running when I close busmon or my terminal, so that work continues unattended.
10. As the person, I want agents restarted with their previous conversation after a reboot when I run `agentbus up`, so that I do not lose context.
11. As the person, I want to scroll an agent's recent output, so that I can see what it did while I was away.
12. As the person, I want a dump of an agent's raw recent output, so that I can attach it to a bug report.
13. As the person, I want the desktop to ring once when an agent waits on a permission prompt, as today, so that I do not miss a blocked agent.
14. As the person, I want upgrading agent-bus not to kill running agents, so that an update is not a team restart.
15. As the dispatcher, I want an agent's screen and activity facts from any host through one interface, so that my wake decisions do not depend on which host runs.
16. As the dispatcher, I want the host to refuse typing at the moment a person is active, so that the inspect-then-type race is closed.
17. As the dispatcher, I want to type only the wake line and `/clear`, by type, so that no message text can ever be typed into a pane.
18. As the dispatcher, I want to observe that my nonce reached the screen, so that delivery is recorded as observed, as today.
19. As the dispatcher, I want each agent's client from its launch profile, so that the right signatures apply without screen guessing.
20. As the dispatcher, I want "blocked" from the client's own permission hook where it has one, so that blocked is exact instead of inferred from screen text.
21. As the dispatcher, I want a settled-screen test based on output quiet time, so that a screen still rendering is never judged ready.
22. As an agent, I want my terminal id and host in my environment, so that `status` and `bind` record my identity without a process search.
23. As master, I want relaunch, swap and suspend to start a new terminal and close the old one once the new agent is proven, so that the relaunch-close rule survives the change.
24. As master, I want self-repair to relaunch a dead agent through the same `Start`, so that there is one way to create an agent's terminal.
25. As sentinel, I want the holder's health (up, protocol version, terminals, draining builds) in `agentbus health`, so that I can restart only what the runbook allows.
26. As a maintainer, I want detection tested from recorded raw output of each client, so that an emulator change that alters a screen fails a test.
27. As a maintainer, I want a recorder that captures a client's raw output to a fixture, so that a new client or a new client version can be probed in minutes.
28. As a maintainer, I want the emulator behind an internal seam with a fidelity oracle in tests, so that the production emulator can be swapped after a bake-off.
29. As a maintainer, I want the herdr adapter and the holder adapter to pass the same contract suite, so that the cutover changes no caller.
30. As a maintainer, I want a shadow mode in which our detector runs beside herdr's `agent get` and logs disagreements, so that we prove parity before cutover.
31. As a maintainer, I want the host chosen by one setting in `config/keys.go`, so that rollback during the trial is a setting change.
32. As a maintainer, I want holder tests to use a temporary socket directory and scripted programs, so that no test touches a running holder or a real agent.
33. As a reviewer, I want each migration step as one PR from main, so that each is reviewable and revertible alone.

## 6. Implementation decisions

### 6.1 Modules
- `termhost` (new): `Host`, `Spec`, `Term`, `Screen`, `Activity`, `Line`; adapters `herdr`,
  `holder` (client side), `fake`. Contract test suite in the package, run on every adapter
  available.
- `agentstate` (new, from `dispatch`): `Signatures` and their fixtures move here; adds `Blocked`
  patterns, hook-fed observed blocked, settle by quiet time. Pure functions: facts in, judgement
  out.
- `holder` (new, server side) + `screen` (internal seam over the emulator: `Write`,
  `Snapshot(lines)`, `Modes`, `Render` for first paint).
- `dispatch`: `Runtime` implemented by `termhost`+`agentstate`; decision code unchanged except
  `Focused` → person active and the new `ErrPersonActive` from `Submit`.
- `cmd/agentbus`: `holder`, `attach`, `term record`, `term dump`; `up`/`start`/`restore`/
  `relaunch`/`swap`/`suspend`/`resume` on `Host`. `config/keys.go`: `terminal.host`
  (`herdr|holder`), `holder.scrollback`, `holder.size`, `dispatch.person_quiet`,
  `dispatch.draft_wait`.
- `cmd/busmon`: preview panel, attach key; no herdr env.
- `service`: holder unit per project.

### 6.2 Libraries
- PTY: `github.com/creack/pty` (Linux, macOS; pure Go). Not ptyx (its Windows half is out of
  scope; its Mux is replaced by our attach).
- Raw mode, size, SIGWINCH for attach: `golang.org/x/term`, `golang.org/x/sys/unix`.
- Emulator: bake-off (§6.4) between pure-Go candidates — `go-vte` + our own grid,
  `charmbracelet/x/vt`, `hinshun/vt10x` (the last two not yet checked out locally: to verify).
  gostty only as a **test oracle** behind a build tag, if Q3 allows it at all.
- herdr-core: reference for holder framing, input gate, attach restore list, drain-on-upgrade.
  No code copied (its own rule, and a different language).

### 6.3 Wire and state
- No Redis schema change except `{p}:agents.<agent>.resume` (string, additive) and the observed
  `blocked` source. `subscribe` JSON unchanged.
- Holder protocol: versioned; additive only within a version, like `subscribe`.

### 6.4 Emulator bake-off (the gate before the holder)
Recorded raw output (via `agentbus term record`, which runs a client under our PTY and saves
bytes + resize events) of each supported client in each state the fixtures cover today. A
candidate passes when, replayed, its screen satisfies every existing signature test (ready,
typed, busy, modal, dialog, old box) for every client, renders wide chars (Kimi's moon, `❯`,
`›`, `─`) at the right column, and keeps SGR dim (Claude/Codex placeholders). Lines of
emulator code we would own count against a candidate.

## 7. Migration plan (one PR from main each)

| # | PR | Behaviour change | Exit criterion |
|---|---|---|---|
| 0 | This spec + decision note | none | approved |
| 1 | `termhost` package, fake, contract suite; herdr adapter wrapping the calls `dispatch` makes | none | dispatch tests green on the adapter |
| 2 | Provisioning callers (`up`, `start`, `restore`) on `Host` | none | installer/up tests green (herdr stubbed) |
| 3 | Replacement callers (`relaunch`, `swap`, `suspend`, `resume`) on `Host` | none | no `herdr(` call left outside the adapter (a test greps, like `TestNoStructWriterOfTheBoard`) |
| 4 | `agentstate` extracted; client from launch profile | none | fixtures green |
| 5 | Hooks for blocked (Claude `Notification`, Codex `notify`) + `Blocked` signatures; **shadow mode**: log disagreement with herdr `agent get` | observability only | ≥ 7 days, disagreement reviewed |
| 6 | `agentbus term record` + raw fixtures for each client | none | fixtures committed |
| 7 | Emulator bake-off; `screen` seam + chosen adapter | none | §6.4 passes |
| 8 | `agentbus holder` + holder adapter (contract suite) + service unit | opt-in only | real-PTY e2e green |
| 9 | `agentbus attach`, busmon preview + attach key | opt-in | manual live check by the person |
| 10 | Resume to `{p}:agents.<a>.resume`; `up` restores from it | opt-in | reboot trial |
| 11 | Live trial on one project with `terminal.host=holder` | trial | Q8 project runs a week |
| 12 | Default flips to `holder` | default | release |
| 13 | herdr removal (§4.8), decisions superseded | herdr gone | `go build` with no herdr reference; scrub/test green |

Steps 1–5 are useful even if the project stops there: they make herdr replaceable and our
detection herdr-independent.

## 8. Testing decisions

- One external seam: `termhost.Host`. The contract suite (start, list, screen after output,
  submit with bracketed paste, refusal while a person is active, wait-screen, close and reap,
  identity env) runs against `fake` always, `holder` always, `herdr` only on opt-in (needs a
  running herdr in an isolated session).
- `agentstate`: table tests from screen fixtures (existing `dispatch/testdata/screens`, moved)
  and from raw-byte fixtures replayed through the chosen emulator; good tests assert the
  judgement, not regexes.
- `holder`: end-to-end with real PTYs running small scripted programs (a shell script that
  draws a fake prompt, enables bracketed paste, prints a spinner), temp `XDG_RUNTIME_DIR`. Never
  a real agent, never the production broker (CLAUDE.md test rule unchanged).
- Prior art: dispatch tests with a fake `Runtime`; installer tests that stub `docker`/`herdr`;
  service tests that fake `systemctl`/`launchctl`.
- Mutation on `Submit`'s person-active refusal and on the `Line` constructors: the two
  guarantees a reviewer would otherwise only read.

## 9. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| herdr's `agent get` encodes detection we have not seen (blocked, done) | wakes into a permission prompt, or none at all | shadow mode (step 5) before any cutover; hooks for blocked |
| Pure-Go emulator fidelity (Ink/ratatui redraws, wide chars, synchronized output) | signatures miss, agent never woken | bake-off on recorded output; oracle test; a client failing stays unsupported (never prompted), the safe direction |
| Holder crash kills every agent of the project | team down | holder kept small, no policy; per project, not per host; resume via `up`; sentinel health check |
| Upgrade kills agents | interruption | drain-on-upgrade, protocol version in socket name |
| Losing the tiled always-visible layout | the person sees less at a glance | busmon preview + terminal splits; Q1 |
| Person/agent typing race | wake line inside a draft | host-side refusal at typing time, draft tracking |
| macOS process facts (foreground group, start time) | identity/foreground wrong on Mac | `tcgetpgrp` on the PTY master is portable; per-OS facts behind one platform file, tested on its OS |
| Two hosts during the trial | confusion over where an agent runs | `terminal.host` per project; busmon shows the host per agent |
| Scope creep into a multiplexer | months of work | non-goal stated; busmon only previews and attaches |

## 10. Questions for the person

1. **Visibility.** herdr shows all agents tiled at once. The design replaces it with busmon's
   list + live preview of one agent + full-screen attach, and leaves side-by-side to your
   terminal's splits (one `agentbus attach` each). Is that acceptable, or must busmon show
   several agents' screens at once (a much larger, multiplexer-like piece)?
2. **The focus rule.** With no window focus, which rule stops a wake line: (a) you typed in that
   agent in the last 3 s or hold an unsent draft (recommended), or (b) any attached viewer at
   all, even idle?
3. **"100 % Go".** Is a Go module that embeds a native library (gostty: Ghostty's Zig-built VT
   core, loaded without cgo) acceptable in production, in tests only, or not at all?
4. **Holder scope.** One holder per project (recommended: a crash takes one project down) or
   one per host (matches the one-dispatcher-per-host decision)?
5. **Lifetime.** Agents die on reboot and come back with `agentbus up` using their recorded
   resume. Is that enough, or should the holder start at login (with systemd linger) and
   relaunch them itself?
6. **herdr after cutover.** Delete the herdr adapter entirely (recommended, step 13), or keep it
   as an optional host?
7. **Gemini.** The dispatcher supports Antigravity (`agy`), not Gemini CLI. Is Gemini CLI a
   client to support in this work (needs probing and signatures), or later?
8. **Trial project.** Which project runs the live trial (step 11): agent-bus-monitor itself
   (dogfood, highest visibility) or a quieter one?
9. **Detach chord.** `ctrl+\` (herdr-core) or `ctrl+]` (go-pty, telnet habit)?

## 11. Out of scope

Windows (ConPTY). A tiling multiplexer in busmon. Remote hosts other than over ssh. OSC 7501
emission by clients. Changing the dispatcher's decision rules beyond focus → person active.
Any new automated pane writer.
