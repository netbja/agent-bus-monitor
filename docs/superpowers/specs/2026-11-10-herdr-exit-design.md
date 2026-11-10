# Leaving herdr: a Go terminal host for the agents

- Status: draft, for review (coder, foureyes) and for the person's decisions (§10)
- Date: 2026-11-10
- Author: architect
- Task: `herdr-exit-spec`
- Base: `origin/main` at b3dd13a2
- Revision 11: foureyes P3 on r9 — `ctrl+\` becomes a prefix key, so detach and "submit
  anyway" no longer collide (§4.6, Q9); the person's Redis 7.4+/8/Valkey 9 ideas evaluated one
  by one (§6.5)
- Revision 10: foureyes P3s on r5/r6 and r8 — `WakeLine` caps the count at 999 instead of
  refusing it (§4.2); one plain systemd unit per build, not a template (§4.4a)
- Revision 9: reviewer-codex at dece79b9 — a left-behind line must not be committable by the
  person's queued Enter (submit guard, §4.2); notice distinguishes wake line and `/clear`;
  herdr residual lasts as long as a project runs on herdr (§4.2)
- Revision 8: foureyes at dece79b9 — P2 one restartable unit cannot drain: per-build unit
  instances, holder excluded from restart paths (§4.4a); P3 stale risk row, self-attested
  ancestor proof (§4.7), linger under Q5
- Revision 7: the person's review (written against bc2d8394), every item applied:
  P1 paste→CR race (§4.2, §8); P1 resume caller proof by ancestor walk (§4.7, §8);
  P2 drain keyed on build id, not protocol (§4.4); P2 holder crash recovery: restart policy,
  no automatic relaunch, sentinel runbook (§4.4a); P2 broker persistence for `{p}:resume` and
  Valkey (§4.7, §6.3); P3 step numbers in §7 and §9. The §10 questions stay open.
- Revision 6: reviewer-codex at 2e769888 (the herdr residual has no hard bound)
- Revision 5: reviewer-codex at bd371e32 (the herdr adapter cannot check and type atomically)
- Revision 4: foureyes P2 at bd371e32 (names in `WakeLine` and in the submit frame)
- Revision 3: reviewer-codex CHANGES at bc2d8394 (Runtime.Prompt still takes text; focus
  fallback for viewers without focus reports)
- Revision 2: foureyes CHANGES at bc2d8394 (P1 host-reported facts; P2 drain bound, wire-level
  closed set, herdr callers outside dispatch, resume storage; P3 interrupt, macOS socket dir,
  file count, step 8 split, socket enumeration)

## 1. Problem

agent-bus runs every agent inside herdr (0.9.5, Rust). herdr gives us four things we do not
own: the terminals the agents run in, the reading of each agent's state (`agent get`), typing
into a pane (`agent prompt`), and the window the person looks at (workspaces, tabs, splits,
the plugin overlay). 145 Go files mention it (`git grep -il herdr -- '*.go'` at b3dd13a2,
tests included). Every herdr release can move a field we parse,
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
| **Reporting about herdr itself** | `healthchecks.go` (version, plugin list, herdr probe), `suspendsnap.go` (herdr session), busmon `projects_admin.go` (workspaces per project), busmon `about.go` (herdr and plugin versions), `setup/plugin.go`, `setup/status.go`, `setup/uninstall.go` | `--version`, `plugin list`, `workspace list`, `session list` | Not agent-facing; they describe or install herdr. They move to `Host.Describe` in step 4 or die in step 16 (§7). |

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
   quiet time); one `agentstate` module judges, for every backend. herdr's own state word is
   carried as one more *fact*, optional and labelled as the host's opinion (§4.2 `HostView`),
   so nothing changes while herdr hosts the agents.
2. **"create/run" and "send text+keys" are too wide.** Callers never need a split, a tab or a
   raw key stream: they need "start this agent's command as a terminal named X in project P" and
   "submit one of the two fixed lines" (wake line, `/clear`). No interrupt: nothing calls one
   today, and adding one would be a new kind of automated write needing its own decision. A narrow
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

`Activity` carries an optional `HostView` — `{State, Client, Focused}` as the host itself
judges them — set **only by the herdr adapter** (from `agent get`), nil from the holder and the
fake. It is how herdr's judgement reaches the dispatcher in steps 1–14 (and on any project still on herdr) without a state word in
the interface's contract: `agentstate` uses it as its source of state, client and focus while
`terminal.host=herdr`, exactly as `Inspect` uses `agent get` today (dispatch/herdr.go:422), so
steps 1–5 change no behaviour. From step 6, `agentstate` also computes its own judgement from
the other facts and shadow mode logs every disagreement with `HostView`. At cutover (`holder`)
`HostView` is nil and only our judgement remains; the field is deleted with the herdr adapter.

Interface rules (part of the interface, not comments):
- `Submit` takes a `Line`, a type whose only constructors are `WakeLine(nonce, …)` and
  `ClearLine()`. No free text crosses the seam from the dispatcher. It types as a paste
  (bracketed when the program enabled mode 2004), waits ~200 ms, then sends CR.
- **Re-check before CR.** A person can start typing during that ~200 ms. Viewer input is held
  in the terminal's input queue while a submit is in flight, and immediately before CR the
  holder re-checks person-active (§4.5) including any viewer bytes held since the paste began.
  If a person became active it does **not** send CR and returns `ErrPersonActive`; then:
  - it clears the pasted line **only when that is safe**: the client's signature declares a
    clear-input key (e.g. `ctrl+u` where probed) and the current input region (§4.3) holds
    exactly the pasted text and nothing else; it sends that key, checks the input region is
    empty, and only then releases the held viewer bytes;
  - otherwise it leaves the pasted text in place and arms a **submit guard** on that terminal,
    so the left-behind line cannot be committed by a key the person did not mean for it:
    - the held viewer bytes are released **with every submit key removed** (CR, LF, and the
      Enter encodings of kitty keyboard `CSI 13…u` and modifyOtherKeys `CSI 27;…;13~`); the
      other bytes go in after the pasted text, as typed;
    - while the guard stands, every further viewer submit key is swallowed, not forwarded;
    - the guard lifts when the input region (§4.3) no longer contains the left-behind text (the
      person deleted it: checked on the screen after each viewer byte), or when the person
      acknowledges with the attach chord followed by `y` (`ctrl+\ y`: "submit anyway"), after
      which submit keys flow again;
    - the attach client shows the guard without touching the agent's screen: bell once, and the
      outer terminal's title (OSC 2) set to `agentbus: <kind> left in <agent>'s input — delete
      it, or ctrl+\ y to submit anyway`, restored when the guard lifts;
    - the dispatcher records a person notice naming the **kind**: "wake line left unsent in
      <agent>'s input" for a `WakeLine`, "`/clear` left unsent in <agent>'s input — submitting
      it clears the conversation" for a `ClearLine`.
    It never sends a key it has not probed for that client, and never discards a printable byte
    the person typed.
  On the herdr adapter (`AtomicSubmit = false`) no re-check is possible between paste and CR
  (`agent prompt` is one call); this is part of the residual stated below.
- **The closed set holds on the wire too.** A Go type protects only inside one process; the
  holder is reached over a socket. So the holder's submit frame carries only
  `{kind: wake|clear, term, nonce, count}` — no text field and **no names** — and the **holder
  composes** the line from a template compiled into it (the same constant the dispatcher uses
  today). The holder fills the project from its own (it serves one, §4.4) and the agent from
  the terminal's name, both checked with `ValidName` when the terminal was started (`Start`
  refuses an invalid name). A frame with any other kind, any field beyond these four (a name or
  text field included), a nonce not matching `^[0-9a-f]{6,16}$`, or a `count` outside 1–999 is
  refused.
  The only frame that carries raw input bytes is the attach stream (§4.6), opened by a person's
  `agentbus attach`; observer attaches carry none. This is a trust boundary between same-uid
  processes, so §4.4 and §4.6 go to the **security role** for review before step 9 starts.
- `Submit` refuses (typed error `ErrPersonActive`) when a person is active (§4.5). How strong
  that refusal is depends on the adapter, and the interface says so: `HostInfo.AtomicSubmit`.
  - **holder** (`AtomicSubmit = true`): the check and the write happen under the terminal's
    input lock, which every viewer's input also takes; no viewer byte can land between the
    check and the last byte of the line. This closes the inspect→type race.
  - **herdr** (`AtomicSubmit = false`): herdr has no check-and-prompt call. The adapter does
    what the dispatcher does today: a fresh `agent get` for focus right before `agent prompt`,
    then the prompt. The residual window is the time between the end of the first call and the
    start of the second: normally one CLI round-trip, but **without a hard bound**
    (`herdrCallTimeout` limits each call on its own, not the gap; a scheduler pause can stretch
    it). This is **today's documented residual, unchanged**, and it stays for as long as a project runs on herdr: every project until step 10 (the
    holder becomes opt-in), all but the trial project at step 14, every project not yet
    switched until step 15 flips the default, and any project kept on herdr after that until
    step 16 removes the adapter; the
    spec claims no improvement for herdr.
  - The contract suite's race test (a viewer types concurrently with `Submit`; no interleaving,
    or a refusal) runs only on adapters with `AtomicSubmit`; on the others the suite checks only
    the pre-typing refusal. busmon's About/health shows `atomic submit: no` for a herdr project.
- `Start` puts `AGENTBUS_TERM_ID` and `AGENTBUS_HOST` in the program's environment, and returns
  the pid it spawned. Identity no longer needs a process search: the host knows its child.
- Every call reports what it observed, never what it assumed (herdr-core's rule, adopted).

Where callers land:
- `dispatch.Runtime` keeps its three operations, but **`Prompt` loses its text parameter**:
  today it is `Prompt(ctx, pane, text, nonce string)` and only checks that `text` contains the
  nonce marker, so any string still crosses the seam. It becomes
  `Prompt(ctx, pane string, line termhost.Line)`; the dispatcher builds the line with
  `termhost.WakeLine(project, agent, count, nonce) (Line, error)` and `Clear` passes
  `termhost.ClearLine()`. `WakeLine` refuses a `project` or `agent` that fails `bus.ValidName`,
  a `count` < 1, and a nonce not matching `^[0-9a-f]{6,16}$`, so nothing outside those
  alphabets can be interpolated into the typed line. A `count` above 999 is **capped, not
  refused**: the count is `Obligation.Size()` (pending + unread, unbounded), and refusing a
  large backlog would mean never waking the agent that needs it most — the unsafe direction.
  `WakeLine` stores 999 and the line reads `999+ message(s) waiting`; the frame's 1–999 range
  then always holds. Test at 999, 1000 and a very large count.
  `Line` has unexported fields and no exported constructor from a string, so no caller in any
  package can make one carry other text; the observed marker is read from the `Line`, not
  passed beside it. This change lands in step 1, with a test that fails if any exported
  function of `dispatch` or `termhost` takes a `string` that reaches `Submit`. Over the
  holder's socket the same set is enforced by the frame (below). `Inspection` keeps its fields;
  `State`, `Ready`, `Busy` come from `agentstate`, `Focused` becomes "person active".
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
  `<rundir>/agentbus/<project>/holder-<protoversion>.sock`, mode 0600, directory 0700, same-uid
  peer check (`SO_PEERCRED` on Linux, `getpeereid` on macOS). `<rundir>` is `$XDG_RUNTIME_DIR`
  when set (Linux with a user session); otherwise (macOS, Linux without logind) a per-uid
  directory `$TMPDIR/agentbus-<uid>` (falling back to `/tmp/agentbus-<uid>`), created 0700 and
  refused if it exists with another owner or wider mode. A unix socket path must fit in 104
  bytes on macOS: the path is checked at start and the holder refuses to run with a clear error.
- Owns the PTYs (creack/pty), is every agent's parent, reaps them, always reads output (a slow
  viewer never blocks a program: per-viewer bounded queue, a viewer that falls behind is
  repainted, herdr-core's rule).
- Keeps, per terminal, a pure-Go emulator (screen + N lines of scrollback, setting
  `holder.scrollback`, default 5 000) and a raw tail (~1 MB) for `agentbus term dump`.
- Wire: length-prefixed frames on a unix socket; JSON control frames, raw bytes for terminal
  data; hello/welcome carry the protocol version **and the build id** (the `buildinfo` version
  plus VCS revision, the same id `agentbus --version` prints); `Describe` reports both.
- **Upgrade without killing agents**: the socket is named by build, not by protocol:
  `holder-<buildid>.sock`. **Any new build drains, same protocol or not** — a same-protocol
  bug-fix build never replaces the old socket, because replacing a holder means killing its
  agents. The new build listens on its own socket and takes new terminals; the old one refuses
  new terminals and exits when its last terminal ends (herdr-core's "older build drains" rule).
  Clients list every holder socket of the project and speak to each in its own protocol version
  (a client too old for a holder's protocol says so and does not touch it).
  `holder_draining` compares build ids, so a bug-fix build is reported like a protocol change.
  No fd-passing, no state migration. **The drain has no natural bound**: `/clear` and
  auto-clear type into the same process and end no terminal; only relaunch, swap, suspend and
  self-repair do. An old holder can therefore serve for as long as its agents live. Two things
  bound it:
  - `agentbus health` reports `holder_draining` (warn) with the old build id, its age and its
    terminals; after `holder.drain_warn` (default 24 h) it is shown in For you.
  - `agentbus holder drain <project>` finishes it on the person's word: a rolling relaunch of
    each terminal still on the old build, one agent at a time, through the existing relaunch
    path (new terminal, proof, close the old one — relaunch-close decision), skipping an agent
    that is working or has a person active. It is never automatic: relaunching an agent is a
    person's or master's decision, not the holder's.
- The holder runs no repository code and makes no bus writes; facts it exposes are read by the
  dispatcher, which keeps doing the fenced writes. (The dispatcher stays the one automated pane
  writer: the holder types only what `Submit` hands it.)

### 4.4a Holder crash and restart

The holder is every agent's parent and holds every PTY master: when it dies, the kernel hangs
up every terminal and the project's agents die with it. Nothing in this design hides that; it
bounds and reports it.

- **One unit per build (an "instance" below = one such unit), never restarted to upgrade.** A single restartable unit
  cannot drain: starting a new build would mean restarting the unit, and with
  `KillMode=control-group` that kills every agent. So each (project, build) gets **its own plain
  unit file**, `agentbus-holder-<project>-<buildid>.service` (systemd; `<buildid>` reduced to
  `[a-z0-9.-]`), not a template: a template `agentbus-holder@.service` has one `ExecStart` for
  all instances, systemd cannot split `%i` into project and build, and rewriting the template
  on upgrade would change the binary of every old instance at its next restart. On macOS a
  launchd job per build, label `com.agentbus.holder.<project>.<buildid>` (one plist per label
  already). An upgrade **writes and starts a new unit** beside the old one; it never edits,
  stops or restarts an old unit, and removes an old unit file only after its holder exited 0
  for good. Each instance has its own cgroup, so the old
  build's agents live on in theirs.
- **Each instance runs its own build.** The installer copies the holder binary to
  `<datadir>/agentbus/holder/<buildid>/agentbus` (0755, owner only) and the instance's
  `ExecStart` / `ProgramArguments` points there, not at the installed `agentbus`, which the
  next upgrade replaces. A crashed old instance therefore restarts its own build, not the new
  one under the old name. The copy is removed when its instance exits for good.
- **Restart policy, per instance.** systemd: `Restart=on-failure`, `RestartSec=2`,
  `StartLimitIntervalSec=300`, `StartLimitBurst=5`, `KillMode=control-group` (a crash leaves no
  orphaned agent in that instance). launchd: `KeepAlive = {SuccessfulExit = false}`,
  `ThrottleInterval = 10`. A holder that has no terminals and is **not** its project's current
  build (the newest instance) exits 0 at once: a drained or crashed old build retires instead of
  coming back empty. `agentbus down` stops every instance of the project; each exits 0.
- **Excluded from every restart path.** The holder makes no Redis connection (§4.4), so a
  password change does not concern it: `setup/rotate.go`'s `restartRunningServices` and
  `setup/plugin.go`'s `ServicesHint` ("services keep running the previous agentbus until
  restarted") skip holder instances by kind, with a test for each. `agentbus service restart`
  refuses the holder unless `--kill-agents` is given, and says how many agents it would kill.
  An upgrade's only holder action is "start the new build's instance".
- **The restarted holder starts empty and relaunches nothing.** Starting agents is a decision
  (cost, quota, the person's intent); a crash loop that relaunches five clients each time would
  burn quota. Agents come back by one of the two paths that already exist: `agentbus up
  <project>` (the person or master), which uses `{p}:resume` (§4.7); or the dispatcher's
  self-repair (decision 2026-11-09) once built and enabled, which `Start`s a new terminal per
  dead agent with its recorded resume argv, under that decision's own rules and limits.
- **Detection.** `agentbus health` gains `holder` (crit when the project's service is enabled
  and no holder answers; warn `holder_restarted` with the time and the number of terminals lost
  when a holder's start time is newer than the agents' last proved identities).
- **Sentinel runbook entry** (`agent-bus-sentinel` skill): on `holder` crit, the one allowed
  action is the documented service start of the project's current holder instance
  (`agentbus service start holder`), never `restart` of a running one, once per
  episode, and only for an instance that is down (the restart of a down instance kills nothing);
  then annotate and escalate to master/the person with the list of agents lost. Never
  `agentbus up`, never relaunch an agent: that stays master's or the person's. On
  `holder_restarted` warn: escalate only.

### 4.5 The focus rule, restated for a host without focus

Today: "never type into a focused pane" — herdr knows which pane has focus. A holder has no
window; it has viewers. Proposed rule (Q2 decides the threshold):

> The dispatcher never types into a terminal while a person is **active** in it: a viewer has
> typed within `dispatch.person_quiet` (default 3 s), or holds an unsent draft (typed bytes
> since the last CR, up to `dispatch.draft_wait`, default 60 s), or a non-observer viewer is
> attached and **not proven unfocused**.

"Proven unfocused" fails closed. Whether a viewer's terminal supports focus reports (mode 1004)
cannot be queried; it can only be observed. So `agentbus attach` enables 1004 on the outer
terminal and forwards focus-in/focus-out to the holder as control frames (never as program
input). A viewer counts as **focused from the moment it attaches** and stays so until the
holder has received a focus-out from it; after a focus-out it is unfocused until the next
focus-in. A terminal that never sends focus reports therefore blocks typing for as long as it
is attached — today's invariant ("never type into a focused pane") holds without knowing the
terminal. An observer (`--observe`) never blocks: it cannot type. The quiet and draft timers
apply in addition, never instead.

Strict alternative (Q2 b): any attached non-observer viewer blocks typing, focus reports
ignored. The focus notice
(`dispatch.focus_notice`) keeps its meaning: a wake waiting on the person is shown in busmon.

### 4.6 The person's console: busmon + attach

- `agentbus attach <agent> [--observe]`: puts the current terminal on the agent's terminal.
  Raw bytes both ways, first paint from the emulator's screen. `ctrl+\` is a
  **prefix key**, not a bare chord (a bare chord would detach before `ctrl+\ y` could be
  read): `ctrl+\ d` detaches, `ctrl+\ y` is "submit anyway" (§4.2, only while a guard
  stands; otherwise ignored), `ctrl+\ ctrl+\` sends one literal `ctrl+\` (SIGQUIT for the
  program, which some need), and any other key after the prefix is dropped with a bell. The
  prefix waits for its second key without a timeout and is shown in the outer terminal's title
  while pending. Outer terminal restored on detach (synchronized output,
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

### 4.7 Resume and restore (storage revised)

- Process lifetime = holder lifetime. The holder survives busmon, ssh sessions and the person's
  terminal closing; it does not survive reboot or `agentbus down`.
- The resume argv the SessionStart hook reports today to herdr is written instead to a
  **separate hash**, `{p}:resume` (field = agent, value = JSON `{argv, session, at}`), not into
  the `{p}:agents` snapshot. Reason: a snapshot field would need a `mergeSnapshot` carry rule,
  and resume's is the opposite of every identity field there (BootID, Model, ClientFP follow the
  pid; resume must **outlive** the process and survive later statuses that do not carry it —
  the c19b3255 hand-off bug shows what a missing rule costs). A hash of its own has no merge.
- Write path: not lease-fenced (the hook is not the dispatcher) but an optimistic transaction
  in `bus/` on the `TurnState` pattern: `WATCH {p}:agents`, check the caller is **a descendant
  of** the agent's stored verified process, then `HSET {p}:resume`. An unproved caller writes
  nothing. Persisted `at` comes from Redis `TIME`.
- **The caller proof is an ancestor walk.** The SessionStart hook is not the agent's process:
  it runs as a child (or grandchild, through a shell) of the client, so "the caller is the
  stored pid" would always fail. The hook passes its ancestor chain — each ancestor's pid
  **and start time** — and `bus/` accepts the write only when one ancestor matches the stored
  verified (pid, start) exactly. The walk starts at the hook's own parent, stops at pid 1, at
  the first unreadable process, or after 32 ancestors, whichever comes first. A reused pid
  fails because its start time differs. Prior art: the turn and prove-session hooks already
  walk ancestors with `dispatch.Ancestors` (Linux, `/proc/<pid>/stat` ppid + start). macOS
  gets the same function behind one platform file: `sysctl kern.proc.pid.<pid>`
  (`kinfo_proc`: `e_ppid`, `p_starttime`) through `golang.org/x/sys/unix`; until then macOS
  writes no resume (fail closed), as `Ancestors` is empty there today.
- **The proof is self-attested**, exactly like the turn and prove-session hooks: the hook
  reports its own ancestor chain and `bus/` checks the claim against the stored identity; the
  broker cannot read the host's process table. It stops an honest mistake (a hook of another
  agent, a stale process, a reused pid), not a same-uid process that lies — which could write
  any bus entry anyway. The resume argv it records is run only through `agent-launch` for that
  role (§4.7, `up`), never as a free command line.
- Cleared only by `agentbus forget <agent>` and replaced by the next proved session's report;
  an agent's death or a status never touches it.
- **It must survive a reboot, so the broker must persist.** The shipped `docker-compose.yml`
  runs Redis with `--appendonly yes` on a named volume. Requirement stated in the README and
  checked: `agentbus health` gains `broker_persistence` (warn when `CONFIG GET appendonly` is
  `no` and `CONFIG GET save` is empty; unknown — not warn — when `CONFIG` is refused, as on
  some managed brokers). Without persistence, a reboot loses `{p}:resume` and agents start
  fresh; nothing worse.
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
14a. As the person, I want to be told when an old holder build is still serving, and to finish its drain with one command that relaunches agents one at a time, so that an upgrade does not linger forever.
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
30. As a maintainer, I want a shadow mode in which our detector runs beside herdr's own judgement (`HostView`) and logs disagreements, so that we prove parity before cutover while herdr still decides.
30a. As a security reviewer, I want the holder's submit frame to carry no text, so that no same-uid process can make the holder type arbitrary input into an agent.
31. As a maintainer, I want the host chosen by one setting in `config/keys.go`, so that rollback during the trial is a setting change.
32. As a maintainer, I want holder tests to use a temporary socket directory and scripted programs, so that no test touches a running holder or a real agent.
33. As a reviewer, I want each migration step as one PR from main, so that each is reviewable and revertible alone.

## 6. Implementation decisions

### 6.1 Modules
- `termhost` (new): `Host`, `Spec`, `Term`, `Screen`, `Activity`, `Line`; adapters `herdr`,
  `holder` (client side), `fake`. Contract test suite in the package, run on every adapter
  available.
- `agentstate` (new, from `dispatch`): `Signatures` and their fixtures move here; adds `Blocked`
  patterns, an optional probed `ClearInput` key per client (§4.2 re-check before CR), hook-fed observed blocked, settle by quiet time. Pure functions: facts in, judgement
  out.
- `holder` (new, server side) + `screen` (internal seam over the emulator: `Write`,
  `Snapshot(lines)`, `Modes`, `Render` for first paint).
- `dispatch`: `Runtime` implemented by `termhost`+`agentstate`; decision code unchanged except
  `Focused` → person active and the new `ErrPersonActive` from `Submit`.
- `cmd/agentbus`: `holder`, `attach`, `term record`, `term dump`; `up`/`start`/`restore`/
  `relaunch`/`swap`/`suspend`/`resume` on `Host`; `holder drain`. `config/keys.go`:
  `terminal.host` (`herdr|holder`), `holder.scrollback`, `holder.size`, `holder.drain_warn`,
  `dispatch.person_quiet`, `dispatch.draft_wait`.
- `Host` also answers `Describe(ctx) (HostInfo, error)` — name, version, `AtomicSubmit`, socket/session, per
  project terminal count, draining builds — for the callers that today report on herdr itself
  (health, about, projects admin, suspend snapshot).
- `cmd/busmon`: preview panel, attach key; no herdr env.
- `service`: holder as one plain unit (systemd) / one job (launchd) per project and build (§4.4a).

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
- Redis: one new hash `{p}:resume` (§4.7) and one new stream `{p}:events` (§6.5), both added
  to the project's key kinds so `--reset`/`--delete`, backup and restore cover them, and the
  observed `blocked` source. The design uses only
  commands available in Redis 7.2 (`WATCH`/`MULTI`, `HSET`, `HGETALL`, `HDEL`, `TIME`,
  `CONFIG GET`), so **Valkey (a Redis 7.2 fork) is acceptable as broker for this feature**.
  Declaring Valkey support for agent-bus as a whole is a separate check, done before any such
  claim: grep the code for commands newer than 7.2 (the hash-field TTL family `HEXPIRE`/
  `HPEXPIRE`/`HTTL`/`HPERSIST`, `HGETDEL`/`HGETEX`/`HSETEX`, `XACKDEL`/`XDELEX`, vector sets)
  and run the Redis suite against a disposable Valkey. A first grep at b3dd13a2 found none of
  these names; the suite run is the evidence that counts. `{p}:agents` and `subscribe`
  JSON unchanged.
- Holder protocol: versioned; additive only within a version, like `subscribe`.

### 6.4 Emulator bake-off (the gate before the holder)
Recorded raw output (via `agentbus term record`, which runs a client under our PTY and saves
bytes + resize events) of each supported client in each state the fixtures cover today. A
candidate passes when, replayed, its screen satisfies every existing signature test (ready,
typed, busy, modal, dialog, old box) for every client, renders wide chars (Kimi's moon, `❯`,
`›`, `─`) at the right column, and keeps SGR dim (Claude/Codex placeholders). Lines of
emulator code we would own count against a candidate.

### 6.5 Newer broker features (the person's four ideas, evaluated)

Context. Today the floor is Redis 7.2: this spec uses only 7.2 commands, so Valkey 7.2+ works
(§6.3). Hash-field expiration (`HEXPIRE`/`HSETEX`/`HGETEX`…) needs Redis 7.4+ (8 for
`HSETEX`/`HGETEX`) or Valkey 9; adopting it **raises the floor** for every install. The shipped
broker is `redis:8-alpine`, so the floor is a promise to people running their own broker, not a
constraint on ours. The project already has: per-project streams `status`, `report`, `notify`,
`cmd` that busmon replays (`Recent`/`TailFrom`) and follows; `{p}:agents` written only through
`putSnapshot` (an optimistic `WATCH` merge); `{p}:observed`, a whole-hash TTL written by the
dispatcher; busmon polls about once a second; the client is go-redis v9.19.

| # | Idea | Verdict | Why |
|---|---|---|---|
| 1 | Presence by field TTL | **Defer** (and never on `{p}:agents` or `{p}:resume`) | See below. |
| 2 | Per-project event stream | **Adopt**, as `{p}:events` | See below. |
| 3 | Keyspace notifications for busmon | **Defer** | See below. |
| 4 | RESP3 client-side caching | **Reject for now** | See below. |

**1. Presence by field TTL — defer.** The idea needs a writer that refreshes the field on a
timer. An agent cannot: a client between turns runs nothing, so an idle agent would look
"silent" while healthy — the opposite of what busmon must say. The holder could (it knows each
terminal is alive), but the holder makes no Redis connection by design (§4.4a: it must not be
in the password-rotation path). The dispatcher already writes `{p}:observed` with a whole-hash
TTL and every observation carries its own `At`, judged by `Observation.Fresh` on the reader
side; per-field TTL would only move that age check from the reader to the server, at the cost
of raising the floor to 7.4/Valkey 9. Never on `{p}:agents`: a field that expires there would
silently drop a verified identity, which `putSnapshot`'s merge rules exist to prevent. Never on
`{p}:resume` (it must outlive the process; §4.7). Revisit if the floor moves to 7.4 for another
reason: then `{p}:observed` per-agent expiry is the natural first use, written by the
dispatcher under its lease fence.

**2. Event stream — adopt as `{p}:events`.** Terminal-lifecycle facts have no home today and
this spec adds several: wake typed / refused (person active) / left behind (guard armed),
blocked observed, attach / detach (observer or not), relaunch, holder started / restarted /
draining / drained. One stream per project, `XADD {p}:events MAXLEN ~ 10000`, fields
`{kind, agent, term, by, detail}`, time from the entry id (Redis `TIME`). Writers: the
dispatcher (fenced, like its other writes), `agentbus attach` for attach/detach (the person's
own process; unfenced, as a person's `notify` is today), `holder drain` and `up` for
relaunch/drain. Not the holder (no Redis). busmon replays it on start (`XREVRANGE` of the last
N) and follows it with `XREAD BLOCK`, as it already does for `notify`; it is a **timeline, not a
source of truth**: state stays in `{p}:agents`, `{p}:observed`, `{p}:resume`. Added to
`projectKinds` (so `--reset`, `--delete`, backup cover it), not to `activityKinds` (a wake
attempt must not date a project as active). Works on 7.2. Lands with the steps that produce the
events: wake/refusal kinds in step 6, attach/detach in 12, relaunch/drain in 11 and 13.

**3. Keyspace notifications — defer.** They need `notify-keyspace-events` set on the broker, a
global setting that managed brokers often refuse and that costs every write on the server; the
messages are fire-and-forget Pub/Sub, lost while busmon reconnects, so polling stays as the
fallback anyway. busmon's 1 s poll of one project is a handful of small reads. Adopting `{p}:events`
(item 2) already gives busmon a blocking read that wakes it on the changes this spec adds.
Revisit if a measured poll cost or latency complaint appears; then as a refresh hint only, with
`agentbus health` reporting `broker_keyspace_events` (unknown when `CONFIG` is refused).

**4. RESP3 client-side caching — reject for now.** busmon reads a few small hashes per second
for one person; the gain is unmeasured and the cost is real: `CLIENT TRACKING`, an invalidation
channel to handle on reconnect, and a cache that can serve a stale verified identity in the
window before invalidation — the one record whose staleness matters. Whether go-redis v9.19
supports it natively is unverified here (rueidis does). As the person says, measure first: a
busmon profile showing Redis round-trips as the bottleneck would reopen it.

## 7. Migration plan (one PR from main each)

| # | PR | Behaviour change | Exit criterion |
|---|---|---|---|
| 0 | This spec + decision note | none | approved |
| 1 | `termhost` package, fake, contract suite; herdr adapter (with `HostView`) wrapping the calls `dispatch` makes; `Runtime.Prompt` takes a `termhost.Line`, no text | none (same wake line typed) | dispatch tests green on the adapter |
| 2 | Provisioning callers (`up`, `start`, `restore`) on `Host` | none | installer/up tests green (herdr stubbed) |
| 3 | Replacement callers (`relaunch`, `swap`, `suspend`, `resume`) on `Host` | none | a test fails on any herdr exec in the **agent-facing** packages (`dispatch`, and the `cmd/agentbus` files of steps 1–3), with an explicit allowlist of the files moved in step 4 (reporting callers) and step 16 (`reportresume.go`, `setup/*`) |
| 4 | Reporting callers on `Host.Describe`: health checks, `suspendsnap`, busmon `projects_admin` and `about` | none (same text with herdr) | allowlist shrinks to `reportresume.go`, `setup/*` |
| 5 | `agentstate` extracted; client from launch profile; `HostView` still decides | none | fixtures green |
| 6 | Hooks for blocked (Claude `Notification`, Codex `notify`) + `Blocked` signatures; **shadow mode**: our judgement beside `HostView`, disagreements logged | observability only | ≥ 7 days, disagreement reviewed |
| 7 | `agentbus term record` + raw fixtures for each client | none | fixtures committed |
| 8 | Emulator bake-off; `screen` seam + chosen adapter | none | §6.4 passes |
| 9 | `agentbus holder` server alone + real-PTY e2e (wire frames, closed submit set, peer check, socket dir) — after the security review of §4.4/§4.6 | none (nothing uses it) | e2e green, security role approves |
| 10 | Holder adapter of `termhost` (contract suite) + `terminal.host` setting | opt-in | contract suite green on holder |
| 11 | Holder unit per project and build (plain systemd unit, launchd job, per-build binary copy; test: writing a new build's unit never changes an old unit's file or binary) with the §4.4a restart policy; rotate/upgrade paths skip it + `holder`, `holder_restarted`, `holder_draining` health + `holder drain` + sentinel runbook entry | opt-in | service tests (fake systemctl/launchctl) |
| 12 | `agentbus attach`, busmon preview + attach key | opt-in | manual live check by the person |
| 13 | `{p}:resume` + hook writes it (ancestor-walk proof, macOS `Ancestors`); `up` restores from it; `broker_persistence` health; `reportresume.go` keeps its herdr call until step 16 | opt-in | Redis tests of §8; reboot trial |
| 14 | Live trial on one project with `terminal.host=holder` | trial | Q8 project runs a week |
| 15 | Default flips to `holder` | default | release |
| 16 | herdr removal (§4.8: adapter, `HostView`, `reportresume` herdr call, `setup/*` herdr parts, plugin), decisions superseded | herdr gone | no herdr exec anywhere (the step 3 test with an empty allowlist); scrub/test green |

Steps 1–6 are useful even if the project stops there: they make herdr replaceable and our
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
- Attach prefix: `ctrl+\ d` detaches; `ctrl+\ y` lifts a standing guard and is ignored
  without one; `ctrl+\ ctrl+\` delivers one `0x1c` to the program; `ctrl+\ x` delivers
  nothing and rings; the prefix split across two reads is still recognised.
- Paste→CR race (holder): a viewer byte arriving between paste and CR → no CR,
  `ErrPersonActive`; with a probed clear key and an input region equal to the paste → line
  cleared, then the viewer's bytes delivered; without one → paste left, guard armed, notice
  recorded. Both kinds, each in two cases:
  - `WakeLine`, the viewer pressed Enter (CR, and the kitty encoding) during the paste window →
    the wake line is **not** submitted; the viewer's other bytes are delivered; the notice says
    "wake line";
  - `ClearLine`, same → `/clear` is **not** submitted; the notice says "`/clear`";
  - each kind, viewer Enter after the guard is armed → swallowed; after deleting the line (screen
    shows the region without it) → Enter forwarded; after `ctrl+\ y` → Enter forwarded.
  Contract suite (`AtomicSubmit` adapters).
- Resume caller proof (Redis tests, disposable broker): a direct descendant of the stored
  (pid, start) writes; a grandchild through a shell writes; a non-descendant writes nothing; an
  ancestor with the stored pid but another start time (reused pid) writes nothing; a chain
  longer than 32 writes nothing. The walk itself: table tests with an injected stat reader, as
  `ancestors` is tested today.
- Focus rule: a viewer that never sends a focus report blocks `Submit` for its whole attach;
  focus-out then focus-in toggles it; an observer never blocks. Contract suite, all adapters
  that have viewers.
- Mutation on the re-check before CR (removing it must fail the race test), on the removal of
  submit keys from held bytes and on the guard's swallowing (each must fail an Enter case), on the
  clear-only-when-safe condition, on the ancestor match (pid only, without start, must fail the
  reused-pid test), on the depth bound, and on `Submit`'s person-active refusal, on the `Line` constructors and on the holder's
  frame validation (unknown kind, malformed nonce, text field present, a name field present,
  count out of range), on `WakeLine`'s `ValidName` check (one mutant per argument: project,
  agent) and on `Start`'s name check: the guarantees a
  reviewer would otherwise only read.
- `{p}:resume`: Redis tests (disposable broker) — an unproved caller writes nothing; a status,
  a hand-off and the agent's death leave it; `forget` clears it.
- The step-3 guard test: an allowlist that only shrinks; step 16 empties it.

## 9. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| herdr's `agent get` encodes detection we have not seen (blocked, done) | wakes into a permission prompt, or none at all | shadow mode (step 6) before any cutover; hooks for blocked |
| Pure-Go emulator fidelity (Ink/ratatui redraws, wide chars, synchronized output) | signatures miss, agent never woken | bake-off on recorded output; oracle test; a client failing stays unsupported (never prompted), the safe direction |
| Holder crash kills every agent of the project | team down | holder kept small, no policy; per project, not per host; restart policy (§4.4a); agents back via `up` or self-repair from `{p}:resume`; `holder` health + sentinel runbook |
| Broker without persistence | `{p}:resume` lost on reboot, agents start fresh | AOF in the shipped compose; `broker_persistence` health warn |
| Upgrade kills agents | interruption | per-build unit instance and socket (`holder-<buildid>.sock`), each build drains, no restart path touches a running holder (§4.4, §4.4a) |
| Old holder never drains (`/clear` keeps the process) | two builds forever, fixes never reach old terminals | `holder_draining` health, `holder drain` rolling relaunch on the person's word |
| A same-uid process drives the holder | arbitrary typing into an agent | text-free submit frame, holder-composed lines, raw input only on a person's attach, 0600/0700 socket, security review before step 9 |
| Losing the tiled always-visible layout | the person sees less at a glance | busmon preview + terminal splits; Q1 |
| Person/agent typing race | wake line inside a draft | holder: check and write under the input lock (closed); herdr: today's re-inspect-then-prompt residual, normally one CLI round-trip but with no hard bound, kept until step 15 and shown as `atomic submit: no` |
| macOS process facts (foreground group, start time) | identity/foreground wrong on Mac | `tcgetpgrp` on the PTY master is portable; per-OS facts behind one platform file, tested on its OS |
| Two hosts during the trial | confusion over where an agent runs | `terminal.host` per project; busmon shows the host per agent |
| Scope creep into a multiplexer | months of work | non-goal stated; busmon only previews and attaches |

## 10. Questions for the person

1. **Visibility.** herdr shows all agents tiled at once. The design replaces it with busmon's
   list + live preview of one agent + full-screen attach, and leaves side-by-side to your
   terminal's splits (one `agentbus attach` each). Is that acceptable, or must busmon show
   several agents' screens at once (a much larger, multiplexer-like piece)?
2. **The focus rule.** With no window focus, which rule stops a wake line: (a) you typed in that
   agent in the last 3 s, hold an unsent draft, or are attached and your terminal has not
   reported losing focus (recommended; a terminal without focus reports always blocks), or
   (b) any attached viewer at all, even when it reports being unfocused?
3. **"100 % Go".** Is a Go module that embeds a native library (gostty: Ghostty's Zig-built VT
   core, loaded without cgo) acceptable in production, in tests only, or not at all?
4. **Holder scope.** One holder per project (recommended: a crash takes one project down) or
   one per host? Note the consequence with the one-dispatcher-per-host decision (2026-10-26):
   a per-host dispatcher enumerates every project's holder sockets
   (`<rundir>/agentbus/*/holder-*.sock`, project from the directory name, validated with
   `ValidName` before use) and keeps one connection per holder; a per-project dispatcher
   (today) has one directory to read.
5. **Lifetime.** Agents die on reboot and come back with `agentbus up` using their recorded
   resume. Is that enough, or should the holder start at login and relaunch them itself? On
   Linux this also decides **logout**: without `loginctl enable-linger`, systemd stops a user's
   units when the last session ends, so logging out (or the last ssh session closing) kills
   every holder and its agents. Enable linger (agents outlive logout), or accept that logout
   ends the team? (macOS: a LaunchAgent stops at logout; a LaunchDaemon would be needed.)
6. **herdr after cutover.** Delete the herdr adapter entirely (recommended, step 16), or keep it
   as an optional host?
7. **Gemini.** The dispatcher supports Antigravity (`agy`), not Gemini CLI. Is Gemini CLI a
   client to support in this work (needs probing and signatures), or later?
8. **Trial project.** Which project runs the live trial (step 14): agent-bus-monitor itself
   (dogfood, highest visibility) or a quieter one?
9. **Attach prefix key.** `ctrl+\` (herdr-core's key; `ctrl+\ d` detaches) or `ctrl+]`
   (go-pty, telnet habit; `ctrl+] d`)? Either way it is a prefix, and pressing it twice sends
   it to the agent.

## 11. Out of scope

Windows (ConPTY). A tiling multiplexer in busmon. Remote hosts other than over ssh. OSC 7501
emission by clients. Changing the dispatcher's decision rules beyond focus → person active.
Any new automated pane writer.
