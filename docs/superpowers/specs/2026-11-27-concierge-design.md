# The concierge: one conversation across every project (concierge)

Status: spec r5, for review (subject `concierge-spec`; r5, foureyes and reviewer-codex on r4:
the person stream is busmon-only through a new `aclBusmonOnly` list pinned by the ACL
inventory (§5.3); the fallback list is `fallback_profiles`, and `alt_profiles` holds the deep
answer (§5.5); remember gains the `concierge:person:<id>` source in person-memory's resolver,
and rejections persist on the broker (§5.4); r4: the person's Q4–Q6 answers, the key
(§4), remember (§5.4) and the client list (§5.5); r3: a Redis entry does not record its
writer's ACL user, so `typed_ref` must point into `{concierge}:person`, a stream only the busmon
user can write; r2, foureyes and reviewer-codex: busmon,
not the concierge, sends a pressed action, and the concierge writes nothing outside its project
(§5.3); go-gui's release date added; the person's answers Q1–Q3 folded in). Not built. Base: origin/dev `93727f7b`. Author: architect, from the person's typed yes relayed by master (cmd
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

Checked on GitHub on 2026-11-27 (`gh api repos/<r>`, and `gh api repos/<r>/releases/latest`
for the latest release: created, latest release, contributors, `go.mod`). All seven exist and are MIT. Facts about cgo and platforms come from each README,
**not from a build**.

| Candidate | Created | Latest release | Contributors | Stars | What it is (README) | Notes |
|---|---|---|---|---|---|---|
| `egoist/mygo` | 2026-09-26 | v0.10.0 | 100 | 6.9k | native UI drawn on the GPU, or the OS webview (WKWebView, WebKitGTK, WebView2); "pure Go, no cgo" through `purego`; packaging, signing, notarization | Two months old and moving fast. `go.mod` requires **go 1.27.3**; this repository is on Go 1.26, so a toolchain bump comes first. It pulls in audio codecs (mp3, ogg, flac, aac) and an rpm writer. |
| `gogpu/ui` | 2025-12-05 | v0.1.55 | 9 | 544 | widgets on WebGPU (Vulkan, DX12, Metal, GLES, software), zero cgo | One year old, pre-1.0, a stack of six `gogpu` modules. go 1.25. |
| `go-gui-org/go-gui` | 2026-03-01 | v0.100.0 (2026-11-24; v0.99.1 before it) | 5 | 186 | cgo-free Metal or OpenGL, tray, dialogs | README: Wayland cannot place a window. It pulls in dbus, pulse audio, x11 and goldmark. |
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

**One key from anywhere: Ctrl+Space** (the person, Q4). It sends NUL, so it works on any
keyboard layout and over SSH. The aliases are `²` (AZERTY, top left, the same physical key)
and `` ` `` (QWERTY). A lone backtick is a dead key on AZERTY (AltGr+7), so it is only an alias.
The window drops down from the top over whatever page is open, in any project, like a console.
The same key again, or Esc, hides it. The binding is a setting (`concierge.key`, Shift+C), and
busmon's help and `roles doctor` show the active one. The conversation stays where it was. busmon's project switch, when
it exists (idea, 2026-11-14), keeps the window open across projects.

```
┌ concierge ─────────────────────────────── 3 projects · memory 41 facts · ● ┐
│ you    Where does agent-bus stand on the merge queue?                 09:12 │
│ conc.  Two PRs wait for you on agent-bus-monitor: #831 (approved by both),  │
│        #834 (foureyes only). solana has none.                               │
│        sources: agent-bus-monitor:merge:#831 · #834 · board                 │
│        ▸ open #831   ▸ ask master to merge #831   ▸ remember                │
│ you    ▍                                                                    │
└ ↵ send · tab context · ↑ history · ^␣ hide ── claude-code/sonnet · on: Board ┘
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
  - remember a quote (person-memory's `remember`, the person confirming, §5.4).

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
  `ACLObserver`: `+@read`, `%R~*`), and writes to its own project's keys only. It has no write,
  script, board entry or lease in any other project (r2). Every action on a project goes
  through busmon (§5.3).
- **The person's memory** (layer 1), and `history` across projects through person-memory's
  layer 2 `mem search`, which this spec is the need for.
- **Never a repository's code.** It is not a coding agent and gets no worktree. A question about
  code goes to that project's master.

### 5.3 What it may do: only through the person, and busmon sends it

The concierge **writes nothing outside its own project**. A message to a project's master is
sent by **busmon**, the person's own process, when the person presses a suggested action. The
concierge proposes; it never sends (r2, foureyes and reviewer-codex: in r1 the concierge
appended to other projects' `cmd` streams, so it could send without any press, and it could
forge the `typed=yes` entry its `typed_ref` pointed to).

A suggested action that writes to a master carries its target project and its exact text. When
the person presses it, busmon shows the text and the target once more and sends it on the press.
What busmon sends depends on whose words it is (the person, Q3):

| The text is | busmon sends to `<project>`'s master | Marked |
|---|---|---|
| **The person's own typed words** (a line they typed in the window, quoted unchanged) | A directive through `bus.TypedCmd`, from the person, with `typed=yes` and `typed_ref=<id>`: the id of the person's entry in the **person stream** (below) | `typed=yes` |
| **The concierge's draft**, accepted by the press | A directive from the person with `human=yes` and `draft_by=concierge`, never `typed=yes` | `human=yes`, a draft |

**The person stream.** Every line the person types in the window is written by busmon to one
stream, `{concierge}:person`, as well as to the conversation.
- **Today every data role may write every key kind** unless the kind is listed master-only or
  dispatcher-only (`bus/acl.go`, `aclWrites` over `keyKinds`: `ACLAgent`, `ACLMaster` and
  `ACLDispatcher` all get it). So a new kind alone would be writable by every agent (r5,
  foureyes).
- So the build adds **`person`** to `keyKinds` and a third exclusion list, `aclBusmonOnly =
  {"person", "concierge:rejected"}` (the second key is from §5.4). Every `aclWrites` call
  (agent, master, dispatcher, and the new concierge user) excludes it. Only `ACLBusmon`
  (`~*`) and `ACLAdmin` can write those keys.
- **`TestACLInventory` pins it:** an XADD to `{concierge}:person` is refused for every role but
  busmon and admin.

So no agent can append to the person stream.

**Master's check of `typed_ref`** (in master's skill, and in `agentbus` as one call master runs):
- The referenced id is an entry of `{concierge}:person`. A Redis entry does not record the ACL
  user that wrote it, so the key's write rule is the proof, not any field in the entry (r3).
- Its text equals the directive's text byte for byte.
- It is under one hour old.
- It has not been used before. Master records each one it accepts in its own project's
  `typed_refs` set, in the same step, and refuses a second use.

Each condition that fails demotes the directive to a draft (`human=yes`), and master treats it
as one.

**Why this holds:**
- Only busmon and the person's own CLI connect as the `busmon` ACL user.
- The concierge connects as its own ACL user. It has no write outside its project, and none on
  `{concierge}:person`.
- So it cannot create a directive in another project, and it cannot create the entry a
  `typed_ref` must point to.
- A `typed=yes` field it writes anywhere else proves nothing. Master checks only entries of the
  person stream.

**Every relay is audited:** busmon writes one audit line per sent action (`action=relay`, the
target project, the entry id, `typed` or `draft`). The concierge's own answers are not acts and
are not audited.

- It **answers** the person, in `person.language`, quoting its sources.
- It **remembers** only as §5.4 says.
- It never accepts tracked requests, never delivers, merges or pushes, never pops agents, and
  never types into a pane.

### 5.4 Remember: a proposed quote, the person decides (the person, Q5)

- The concierge may propose saving a memory. The proposal is always a **verbatim quote of the
  person's own words**, with its source: date, project and message reference. Never an
  inference. The source is one person-memory's `remember` already checks, or one it learns:
  - **a typed entry of a project's stream:** `--from <project>:<entry>`, as person-memory §4.3
    checks today (`typed=yes`, the person's words);
  - **an entry of `{concierge}:person`:** a new source form, `--from concierge:person:<id>`.
    person-memory's resolver gains one case. It reads that exact key's entry, and takes
    provenance from the key's ACL (§5.3: busmon-only), not from a field. It checks that the
    quote is a substring of the entry's text, byte for byte. This extends person-memory §4.3 by
    one source, and the build changes its resolver and its tests (r5, reviewer-codex).

  Any other source is not proposed.
- It is shown in full. One key confirms it, another rejects it. A rejected quote is never proposed
  again: busmon adds its source id to **`{concierge}:rejected`**, a set on the broker that only
  busmon writes (§5.3's `aclBusmonOnly`), which the concierge reads before proposing.
  - It is host-level and survives a restart, a lost local cache and a second busmon (r5,
    reviewer-codex).
  - It is in the broker's backups like any key.
  - The person can clear an entry from the Memory page.
- If a similar fact exists (same topic), the proposal is **replace**, not add, and shows both.
- busmon writes the confirmed fact through person-memory's `remember`. It is tagged `human=yes`
  (the press) with the quote's `typed_ref`, and it is editable and deletable on the Memory page
  (person-memory §4.4).

### 5.5 Which client and model: an ordered list, client-agnostic (the person, Q6)

- The concierge role's profile, then its **`fallback_profiles`** in order (at most
  `MaxFallbacks` = 3, `rolecfg/config.go:301`), is the list of `{client, model}`. Those are the
  fallbacks that swap and the quota ask already read. So no new `roles.toml` key is added (a new
  key broke an update once, 2026-11-25), and no new fallback logic either.
  - `alt_profiles` is not used for this: it is "never a quota fallback" (`rolecfg/config.go:316`;
    r5, foureyes). It holds the deep-answer profile instead (below).
  - The default: Claude Code with Sonnet, then Codex. kimi, grok and agy are added once each is
    verified.
- **Fallback:** a pop takes the first entry whose client is installed, whose provider is not
  quota-paused (the quota hold), and whose last launch did not fail. The next pop retries from
  the top.
- **Eligibility,** checked per entry by `roles doctor`'s smoke test:
  - the client supports MCP, or the bus CLI the concierge uses;
  - the restricted tool set is enforced: read, and propose actions;
  - the client honours the role's tool rules.

  An entry that fails is skipped, with the reason.
- **"Deep answer":** one key (`D` in the window) asks the next reply from the concierge's first
  `alt_profiles` entry (for example Opus), for that one reply: a separate pop
  (`pop concierge --profile <alt>`, what `alt_profiles` is for) that parks after answering.
- The window's footer shows the active client and model.

## 6. Later, if the terminal proves limiting

**The concierge's logic is UI-agnostic** (the person, Q1). Everything it knows and does goes
through the broker: its project's stream, the relay of §5.3. busmon's window is one client. A
native window, or a browser surface (an `agentbus web` reached from a phone over Tailscale, which
the person names for the three-month review), would be another client of the same concierge
project. It would follow the same rule: the person's own client sends, never the concierge. It is a separate binary, so busmon stays dependency-free. It
is chosen then, from §3's re-check, on the person's yes.

## 7. Build steps (in series)

1. **The concierge role and host project:** the role file and skill (language rule, the
   source-citing rule, §5.3's limits), the host project `concierge`, its ACL user (observer
   reads, its own project's writes only), and the `person` kind with `aclBusmonOnly`.
   Tests: the ACL inventory test refuses the concierge user every write outside its project,
   the `cmd` stream of another project included, and any write to `{concierge}:person`.
2. **busmon's window:** the Ctrl+Space overlay (with its aliases and `concierge.key`), the conversation view, send, history, the hidden-answer
   dot. Goldens at 60x20, 80x24, 140x40.
3. **Context chips and sources:** `tab` attaches the selection; sources open their pages.
4. **Suggested actions, the relay and remember (§5.4):** up to three keys per answer, each mapped to an existing
   busmon act; busmon's relay (§5.3), its audit line, and master's `typed_ref` check with the
   `typed_refs` set.
   - Tests:
     - typed words relayed are `typed=yes`, and a draft is `human=yes` with `draft_by`;
     - master demotes a reference with a changed byte, one older than an hour, one used a second
       time, and one that points outside `{concierge}:person` (an entry the concierge wrote on its own stream with `typed=yes`);
     - every relay leaves one audit line;
     - `remember --from concierge:person:<id>` accepts a substring of a busmon-written entry and
       refuses any other key;
     - a rejected id stays rejected after busmon restarts with an empty local cache.
5. **The client list (§5.5):** the fallback chooser over the profile and `alt_profiles`, the
   doctor smoke test, and the `D` key on the first `alt_profiles` entry.
6. **`mem search`** (person-memory layer 2), when step 1's concierge needs it.

## 8. Not doing

- No new UI framework or dependency, and no web server.
- No concierge acting on a project by itself.
- No code reading.
- No second memory: it reads person-memory, it does not keep its own.

## 9. Questions for the person (ask `1795736424584-0`, in French)

**Answered so far (typed, 2026-11-27):**
- **Q1:** the busmon overlay. A native GUI is deferred, re-checked in three months (mygo,
  gogpu/ui) against an `agentbus web` surface. The logic stays UI-agnostic (§6).
- **Q2:** one concierge per host, in a host project, with the standard lifecycle (pop, park,
  quota, restart, cap B). It is not a project master. Cross-project access is read-only, and
  every action on a project goes as a directive to its master. A cheap, fast model by default,
  configurable (which also answers Q6).
- **Q3:** writes only on the person's press. The person's typed text goes verbatim with
  `typed_ref`, which master verifies (byte for byte, the person's entry, unexpired,
  single-use). A press alone is `human=yes`, and a draft accepted by a press is the
  concierge's draft, never `typed=yes`. Every relay is audited (§5.3).

- **Q4:** Ctrl+Space, with `²` and `` ` `` as aliases, configurable (§4).
- **Q5:** proposed verbatim quotes with their source; confirm, reject never again, replace
  when similar; `human=yes` with `typed_ref` (§5.4).
- **Q6:** an ordered `{client, model}` list with fallback, a deep-answer key, the doctor smoke
  test, and the active one shown (§5.5).

All six are answered. The questions as asked:

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
