# The concierge: one conversation across every project (concierge)

Status: spec r1, for review (subject `concierge-spec`). Not built. Base: origin/dev `93727f7b`. Author: architect, from the person's typed yes relayed by master (cmd
`1795735958552-0`, task `concierge-spec`; idea `i3`). It builds on
[person-memory](2026-11-21-person-memory-design.md). The person's constraints: simple, robust, no
new dependency without the person's yes. Questions are last (§9).

## 1. What the person asked

- A **concierge**: one agent the person talks to about every project, in their own language. It
  remembers what the person said, through person-memory.
- A **floating window**, with an innovative UX/UI.
- An honest answer to **"would this need a new framework?"**, for two options:
  - (a) stay in the terminal, as a floating overlay in busmon with no new dependency;
  - (b) a native GUI.

  The person's seven GUI candidates are checked in §3.

## 2. What exists

- **busmon** is a bubbletea v2 TUI (`go.mod`: `charm.land/bubbletea/v2`, lipgloss, ultraviolet).
  It already draws floating layers over any page:
  - the compose window (`cmd/busmon/compose.go`);
  - the `@` picker popup (`compose_at_popup.go`);
  - confirm dialogs;
  - the help overlay, with its golden `testdata/overlay/help.txt`.

  It renders Markdown itself (`markdown.go`). It works over SSH, and the person watches the
  host's bus from a laptop through a tunnel (`README.md`, "Watching another machine's bus").
- **person-memory** (spec 2026-11-21, steps building): layer 1 is the person's facts, a host-level
  git repository of quoted statements, with `typed=yes` provenance. Layer 2 is `history` plus a
  cross-project `mem search`, "built only when the concierge needs it" (its §6 Q4). The concierge
  is that need.
- **Roles and ACL:** broker ACL users are per kind of caller, not per project (`bus/acl.go`:
  admin, master, dispatcher, agent, busmon, ping, observer). An **observer** reads only
  (observer-role).
- **The language setting** (2026-11-18) already names the concierge: when it gets a role file,
  its skill takes the same rule (answer the person in `person.language`).
- **Relays went wrong before:** messages "from the person" relayed by another agent were
  unverifiable (`typed` missing). The concierge must not become a new unverified relay (§5.3).

## 3. Does it need a new framework? The candidates, checked

Checked on GitHub on 2026-11-27 (`gh api repos/<r>`: created, latest release, contributors,
`go.mod`). All seven exist and are MIT. Facts about cgo and platforms come from each README,
**not from a build**.

| Candidate | Created | Latest release | Contributors | Stars | What it is (README) | Notes |
|---|---|---|---|---|---|---|
| `egoist/mygo` | 2026-09-26 | v0.10.0 | 100 | 6.9k | native UI drawn on the GPU, or the OS webview (WKWebView, WebKitGTK, WebView2); "pure Go, no cgo" through `purego`; packaging, signing, notarization | Two months old and moving fast. `go.mod` requires **go 1.27.3**; this repository is on Go 1.26, so a toolchain bump comes first. It pulls in audio codecs (mp3, ogg, flac, aac) and an rpm writer. |
| `gogpu/ui` | 2025-12-05 | v0.1.55 | 9 | 544 | widgets on WebGPU (Vulkan, DX12, Metal, GLES, software), zero cgo | One year old, pre-1.0, a stack of six `gogpu` modules. go 1.25. |
| `go-gui-org/go-gui` | 2026-03-01 | v0.100.0 | 5 | 186 | cgo-free Metal or OpenGL, tray, dialogs | README: Wayland cannot place a window. It pulls in dbus, pulse audio, x11 and goldmark. |
| `golang-gui/goui` | 2026-02-11 | v0.8.1 | 2 | 117 | declarative, native rendering | two contributors |
| `dxui-org/dxui` | 2026-09-16 | v0.0.4 | 1 | 30 | declarative desktop GUI | one contributor |
| `bankai-tensei/gowaku` | 2026-11-17 | v0.3.0 | 2 | 4 | GPU-drawn, no cgo | ten days old |
| `goi-gui/goi` | 2026-10-30 | v0.32.8 | 1 | 4 | declarative | one contributor, four weeks old |

For comparison, the established Go GUI toolkits are older but need cgo on the desktop: Fyne
(2018, v2.8.2), Gio (2020, no release tags), and Wails (2018, a webview, v3 nightly).

**Every native GUI costs the same five things here:**
1. **A new dependency tree**, a second UI to keep in step with busmon, and its own release
   artifacts.
2. **It does not reach the host the team runs on.** The agents run on a host the person reaches
   by SSH. A GUI there needs a display. So the GUI would run on the laptop, against the bus
   through the tunnel, while busmon and the CLI keep working everywhere.
3. **Platforms:** a webview build depends on the OS's engine (WebKitGTK on Linux, so the
   person's distribution matters); a GPU build depends on drivers; Wayland forbids placing a
   window, which defeats a "floating window" that remembers its place; macOS adds signing and
   notarization.
4. **Size:** today's binaries are 21 MB (busmon) and 25 MB (agentbus). A GUI binary is a third
   artifact on top of them.
5. **Maturity:** the cgo-free candidates are all under a year old, five of the seven under ten
   months. Five have one or two contributors. None is past 1.0 in the sense of a stable API.

**Recommendation: (a), the terminal, now.** No framework and no dependency. It runs wherever
busmon runs, SSH included, and §4 shows that a floating window is a busmon layer like the
compose window. **A native GUI is not recommended today.** If the person wants one later, it is a
thin client of the same concierge (§6), so nothing in §4–§5 is thrown away. The two to
re-evaluate then are `egoist/mygo`, for its webview mode, which suits a chat of Markdown and is
the most active, after its Go requirement and dependencies settle, and `gogpu/ui`, the oldest
cgo-free one. A three-month re-check is proposed, on the person's yes.

## 4. The window: a floating concierge in busmon

**One key from anywhere: `` ` `` (backtick)**, free in busmon today (`cmd/busmon/keys.go`). It
drops a window down from the top over whatever page is open, in any project, like a console.
`` ` `` again, or Esc, hides it. The conversation stays where it was. busmon's project switch, when
it exists (idea, 2026-11-14), keeps the window open across projects.

```
┌ concierge ─────────────────────────────── 3 projects · memory 41 facts · ● ┐
│ you    Where does agent-bus stand on the merge queue?                 09:12 │
│ conc.  Two PRs wait for you on agent-bus-monitor: #831 (approved by both),  │
│        #834 (foureyes only). solana has none.                               │
│        sources: agent-bus-monitor:merge:#831 · #834 · board                 │
│        ▸ open #831   ▸ ask master to merge #831   ▸ remember                │
│ you    ▍                                                                    │
└ ↵ send · tab context · ↑ history · ` hide ─────────────────── on: Board ────┘
```

What makes it more than a chat box:
- **The page is the context.** The footer says what busmon shows (`on: Board`, the selected
  row). `tab` attaches it to the next message as a chip, for example `[board: sibling-enforce-spec]`
  or `[PR #831]`. The concierge then gets the entry's id, not a pasted copy.
- **Every answer cites its sources.** The concierge answers from bus entries, plan files and
  memory facts, and lists them by id (`project:entry`, `memory:<topic>`). `Enter` on a source
  opens it in busmon's own page for it, in its project.
- **Suggested actions are keys, not prose.** An answer can end with up to three actions. `1`–`3`
  or Enter on one runs it, and each is something busmon already does:
  - open a page;
  - write to a project's master (§5.3, the person's own words);
  - remember a quote (person-memory's `remember`, the person confirming).

  The concierge proposes; the person presses.
- **It is never in the way.** An answer that arrives while the window is hidden lights a dot
  (`●`) in busmon's header and nothing else: no popup and no bell.
- **The memory is visible.** The header shows how many facts the concierge reads. `m` in the
  window opens person-memory's Memory page (its §4.4), where the person views, edits and deletes.

It is drawn with lipgloss like the compose window and reuses its editor, so there is no new
widget library. Its width follows the terminal: full width below 100 columns, 80 % above.

## 5. The concierge agent

### 5.1 Where it runs

One concierge per host, not per project. It is a role, `concierge`, in a **host project**
named `concierge`, with its own stream, board and holder terminal, like any project. So pop, park
(cleaning-up-agents), relaunch, quota and context fill all apply unchanged. busmon talks to it
on that project's bus from any project's page. It is popped on demand: the first message while
it is not running pops it. It parks after `dispatch.idle_park_after` like any pop (cleaning-up-agents Q8).

### 5.2 What it reads

- **Every project's state, read-only:** boards, plans, asks, merge queue, health, the recent
  streams. Its ACL user is a new `concierge` user: the observer's reads (`bus/acl.go`,
  `ACLObserver`: `+@read`, `%R~*`), writes to its own project's keys, and one write elsewhere,
  appending to each project's `cmd` stream, so §5.3's message reaches that project's master.
  It has no script, no board write and no lease in another project.
- **The person's memory** (layer 1), and `history` across projects through person-memory's
  layer 2 `mem search`, which this spec is the need for.
- **Never a repository's code.** It is not a coding agent and gets no worktree. A question about
  code goes to that project's master.

### 5.3 What it may do: only through the person

- It **answers** the person, in `person.language`, quoting its sources.
- It **writes to a project's master only as a suggested action the person presses** (§4). The
  message carries the person's own typed words verbatim and a reference
  `typed_ref=concierge:<entry id>` to the person's typed entry on the concierge's bus. Master
  verifies it with `agentbus --project concierge thread <id>`: its ACL user reads every key
  (`ACLMaster`: `%R~*`). The entry must be the person's, with `typed=yes`, and its text must
  equal the quoted words. So master can tell a real instruction from a concierge's paraphrase,
  and the concierge cannot invent one.
- It **remembers** only through person-memory's `remember`: the person's quote, confirmed by the
  person's key press. Never an inference (person-memory §4.3).
- It never accepts tracked requests, never delivers, merges or pushes, never pops agents, and
  never types into a pane.

## 6. Later, if the terminal proves limiting

A native window, if the person ever wants one, is a client of §5's concierge project: the same
stream, the same `typed_ref` rule. It is a separate binary, so busmon stays dependency-free. It
is chosen then, from §3's re-check, on the person's yes.

## 7. Build steps (in series)

1. **The concierge role and host project:** the role file and skill (language rule, the
   source-citing rule, §5.3's limits), the host project `concierge`, its ACL user (observer reads,
   its own project's writes, appending to each `cmd` stream), and `typed_ref` on directives with master's skill text to verify it.
   Tests: a relay without a valid `typed_ref` is flagged by master's check; the concierge's
   writes to another project's keys are refused by the ACL inventory test.
2. **busmon's window:** the `` ` `` overlay, the conversation view, send, history, the hidden-answer
   dot. Goldens at 60x20, 80x24, 140x40.
3. **Context chips and sources:** `tab` attaches the selection; sources open their pages.
4. **Suggested actions:** up to three keys per answer, each mapped to an existing busmon act.
5. **`mem search`** (person-memory layer 2), when step 1's concierge needs it.

## 8. Not doing

- No new UI framework or dependency, and no web server.
- No concierge acting on a project by itself.
- No code reading.
- No second memory: it reads person-memory, it does not keep its own.

## 9. Questions for the person (ask `ask:`, in French)

1. **UI:** the floating window in busmon now, a native GUI not now (re-checked in three months).
   Recommended: yes.
2. **Where it runs:** one concierge per host, in a host project `concierge`, popped on demand and
   parked when idle. Recommended: yes.
3. **What it may do:** read every project; write to a project's master only when you press its
   suggested action, with your words verbatim and `typed_ref`. Recommended: yes.
4. **The key:** `` ` `` (backtick) opens and hides it from any page. Recommended: yes.
5. **Memory:** it may propose "remember this", and you confirm with one key. Recommended: yes.
   The alternative: it never proposes; you remember from the Memory page.
6. **Its model:** Sonnet (cheap, always available) or Opus (deeper)? Recommended: Sonnet, with
   the person able to switch it in `roles.toml`.
