# A client's "do you trust this folder?" on a project's first run (first-run-trust)

Status: merged (PR 423); the person answered part 7 on 2026-11-10 (task `first-run-trust-impl`):
option (b), being implemented in steps (part 6). Base `origin/main`. This spec names functions and seams, not line numbers.

## 1. The ask

The person's screenshot (2026-11-10): at the first run of a project, Claude Code in an agent pane
stops on its own safety check:

```
Accessing workspace: /home/<user>/projects_local/<repository>
Quick safety check: Is this a project you created or one you trust? ...
Claude Code'll be able to read, edit, and execute files here.
❯ No, exit
  Yes, I trust this folder
```

The person wants agent-bus to get around it on a first run. This dialog is **the client's
security gate**, not agent-bus's. This spec weighs the options. It builds nothing.

## 2. What exists

- **`agentbus up` already plans around it** (specialist roles spec, part B; `cmd/agentbus/trust.go`).
  For each client whose trust gates a start (`trustGated`: Claude Code and Codex), it reads only
  the trust flag (a bool, never the file's content). It starts that client's **first** agent
  alone, prints `<client> does not trust <folder> yet: answer its dialog in the <role> tab`, and
  starts the client's other agents once the trust is recorded (`trustPlan`, `errTrustPending`).
  It never writes a client's file.
- **The pane's prompt is seen** (decision 2026-10-27, prompt-notify): a pane blocked on its
  client's prompt shows busmon's `prompt` flag, and the dispatcher rings the desktop once per
  episode.
- **What is missing:** the `up` line scrolls away in a terminal the person may not be watching.
  busmon says `prompt` but not *which* prompt, nor what to press. On an agent started by herdr's
  restore or by `agentbus start` rather than `up`, nothing names the dialog at all.

## 3. How each client stores the answer (checked 2026-11-10 on this host)

| Client (version) | Where | Shape | Read today |
|---|---|---|---|
| Claude Code 2.1.322 | `~/.claude.json` | `projects["<repo root>"].hasTrustDialogAccepted: true`; the key is the git repository's root (a worktree's main checkout), else the folder; outside a repository a trusted ancestor covers it | yes (`claudeTrusted`) |
| Codex 0.166.0 | `~/.codex/config.toml` | `[projects."<path>"] trust_level = "trusted"` (repository root or the folder) | yes (`codexTrusted`) |
| Gemini CLI 0.63.0 | `~/.gemini/trustedFolders.json` | `{"<path>": "TRUST_FOLDER"}` | no (not gated) |
| Kimi 2.2.0, Grok 1.0.77, agy 1.5.4 | no trust store found in their config folders | — | no (not gated); to verify by a first start in a new folder |

Two facts that bear on writing these files:

- **`~/.claude.json` is Claude Code's live state file.** It also holds the account's data, and
  every running Claude Code rewrites it. A second writer can lose its edit, or another's, unless
  it writes when no Claude Code runs; nothing in it is meant to be edited by another program.
- **Codex here already has an entry for `~`** (`[projects."~"] trust_level = "trusted"`, set
  earlier). agent-bus's reader (`codexTrusted`) takes it for the home folder itself only, not for
  the repositories under it. Whether Codex itself extends it to subfolders is not verified here:
  to check with a first Codex start in a new repository under home.

## 4. What the trust protects

Accepting the dialog lets the client read, edit and **run** files in the folder without asking
again. It also loads what the repository itself carries: Claude Code's project settings
(`.claude/settings.json`, its hooks, which run commands), its MCP servers (`.mcp.json`), and
instruction files (`CLAUDE.md`, `AGENTS.md`) that steer the agent. For a repository the person
did not write (a clone of someone else's work, a dependency checked out to read), the dialog is
the one moment the person is warned before that code runs. Accepting it for them removes that
moment.

**What changes with trust, and what does not** (reviewer-codex): agent-bus's own rules are
unchanged: the role's denies from `roles.toml`'s `[tool_rules]`, given to the client at launch. But
the client's **effective** permissions are not only those: once the folder is trusted, the
project's own `.claude/settings.json` takes part in them, and may add allow rules, hooks and MCP
servers. Claude Code documents that a deny from any settings source wins over an allow, which
would keep the role's denies in force; this spec does **not** verify that precedence on the
installed version, and part 6 asks for a test of it. Trusting a repository the person did not
write therefore widens what the agent may do by whatever that repository's settings allow, within
the role's denies if the precedence holds. A client started outside agent-bus asks as before, and
agent-bus never trusts a folder the person did not name.

## 5. Options

### (a) `up` pre-accepts the trust of the folder the person named

`agentbus up <project>` writes the trust for the template's `working_dir` (its repository root,
the key the client reads) before it starts the agents: Claude Code's `hasTrustDialogAccepted`,
Codex's `trust_level`, Gemini's `TRUST_FOLDER`. It prints each entry it wrote. `agentbus trust
revoke <project>` removes them.

- **For:** no dialog, ever, for a project started through `up`.
- **Against:** it decides for the person on every project, including one they cloned and never
  read. It writes another program's live state file (`~/.claude.json`, part 3), which risks a
  lost or corrupted write. And it ties agent-bus to each client's private format, which a client
  update may change without notice.

### (b) No pre-accept: say exactly what to press, where the person looks

Nothing is written to a client's file. The person is told, where they look, what the dialog is
and what to choose: `Claude Code asks whether you trust <folder>. In the <role> tab, choose "Yes,
I trust this folder" (↓ then Enter in Claude Code 2.1). Trusting lets it run the repository's own
settings, hooks and MCP servers.` The answer is named by its label; the keys are only a hint, since
they change between client versions (foureyes). prompt-notify still rings the desktop.

**Who says it** (foureyes: the dispatcher only reads asks today; opening one would be a new
dispatcher write, which must be fenced):

- **`agentbus up` and `agentbus start`**, for the agents they start: they already know the client
  and the folder (the template's `working_dir`), and already read the flag (`trustPlan`). They
  open the ask unfenced, like every ask, **as built (first-run-trust-1)**: from agentbus itself
  (`bus.SystemSender`, a name no role may take), never from an agent. An ask takes its sender's one
  open-ask slot, and an agent's open ask also mutes its prompt-notify ring and holds its
  auto-clear: asked from the gate agent, the reminder would block that agent's own asks and
  silence the ring for the very dialog it is about (foureyes). One agentbus ask holds a question
  `trust-<client>` headed `Folder trust` per untrusted client, naming the gate agent's pane, its
  context opening with the folder, which binds it: a reminder about another folder (the template
  moved) is withdrawn and replaced (reviewer-codex). A person's answer to it is replied to people
  with `wake=no`: nobody is woken, no group or wake is owed to agentbus. `up` closes a reminder
  when the trust is accepted during its wait (`errTrustPending`'s wait). Accepted after `up`
  exits, it stays open until the next `up` or `start` closes it; meanwhile busmon, reading the
  same flag, shows it as `trusted now: nothing to do` (foureyes).
- **busmon, for an agent herdr restored** (no `up` ran): busmon shows a For-you line computed from
  what it already reads, with no write: an agent of a gated client (`AgentSnapshot.Client`) whose
  pane shows the `prompt` flag, in a project whose template's `working_dir` (`projlist`) that
  client does not trust (the same readers). The line goes away once the flag reads true. busmon
  can read the clients' files only on the host they live on: against a remote broker it shows
  the `prompt` flag as today, with no trust line. **As built (first-run-trust-2):** the readers
  live in package `trust`; the line is the prompt row's own text in For you (`Claude Code asks
  whether you trust <folder>: choose "Yes, I trust this folder"`), read in the poll outside its
  lock and only for an agent showing the flag. "On this host" is proved per agent, not by the
  broker's address (a tunnel makes a remote broker look local). The proved identity in `{p}:agents`
  carries the host's boot id with its pid (`AgentSnapshot.BootID`, `boot_id`, from
  `/proc/sys/kernel/random/boot_id`, set by `status` and `bind` and moved only with the pid on a
  proof): busmon shows the line only when it equals busmon's own and the pid runs here with its
  recorded start time (`dispatch.ProcStart`). Pid and start time (ticks since a kernel's boot)
  alone could match a remote agent by chance, whatever its name (reviewer-codex). No boot id on
  either side (an agent launched before the field, no `/proc`): no trust line.
- No new dispatcher write: a fenced dispatcher ask (a new bus function) would cover a restore
  with busmon closed, at the cost of a new fenced writer; not proposed.

- **For:** the person decides, informed, in one place they already watch (For you); no client
  file is written; it covers agents started by restore or `start`, not only `up`.
- **Against:** one keypress per new project and client remains.

### (c) A `--trust` flag on `up`, passed knowingly

As (a), but only when the person runs `agentbus up <project> --trust`. Without it, (b) applies.
Its rules:

- only the template's `working_dir`, at the key the client reads (repository root). Both the
  `working_dir` and that key are resolved (`EvalSymlinks`) and must lie strictly inside the
  resolved home, never be `~` or `/` (reviewer-codex): a `working_dir` under home that is a link
  to a folder outside it, or a worktree whose main checkout (Claude Code's key) lies outside
  home, is refused;
- only for the gated clients whose format is checked here (Claude Code, Codex); another client
  keeps its dialog;
- Claude Code's file is written only while no Claude Code process runs (else `--trust` is refused
  with that reason), through a read, edit of that one key, and atomic replace that keeps every
  other byte. **This is not a safe write** (reviewer-codex): the process check is a check, then a
  write; a Claude Code started between them can rewrite the file from its own memory and drop the
  edit, or have its newer account and project state replaced by the older copy. The atomic
  replace prevents a torn file, not a lost update. A backup of the file is taken before the
  replace and its path printed, so the person can restore it; it holds the account's data, so it
  is written mode 0600 beside the file (the same folder, the same filesystem), never in a shared
  or project folder (foureyes). The race remains and is said;
- each entry written is printed, and `agentbus trust revoke <project>` removes exactly those.

- **For:** no dialog when the person says so, for a project they know; nothing silent.
- **Against:** the write risks of (a), on demand, including the residual race above; a new verb to maintain. **And for Claude Code,
  as limited above, `--trust` is refused whenever any Claude Code runs on the host** (foureyes):
  with this team up (master, coder, foureyes are Claude Code), starting a second project with
  `--trust`, the person's own case, is refused for Claude Code and only works for Codex. Whether
  a write is safe while Claude Code runs (does it re-read `~/.claude.json` before it rewrites it,
  or rewrite it from memory and drop another program's edit?) is **not verified**; checking it
  needs a test against a separate `CLAUDE_CONFIG_DIR`, never the person's file. Until it is, (c)
  helps Claude Code only when no Claude Code runs.

**Recommendation:** (b) now. (c) as the explicit opt-in only once a write to `~/.claude.json`
while Claude Code runs is verified safe (else it helps Codex alone while a team runs). Never (a).
The dialog is a security gate the client owns; agent-bus can make it impossible to miss and say
exactly what it does, but it should not answer it unasked.

## 6. Tests (when built)

- (b): `up`/`start` with an untrusted gated client open one ask naming the folder, the tab and the
  choice's label; it closes when the flag reads true; an already-trusted folder opens none; a
  non-gated client's agent opens none. busmon's For-you line appears for a restored agent with the
  `prompt` flag in an untrusted folder, and not for a trusted one, nor for an agent whose process is
  not on busmon's host.
- (c): `--trust` writes only the repository root's key, refuses `~`, `/`, a folder outside home,
  a `working_dir` that is a link out of home, a worktree whose main checkout lies outside home,
  and a running Claude Code; keeps every other byte of the file; backs it up first (0600, beside the file) and prints the
  backup's path; prints each entry; `trust revoke` removes exactly those; without `--trust`
  nothing is written.
- The precedence part 4 relies on, on the installed Claude Code: in a trusted test folder whose
  `.claude/settings.json` allows a tool the role denies, the role's deny still wins.

## 7. Questions for the person

**The person's answers (2026-11-10):** (1) option (b): no write, say exactly what to choose;
(c) not built. (2) Without (c), no client file is written at all. (3) The reminder is a busmon
**ask** in For you that closes itself. (4) Codex's `~` entry: the person thinks it a mistake and
edits it himself; agent-bus never writes or removes an entry it did not write.

The questions as they were put:

1. **Which option?** (a) pre-accept always, (b) tell exactly what to choose (no write), or (c) (b)
   plus an explicit `up --trust`. With (c), `--trust` is refused for Claude Code whenever a Claude
   Code runs on the host, so while this team runs it helps Codex only (part 5, (c)), unless a
   safe concurrent write is verified first. **Recommended: (b) now**; (c) only once a concurrent
   write to `~/.claude.json` is verified safe on a separate config directory.
2. **With `--trust`, which clients?** Claude Code and Codex only (formats checked), or Gemini too
   (`trustedFolders.json`)? **Recommended: Claude Code and Codex only**; others once their format
   is verified.
3. **The notice in (b):** a busmon ask in For you (it waits for you and closes itself), or only a
   notice line? **Recommended: an ask.**
4. **Codex's existing `~` entry** (part 3): leave it as it is? **Recommended: leave it**; agent-bus
   never writes or removes an entry it did not write.
