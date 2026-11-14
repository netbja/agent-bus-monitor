# Leaving herdr: a Go terminal host for the agents

- Status: **frozen after revision 14** — corrections only, no new features in this spec. For
  review (coder, foureyes, reviewer-codex); the person's decisions are recorded in §10.
- Date: 2026-11-10, revisions 14 to 17 on 2026-11-11, revisions 18 to 24 on 2026-11-13, revision 25
  on 2026-11-14
- Author: architect
- Task: `herdr-exit-spec`; revision 18: `herdr-spec-security`
- Base: `origin/main` at a3e60956 (revision 13 merged as PR 428); revision 18 from `origin/main`
  at c2047e83
- Revision 25 (correction): foureyes on step 5d (0ceba37e): "stops reading the viewers'
  sockets" deadlocked the hold — a viewer's Release, Discard and Detach travel on the same socket,
  behind its data. Master's decision (option a): the holder never stops reading a viewer; past
  1 MiB further bytes are refused, counted in the hold notice and never delivered; held bytes
  are still never dropped (§4.2, §8)
- Revision 24 (correction): master's decision after the security role's confirmation
  (s1-profile-confirm, report 1794585522550-0): the resume record stays `{session, profile,
  at}` (r21; r22's `{session, at}` and r23 are withdrawn), with two conditions checked before
  `Start` — the profile in the role's own plus `fallback_profiles` from the local manifest
  only, and the session matching that profile's client (`ResumeVerified`) — the accepted
  residual risk stated, and tests for both; r22's 10-minute outcome check kept (§4.2, §4.7, §7,
  §8)
- Revision 21 (correction): foureyes on r20 — the 5 s acceptance held only if acceptance never
  waits behind other work: `host` directives are read by their own goroutine and accepted
  operations run through one serial queue; the outcome report is the confirmation, and a
  dispatcher that stopped reports unfinished accepted operations as not run. The resume record
  is `{session, profile, at}`, as coder's S1 branch (3999ca17) stores it: the session belongs
  to the profile's client; the profile is re-validated against the manifest (§4.2, §4.7, §7, §8)
- Revision 20 (correction): reviewer-codex on r19 — the relay's 10 s wait and 60 s expiry let
  a directive run after the caller was told it failed: one 5 s acceptance deadline on Redis
  `TIME`, checked and answered in one fenced script, and the caller fails only past it, so no
  operation runs after a failure is announced; §4.7's last item no longer sends self-repair
  through `up`'s fresh branch (§4.2, §4.7, §8)
- Revision 19 (correction): foureyes on r18 — the in-tree relay covered too few commands and
  too few callers: `reload` (sentinel's job), `retire` and the pop are added to the closed op
  set, and the dispatcher applies each command's own authorisation instead of "master or the
  person"; the relay's reply, timeout, refusal text and expiry are stated. S1's fresh start on
  an invalid session is limited to `up`: self-repair starts nothing and reports (the person, Q4
  of the self-repair spec). The subreaper's reaping and the emulator-not-viewer meaning of the
  holder's query answers are stated (§4.2, §4.4, §4.6, §4.7, §8)
- Revision 18 (correction): the security role's review of r17 (reports 1794583768072-0 and
  1794583768090-0), every finding applied:
  - **S1** (high): a stored resume argv is never executed; `up` and self-repair rebuild it from
    the launch template with the re-validated session id; `argv` leaves the record (§4.4a, §4.7,
    §7 steps 8 and 10, §8);
  - **S2**: `Start` is a closed set on the wire (`{role, profile?, resume_session?}`, no argv,
    env or cwd); the holder refuses a non-observer attach, `Start` and `Close` from a peer inside
    its own process tree (subreaper on Linux), so master's and agents' host commands go through
    the dispatcher; the residual risk is stated (§4.2, §4.4, §4.6, §9);
  - **S3**: attach filters the program's output: OSC 52 off by default, OSC 8 stripped, report
    queries dropped and answered by the holder's emulator (§4.6);
  - **S4**: submit requires bracketed paste (mode 2004), rechecks Ready and `EmptyInput` right
    before the paste, and one fixture per client pastes both lines into its permission dialog
    (§4.2, §6.4, §8);
  - **S5**: after `ErrNotLanded` the held viewer bytes stay held until the person releases or
    discards them, or the input is verified empty (§4.2, §4.6, §8);
  - **S6**: exact run-directory rules (`Lstat`, no symlink at any level, owner and 0700 at every
    level), a mutual uid check, and the supervisor's process-group kill bound to a checked file
    (§4.4, §4.4b, §8);
  - **S7**: one injective build key for socket, unit, label, binary directory and pidfile;
    cleanup matches a project recorded in the unit, never a name prefix (§4.4, §4.4a, §4.4b, §8)
- Revision 17 (correction): reviewer-codex on r15 — the prefix default was chosen across
  supported clients only, yet an unsupported client can be attached: the probe and the default
  now cover every client with a profile, and a non-observer attach is refused when the prefix is
  bound or unverified for the terminal's client unless the person picks `--prefix` (§4.6, §7,
  §8, §10)
- Revision 16 (correction): foureyes on r15 — the simplified submit held for the person's bytes
  but not for text already in the input that no viewer typed (a left-behind line of an earlier
  submit; text the client puts back). Step 1 now also requires a verified empty input
  (`EmptyInput` signature per client, from the §6.4 fixtures); after `ErrNotLanded` the terminal
  takes no submit until its input is verified empty; a client without `EmptyInput` is
  unsupported; two submit tests, two mutants, the risk row (§4.2, §4.9, §6.1, §8, §9)
- Revision 15 (correction): reviewer-codex on r14 — §4.6 claimed F12 unbound in every checked
  client while §4.9 left it unverified for Codex, Grok and `agy`. Grok is now verified from its
  bundled shortcut guide (F12 free; it binds `ctrl+\`, `ctrl+q`, F2, F8); Codex and `agy` cannot
  be verified offline, so F12 is the **provisional** default until step 3's `f12-at-input` probe,
  which also chooses the fallback (§4.6, §4.9, §7, §8, §10)
- Revision 14: the person's review of revision 13 and the decisions of 2026-11-11, every item
  applied:
  - **direct cutover**: no herdr adapter, no `HostView`, no `AtomicSubmit` flag, no
    `terminal.host` setting, no shadow mode, no trial or default flip; `termhost` has two
    adapters, `holder` and `fake`; §7 rewritten as a PR series ending in one cutover PR that
    removes herdr (§4.8); the gate before cutover is the recorded raw fixtures per client (§6.4);
    the person tags the last release with herdr before the cutover PR merges (§7);
  - **submit simplified** (P1): viewer bytes are held for the whole submit; the submit guard,
    Enter swallowing, `prefix y`, the OSC 2 title and the empty-input lift are removed; the
    reasoning is checked in §4.2, with the one condition it needs (the line must be seen in the
    input before CR);
  - **cross-platform** (P1): a mode with no service manager (§4.4b); Windows out of scope, PTY
    and process code one file per platform (§11);
  - **cleanup owner** of per-build units and binaries, with a two-upgrade test (P2, §4.4a);
  - **Codex `notify` verified**: it reports turn completion only; Codex's blocked comes from its
    `PermissionRequest` hook (to verify by probe) and screen signatures (P2, §4.3, §4.9);
  - **broker**: `valkey/valkey:9` with AOF in `docker-compose.yml`, floor Redis 7.2 / Valkey 7.2
    (P3, §6.3, §6.5);
  - **inconsistencies** fixed: §9 herdr residual row, user stories 4 and 25, the §6.5 guard test
    wording (P3);
  - **master told on blocked enter and leave** (P1, §4.3a);
  - **background-work fact** in `agentstate` and the actions that skip such an agent (P1, §4.3b);
  - **`attach.prefix` setting**, default F12 (provisional since r15), legacy and kitty encodings (P2, §4.6);
  - **every client** as data, with the client matrix filled where verified (P1, §4.9); Gemini CLI
    removed, DeepSeek out of scope;
  - **§10 decisions** Q1–Q9 recorded, including the draft rule (§4.5).
- Revision 13: reviewer-codex on r11 — `projectKinds` does not drive `--reset`; `{p}:events` gets
  an explicit place in busmon's purge list and its own replay/tail (§6.3, §6.5)
- Revision 12: reviewer-codex on r9 — the guard lifts only on a verified empty input region
  (superseded in r14: the guard is gone)
- Revision 11: foureyes P3 on r9 — attach prefix key; the person's Redis 7.4+/8/Valkey 9 ideas
  evaluated one by one (§6.5)
- Revision 10: foureyes P3s — `WakeLine` caps the count at 999 instead of refusing it (§4.2); one
  plain systemd unit per build, not a template (§4.4a)
- Revision 9: reviewer-codex at dece79b9 — submit guard, kind-specific notice, herdr residual
  span (superseded in r14)
- Revision 8: foureyes at dece79b9 — per-build holder units, holder excluded from restart paths
  (§4.4a); self-attested ancestor proof (§4.7); linger under Q5
- Revision 7: the person's review of bc2d8394 — paste→CR race (§4.2); resume caller proof by
  ancestor walk (§4.7); drain keyed on build id (§4.4); holder crash recovery (§4.4a); broker
  persistence (§4.7, §6.3); step numbers
- Revisions 2–6: foureyes and reviewer-codex on bc2d8394, bd371e32, 2e769888 (host facts not a
  state word, drain bound, wire-level closed set, names validated, `Prompt` takes a `Line`, focus
  fails closed, herdr residual unbounded)

## 1. Problem

agent-bus runs every agent inside herdr (0.9.5, Rust). herdr gives us four things we do not
own: the terminals the agents run in, the reading of each agent's state (`agent get`), typing
into a pane (`agent prompt`), and the window the person looks at (workspaces, tabs, splits,
the plugin overlay). 145 Go files mention it (`git grep -il herdr -- '*.go'` at b3dd13a2,
tests included). Every herdr release can move a field we parse, the plugin contract binds our
release to theirs, and a Rust toolchain sits in the critical path of a Go project. The person
wants agent-bus to stop depending on herdr and to stay 100 % Go.

The hard part is not running a program in a PTY. It is the **agent console**: knowing, for
every supported **client** (the CLI that runs the model, not the model: Claude Code, Codex CLI,
Kimi CLI, Grok CLI, Antigravity `agy`), whether the agent is idle, working, blocked on a
permission prompt, or still running background work; typing the wake line only when it is safe;
letting the person watch, take over and give back a terminal; and bringing an agent back after a
restart.

The project is still in development: the person decided (2026-11-11) on a **direct cutover**,
not a gradual migration with herdr kept alive beside the new host.

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
| **Reporting about herdr itself** | `healthchecks.go` (version, plugin list, herdr probe), `suspendsnap.go` (herdr session), busmon `projects_admin.go` (workspaces per project), busmon `about.go` (herdr and plugin versions), `setup/plugin.go`, `setup/status.go`, `setup/uninstall.go` | `--version`, `plugin list`, `workspace list`, `session list` | Not agent-facing. They move to `Host.Describe` or are deleted in the cutover PR (§7). |

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
- Against: does not meet the goal (Rust stays, plugin contract stays).

### Option B — move to herdr-core (`hc`)

herdr-core is the maintainer's new runtime: holder + core, raw attach, OSC 7501 status.
- For: designed for exactly this (runtime, not multiplexer); a smaller API than herdr.
- Against: still Rust, still someone else's release cadence; and its rule "status comes only
  from OSC 7501, no screen detection, no vendor logic" means **none of our clients would
  report a state** — they do not emit OSC 7501. We would keep all detection ourselves anyway.
  Read as reference only.

### Option C — our own Go host, pure-Go emulator (recommended)

A small Go daemon owns the PTYs; a pure-Go VT emulator gives the rendered screen; agent-state
detection stays ours and moves above the seam; busmon becomes the view, with attach passthrough.

### Option D — our own Go host, gostty (libghostty-vt) emulator

As C, but the screen model is gostty: Ghostty's VT core, best fidelity, snapshots with restore.
- For: fidelity (Kitty keyboard, wide chars, every mode), first-paint render for free.
- Against: not Go. gostty is "no cgo" but **embeds a native Zig-built shared library** loaded
  with purego and unpacked into the user's cache at init. Decided (Q3): tests only, as an oracle
  behind a build tag.

### Option E — tmux as the host

- For: battle-tested, `capture-pane`, `send-keys`, attach for free.
- Against: a C dependency instead of a Rust one; we would again parse someone else's CLI. Not
  pursued, listed so the review does not have to ask.

### Challenge to master's proposal

The proposal (a `termhost` interface "list/create/run/read screen/send text+keys/close/pane
state"; a Go holder with creack/pty or ptyx and go-vte or gostty; busmon as view with attach) is
the right shape. Three changes:

1. **"pane state" must not be in the interface.** State (idle/working/blocked) is a judgement
   from screen + hooks + process facts, and it is the part we must own and test across clients.
   The interface returns **facts** (screen snapshot, process facts, viewer/typing activity,
   output quiet time); one `agentstate` module judges.
2. **"create/run" and "send text+keys" are too wide.** Callers never need a split, a tab or a
   raw key stream: they need "start this agent's command as a terminal named X in project P" and
   "submit one of the two fixed lines" (wake line, `/clear`). No interrupt: nothing calls one
   today, and adding one would be a new kind of automated write needing its own decision. A narrow
   interface keeps the dispatcher unable to type anything but its fixed lines (a CLAUDE.md rule)
   — enforced by type, not by review.
3. **go-vte is a parser, not a screen.** It turns bytes into Print/Execute/CSI callbacks; we
   would still write the grid (cursor, erase, scroll regions, SGR, wide chars, alt screen,
   modes). The emulator choice is a bake-off on recorded agent output (§6.4), not a given.
   ptyx's extra (Windows ConPTY, console mux) is out of scope; creack/pty is the smaller choice.

## 4. Recommended design (Option C)

### 4.1 Vocabulary

- **Client**: the CLI that runs the model (Claude Code, Codex CLI, Kimi CLI, Grok CLI,
  Antigravity `agy`). Detection, hooks, resume and key bindings are per client, never per model.
- **Client profile**: the data that describes one client (§4.9): its launch form, hooks,
  signatures, background-work signals, helper processes, resume form, clear command, key bindings.
- **Terminal**: one PTY running one program, owned by the host. Replaces herdr's *pane*.
  Identified by a `TermID` (opaque, unique for the host's lifetime) and named `<agent>` within
  its **project**. There are no tabs, splits or workspaces in the domain.
- **Host**: the process that owns terminals: the **holder** (`agentbus holder`).
- **Screen**: a snapshot of a terminal's rendered lines (text and, per line, the SGR-styled
  form), cursor, and modes (bracketed paste, alt screen, focus reporting).
- **Viewer**: a person's terminal attached to a Terminal (raw passthrough), or an observer
  (read-only).
- **Activity**: when a viewer last typed, whether a **draft** stands (§4.5) and since when, when
  the program last wrote output.
- **Process facts**: the terminal's process tree under the holder: pid and start time of each
  descendant, its command name, its CPU time.
- **Agent state**: the judgement `idle | working | blocked | unknown` plus `ready` and
  `background` (§4.3b), made by `agentstate`. Never made by a host.

### 4.2 The seam: `termhost`

One package, one interface, two adapters: `holder` (the client side of the holder's socket) and
an in-memory `fake` for tests. Shape (prose first; the type sketch only fixes the decision):

```go
type Host interface {
    // Start runs a role's launch as a new terminal. It never reuses an existing one.
    Start(ctx, Spec) (Term, error)            // Spec: role, profile?, resume session?, size — no argv, env or cwd
    List(ctx, project string) ([]Term, error) // Term: id, name, pid, pidStart, alive, viewers, build
    Close(ctx, TermID) error                  // SIGHUP, then SIGKILL after a grace period
    Screen(ctx, TermID, lines int) (Screen, error)
    Activity(ctx, TermID) (Activity, error)   // viewer typing, draft, output quiet
    Processes(ctx, TermID) ([]Proc, error)    // the terminal's process tree (§4.3b)
    Submit(ctx, TermID, Line) error           // Line: a closed set (wake line, /clear), see below
    WaitScreen(ctx, TermID, match string, d time.Duration) (bool, error)
    Ping(ctx) error
    Describe(ctx) (HostInfo, error)           // builds, sockets, terminal counts, draining builds
}
```

Interface rules (part of the interface, not comments):

- `Submit` takes a `Line`, a type whose only constructors are `WakeLine(…)` and `ClearLine()`.
  No free text crosses the seam from the dispatcher.
- **Submit is one critical section on the terminal's input lock**, which every viewer's input
  also takes. Under the lock the holder:
  1. checks person-active (§4.5), the client's Ready signature **and its `EmptyInput`
     signature** (the input region holds nothing) on a settled screen, that the program has
     **bracketed paste on** (mode 2004), and that the terminal is not marked *left-behind*
     (below); any check failing → `ErrPersonActive` / `ErrNotReady`, nothing written. A
     non-empty input sets the row's `input not empty`. Without mode 2004 the holder never types
     the line key by key: the wake line holds digits (the count, the nonce), and a menu that
     reads single keys would take them as choices (security S4);
  2. **holds** every viewer byte that arrives from now on (all viewers; a per-terminal queue,
     never dropped: when the queue reaches 1 MiB, further bytes are refused, counted and told
     in the hold notice (`M keys refused`), never delivered, and the client stops sending; the
     holder never stops reading a viewer, so its Release, Discard and Detach always get
     through — r25);
  3. **checks again**, still under the lock and with the bytes held, Ready, `EmptyInput` and
     mode 2004 on a newly settled screen — a dialog can open between step 1 and the hold (a
     subagent's permission request); failing → `ErrNotReady`, nothing typed, the held bytes
     released (nothing was typed, so they are safe to deliver);
  4. types the line as a bracketed paste, and only so;
  5. waits until the line is **seen in the input** (the wake line's nonce, or `/clear`, in the
     client's input region on a settled screen), up to `dispatch.submit_wait` (default 2 s);
  6. if seen, sends CR, then releases the held viewer bytes in order, and the lock. If not
     seen, sends nothing more, marks the terminal **left-behind**, **keeps the viewer bytes
     held** (below), releases the lock and returns `ErrNotLanded`.

  **Held bytes after `ErrNotLanded` (security S5).** A line that did not land may land late
  (the person's held Enter would then submit a left-behind `/clear` and lose the conversation),
  or may sit in a dialog (the person's held keys would answer a prompt they never saw). So the
  hold stays on for every viewer, and new viewer bytes join the queue (same 1 MiB bound; past
  it, refused and told, never delivered, r25). Every non-observer viewer, and every one that attaches while
  the hold stands, sees one line: `a /clear may be left in the input; N keys held: <prefix> r
  releases, <prefix> x discards` (`a wake line` for a wake). `<prefix> r` delivers the held
  bytes in order and ends the hold; `<prefix> x` drops them and ends the hold — then the person
  types normally and can erase the line. The hold ends by itself only when a settled screen shows
  `EmptyInput` (the bytes are then delivered in order). Ending the hold does not lift the
  left-behind mark; only a verified empty input does.

  **Why no guard is needed any more (the reasoning the person asked to check).** The old guard
  existed for one case: a person's bytes arriving between the paste and the CR, mixing with the
  pasted line or committing it with their Enter. Holding the viewer bytes for the whole submit
  removes that case: between step 2 and step 6 no viewer byte reaches the program. But "no
  draft" (no viewer byte since the last CR, §4.5) is not "an empty input": text can sit in the
  input that no viewer typed since the last CR — (a) a line of an earlier `Submit` that was
  pasted but not seen within `submit_wait` (no CR): the next submit would paste after it, and a
  left-behind `/clear` followed by a wake line submits `/clear agentbus#…`, which Claude Code
  runs as `/clear` and clears the conversation; (b) text the client itself puts back in the
  input, e.g. a queued message returned to the input on Esc, which was sent with a CR and so is
  no draft. (foureyes on r15; r12's guard covered both by lifting only on a verified empty
  input, and the person removed the guard, not that precondition.) So step 1 requires the input
  region to be **verified empty** on the settled screen, from the client's `EmptyInput`
  signature (§4.9), recorded from the empty-input fixtures (§6.4). Then, when CR is sent, the
  input holds the pasted line and nothing else, and the person's held keys (their Enter
  included) are delivered **after** the line was submitted, as a new input.
  The reasoning holds under three conditions, all enforced, not assumed:
  - **the input is empty before the paste** — step 1's `EmptyInput` check; a client with no
    verified `EmptyInput` signature is unsupported (never prompted, §4.9);
  - **every byte reaches the program through the holder** — true by construction: the holder
    owns the PTY master; `agentbus attach` is the only way to type, and there is no herdr
    beside it after the cutover;
  - **the line landed in the input before CR** — step 5. Step 3 narrows the window to the time
    between its settled screen and the paste; without step 5, a dialog that opened in that
    window would receive the paste, and CR would answer the dialog. The paste itself must select
    nothing in a dialog: bracketed only (steps 1 and 4), and one fixture per client pastes both
    lines into its permission dialog (§6.4); a client whose dialog acts on the paste is
    unsupported. When
    the line is not seen, no CR is sent and nothing is cleared: the holder never sends a key it
    has not probed, and the case is rare (a dialog or a redraw in the same ~200 ms). The
    dispatcher records a person notice and an event naming the kind — "wake line may be left
    unsent in <agent>'s input" or "`/clear` may be left unsent in <agent>'s input — submitting it
    clears the conversation" — and the agent's row shows it. **A left-behind terminal takes no
    `Submit`** until a settled screen shows its input empty (`EmptyInput` matches); the mark is
    cleared only then, so a left `/clear` can never be completed by the next wake. This is the
    only left-behind case, and it no longer depends on the person typing.

  Remaining cost, accepted (the person, 2026-11-11): the person's keys typed during a submit are
  delayed by the submit (about 200 ms plus the landing wait) and reach an agent that has just
  started a turn. Annoying, not unsafe.
- **The closed set holds on the wire too.** A Go type protects only inside one process; the
  holder is reached over a socket. So the holder's submit frame carries only
  `{kind: wake|clear, term, nonce, count}` — no text field and **no names** — and the **holder
  composes** the line from a template compiled into it (the same constant the dispatcher uses
  today). The holder fills the project from its own (it serves one, §4.4) and the agent from
  the terminal's name, both checked with `ValidName` when the terminal was started (`Start`
  refuses an invalid name). A frame with any other kind, any field beyond these four (a name or
  text field included), a nonce not matching `^[0-9a-f]{6,16}$`, or a `count` outside 1–999 is
  refused.
  The only frame that carries raw input bytes is the attach stream (§4.6); observer attaches
  carry none. This is a trust boundary between same-uid processes, so §4.4 and §4.6 go to the
  **security role** for review before the holder PR (§7 step 5).
- **`Start` is a closed set on the wire too** (security S2a). Its frame carries only
  `{role, profile?, resume_session?, size?}`: no argv, no env, no cwd. The holder builds the
  command itself from the role's launch template, exactly as `agent-launch` does today
  (`rolecfg` reads the manifest and the role; the cwd and env come from the profile), with
  `--resume <id>` only when `resume_session` matches the client profile's session id pattern
  (`sessionIDPattern` for Claude Code) and the client is resumable (§4.7). A role or profile
  failing `ValidName`, not in the manifest, or a frame with any other field is refused. So
  `Start` can only start one of the project's roles, never an arbitrary program.
- **Who may drive the holder (security S2b).** The peer check proves the uid, not "a person" or
  "the dispatcher": every agent, and whatever an agent runs (a test, a command written by a
  prompt injection), has the same uid. The holder therefore also checks **where the peer comes
  from**: it reads the peer's pid (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS) and walks
  its ancestors (pid and start time, `dispatch.Ancestors`, bounded at 32); a peer one of whose
  ancestors is the holder itself — a process inside an agent's terminal — is **in-tree**. On
  Linux the holder sets `PR_SET_CHILD_SUBREAPER`, so a double-forked or orphaned process stays
  its descendant and stays in-tree. An in-tree peer may `List`, `Screen`, `Activity`,
  `Processes`, `Describe`, `Ping` and attach as an **observer**; the holder **refuses** its
  non-observer attach, `Start`, `Close` and `Submit`. The dispatcher (a service of its own),
  the person's shell, busmon and `ssh host agentbus …` are outside the tree and allowed.
  - **Master's and agents' host commands** are therefore **relayed to the dispatcher** when run
    from inside a terminal. Every command that today starts or closes a pane is in the list:
    `up`, the pop (`agent-spawn <role>`, which becomes `agentbus pop <role>` at the cutover),
    `relaunch`, `reload`, `swap`, `suspend`, `resume`, `retire` and `holder drain`. (`restore`
    and `forget` start and close nothing: `restore` only reads the host to prove a project is
    not live, `forget` writes the bus; both stay direct.) The command sees `AGENTBUS_TERM_ID` in
    its environment (or the holder's in-tree refusal) and sends a `host` directive to `dispatch`
    on `{p}:cmd`: `{op: up|pop|relaunch|reload|swap|suspend|resume|retire|drain, agent,
    profile?, ignore_background?, from}`, a closed set, no argv. The dispatcher checks `op`
    against the set and then applies **that command's own authorisation**, unchanged, with
    `from` as the caller: `reload` keeps `reload.go`'s rules (sentinel reloads the others,
    master reloads only sentinel, `AGENT_BUS_RELOAD=off|propose` refuse an agent caller),
    `retire` keeps its refusals, `--ignore-background` is honoured only from master or the
    person (§4.3b), and every close keeps its proof (relaunch-close). It runs the operation
    against the holder, as an outside peer, and records it in `{p}:events`. The same commands
    run from the person's own shell talk to the holder directly, as they do today.
  - **The reply, and no operation after a failure was announced** (reviewer-codex on r19). One
    deadline decides both sides, measured on one clock, Redis `TIME`, against the directive's
    entry id (also Redis time): a `host` directive may be **accepted only within 5 s of its
    entry id**. The dispatcher accepts in one Lua script, fenced by its lease like every
    dispatcher write, that reads `TIME`, refuses when more than 5 s have passed, and otherwise
    writes the `accepted` reply in the same step; it acts **only** after that script returned
    accepted. So a directive is either accepted within 5 s, with its reply already written, or
    never acted on: a dispatcher that comes back later finds it past its deadline and replies
    `expired`, acting on nothing. The reply goes on `{p}:cmd` as a `reply` entry to `from`, `ref`
    = the directive's id: `accepted` with the event id, or the refusal text the command would
    have printed, or `expired`. The command waits for that reply, not for the new agent to be
    proved (the outcome follows as a report to `from` and an event). It declares failure only
    when the deadline has passed **by Redis `TIME`** and no reply is in the stream: it then
    reads the stream for the reply once more, and only if there is none exits non-zero with:
    `the dispatcher did not answer within 5 s: from inside an agent's terminal, host commands go
    through it. Nothing was done. Start it (agentbus service start dispatch) or run the command
    from your own shell.` The text says "Nothing was done" because, past the deadline, nothing
    can be done for that directive.
  - **Acceptance never waits behind other work** (foureyes on r20). The dispatcher's reconcile
    loop ticks every 5 s and a pass reads terminals; a relaunch, reload or swap then waits for
    its proof (`relaunch.Proof`, up to 60 tries). So the `host` directives are read by **their
    own goroutine** on `{p}:cmd`, beside the reconciliation, as the self-repair chore is
    (`dispatch/selfrepairchore.go`); it never waits on a pass or on an operation. It validates
    the directive, runs the accepting script, and on `accepted` puts the operation on **one
    serial worker queue**: accepted operations run one at a time, in acceptance order, so a
    second reload waits its turn **after** being accepted, not before. (Sentinel reloads
    agents one after the other and its command returns at acceptance: its second reload is
    accepted at once and runs after the first.) The command's own preconditions — background
    work, the authorisation, an agent already gone — are checked again when the worker takes
    the operation, and a failure then is reported like any outcome.
  - **The outcome report is the confirmation.** `accepted` says only that the operation is
    queued. Each accepted operation ends with one report to `from` (and an event): done,
    refused at run time, or failed. A dispatcher that stops with operations accepted and not
    finished reports each one at its next start, to `from` and to master, as `not run: the
    dispatcher stopped` (it reads the accepted events with no outcome); it never runs them late.
    A missing outcome report is what master checks: none 10 minutes after `accepted` (longer
    than any proof wait) means the operation is lost, and master asks again or escalates.
  - **Residual risk (stated, accepted with S1 fixed).** (1) A same-uid process can leave the
    tree through a service manager (`systemd-run --user`, `launchctl submit`, `at`) and then
    passes as outside. On macOS there is no subreaper: an orphan reparents to launchd and leaves
    the tree. (2) The `from` of a bus entry is self-attested, as for every bus entry: an in-tree
    process can ask the dispatcher to relaunch or suspend an agent as if it were master. What it
    gets is bounded by the closed sets: a role's own launch, a close after proof, the
    background-work rule — never a free command, never free typing. The design no longer claims
    that raw input comes "only from a person", only that it never comes from inside an agent's
    terminal.
- `Start` puts `AGENTBUS_TERM_ID` and `AGENTBUS_HOST` in the program's environment, and returns
  the pid it spawned. Identity no longer needs a process search: the host knows its child.
- Every call reports what it observed, never what it assumed (herdr-core's rule, adopted).

Where callers land (all in the cutover PR, §7 step 10, except the `Line` change):

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
  passed beside it. This change lands in step 1 on today's herdr `Runtime` (same wake line
  typed), with a test that fails if any exported function of `dispatch` or `termhost` takes a
  `string` that reaches `Submit` or `Prompt`. `Inspection` keeps its fields; `State`, `Ready`,
  `Busy` come from `agentstate`, `Focused` becomes "person active".
- `up`, `start`, `restore`, `relaunch`, `reload`, `swap`, `suspend`, `retire`, the pop,
  self-repair call `Start/List/Close` only; run from inside an agent's terminal, those that
  start or close relay to the dispatcher (§4.2, "who may drive the holder").
- Identity: `status`/`bind` record `AGENTBUS_TERM_ID` in the existing `pane` field; the verified
  pid+start rule is unchanged.

### 4.3 `agentstate`: one detector for every host and every client

Inputs, strongest first:
1. **Declared status and turn hooks** (already on the bus: `status`, `bus.TurnState` from
   UserPromptSubmit/Stop/SessionStart). Working/idle at turn edges are exact for the clients
   whose turn hooks are wired today: Claude Code, Codex, Grok (auto-state spec 2026-11-01).
2. **Client hooks for "waiting on the person"**, per client (§4.9): Claude Code's
   `Notification` hook with the `permission_prompt` matcher (and its `PermissionRequest` hook);
   Codex's `PermissionRequest` hook. Each wired by the launcher, writing an observed `blocked`
   (not a declared one). Today `blocked` comes from herdr's `agent get`; this is the single
   largest piece of herdr intelligence we must replace.
   **Codex `notify` does not report approvals** (verified on codex-cli 0.166.0): the external
   `notify` program receives one event type, `agent-turn-complete` (payload fields `thread-id`,
   `turn-id`, `cwd`, `client`, `input-messages`, `last-assistant-message`); `approval-requested`
   exists only as one of the TUI's own desktop-notification kinds, not as a `notify` event. So
   `notify` is not a blocked source. Codex 0.166 has a hook system with a `PermissionRequest`
   event (in its hook schema); whether it fires for every approval the TUI shows is **to verify**
   by the probe in §4.9. Until verified, Codex's blocked comes **from screen signatures only**,
   and the Codex approval screens join the fixtures (§6.4).
3. **Screen signatures** (existing `Signatures`: Ready, Busy, Modal, Dialog, plus new `Blocked`
   patterns per client for permission menus, pinned by captured screens). For a client with no
   blocked hook (Kimi, Grok and `agy` today, §4.9) this is the only blocked source.
4. **Activity**: output quiet for ≥ 800 ms replaces "two reads 200 ms apart agree" as the
   settle test.

**Leaving blocked**: the blocked hook's matching end (a `PostToolUse`, `Stop` or
`UserPromptSubmit` after it), or the `Blocked` signature no longer matching on a settled screen.

The **client** is no longer detected: it comes from the agent's launch profile (`roles.toml` →
client), cross-checked with the foreground process name. A client without a verified blocked
source and screen signatures is **unsupported**: never prompted, never cleared (§4.9).

### 4.3a Master is told when an agent is blocked

Today blocked is written to `{p}:observed` (and, from this spec, `{p}:events`), but nobody is
told. On the model of the self-repair watcher's reports (spec 2026-11-09 §5: one line to master
on the bus per event, plus a ledger entry):

- **Enter.** When `agentstate` judges an agent blocked (the hook, or the `Blocked` signature on
  two settled screens), the dispatcher sends master one report: `blocked: <agent> (<client>)
  since HH:MM:SS — <screen>`, where `<screen>` is what the matched signature names (e.g. the
  tool line of the permission menu), when it names something. The screen text is the agent's
  output, so it is treated as data: one line, control characters stripped, capped at 120
  characters, quoted, and labelled `screen:`. The report is from `dispatch` (fenced, like every
  dispatcher write) and wakes master by the ordinary rules.
- **Leave.** One report when the episode ends: `unblocked: <agent> after 3m12s`.
- **One episode, one pair.** The episode is the prompt-notify decision's (2026-10-27): the same
  rule that rings the desktop once per episode decides when a new enter is sent, so a flapping
  screen does not flood master.
- **What master may do.** Master cannot answer a permission prompt: only the person can, by
  attaching. Master may reroute work and escalate to the person. The desktop ring for the person
  stays (user story 13).
- `{p}:events` records `blocked` and `unblocked` with the same fields (§6.5).

### 4.3b Background work: subagents and background tasks

An agent can end its turn (it looks idle) while a subagent or a background command still runs.
A `/clear`, a relaunch or a drain at that moment loses work or context. `agentstate` therefore
judges one more fact per agent: **background**, `none | running(<what>) | unknown`, from three
sources, any one of which says running:

- **Process facts.** The holder is the agent's parent and sees its process tree
  (`Host.Processes`: pid, start time, command name, CPU time). Long-lived helpers are always
  children — MCP servers, language servers, the client's own daemons — so "has children" is not
  enough. A descendant counts as background work when it is **not** in the client profile's
  helper list (§4.9: command-name patterns, plus every MCP server the launcher configured), or
  when it is a helper that is **active** (CPU time grew by more than `agentstate.helper_cpu`,
  default 2 % of the window, over the last 10 s). Not every client keeps its tools under its own
  process: Codex runs through a shared local app-server daemon and Grok has a shared leader
  process (auto-state spec 2026-11-01), so for them a command may not be a descendant of the
  terminal at all. The client profile states whether process facts are authoritative for it;
  where they are not, they can say running but never prove none.
- **Hooks**, where the client has them: subagent start/stop pairs (Claude Code
  `SubagentStart`/`SubagentStop`, Codex and Grok the same names, Kimi none), `agy`'s `Stop` hook
  field `fullyIdle` ("true if all background tasks are done"), tool hooks for background shells
  where the client marks them. Counts reset at `SessionStart` and when the process ends, so a
  lost stop event cannot hold the fact forever.
- **Screen signatures** for clients that show running tasks (a background-tasks footer, Codex's
  "Background terminals" list), pinned by fixtures.

The judgement is `none` only when every source the client profile declares authoritative says
none; `unknown` counts as running for the rule below (fail closed).

**Rule.** While background work is observed, a **wake is allowed** (typing a wake line starts a
turn and loses nothing), but these skip the agent: `/clear`, auto-clear, relaunch, swap,
suspend, pop-retire and `holder drain`. Automated actions (auto-clear, pop-retire, `holder
drain`'s rolling relaunch) skip and retry on their next pass; a command run by master or the
person (`relaunch`, `swap`, `suspend`) refuses with what is running (`coder: skipped —
background work: subagent ×2, process 'pytest' 4m`) and accepts `--ignore-background` from the
person or master, never from an automated caller. `agentbus down` warns with the same list.
Self-repair is unaffected: it acts only on a dead agent, which has no background work.

Contract and table tests per client: each source alone says running; helpers idle → none;
an active helper → running; a lost stop event cleared by `SessionStart`; a client whose process
facts are not authoritative never yields none from process facts alone; each skipping action
skips and each command refuses without the flag.

### 4.4 The holder (`agentbus holder`)

- One process **per project** (Q4), started as a project service (§4.4a, §4.4b), socket
  `<rundir>/agentbus/<project>/holder-<buildkey>.sock` (the build key below), mode 0600,
  same-uid peer check (`SO_PEERCRED` on Linux, `getpeereid` on macOS) plus the in-tree check
  (§4.2). `<rundir>` is `$XDG_RUNTIME_DIR` when set (Linux with a user session); otherwise
  (macOS, Linux without logind) a per-uid directory `$TMPDIR/agentbus-<uid>` (falling back to
  `/tmp/agentbus-<uid>`). A unix socket path must fit in 104 bytes on macOS: the path is checked
  at start and the holder refuses to run with a clear error.
- **Run-directory rules (security S6), the same in the holder, the supervisor and every client,
  in one function.** Each level — `<rundir>` when it is the `/tmp` or `$TMPDIR` fallback,
  `<rundir>/agentbus`, `<rundir>/agentbus/<project>` — is created 0700 if missing, then checked
  with `Lstat` (never `Stat`): a symlink at any level is refused; the owner must be the current
  uid and the mode exactly 0700, under `$XDG_RUNTIME_DIR` as well, not only in the fallback.
  The check is **mutual**: before it sends a `Start` or a single attach byte, a client checks the
  server's uid on the connected socket (`SO_PEERCRED` / `getpeereid`) and refuses another uid.
  macOS ignores a socket file's mode, so there the 0700 directory is the control, which is why
  every level is checked. Another local user who creates `/tmp/agentbus-<uid>` first denies the
  service (an accepted denial of service): the refusal says so — `owned by uid N, not you:
  another user created it; remove it or set TMPDIR`.
- **One build key (security S7)**, from one function, `service.BuildKey(raw)`, used for the
  socket, the unit name, the launchd label, the binary directory and the pidfiles: the raw build
  id lowered and reduced to `[a-z0-9.]` (any other run of characters becomes one `.`), cut to 40
  characters, then `.` and the first 10 hex digits of the SHA-256 of the raw id. Two raw ids that
  reduce alike (`+dirty`, upper case) get different keys through the hash; the raw id is recorded
  beside the key (unit, plist, pidfile), and an install whose key is already recorded with
  another raw id is refused. The key has no `-`, and a project name (`ValidName`) has no `.`, so
  every name built as `<project>.<key>` splits one way only.
- Owns the PTYs (creack/pty), is every agent's parent, reaps them, always reads output (a slow
  viewer never blocks a program: per-viewer bounded queue, a viewer that falls behind is
  repainted, herdr-core's rule).
- **Reaping as a subreaper** (Linux, §4.2). Orphaned grandchildren become the holder's children,
  so a `wait(-1)` reaper would race `os/exec`'s `Wait` on an agent's pid and could take its exit
  status. The holder never calls `wait(-1)`. On `SIGCHLD` it lists its children (`/proc/self/
  task/*/children`, or `waitid(P_ALL, …, WEXITED|WNOHANG|WNOWAIT)` to peek) and reaps with
  `wait4(pid)` **only pids it did not start** (not an agent's tracked pid); each agent's pid
  is reaped by its own `Wait`. A pid peeked but tracked is left for that `Wait`.
- Keeps, per terminal, a pure-Go emulator (screen + N lines of scrollback, setting
  `holder.scrollback`, default 5 000) and a raw tail (~1 MB) for `agentbus term dump`.
- Wire: length-prefixed frames on a unix socket; JSON control frames, raw bytes for terminal
  data; hello/welcome carry the protocol version **and the build id** (the `buildinfo` version
  plus VCS revision, the same id `agentbus --version` prints); `Describe` reports both.
- **Upgrade without killing agents**: the socket is named by build, not by protocol. **Any new
  build drains, same protocol or not** — a same-protocol bug-fix build never replaces the old
  socket, because replacing a holder means killing its agents. The new build listens on its own
  socket and takes new terminals; the old one refuses new terminals and exits when its last
  terminal ends (herdr-core's "older build drains" rule). Clients list every holder socket of
  the project and speak to each in its own protocol version (a client too old for a holder's
  protocol says so and does not touch it). `holder_draining` compares build ids. No fd-passing,
  no state migration. **The drain has no natural bound**: `/clear` and auto-clear type into the
  same process and end no terminal; only relaunch, swap, suspend and self-repair do. Two things
  bound it:
  - `agentbus health` reports `holder_draining` (warn) with the old build id, its age and its
    terminals; after `holder.drain_warn` (default 24 h) it is shown in For you.
  - `agentbus holder drain <project>` finishes it on the person's word: a rolling relaunch of
    each terminal still on the old build, one agent at a time, through the existing relaunch
    path (new terminal, proof, close the old one — relaunch-close decision), skipping an agent
    that is working, has a person active or a draft, or has background work (§4.3b). It is never
    automatic.
- The holder runs no repository code and makes no bus writes; facts it exposes are read by the
  dispatcher, which keeps doing the fenced writes. (The dispatcher stays the one automated pane
  writer: the holder types only what `Submit` hands it.)

### 4.4a Holder crash, restart and cleanup (with a service manager)

The holder is every agent's parent and holds every PTY master: when it dies, the kernel hangs
up every terminal and the project's agents die with it. Nothing in this design hides that; it
bounds and reports it.

- **One unit per build (an "instance" below = one such unit), never restarted to upgrade.** A
  single restartable unit cannot drain: starting a new build would mean restarting the unit, and
  with `KillMode=control-group` that kills every agent. So each (project, build) gets **its own
  plain unit file**, `agentbus-holder.<project>.<buildkey>.service` (systemd; the build key of
  §4.4), not a template: a template has one `ExecStart` for all instances, systemd
  cannot split `%i` into project and build, and rewriting the template on upgrade would change
  the binary of every old instance at its next restart. On macOS a launchd job per build, label
  `com.agentbus.holder.<project>.<buildkey>`. Each unit records `X-Agentbus-Project=`,
  `X-Agentbus-Build=` (the key) and `X-Agentbus-Build-Raw=` in its `[Unit]` section (systemd
  ignores `X-` keys), each plist the same three as keys of its own; every command that finds
  a project's units (`down`, `service gc`, `status`) matches these recorded values exactly,
  **never a name prefix** (project `a` must not match `a-b`'s units). An upgrade **writes and starts a new unit** beside
  the old one; it never edits, stops or restarts an old unit. Each instance has its own cgroup,
  so the old build's agents live on in theirs.
- **Each instance runs its own build.** The installer copies the holder binary to
  `<datadir>/agentbus/holder/<buildkey>/agentbus` (directory and binary 0700, owner only) and the instance's
  `ExecStart` / `ProgramArguments` points there, not at the installed `agentbus`, which the next
  upgrade replaces. A crashed old instance therefore restarts its own build.
- **Restart policy, per instance.** systemd: `Restart=on-failure`, `RestartSec=2`,
  `StartLimitIntervalSec=300`, `StartLimitBurst=5`, `KillMode=control-group`. launchd:
  `KeepAlive = {SuccessfulExit = false}`, `ThrottleInterval = 10`. A holder that has no
  terminals and is **not** its project's current build exits 0 at once: a drained or crashed old
  build retires instead of coming back empty. `agentbus down` stops every instance of the
  project; each exits 0.
- **Cleanup: owned by `agentbus service gc`.** A unit file and its binary copy are removed by
  one command, never by the holder itself (a unit deleting its own file while systemd runs it is
  fragile, and a crashed holder never runs its exit path). `service gc <project>` removes, for
  every build that is **not** the project's current build, the unit (`systemctl --user disable`
  then the file, then `daemon-reload`; launchd `bootout` then the plist) and
  `<datadir>/agentbus/holder/<buildkey>/` — only when that build's instance is **not active**
  (systemd `is-active` inactive or failed; launchd not loaded) and no holder socket of that build
  answers. It never touches the current build or a running instance. It runs at three moments:
  every install or upgrade (after the new unit is started), every `agentbus up`, and `agentbus
  holder drain` when the last old terminal has gone. `agentbus health` reports
  `holder_leftovers` (warn) when an inactive old build's unit or binary is still present after a
  gc, so a failure to clean is seen.
  Test (fake `systemctl`/`launchctl`, temp datadir): install A, upgrade to B, upgrade to C; A's
  and B's holders exit → after the gc only C's unit and binary remain; with B still active
  after the second upgrade, B's unit and binary are kept and C's start; a gc never stops a unit.
- **Excluded from every restart path.** The holder makes no Redis connection (§4.4), so a
  password change does not concern it: `setup/rotate.go`'s `restartRunningServices` and
  `setup/plugin.go`'s `ServicesHint` skip holder instances by kind, with a test for each.
  `agentbus service restart` refuses the holder unless `--kill-agents` is given, and says how
  many agents it would kill. An upgrade's only holder actions are "start the new build's
  instance" and the gc.
- **The restarted holder starts empty and relaunches nothing.** Starting agents is a decision
  (cost, quota, the person's intent). Agents come back by one of the two paths that already
  exist: `agentbus up <project>` (the person or master), which uses `{p}:resume` (§4.7); or the
  dispatcher's self-repair (decision 2026-11-09) once built and enabled, which `Start`s a new
  terminal per dead agent with its recorded resume session (`Start`'s `resume_session`; the
  argv is rebuilt from the template, §4.7), under that decision's own rules.
- **Detection.** `agentbus health` gains `holder` (crit when the project's service is enabled
  and no holder answers; warn `holder_restarted` with the time and the number of terminals lost
  when a holder's start time is newer than the agents' last proved identities).
- **Sentinel runbook entry** (`agent-bus-sentinel` skill): on `holder` crit, the one allowed
  action is the documented service start of the project's current holder instance (`agentbus
  service start holder`), never `restart` of a running one, once per episode, and only for an
  instance that is down; then annotate and escalate to master/the person with the list of agents
  lost. Never `agentbus up`, never relaunch an agent. On `holder_restarted` warn: escalate only.
- **Lifetime (Q5, decided).** Services start at login **without linger**, as the existing option
  `services.start_at_login`; logging out (or the last ssh session closing) ends the team on a
  systemd host. Agents come back through `up` or self-repair with their recorded resume.

### 4.4b No service manager (Linux without systemd, or by choice)

agent-bus must run where neither systemd nor launchd is available (a Linux without systemd, a
container) and on macOS and Linux by choice. A new setting `services.manager`
(`auto | systemd | launchd | none`, default `auto`: systemd when `systemctl --user` answers,
launchd on macOS, otherwise `none`) selects the mode. It applies to every project service
(holder, dispatch, missions), not only the holder.

With `none`:
- **Start.** `agentbus up <project>` starts each project service **detached**: a small
  supervisor process, `agentbus service supervise <kind> <project> <buildkey>`, started in its own
  session (`setsid`), stdio to `<statedir>/agentbus/logs/<project>-<kind>.log`, which runs the
  service (the holder from its per-build binary copy, as in §4.4a). The supervisor's pid and
  start time are written to `<rundir>/agentbus/<project>/<kind>-<buildkey>.pid` (with the
  raw build id); a second `up` finds it alive and starts nothing.
- **Same restart rules.** The supervisor applies §4.4a's policy itself: restart on a non-zero
  exit after 2 s, at most 5 starts in 300 s, then it stops and the `holder` health check goes
  crit; an exit 0 ends the supervisor. A holder crash leaves the PTYs hung up as under systemd;
  as there is no cgroup, the supervisor then kills the process groups the holder recorded for its
  terminals (`<rundir>/agentbus/<project>/holder-<buildkey>.pgids`, the leader's pid and start
  time each) — the equivalent of `KillMode=control-group`. The supervisor reads that file only
  from a directory that passes §4.4's run-directory check, and kills a group only when its
  leader's pid **and** start time still match the recorded pair (a reused pid, or a group whose
  leader has changed, is left alone).
- **Same drain rules.** Builds are told apart by socket and by pidfile, as with units: an upgrade
  starts the new build's supervisor beside the old one, never stops an old one; `service gc`
  removes an old build's binary copy and pidfile once its supervisor is gone.
- **Stop.** `agentbus down` signals each supervisor (SIGTERM; it stops its service, which exits
  0). `agentbus service restart` keeps its `--kill-agents` rule.
- **What does not exist without a manager.** No start at login (`services.start_at_login` is
  refused with the reason), and whether the processes survive a logout depends on the system
  (logind with `KillUserProcesses=yes` ends them). Both are stated by `agentbus service status`.
- Tests: the supervisor's restart, burst and exit-0 rules with a scripted program; the
  process-group kill skips a reused pid; `up` twice starts one supervisor; an upgrade starts a
  second supervisor and leaves the first.

### 4.5 The focus rule and drafts, restated for a host without focus

Today: "never type into a focused pane" — herdr knows which pane has focus. A holder has no
window; it has viewers. Decided rule (Q2 a):

> The dispatcher never types into a terminal while a person is **active** in it: a viewer has
> typed within `dispatch.person_quiet` (default 3 s), or a **draft** stands, or a non-observer
> viewer is attached and **not proven unfocused**.

**Drafts.** A draft is the viewer bytes typed since the last CR the person sent (focus reports
are control frames, not input, and mouse reports — SGR `CSI < … M/m` and X10 `CSI M` — do not
edit the input, so neither starts a draft). **A draft never expires into permission to type**:
the dispatcher does not type into a terminal holding a draft, however old it is. (Revision 13
read as if the draft stopped blocking after `dispatch.draft_wait`; that was wrong and is fixed
here.) What `dispatch.draft_wait` (default 60 s) now governs is only the notice:
- while a draft stands, the agent's row in busmon shows it: `draft 2m` (its age) and `wake
  waiting` when a wake is held behind it;
- after `dispatch.draft_wait`, the desktop rings **once** for that draft (`notify` event
  `draft`), naming the agent;
- the draft ends at the person's next CR (an empty Enter ends it too; whether an empty submit is
  a no-op is checked per client in the fixtures, §4.9).
The dispatcher reads `Activity` on each pass and writes the draft's start into `{p}:observed`,
so busmon shows it on the laptop as well (it reads Redis, not the holder).

**"Proven unfocused" fails closed.** Whether a viewer's terminal supports focus reports (mode
1004) cannot be queried; it can only be observed. So `agentbus attach` enables 1004 on the outer
terminal and forwards focus-in/focus-out to the holder as control frames (never as program
input). A viewer counts as **focused from the moment it attaches** and stays so until the
holder has received a focus-out from it; after a focus-out it is unfocused until the next
focus-in. A terminal that never sends focus reports therefore blocks typing for as long as it
is attached. An observer (`--observe`) never blocks: it cannot type. The quiet timer and the
draft apply in addition, never instead. The focus notice (`dispatch.focus_notice`) keeps its
meaning: a wake waiting on the person is shown in busmon.

### 4.6 The person's console: busmon + attach

- `agentbus attach <agent> [--observe]`: puts the current terminal on the agent's terminal.
  Raw bytes both ways, first paint from the emulator's screen. Outer terminal restored on detach
  (synchronized output, kitty key flags, modes, scroll region, cursor, colors — the list in
  herdr-core `attach.rs`). `--observe` never types or resizes. Works over `ssh host agentbus
  attach coder` with no extra machinery (laptop ⇄ host topology). A non-observer attach from
  inside an agent's terminal is refused by the holder (§4.2); an observer attach is allowed.
- **The program's output is filtered on its way to the person's terminal (security S3).**
  herdr re-rendered agent output; raw passthrough would hand the person's own terminal whatever
  an agent prints — repository files, fetched pages, tool output. The attach side (in the holder,
  on each viewer's stream, so `ssh` attaches get it too) runs the output through the emulator's
  parser and drops, before it reaches the viewer:
  - **OSC 52** (clipboard write, which could poison the person's next paste into a shell),
    unless the setting `attach.osc52` is `on` (default `off`). Whether a client's own copy
    command relies on OSC 52 (Claude Code's `/copy`, a TUI's selection copy) is a step-3 probe;
    with the setting off, that copy does not reach the person's clipboard, and the person may
    turn it on;
  - **OSC 8** hyperlinks: the link is stripped, its text kept (a link that shows one address and
    opens another);
  - **report queries**, whose replies the outer terminal would type back into the program as
    input: window and title reports (`CSI … t` with a report code), `DECRQSS` and XTGETTCAP
    (`DCS $ q`, `DCS + q`), OSC colour queries (an OSC 4/10/11/12 payload of `?`), and DA/DSR
    (`CSI c`, `CSI > c`, `CSI 5 n`, `CSI 6 n`) and the kitty keyboard query (`CSI ? u`).
    The holder's emulator **answers** DA, DSR, the cursor position and the kitty keyboard query
    itself, from its own state, whether a viewer is attached or not (a program waiting on a reply
    must not hang because no one is watching); the other queries get no answer. The answers
    describe the **emulator**, not the viewer's terminal: a program told that kitty keyboard
    flags are on may still receive legacy keys from a viewer whose terminal lacks the protocol;
    the clients accept both encodings, as they do today under herdr.
  The first paint is rendered from the emulator's screen and carries none of these. The filter
  is in the holder's e2e suite (§8).
- **The prefix key is a setting**, `attach.prefix`, shown and editable in busmon's config screen
  (Q9 replaced, the person 2026-11-11). `<prefix> d` detaches; `<prefix> <prefix>` sends one
  literal prefix key to the program; while viewer keys are **held** after `ErrNotLanded`
  (§4.2), `<prefix> r` releases them and `<prefix> x` discards them; any other key after the
  prefix (and `r` or `x` when nothing is held) is dropped with a bell. The
  prefix waits for its second key without a timeout and is shown in the outer terminal's title
  while pending. (`<prefix> y` is gone with the submit guard.)
  - **Default `f12`, provisional until step 3 verifies it for every client.** `ctrl+\` and
    `ctrl+]` need AltGr on French and Swiss layouts and are unreliable there; `ctrl+]` is bound
    by Claude Code (`app:openArtifact`, in 2.1.322's default key table) and `ctrl+\` by Grok
    (Agent Dashboard, its bundled shortcut guide). No `ctrl+letter` is free across the clients:
    Claude Code alone binds b c d e f g j l n o p r s t u v x y and `_`, uses readline's a e k w h
    in its input, and handles z (suspend); Grok binds `ctrl+q` (quit); `ctrl+q`/`ctrl+s` are also
    flow control on a cooked tty, and `ctrl+h/i/j/m` are backspace, tab, LF and CR. F12 needs no
    modifier on any layout. Where F12 stands per client (§4.9):
    - **not bound, verified**: Claude Code (its default key-binding table has no `f12` entry;
      the `f12` strings are its key-name parser) and Grok (its shortcut guide binds `F2` and
      `F8`, not `F12`);
    - **not bound as far as the binary shows**: Kimi (`f12` only in a key-name parser) — weaker
      than a binding table, so the probe below confirms it;
    - **not verifiable offline**: Codex CLI and `agy`. A compiled binding leaves no `f12` string
      (a crossterm binding is `KeyCode::F(12)`), so finding none proves nothing.
    The **probe** (§7 step 3, one recording per client): `agentbus term record <client>
    f12-at-input` — start the client, type `abc` at the input, press F12 in the legacy and the
    kitty encoding, wait 2 s, record; F12 is free for that client when the screen after F12 is
    identical to the screen before it (input still `abc`, no overlay, no mode change). The
    probe runs for **every attachable client**, supported or not: `Start` runs only a role's
    launch profile, so the attachable clients are exactly the clients that have a profile
    (§4.9), and an unsupported client (never prompted) can still be attached by the person. The
    default becomes final when the probe passes for every client with a profile; if one binds
    F12, step 3 picks the default instead from the same probe run over `f1`–`f12`: the first key
    free for every client with a profile, preferring F12, F11, F10, then descending. Until then
    F12 is the default only as a proposal, and step 7 ships whatever step 3 settled. Each
    profile records, per probed key, `free` or `bound`; a key not probed for a client is
    `unknown` for it. Cost: on Mac
    keyboards F12 is a media key unless `fn` is held or "use F1, F2… as standard function keys"
    is set — the setting is the answer there.
  - **Accepted values**: `f1`–`f12`, or `ctrl+<letter>`, `ctrl+\`, `ctrl+]`. A value is refused
    when the client matrix (§4.9) lists it as bound by a client used in the project, or when it
    is one of `ctrl+c/d/h/i/j/m/q/s/z`; busmon's config screen shows why.
  - **Attach is gated per client** (reviewer-codex on r15), because a profile added later, or
    a client's new version, can bind the key the default was chosen without. A non-observer
    `agentbus attach` reads the terminal's client profile and the effective prefix:
    - prefix `free` for that client → attach;
    - prefix `bound` for that client → **refused**, naming the client, the binding and the keys
      the profile lists as `free`; `agentbus attach --prefix <key>` with a `free` key attaches
      with that prefix for this session only;
    - prefix `unknown` for that client (not probed, or a client version newer than the profile's
      probed range) → **refused** unless the person chooses the prefix explicitly for this
      attach: `--prefix <key>` (a `free` key, or an `unknown` one the person accepts; then the
      attach starts with one line, `<key> is not verified for <client>: press it twice to send
      it to the program`). The default is never used silently on a client it was not checked
      against.
    `--observe` is never gated: an observer sends no input, so intercepting the prefix takes
    nothing from the program. busmon's attach key runs the same command and shows a refusal in
    its status line, with the `--prefix` choices.
  - **Both encodings are recognised**, because the outer terminal switches to the kitty keyboard
    protocol whenever the program inside asks for it (attach forwards the program's key flags):
    F12 as legacy `CSI 24 ~` and kitty `CSI 24 ~` / `CSI 24 ; 1 : 1 ~` (press; the release event
    `: 3` and repeats are ignored); `ctrl+<letter>` as the C0 byte and kitty `CSI <code> ; 5 u`.
    The prefix split across two reads is still recognised.
- **Size**: the most recently active viewer sets the size; with none, the size the terminal was
  started with (default 200×50, setting `holder.size`). Agents do not see a resize from a
  read-only observer.
- busmon (bubbletea): the Agents panel gains a **live preview** of the selected agent's screen
  (read-only, from `Screen`), and `enter`/`a` runs `agentbus attach` through `tea.ExecProcess`;
  detach returns to busmon. The row shows drafts (§4.5), blocked (§4.3a) and background work
  (§4.3b). busmon itself is no longer a herdr pane or plugin; it runs in any terminal.
- **Visibility (Q1, decided)**: busmon list + live preview + full-screen attach; side-by-side
  viewing of several agents through the terminal's own splits (one `attach` each). busmon does
  **not** become a multiplexer.

### 4.7 Resume and restore (storage revised)

- Process lifetime = holder lifetime. The holder survives busmon, ssh sessions and the person's
  terminal closing; it does not survive reboot, `agentbus down`, or (without linger) logout.
- The resume the SessionStart hook reports today to herdr is written instead to a
  **separate hash**, `{p}:resume` (field = agent, value = JSON `{session, profile, at}`), not into
  the `{p}:agents` snapshot. Reason: a snapshot field would need a `mergeSnapshot` carry rule,
  and resume's is the opposite of every identity field there (BootID, Model, ClientFP follow the
  pid; resume must **outlive** the process and survive later statuses that do not carry it —
  the c19b3255 hand-off bug shows what a missing rule costs). A hash of its own has no merge.
- **Per client.** Today only Claude Code resumes (`rolecfg/launch.go`: "session resume is
  supported only for Claude Code"). The other clients have a resume form (§4.9) and a session id
  source of varying strength; each becomes resumable when its row in the matrix is verified, as
  data in its client profile. A client without a verified session id source starts fresh (never
  `--continue`, which picks "the most recent session for this folder" and can pick another
  agent's when roles share a folder).
- Write path: not lease-fenced (the hook is not the dispatcher) but an optimistic transaction
  in `bus/` on the `TurnState` pattern: `WATCH {p}:agents`, check the caller is **a descendant
  of** the agent's stored verified process, then `HSET {p}:resume`. An unproved caller writes
  nothing. Persisted `at` comes from Redis `TIME`.
- **The caller proof is an ancestor walk.** The SessionStart hook is not the agent's process:
  it runs as a child (or grandchild, through a shell) of the client. The hook passes its ancestor
  chain — each ancestor's pid **and start time** — and `bus/` accepts the write only when one
  ancestor matches the stored verified (pid, start) exactly. The walk starts at the hook's own
  parent, stops at pid 1, at the first unreadable process, or after 32 ancestors. A reused pid
  fails because its start time differs. Prior art: `dispatch.Ancestors` (Linux, `/proc/<pid>/stat`
  ppid + start). macOS gets the same function behind one platform file: `sysctl
  kern.proc.pid.<pid>` (`kinfo_proc`: `e_ppid`, `p_starttime`) through `golang.org/x/sys/unix`;
  until then macOS writes no resume (fail closed). A client whose hooks run under a shared daemon
  rather than under the agent's process (Codex's app-server) fails this proof by construction;
  its row in §4.9 says so and its resume waits for a proof that works for it.
- **The proof is self-attested**, exactly like the turn and prove-session hooks: it stops an
  honest mistake (a hook of another agent, a stale process, a reused pid), not a same-uid process
  that lies — which could write any bus entry anyway. That is acceptable only because what it
  records can never be executed as given (next item).
- **A stored argv is never executed (security S1, high).** `bus.RecordResume` stores any argv
  today, and `parseResume` checks only that `argv[0]` is set: a record run as given would turn
  write access to the broker (the laptop over the tunnel, any holder of the Redis password, any
  same-uid `HSET`, which skips the ancestor proof) into command execution on the host,
  automatically on self-repair. So:
  - the record is a closed set of names, **never a command**: `{session, profile, at}`;
    step 8 drops `argv` from `RecordResume` and from `ResumeRecord` (coder's
    `fix/resume-argv-s1`, 3999ca17, has this shape). `profile` is the profile the session ran on
    (`""` = the role's own): a session belongs to its client, so a role swapped to another
    client's profile must resume on that profile, not on the role's default. An old record still
    holding `argv` is read for its `session` alone (profile `""`); the `argv` field is ignored,
    never displayed as a command to run;
  - at launch, `up` and self-repair pass `resume_session` to `Start` (§4.2), and the holder
    rebuilds the command from the template — today's `rolecfg.ResumeArgv(launcher, manifest,
    role, project, profile, session)`, which re-validates the session id
    (`sessionIDPattern`), the names and the absolute paths. Two conditions on the record
    (security, s1-profile-confirm, report 1794585522550-0), both checked **before** `Start`:
    1. **Allowed profiles.** The record's `profile` (re-validated with `ValidName`) must be in
       the role's **allowed set: its own profile plus its `fallback_profiles`, read from the
       local manifest (`roles.toml`) only** — never from the broker, so neither from
       `{p}:swap:*` (the swap intent) nor from `{p}:fallback`, both broker keys. Not "any
       manifest profile": `rolecfg.ResolveWith(role, profile)` accepts any, because it serves
       swap, and a broker writer could then start a read-only reviewer as a
       `bypassPermissions` client. A profile outside the set is an **invalid record** (next
       item). Cost, accepted: an agent that dies while swapped to an undeclared profile
       (`swap --force`) restarts fresh on `up` and is reported, not resumed, by self-repair —
       the safe direction;
    2. **The session belongs to that profile's client.** The session id must match the
       session pattern of the client the profile runs (§4.2's `resume_session` rule) and that
       client must be resumable (`rolecfg.ResumeVerified(client)`, as `LaunchProfileResume`
       already checks); otherwise an **invalid record**;
  - **Accepted residual risk** (security). Within the allowed set, a broker writer can still
    choose between the role's own profile and its declared fallbacks, and can name another
    agent's session of the same client (session ids are not secret: a resume record holds
    one). Both stay within launches the person configured for that role, and a broker writer
    can already steer agents through directives, so this adds no new capability. Refusing a
    resume on a profile more permissive (permission mode, sandbox) than the role's own is
    optional hardening, not required;
  - an invalid record — a session id or profile that fails validation or either condition —
    or none recorded: on **`up`** a **fresh** start and a
    person notice (the person or master asked for the agent); on **self-repair** no `Start` at
    all and one report to master — self-repair never starts an agent fresh (the person, Q4 of
    the self-repair spec, its §4.1 item 5; `selfrepair.Decide` already reports instead);
  - regression test: `HSET {p}:resume coder '{"argv":["sh","-c","touch /tmp/x"],"session":"abc"}'`,
    then the `up` path and the self-repair path against the fake host: the started command
    equals the template's resume command for session `abc`, and nothing else runs; with a
    session `abc;rm`, `up` starts fresh and self-repair starts nothing and reports. Condition
    1: a record `{session, profile: "claude-opus"}` for `reviewer-codex` (a profile that is
    neither its own nor a fallback) → `up` starts fresh on `codex-review` with a notice,
    self-repair starts nothing and reports; a record on one of the role's `fallback_profiles`
    resumes on it (an agent that died after a completed cross-client swap resumes on its
    fallback). Condition 2: a Codex-shaped session id with a Claude profile is refused before
    `Start`; so is a session on a client that `ResumeVerified` does not list.
- Cleared only by `agentbus forget <agent>` and replaced by the next proved session's report;
  an agent's death or a status never touches it.
- **It must survive a reboot, so the broker must persist.** The shipped `docker-compose.yml`
  runs the broker with AOF on a named volume (§6.3). `agentbus health` gains
  `broker_persistence` (warn when `CONFIG GET appendonly` is `no` and `CONFIG GET save` is empty;
  unknown — not warn — when `CONFIG` is refused). Without persistence, a reboot loses
  `{p}:resume` and agents start fresh; nothing worse.
- `agentbus up` on a project whose holder has no terminal for a role starts it with the
  recorded session when one is recorded (the command rebuilt from the template, above), else
  fresh. Self-repair (decision 2026-11-09) shares **only** the command's construction (`Start`
  with `resume_session`, the command rebuilt by the holder from the template): when the session
  is missing or fails validation it calls no `Start` and reports to master, as above. It never
  takes `up`'s fresh branch.

### 4.8 What the cutover deletes

`herdr-plugin.toml`, `agentbus plugin`, `busmon --pick` as a herdr overlay (the projects page
stays), herdr parts of `setup.sh` (link, key binding block), `dispatch/herdr.go` (its signatures
moved to `agentstate` earlier), `herdrbin.go`, `reportresume.go`'s herdr call, every `HERDR_*`
read, the herdr stubs in tests, the herdr reporting in `healthchecks.go`, `suspendsnap.go`,
busmon `projects_admin.go` and `about.go`, `setup/plugin.go`, `setup/status.go`,
`setup/uninstall.go`'s herdr parts; decisions 2026-10-19 (plugin) and the herdr parts of
2026-10-27 (relaunch-close) marked superseded. Exit check: `git grep -il herdr -- '*.go'` lists
only the test that forbids a herdr exec (and its own name).

### 4.9 Clients: the matrix, as data

Detection is per client. Every point of this spec — blocked, background work, resume, the clear
command, an empty submit, the key bindings the prefix must avoid — has an answer per client, kept
as **data**: a client profile (`agentstate/clients/<client>.toml`, embedded) plus its recorded
raw fixtures. Adding a client means a profile and fixtures, not code — with one limit stated
honestly: wiring hooks uses one of a small closed set of mechanisms (Claude Code's `--settings`
JSON, Codex's `-c` overrides, Grok's project hook file, `agy`'s `hooks.json`), and a client that
needs a new mechanism needs code for it once.

A client profile holds: binary and version range probed; launch form (already in
`rolecfg/launch.go`); turn and blocked hook routes; `Ready`/`EmptyInput`/`Busy`/`Modal`/`Dialog`/
`Blocked` signatures; background-work sources and which are authoritative; helper process patterns;
resume form and session id source; clear command; whether an empty submit is a no-op; key
bindings, and per probed prefix key `free` or `bound` (§4.6). **Supported** = a verified blocked source **and** screen signatures for Ready,
`EmptyInput` and Blocked passing on recorded fixtures. An unsupported client is never prompted and never cleared.
A new client or version is probed with `agentbus term record` (§6.4).

Verified on this host on 2026-11-11 (Claude Code 2.1.322, codex-cli 0.166.0, Kimi 2.2.0, Grok
1.0.77, `agy` 1.5.6) from the installed binaries, their `--help`, `agy`'s bundled
`agy-customizations/docs/hooks.md`, Grok's bundled user guide (`~/.grok/docs/user-guide/`), and this repository; "to verify" names the probe.

| Client | Blocked source | Background-work signal | Resume | Clear command | Bindings the prefix avoids |
|---|---|---|---|---|---|
| **Claude Code** | `Notification` hook, matcher `permission_prompt` (verified in the binary), and `PermissionRequest` hook; + `Blocked` screen signature | `SubagentStart`/`SubagentStop` hooks (verified in the binary); process tree (tools run as its descendants); background-tasks footer: to verify (fixture of a `run_in_background` shell) | **yes, built** (`--resume <id>`, id from the SessionStart hook) | `/clear` (the only client `dispatch` clears today, `clearClients`) | ctrl+ b c d e f g j l n o p r s t u v x y `_` `]`, and readline a e k w h; F12 **not bound** (verified: absent from the default key-binding table) |
| **Codex CLI** | **not** `notify` (turn complete only, verified); `PermissionRequest` hook in the 0.166 hook schema — **to verify**: launch with `--ask-for-approval on-request` and the hook wired, record a command needing approval, the hook must fire before the approval screen settles; until then screen only, with Codex approval fixtures | `SubagentStart`/`SubagentStop` hooks (in the schema; to verify by the same probe with a subagent); "Background terminals" on screen (string verified; fixture to record); process tree **not authoritative** (shared local app-server daemon) | flag exists (`codex resume <id>`); session id from the SessionStart hook (wired today for the prover); caller proof under the app-server: **to verify** (record the hook's ancestor chain) | `/clear` semantics not probed: **to verify**; not cleared until then | F12 **not verifiable offline** (no binding table readable; a crossterm binding leaves no string): the `f12-at-input` probe (§4.6) |
| **Kimi CLI** | screen signatures only: Kimi hooks are not used (the person, 2026-11-02, auto-state spec) | process tree (to verify that tools are its descendants: record `ps` during a tool run); screen: to verify | flag exists (`--session <id>`); no session id source without hooks: **to verify** (probe the session store after a launch); fresh start until then | to verify | F12 not bound as far as the binary shows (`f12` only in a key-name parser): confirmed by the `f12-at-input` probe; others to verify by recording |
| **Grok CLI** | screen signatures; a permission hook event: **to verify** (the binary holds `Notification`; project hooks are wired for turns) | `SubagentStart`/`SubagentStop` (its hook events carry `subagentType`, already filtered by `turnhook.go`); process tree **to verify** (shared leader process) | flag exists (`--resume <id>`, `--session-id <uuid>` for a new one: the launcher can choose the id); to verify end to end | to verify | `ctrl+q` (quit), `ctrl+\` (Agent Dashboard), `F2` (settings), `F8` (voice) bound; F12 **not bound** (verified: bundled `~/.grok/docs/user-guide/03-keyboard-shortcuts.md` and the config reference) |
| **Antigravity `agy`** | screen signatures only: its documented hook events (`SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`) include no approval event | `Stop` hook field `fullyIdle` ("true if all background tasks are done", documented); process tree to verify | flag exists (`--conversation <id>`); id = `conversationId`, in every hook payload (documented); hooks not wired today: to build and verify | to verify | F12 **not verifiable offline**: the `f12-at-input` probe (§4.6); others by recording |

Out of scope: Gemini CLI (the Google client is `agy`; the person, Q7) and DeepSeek (the person,
2026-11-11). Every "to verify" above is a §7 step-3 deliverable: the recording, the fixture and
the profile row, or the row stays as it is and the feature it gates stays off for that client.

## 5. User stories

1. As the person, I want agent-bus to install and run without herdr, so that one Go toolchain builds everything I depend on.
2. As the person, I want `agentbus up <project>` to start every role's agent in its own terminal, so that a project starts with one command as today.
3. As the person, I want to see which agents run, their state and a live preview of the selected one in busmon, so that I can watch the team without opening each agent.
4. As the person, I want to attach to an agent full-screen from busmon and return with the attach prefix followed by `d`, so that I can take over a conversation.
5. As the person, I want to attach to an agent from another terminal or over ssh, so that I can work from my laptop against the host.
6. As the person, I want an observe-only attach, so that I can watch without any risk of typing into the agent.
7. As the person, I want the dispatcher never to type while I am typing or have a draft in an agent, however old the draft, so that a wake line never lands in the middle of my sentence.
8. As the person, I want to see in busmon that a draft stands in an agent, its age, and that a wake waits behind it, and to be rung once when it has waited long, so that I do not leave a team stalled.
9. As the person, I want agents to keep running when I close busmon or my terminal, so that work continues unattended.
10. As the person, I want agents restarted with their previous conversation after a reboot when I run `agentbus up`, so that I do not lose context.
11. As the person, I want to scroll an agent's recent output, so that I can see what it did while I was away.
12. As the person, I want a dump of an agent's raw recent output, so that I can attach it to a bug report.
13. As the person, I want the desktop to ring once when an agent waits on a permission prompt, as today, so that I do not miss a blocked agent.
14. As the person, I want upgrading agent-bus not to kill running agents, so that an update is not a team restart.
14a. As the person, I want to be told when an old holder build is still serving, and to finish its drain with one command that relaunches agents one at a time, so that an upgrade does not linger forever.
14b. As the person, I want old holder units and binaries removed once their build is done, so that upgrades do not accumulate files.
14c. As the person, I want agent-bus to run on a machine without systemd or launchd, so that it works on any Linux or macOS I use.
14d. As the person, I want to choose the attach prefix key, so that it works on my French or Swiss keyboard.
15. As the dispatcher, I want an agent's screen, activity and process facts from the host through one interface, so that my wake decisions do not depend on how the host is built.
16. As the dispatcher, I want the host to refuse typing at the moment a person is active, and to hold the person's keys while it types, so that a wake line can never mix with their input.
17. As the dispatcher, I want to type only the wake line and `/clear`, by type, so that no message text can ever be typed into a terminal.
18. As the dispatcher, I want to observe that my line reached the input before I submit it, so that a CR never answers a dialog.
19. As the dispatcher, I want each agent's client from its launch profile, so that the right signatures apply without screen guessing.
20. As the dispatcher, I want "blocked" from the client's own permission hook where it has one, so that blocked is exact instead of inferred from screen text.
21. As the dispatcher, I want a settled-screen test based on output quiet time, so that a screen still rendering is never judged ready.
22. As an agent, I want my terminal id and host in my environment, so that `status` and `bind` record my identity without a process search.
23. As master, I want relaunch, swap and suspend to start a new terminal and close the old one once the new agent is proven, so that the relaunch-close rule survives the change.
24. As master, I want self-repair to relaunch a dead agent through the same `Start`, so that there is one way to create an agent's terminal.
24a. As master, I want a report when an agent becomes blocked and when it stops being blocked, so that I can reroute work and escalate to the person.
24b. As master, I want `/clear`, relaunch, swap, suspend and drain to skip an agent whose subagents or background commands still run, so that no work or context is lost.
25. As sentinel, I want the holder's health (up, build id, terminals, draining builds, leftovers) in `agentbus health`, so that I can start only what the runbook allows.
26. As a maintainer, I want detection tested from recorded raw output of each client, so that an emulator change that alters a screen fails a test.
27. As a maintainer, I want a recorder that captures a client's raw output to a fixture, so that a new client or a new client version can be probed in minutes.
28. As a maintainer, I want the emulator behind an internal seam with a fidelity oracle in tests, so that the production emulator can be swapped after a bake-off.
29. As a maintainer, I want each client described as data (profile + fixtures), so that adding a client is data and recordings, not code.
30. As a security reviewer, I want the holder's submit frame to carry no text, so that no same-uid process can make the holder type arbitrary input into an agent.
30a. As a security reviewer, I want `Start` to carry only a role and a stored resume to carry only a session id, so that neither a same-uid process nor a broker write can make the host run a command of its choosing.
30b. As a security reviewer, I want the holder to refuse typing, starting and closing from inside an agent's terminal, so that an agent cannot drive another agent's terminal or escape its sandbox through the holder.
30c. As the person, I want an agent's output filtered before it reaches my terminal, so that it cannot write my clipboard or type replies into the program.
31. As the person, I want the last release with herdr tagged before the cutover, so that I can go back to it if the cutover fails.
32. As a maintainer, I want holder tests to use a temporary socket directory and scripted programs, so that no test touches a running holder or a real agent.
33. As a reviewer, I want each step as one PR from main, so that each is reviewable and revertible alone until the cutover.

## 6. Implementation decisions

### 6.1 Modules
- `termhost` (new): `Host`, `Spec`, `Term`, `Screen`, `Activity`, `Proc`, `Line`, `HostInfo`;
  adapters `holder` (client side) and `fake`. Contract test suite in the package, run on both.
- `agentstate` (new, from `dispatch`): `Signatures` and their fixtures move here; client profiles
  (`clients/*.toml`, §4.9); `Blocked` and `EmptyInput` patterns, hook-fed observed blocked, background-work fact
  (§4.3b), settle by quiet time. Pure functions: facts in, judgement out.
- `holder` (new, server side) + `screen` (internal seam over the emulator: `Write`,
  `Snapshot(lines)`, `Modes`, `Render` for first paint). PTY and process code one file per
  platform (`pty_unix.go`; `proc_linux.go`, `proc_darwin.go`), so a `_windows.go` (ConPTY) can
  be added later without touching the rest.
- `dispatch`: `Runtime` implemented by `termhost`+`agentstate`; decision code unchanged except
  `Focused` → person active, `ErrPersonActive`/`ErrNotReady`/`ErrNotLanded` from `Submit`, the
  blocked reports to master (§4.3a), and the background-work skip (§4.3b).
- `cmd/agentbus`: `holder`, `attach`, `term record`, `term dump`, `holder drain`, `service gc`,
  `service supervise`; `up`/`start`/`restore`/`relaunch`/`swap`/`suspend`/`resume` on `Host`,
  relayed to the dispatcher when run in-tree (§4.2); the dispatcher's `host` directive handler.
- `service`: `BuildKey` (§4.4) and the run-directory check, one function each, used by every
  caller.
- `config/keys.go`: `holder.scrollback`, `holder.size`, `holder.drain_warn`,
  `dispatch.person_quiet`, `dispatch.draft_wait` (the ring, §4.5), `dispatch.submit_wait`,
  `agentstate.helper_cpu`, `attach.prefix`, `attach.osc52`, `services.manager`. No `terminal.host`: there is one
  host.
- `cmd/busmon`: preview panel, attach key, draft/blocked/background on the agent row,
  `attach.prefix` in the config screen; no herdr env.
- `service`: holder as one plain unit (systemd) / one job (launchd) per project and build, or a
  supervisor with `services.manager=none` (§4.4a, §4.4b); `gc`.

### 6.2 Libraries
- PTY: `github.com/creack/pty` (Linux, macOS; pure Go). Not ptyx (its Windows half is out of
  scope; its Mux is replaced by our attach).
- Raw mode, size, SIGWINCH for attach: `golang.org/x/term`, `golang.org/x/sys/unix`.
- Emulator: bake-off (§6.4) between pure-Go candidates — `go-vte` + our own grid,
  `charmbracelet/x/vt`, `hinshun/vt10x` (the last two not yet checked out locally: to verify).
  gostty only as a **test oracle** behind a build tag (Q3, decided).
- herdr-core: reference for holder framing, input gate, attach restore list, drain-on-upgrade.
  No code copied (its own rule, and a different language).

### 6.3 Wire and state
- Redis: one new hash `{p}:resume` (§4.7) and one new stream `{p}:events` (§6.5), both added
  to `projectKinds` (key recognition: `--delete`, project listing, backup and restore), and the
  observed `blocked`, draft and background facts in `{p}:observed`. `projectKinds` does **not**
  drive `busmon --reset`: that purges busmon's own `streamKinds` list. `{p}:events` therefore
  joins the reset explicitly (§6.5); `{p}:resume` deliberately does not (a reset trims activity,
  it must not forget how to resume agents).
- **Broker floor: Redis 7.2 / Valkey 7.2.** The design uses only commands available in 7.2
  (`WATCH`/`MULTI`, `HSET`, `HGETALL`, `HDEL`, `TIME`, `CONFIG GET`, `XADD … MAXLEN ~`,
  `XREVRANGE`, `XREAD BLOCK`). A first grep at b3dd13a2 found none of the newer commands
  (hash-field TTL family, `HGETDEL`/`HGETEX`/`HSETEX`, `XACKDEL`/`XDELEX`, vector sets).
- **Shipped broker: `valkey/valkey:9`** in `docker-compose.yml` (the person needs none of
  Redis 8's built-in modules), with `--appendonly yes` on the named volume as today. The change
  (§7 step 8) must handle three things, each checked in that PR:
  - the server binary and the health check's CLI are `valkey-server`/`valkey-cli` (whether the
    image also ships `redis-*` names is to verify; use the `valkey-*` names);
  - the config-on-stdin form (`valkey-server -`) and `requirepass` behave as in the Redis
    command: to verify by `docker compose up` and an authenticated `PING`;
  - **the existing volume was written by Redis 8.** Whether Valkey 9 loads a Redis 8 RDB/AOF is
    **to verify** on a copy of the volume; if it refuses, the migration is `agentbus backup` on
    Redis 8, a fresh volume, restore on Valkey — stated in the release note, never a silent
    switch on the person's data.
  The Redis integration suite is re-run against a disposable Valkey 9 (and once against Valkey
  7.2, the floor) before that PR merges; the run is the evidence, not the grep.
- `{p}:agents` and `subscribe` JSON unchanged.
- Holder protocol: versioned; additive only within a version, like `subscribe`.

### 6.4 Raw fixtures and the emulator bake-off (the gate before the cutover)

Shadow mode against herdr is dropped (direct cutover). The gate is recorded output instead:
- `agentbus term record <client> <fixture>` runs a client under our PTY (no holder needed) and
  saves bytes + resize events + the hook events received, with timestamps.
- **Raw fixtures per client** in every state the matrix needs: empty input, typed, working,
  modal, dialog, **each permission screen** (Codex approval screens included), background work
  running and finished, an empty submit, F12 pressed at the input, `/clear` where a client
  has one, and an input holding text the client put there itself (a queued message returned on
  Esc, where the client does that), so `EmptyInput` is pinned both ways.
- **The paste into a dialog (security S4)**, one fixture per client: with its permission dialog
  open, the wake line and then `/clear` are pasted (bracketed); the dialog must select nothing
  and the client's state must be unchanged. A client whose dialog acts on the paste is
  unsupported until a profile rule avoids it. The same recordings show whether each client
  enables mode 2004 at its input (a client that never does is never prompted, §4.2 step 1), and
  whether its copy command relies on OSC 52 (§4.6).
- An emulator candidate passes when, replayed, its screen satisfies every signature test for
  every client (ready, empty input, typed, busy, modal, dialog, blocked, background, old box), renders wide
  chars (Kimi's moon, `❯`, `›`, `─`) at the right column, and keeps SGR dim (Claude/Codex
  placeholders). Lines of emulator code we would own count against a candidate.
- **A client that fails stays unsupported** (never prompted, never cleared). The cutover does not
  wait for every client; it waits for the clients the person's projects use.

### 6.5 Newer broker features (the person's four ideas, evaluated)

Context. The floor is Redis 7.2 / Valkey 7.2 (§6.3). Hash-field expiration (`HEXPIRE`/`HSETEX`/
`HGETEX`…) needs Redis 7.4+ (8 for `HSETEX`/`HGETEX`) or Valkey 9; adopting it **raises the
floor** for every install. The shipped broker becomes Valkey 9 (§6.3), so the floor is a promise
to people running their own broker, not a constraint on ours. The project already has:
per-project streams `status`, `report`, `notify`, `cmd` that busmon replays (`Recent`/`TailFrom`)
and follows; `{p}:agents` written only through `putSnapshot` (an optimistic `WATCH` merge);
`{p}:observed`, a whole-hash TTL written by the dispatcher; busmon polls about once a second;
the client is go-redis v9.19.

| # | Idea | Verdict | Why |
|---|---|---|---|
| 1 | Presence by field TTL | **Defer** (and never on `{p}:agents` or `{p}:resume`) | See below. |
| 2 | Per-project event stream | **Adopt**, as `{p}:events` | See below. |
| 3 | Keyspace notifications for busmon | **Defer** | See below. |
| 4 | RESP3 client-side caching | **Reject for now** | See below. |

**1. Presence by field TTL — defer.** The idea needs a writer that refreshes the field on a
timer. An agent cannot: a client between turns runs nothing, so an idle agent would look
"silent" while healthy. The holder could, but it makes no Redis connection by design (§4.4a: it
must not be in the password-rotation path). The dispatcher already writes `{p}:observed` with a
whole-hash TTL and every observation carries its own `At`, judged by `Observation.Fresh` on the
reader side; per-field TTL would only move that age check to the server, at the cost of raising
the floor. Never on `{p}:agents` (a field that expires there would silently drop a verified
identity) and never on `{p}:resume` (it must outlive the process; §4.7). Revisit if the floor
moves to 7.4 for another reason.

**2. Event stream — adopt as `{p}:events`.** Terminal-lifecycle facts have no home today and
this spec adds several: wake typed / refused (person active, not ready) / not landed, blocked /
unblocked, draft waiting, background work skipped an action, attach / detach (observer or not),
relaunch, holder started / restarted / draining / drained / gc. One stream per project,
`XADD {p}:events MAXLEN ~ 10000`, fields `{kind, agent, term, by, detail}`, time from the entry id
(Redis `TIME`). Writers: the dispatcher (fenced, like its other writes), `agentbus attach` for
attach/detach (the person's own process; unfenced, as a person's `notify` is today), `holder
drain`, `service gc` and `up` for relaunch/drain/gc. Not the holder (no Redis). busmon replays it
on start (`XREVRANGE` of the last N) and follows it with `XREAD BLOCK`; it is a **timeline, not a
source of truth**: state stays in `{p}:agents`, `{p}:observed`, `{p}:resume`. Lists, each named
on purpose:
- `projectKinds` (bus): add `events` and `resume`, so `--delete`, project listing and
  backup/restore know both keys;
- busmon's reset list: `--reset` purges `streamKinds` (status, report, notify, cmd) today. It
  becomes an explicit `resetKinds = streamKinds + events`, used only by `--reset` (`XTRIM`,
  groups kept, as today);
- an explicit `survivesReset` list holding `resume`;
- busmon's tail lists: `streamKinds` stays the ACTIVITY feed (unchanged); the timeline reads
  `events` with its own `Recent`/`TailFrom` cursor, so events never mix into ACTIVITY;
- not `activityKinds` (a wake attempt must not date a project as active).
Tests: `--reset` on a disposable broker trims `{p}:events` and keeps `{p}:resume`; a guard test
fails if a **key kind** in `projectKinds` (stream or hash) is in neither `resetKinds` nor
`survivesReset`, so the next key cannot be forgotten the same way. Works on 7.2.

**3. Keyspace notifications — defer.** They need `notify-keyspace-events` set on the broker, a
global setting that managed brokers often refuse and that costs every write on the server; the
messages are fire-and-forget Pub/Sub, lost while busmon reconnects, so polling stays as the
fallback anyway. `{p}:events` already gives busmon a blocking read that wakes it on the changes
this spec adds. Revisit if a measured poll cost or latency complaint appears.

**4. RESP3 client-side caching — reject for now.** busmon reads a few small hashes per second
for one person; the gain is unmeasured and the cost is real: `CLIENT TRACKING`, an invalidation
channel to handle on reconnect, and a cache that can serve a stale verified identity in the
window before invalidation. Measure first: a busmon profile showing Redis round-trips as the
bottleneck would reopen it.

## 7. Migration plan: a PR series from main, ending in one cutover

Every step is one PR from main. Steps 1–9 add code that **nothing in the running system uses
yet** (herdr still hosts every agent, unchanged); step 10 is the cutover, which switches every
caller and deletes herdr in the same PR. No herdr adapter is ever written, no setting chooses a
host, no shadow mode runs.

| # | PR | Behaviour change | Exit criterion |
|---|---|---|---|
| 0 | This spec (revision 14, frozen; revision 18 the security corrections) + decision note | none | coder, foureyes and reviewer-codex approve |
| 1 | `termhost` package: `Host`, `Line` (`WakeLine`/`ClearLine`), `fake`, contract suite; today's herdr `Runtime.Prompt` takes a `termhost.Line`, no text | none (same wake line typed) | dispatch tests green; the no-string-reaches-`Submit`/`Prompt` test |
| 2 | `agentstate` extracted from `dispatch` (signatures, fixtures), client profiles as data (§4.9), `Blocked` signatures, background-work fact (§4.3b), blocked-enter/leave judgement (§4.3a) — pure functions with table tests; hook wiring for blocked and subagents per client (Claude Code `Notification`/`PermissionRequest`/`SubagentStart`/`SubagentStop`; Codex `PermissionRequest`/subagent hooks once probed; `agy` `Stop`) writing observed facts only | none (herdr still decides; the hooks only record) | fixtures green; hook route tests per client |
| 3 | `agentbus term record` + raw fixtures per client (§6.4); every "to verify" cell of §4.9 probed and the profile rows updated; the `f12-at-input` probe run for every client with a profile, supported or not, and the attach default settled (§4.6); the paste-into-dialog fixture, mode 2004 at the input and the OSC 52 copy probe per client (§6.4) | none | fixtures committed; the matrix has no unexplained "to verify" for the clients the person's projects use |
| 4 | Emulator bake-off; `screen` seam + chosen adapter; gostty oracle behind a build tag | none | §6.4 passes for those clients |
| 5 | `agentbus holder` server alone + real-PTY e2e (wire frames, closed submit and `Start` sets, held viewer bytes and the hold after `ErrNotLanded`, mode-2004 requirement and the recheck, landing check, peer and in-tree checks with the subreaper, run-directory rules, build key, output filter and query answers, process facts) — after the **security review** of §4.4/§4.6, whose S2(a), S3–S7 corrections are in revision 18 | none (nothing uses it) | e2e green, security role approves |
| 6 | Holder adapter of `termhost` (contract suite) + services: per-build units, launchd jobs, binary copy, restart policy, `services.manager` with the `none` supervisor (§4.4b), `service gc` with the two-upgrade test, rotate/upgrade paths skip the holder, `holder`/`holder_restarted`/`holder_draining`/`holder_leftovers` health, `holder drain`, sentinel runbook entry | none (no project runs on it) | contract suite and service tests (fake `systemctl`/`launchctl`, scripted supervisor) green |
| 7 | `agentbus attach` + `attach.prefix` (the default settled by step 3's `f12-at-input` probe, F12 if it passes; both encodings) + busmon preview, attach key, draft/blocked/background on the row, the setting in the config screen | none (used only against a test holder) | attach tests; the person attaches to a scripted program in a test holder |
| 8 | `{p}:resume` + hooks write it (ancestor-walk proof, macOS `Ancestors`) for each client whose row is verified; the record is `{session, profile, at}` (`argv` dropped from `RecordResume`/`ResumeRecord`; `parseResume` ignores an old `argv`, S1); `broker_persistence` health; `{p}:events` with `resetKinds`/`survivesReset`; `valkey/valkey:9` in `docker-compose.yml` (§6.3) | the shipped broker image changes; resume is recorded beside herdr's | Redis tests of §8 on a disposable Valkey 9 and Valkey 7.2; the volume migration checked on a copy |
| 9 | Dispatcher on `termhost`+`agentstate` behind no switch: the code path exists and is covered by tests against the fake and a test holder; the blocked reports to master (§4.3a) and the background skip (§4.3b) wired in it | none (the running dispatcher still uses the herdr `Runtime`) | dispatch suite green on both paths |
| 10 | **Cutover**: `up`/`start`/`restore`/`relaunch`/`swap`/`suspend`/`resume`/self-repair on `Host`, resuming through `Start`'s `resume_session` only, after §4.7's two record conditions (allowed profiles from the local manifest, session of that profile's client), with the S1 tests, relayed to the dispatcher when run in-tree (§4.2); the dispatcher's herdr path removed; reporting callers on `Host.Describe`; herdr deleted (§4.8); decisions superseded; release note | **herdr gone** | the test forbidding any herdr exec, with no allowlist; scrub and the full suite green; the person's live check of the branch on one project before merge |

**Before the cutover PR merges**, the person tags the current release on GitHub as **the last
release with herdr**, with a release note saying so. Master never tags; this spec only states
the step. That tag is the rollback: reinstalling it brings herdr back, and nothing in steps 1–9
changes what it runs. The cutover PR's description names the tag.

Until the cutover, the released build keeps today's herdr behaviour, including today's documented
residual between `agent get` and `agent prompt` (one CLI round-trip, without a hard bound); no
step claims an improvement for herdr, and none exists after the cutover.

## 8. Testing decisions

- One external seam: `termhost.Host`. The contract suite (start, list, screen after output,
  process facts, submit with bracketed paste, refusal while a person is active or the screen is
  not ready, wait-screen, close and reap, identity env) runs against `fake` and `holder` always.
- `agentstate`: table tests from screen fixtures (existing `dispatch/testdata/screens`, moved)
  and from raw-byte fixtures replayed through the chosen emulator; good tests assert the
  judgement, not regexes. Per client: blocked enter and leave, background work (§4.3b's list),
  supported/unsupported from the profile.
- `holder`: end-to-end with real PTYs running small scripted programs (a shell script that draws
  a fake prompt, enables bracketed paste, prints a spinner, starts a child), temp
  `XDG_RUNTIME_DIR`. Never a real agent, never the production broker (CLAUDE.md test rule).
- Submit (holder, contract suite):
  - a viewer typing a sentence and Enter during the submit → the line is submitted alone, then
    the viewer's bytes arrive in order after the CR, Enter included; no byte lost;
  - the same with the kitty Enter encoding and with a large paste filling the hold queue (what
    is held is delivered in order; the bytes past the bound are refused and told, never
    delivered; the same viewer's Release still gets through, r25);
  - a dialog drawn between the step-3 recheck and the paste → the line is not seen in the input, **no CR**,
    `ErrNotLanded`, notice naming the kind (wake line / `/clear`);
  - a draft standing (any viewer byte since the last CR, mouse and focus reports excepted) →
    refused, however old the draft; a CR ends it; an empty Enter ends it;
  - **a left-behind `/clear`, then a wake**: the first submit's line is not seen (no CR,
    `ErrNotLanded`, terminal marked left-behind) while `/clear` stays in the input → the next
    `Submit` (a wake) is refused with nothing typed; once the input is cleared and a settled
    screen matches `EmptyInput`, the mark lifts and the wake goes through alone;
  - **program-placed input text** (a scripted program that redraws a non-empty input after a CR,
    as a client returning a queued message on Esc does; no viewer byte since the CR) → refused,
    `ErrNotReady`, nothing typed, the row shows `input not empty`.
- Attach prefix: the default key (F12 unless step 3 settled another) in legacy and kitty encodings (press recognised, release ignored);
  `<prefix> d` detaches; `<prefix> <prefix>` delivers one prefix to the program; `<prefix> x`
  delivers nothing and rings; the prefix split across two reads; a configured `ctrl+<letter>` in
  C0 and kitty `CSI … ; 5 u`; a value bound by a client in the project is refused. Attach gate: prefix `free` for the
  terminal's client → attaches; `bound` → refused, and `--prefix <free key>` attaches;
  `unknown` (no probe, or a client version beyond the profile's range) → refused without
  `--prefix`, attaches with it and prints the warning line; `--observe` is never refused; a
  mutant that skips the gate for an unsupported client must fail.
- Blocked reports: one enter and one leave per episode; a flapping screen inside one episode
  sends nothing more; screen text stripped and capped.
- Background work: §4.3b's table; each skipping action skips; each command refuses without
  `--ignore-background`; an automated caller cannot pass the flag.
- Services: the two-upgrade gc test (§4.4a); the `none` supervisor tests (§4.4b); rotate and
  `ServicesHint` skip the holder; `service restart` refuses without `--kill-agents`.
- Resume caller proof (Redis tests, disposable broker): a direct descendant of the stored
  (pid, start) writes; a grandchild through a shell writes; a non-descendant writes nothing; an
  ancestor with the stored pid but another start time writes nothing; a chain longer than 32
  writes nothing. The walk itself: table tests with an injected stat reader.
- Security corrections (revision 18; holder e2e and unit tests):
  - **S1**: the forged-record test of §4.7 on the `up` and self-repair paths (the started
    command equals the template's for the record's session and profile; an invalid session,
    a profile outside the role's own plus `fallback_profiles` (local manifest), or a session
    not of that profile's client (`ResumeVerified`): `up` starts fresh, self-repair
    starts nothing and reports to master);
  - **S2**: from a process started inside a test terminal (and from its double-forked
    grandchild), a non-observer attach, a `Start`, a `Close` and a `Submit` are refused, an
    observer attach and `Screen` are accepted; the same calls from outside the tree are
    accepted; a `Start` frame with an `argv`, `env` or `cwd` field, an unknown role, or an
    invalid profile is refused; one relay test per op (`up`, `pop`, `relaunch`, `reload`,
    `swap`, `suspend`, `resume`, `retire`, `drain`): run in-tree it sends one `host` directive
    and the dispatcher applies that command's own authorisation — sentinel's `reload coder`
    runs, sentinel's `reload sentinel` and coder's `reload reviewer` are refused, master's
    `reload sentinel` and `retire architect` run, `--ignore-background` from an agent other than
    master is refused; an unknown op is refused; with no dispatcher the command exits with the
    refusal text once the 5 s deadline has passed by Redis `TIME`; **a dispatcher paused past
    the deadline** (resumed at 6 s, 15 s and 60 s on a fake clock) replies `expired` and starts
    or closes nothing, and the command that already failed sees no later operation; a
    dispatcher resumed at 4 s accepts and the command, still waiting, reports success; a
    mutant that checks the deadline outside the accepting script, or acts before it, fails; a
    directive arriving during a 10 s fake reconcile pass, or while a relaunch waits for its
    proof, is accepted within the deadline; two reloads 1 s apart are both accepted and run in
    order; a dispatcher stopped with one accepted operation reports it `not run` at restart and
    runs nothing;
  - **subreaper**: a double-forked orphan inside a terminal exits and is reaped, and the
    agent's own exit status is still observed by the holder;
  - **S3**: a program writes `ESC ] 52 ; c ; <b64> BEL`, an OSC 8 link, `CSI 21 t`, `CSI 6 n`,
    `DCS $ q`, an OSC 11 `?` query: the viewer's stream holds none of them (the link's text
    kept); with `attach.osc52 on` the OSC 52 passes; DSR and DA are answered by the holder with
    no viewer attached;
  - **S4**: a terminal without mode 2004 never receives a paste (`ErrNotReady`); a dialog drawn
    between step 1 and step 3 → nothing typed, held bytes delivered; the paste-into-dialog
    fixture per client (§6.4);
  - **S5**: the paste lands after `submit_wait`, a held `\r` stays held — no CR reaches the
    program until `<prefix> r`; `<prefix> x` drops the held bytes; a viewer attaching during the
    hold sees the line; a settled `EmptyInput` releases them in order;
  - **S6**: a symlink at each run-directory level, a wrong owner and a wider mode are refused
    (holder, supervisor and client), under `XDG_RUNTIME_DIR` and in the `/tmp` fallback; a
    client refuses a server of another uid; the supervisor leaves a group whose leader's start
    time differs;
  - **S7**: `BuildKey` of two raw ids that reduce alike differ; projects `a` and `a-b`:
    `service gc a` and `down a` leave every `a-b` unit; an install whose key is recorded with
    another raw id is refused.
- Focus rule: a viewer that never sends a focus report blocks `Submit` for its whole attach;
  focus-out then focus-in toggles it; an observer never blocks.
- Mutation on: the hold of viewer bytes (releasing them before CR must fail the
  sentence-and-Enter test); the landing check (sending CR without it must fail the dialog test);
  the empty-input check (dropping it must fail both the left-behind `/clear` and the
  program-placed text tests); the left-behind mark (clearing it on `ErrNotLanded`'s return
  instead of on a verified empty input must fail the left-behind `/clear` test);
  the draft never expiring (a draft older than `draft_wait` must still refuse); the mouse/focus
  exclusion; the ancestor match (pid only must fail the reused-pid test) and depth bound;
  `Submit`'s person-active refusal; the mode-2004 requirement and the step-3 recheck; the hold
  kept after `ErrNotLanded` (releasing it at once must fail the S5 test); the in-tree refusal
  (skipping it must fail the S2 test); the output filter (passing OSC 52 by default must fail);
  `Start`'s frame validation (accepting an `argv` field must fail); the `Lstat` check (`Stat`
  must fail the symlink test); cleanup by recorded project (a prefix match must fail the
  `a`/`a-b` test); the `Line` constructors and the holder's frame validation
  (unknown kind, malformed nonce, text field, name field, count out of range); `WakeLine`'s
  `ValidName` check (one mutant per argument) and `Start`'s name check; the background-work
  rule (`unknown` treated as none must fail); the gc's "not active" condition.
- `{p}:resume`: Redis tests — an unproved caller writes nothing; a status, a hand-off and the
  agent's death leave it; `forget` clears it.
- The cutover's guard test: no herdr exec anywhere, no allowlist.

## 9. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| herdr's `agent get` encodes detection we have not seen (blocked, done) | wakes into a permission prompt, or none at all | raw fixtures per client, including every permission screen; hooks for blocked where verified; a client without a verified blocked source is unsupported (never prompted) |
| Direct cutover with no trial | a fault reaches every project at once | the gate of §6.4, the holder e2e and contract suites, the person's live check of the cutover branch, and the tagged last herdr release as rollback |
| Pure-Go emulator fidelity (Ink/ratatui redraws, wide chars, synchronized output) | signatures miss, agent never woken | bake-off on recorded output; oracle test; a client failing stays unsupported, the safe direction |
| Holder crash kills every agent of the project | team down | holder kept small, no policy; per project; restart policy (§4.4a, §4.4b); agents back via `up` or self-repair from `{p}:resume`; `holder` health + sentinel runbook |
| No service manager | no restart, no start at login | the `none` supervisor applies the same restart and drain rules (§4.4b); what it cannot do is stated by `service status` |
| Per-build units and binaries accumulate | disk and unit clutter | `service gc` at install, `up` and drain; `holder_leftovers` health |
| Broker without persistence | `{p}:resume` lost on reboot, agents start fresh | AOF in the shipped compose; `broker_persistence` health warn |
| Valkey 9 refuses the Redis 8 volume | broker does not start after the image change | checked on a copy in step 8; migration through backup/restore, stated in the release note |
| Upgrade kills agents | interruption | per-build unit and socket, each build drains, no restart path touches a running holder (§4.4, §4.4a) |
| Old holder never drains (`/clear` keeps the process) | two builds forever | `holder_draining` health, `holder drain` on the person's word |
| A same-uid process drives the holder | arbitrary typing into an agent, or a program started outside the client's sandbox | text-free submit frame, holder-composed lines; `Start` a closed set, the command built from the template; in-tree peers refused for non-observer attach, `Start`, `Close`, `Submit` (subreaper on Linux); exact run-directory rules and a mutual uid check; security review before step 5. Residual, stated (§4.2): a process that leaves the tree through a service manager, macOS orphans, and self-attested `from` on relayed host directives — bounded by the closed sets |
| A forged `{p}:resume` record | command execution on the host, automatically on self-repair | the record holds a session id only; the command is always rebuilt from the template with the session re-validated (§4.7, S1) |
| Agent output reaches the person's terminal raw | clipboard poisoning, deceptive links, report replies typed into the program | output filter on attach: OSC 52 off by default, OSC 8 stripped, report queries dropped and answered by the holder (§4.6, S3) |
| A wake line pasted into a dialog, or held keys after `ErrNotLanded` | an option selected; a left `/clear` completed by the person's Enter | bracketed paste required, Ready/`EmptyInput` rechecked before the paste, the paste-into-dialog fixture per client; held keys kept until released, discarded or the input is verified empty (§4.2, S4, S5) |
| Person/agent typing race, and text in the input that no viewer typed | wake line inside a draft; a wake appended to a left-behind `/clear` (clears the conversation) or to text the client returned to the input | check (person active, Ready, **input verified empty**), paste, landing check and CR under the input lock with viewer bytes held; a left-behind terminal takes no submit until its input is verified empty; a client without a verified `EmptyInput` signature is unsupported (§4.2, §4.9): closed; the herdr residual exists only in releases up to the last herdr tag |
| A CR answers a dialog | an unintended choice in the client | the line must be seen in the input before CR (§4.2) |
| Background work lost by `/clear` or relaunch | work or context lost | background-work fact, fail closed on unknown, actions skip (§4.3b) |
| Master flooded by blocked reports | noise | one enter and one leave per episode (§4.3a) |
| Screen text in a master report | prompt injection into master's context | one line, stripped, capped, labelled as screen data (§4.3a) |
| A stale draft holds wakes forever | an agent never woken | the row shows the draft's age, one ring after `draft_wait`; the person ends it with Enter (§4.5) |
| macOS process facts (foreground group, start time) | identity/foreground wrong on Mac | `tcgetpgrp` on the PTY master is portable; per-OS facts behind one platform file, tested on its OS |
| Losing the tiled always-visible layout | the person sees less at a glance | busmon preview + terminal splits (Q1) |
| Scope creep into a multiplexer | months of work | non-goal stated; busmon only previews and attaches |

## 10. Decisions of the person (2026-11-11)

All questions of revision 13 are decided; this spec is frozen after revision 14.

1. **Visibility.** busmon list + live preview + full-screen attach; side by side through the
   terminal's splits. No multiplexer (§4.6).
2. **Focus rule.** (a): typed in the last `dispatch.person_quiet`, a draft standing, or attached
   and not proven unfocused. A draft never expires into permission to type; the row shows its
   age and a waiting wake; one desktop ring after `dispatch.draft_wait` (§4.5).
3. **gostty.** Tests only, as an oracle behind a build tag. Production is pure Go (§6.2).
4. **Holder scope.** One holder per project (§4.4). With the one-dispatcher-per-host decision
   (2026-10-26), a per-host dispatcher enumerates `<rundir>/agentbus/*/holder-*.sock` (project
   from the directory name, validated with `ValidName` before use).
5. **Lifetime.** Services start at login without linger, as the configurable option
   `services.start_at_login`; logout ends the team; agents return through `up` or self-repair
   with their recorded resume (§4.4a).
6. **herdr after cutover.** Settled by the direct cutover: no adapter, herdr deleted (§4.8, §7).
7. **Gemini CLI.** Dropped: the Google client is Antigravity (`agy`), already supported.
8. **Trial project.** Settled by the direct cutover: no trial; the person's live check of the
   cutover branch and the tagged last herdr release replace it (§7).
9. **Attach prefix.** Replaced by the configurable `attach.prefix`; proposed default F12, final once the step-3 probe shows it free in every client with a profile; attach refuses a client for which the prefix is bound or unverified unless the person picks `--prefix` (§4.6).
- **Submit.** Simplified: viewer input held during the submit, CR, then release; the guard
  machinery removed (§4.2).
- **Windows.** Out of scope; a mode without a service manager is in scope (§4.4b, §11).

## 11. Out of scope

Windows: the PTY (ConPTY), process facts and service code are kept one file per platform so a
Windows port can be added later, but nothing in this spec builds or tests it. A tiling
multiplexer in busmon. Remote hosts other than over ssh. OSC 7501 emission by clients. Gemini
CLI and DeepSeek. Changing the dispatcher's decision rules beyond focus → person active, the
blocked reports and the background-work skip. Any new automated pane writer. New features in
this spec after revision 14.
