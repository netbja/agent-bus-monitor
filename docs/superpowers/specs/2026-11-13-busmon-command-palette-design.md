# busmon command palette: `/` in the input line, Ctrl+P anywhere

Status: draft, spec only; the person answered §7 on 2026-11-13 (§7 now records the answers). Base:
origin/main `af6dffa0`.
Author: architect. Requested by the person through master (task `slash-palette-spec`).

## 1. Problem

busmon has grown to about 90 actions on 14 key tables, plus the pages behind them (F1–F5, p, R, T,
U, C, M, H, A, Z…). A key the person does not remember is a feature they do not have. The help page
(F1) lists them all, but you cannot run anything from it, and it has no search. Some things the
person does with `agentbus` in a terminal have no key at all: `reload --as`, `pause`,
`self-repair reset`.

## 2. Solution, from the person's side

- In the one-line input (`i` or `:`), a **leading `/`** turns the line into a command line. A
  list opens above it with every command that matches what is typed, best match first. **↑↓**
  choose, **Tab** completes the name (or the argument being typed), **Enter** runs, and **Esc**
  closes the list without losing the text.
- **Ctrl+P** opens the same line, with the `/` already typed, from the panels and from any page;
  not while the compose window, the settings line editor or a confirmation has the keyboard, and
  never during full-screen attach, where it belongs to the agent (answer 2).
- Each row shows the command, its summary and **the key that does the same thing**, when one
  exists. The palette teaches the keys rather than replacing them.
- Commands with an argument complete it from what busmon already knows (agents, roles, plan
  tasks, settings keys). A command that changes something shows what it will do and asks
  `y` first, as `Z` does today.
- The Activity filter stays where it is: `/` pressed on the panels still opens the filter line.
  The palette exists only *inside* the input line, so the two never meet.

## 3. Inventory (what exists today)

The full table is in Appendix A. In summary:

| Family | Where | How many | In the palette? |
|---|---|---|---|
| busmon panel actions (open a page, triage, sleep, follow, quit…) | `cmd/busmon/keys.go` `panelBindings`; on the panels `modeKey` runs them (`ActQuit` as `tea.Quit`, every other through `panelKey`, `tea_update.go`) | ~30 | **yes**: class A (§4.1), no argument |
| busmon actions on the selected row (copy, delete, `+`/`-` priority, fold, enter) | same table, `Panels:` limited | ~12 | **no**: they need a selection, which a typed line does not have |
| busmon page-local keys (reader, settings, projects, notify setup, ask panel, compose, tour) | the other 13 tables in `keys.go` | ~60 | **no**: they belong to a page that has the keyboard |
| the person's `agentbus` commands (`reload --as`, `pause`, `resume`, `suspend`, `backup`, `self-repair reset`, `request priority`) | `cmd/agentbus/commands.go` (`commands`, the one CLI table) | 7 | **yes**: class C (§4.1), run as `agentbus` argv, with `y`. `answer --as` and `identity set` are the person's too, but stay on busmon's pages |
| agents' and master's `agentbus` commands (`status`, `report`, `subscribe`, `challenge`, `verdict`, `deliver`, `bind`, `swap`, `retire`, `pilot`, `dispatch`, `missions`, `mission`, `up`, `shutdown`…) | same table | ~55 | **no**: an agent's identity and duties. The person asks master instead: `/master <text>` (class B) |
| bus message kinds | `{p}:status`, `{p}:report` (note, auto), `{p}:notify`, and the `{p}:cmd` types `directive`, `reply`, `challenge`, `verdict`, `blocked`, `ask`, `handoff` (architecture.md "Stream layout") | 4 streams, 7 cmd types | only what the person already sends: a directive (`/write <agent>`), the answer to an ask (`/answer`) |
| the self-repair watcher's acts (relaunch a dead agent, mark a crash loop, report to master) | `cmd/agentbus/selfrepairact.go`, `selfrepairchore.go` | 3 automatic acts, 1 command | the watcher acts by itself; only its **reset** is a person command (`/self-repair reset <agent>`, class C) |
| settings (Shift+C) | `config/keys.go`, 74 keys in groups; written by `config.Set` / `config.Unset` | 74 | **yes, as a jump**: `/set <key>` opens the settings page on that key (§7, answer 7); the value is still edited there |

Four facts from the walk shape the design:

- **The key table is already a registry.** Each binding has `Keys`, `Hint`, `Help` and an `Act`.
  On the panels, `modeKey` dispatches every action: `ActQuit` itself, the rest through `panelKey`.
  A palette command that names an `Act` and takes that same path (`runPanelAct`, §4.1) runs
  *exactly* what the key runs. No second handler can drift.
- **busmon already runs `agentbus` as a child process**: `Z` (`sleep.go`), the Projects page's
  `x`/`b` (`projects_admin.go`) and the picker's `up` (`pick.go`), all as argv with no shell. Class C
  reuses that path.
- The `/` filter is **not** a category filter. It matches `@agent`, a thread id or free words
  (`filter.go` `parseFilter`). It is unaffected either way.
- The input line already has a candidate list: the `@agent` completion (`completions`,
  `recipientCompletions`, ↑↓, Tab and Enter pick). The palette is a second source for that same list,
  not a new widget or a new key mode.

## 4. Design

### 4.1 One table

```go
// a palette command (prototype shape, for the decision only)
type command struct {
	Name    string   // "requests"
	Aliases []string // "req", "f2"
	Summary string   // one line, English
	Args    []arg    // none for class A
	Act     Action   // class A: the key's action, run by runPanelAct (§4.1)
	Run     func(m *Model, args []string) tea.Cmd // class B and C, filter <words>, tour, set
	Key     Action   // a Run row: the Act whose key the row shows (ActComposeAt for write); 0 = no key
	Confirm bool     // class C: shows the argv it will run, y runs it
}
type arg struct {
	Name     string                  // "agent"
	Complete func(m *Model) []string // the names the panel shows, roles, plan tasks, settings keys
}
```

- **Class A** (an `Act`, no argument): `requests`, `services`, `health`, `help`, `plan`, `recap`,
  `triage`, `tokens`, `settings`, `projects`, `history`, `about`, `unread`, `follow`, `all`, `compose`, `sleep`
  (it asks first, as the key does), `filter` (it opens the filter line, as `/` does), and `quit`.

  **The route.** On the panels, a key goes through `modeKey`: `ActQuit` becomes `tea.Quit` there, and
  every other action goes to `panelKey`. A page's own table does not carry these actions;
  `readerKey` handles F5 and help, not F2. So a class A row runs as follows:
  1. It leaves the line and closes any open page, so Ctrl+P from a page ends on the panels.
  2. It calls `runPanelAct(act)`, the panel half of `modeKey` taken out as a function: `ActQuit` →
     `tea.Quit`, else `panelKey(act)`.

  `modeKey` itself calls `runPanelAct` on the panels, so a key and its command still share one path.
  Work's Kanban intercepts only movement actions (`workKanbanKey`), none of them class A.
- **Two rows with a Run of their own**, because no panel action takes their argument or path:
  - `filter <words>` does what the filter line's Enter does (`editFilter`):
    `m.ui.filter = parseFilter(words)`, then focus Activity. With no words it is the class A
    `filter`.
  - `tour` calls what the help page's `t` calls (`ActTour` is handled only in `readerKey`).
- **Class B** (writes a message the way the line already does): `write <agent> <text>` is the same
  as `@agent text`; `master <text>` is a directive to master, the way to ask for an agent's or
  master's action; `answer` opens the ask panel (as `!`).
- **Class C** (runs `agentbus` after `y`). The exact argv each row builds, checked against the
  usages in `commands.go`. `--as <identity.as>` goes only to the three commands that take it, and
  `--yes` goes to those that ask for it, because busmon's `y` is the confirmation (as `Z` passes
  `--project <p> suspend --yes`, `sleep.go`):

  Every row names the project once, as the global flag before the command (`agentbus --project <p>
  <command> …`), the form `Z` uses. No row repeats it after the command.

  | Palette | argv after `agentbus --project <p>` |
  |---|---|
  | `reload <role> [fresh]` | `reload <role> [--fresh] --as <person>` |
  | `pause <dur>` | `pause --for <dur>` |
  | `resume` | `resume --yes` (the same run as `Z` on a suspended project) |
  | `suspend` | `suspend --yes` (the same run as `Z`) |
  | `backup` | `backup` (main.go hands the global `--project` to `runBackup` as its project; the Projects page's `b` writes `backup --project <p>` without the global flag; the two are the same run, and both together would name the project twice) |
  | `self-repair reset <agent>` | `self-repair reset <agent> --as <person>` |
  | `priority <task> <low\|normal\|high>` | `request priority <task> <level> --as <person>` |

  `suspend` and `resume` are `Z` (`sleep`) by name: the palette rows call the same run, so the two
  cannot differ.

  With no `identity.as` set, the three `--as` rows are refused before `y`, naming the setting. They
  never fall back to a default name. The registry test (§5) parses every row's argv against the
  flags of its `commands` entry, so a wrong flag fails the suite.

  The line under the list shows the exact argv before `y`. The outcome lands as a flash line, and
  failures keep their stderr, as `Z` does.
- The row's key hint is derived, not typed. It is the first binding in `panelBindings` whose
  `Act` equals the row's `Act`, or, for a Run row, its `Key`. Each Run row is linked explicitly:

  | Run row | `Key` | Hint shown |
  |---|---|---|
  | `write <agent> <text>` | `ActComposeAt` | `@` |
  | `master <text>` | `ActAskOrWrite` | `w` |
  | `answer` | `ActAsk` | `F4 !` |
  | `filter <words>` | `ActFilter` | `/` |
  | `set <key>` | `ActSettings` | `C` |
  | `tour` | none in `panelBindings` (the help page's `t`) | `F1 t`, written in the row |
  | class C rows | none (no key today) | — |

  The registry test checks that every `Key` names an `Act` present in `panelBindings`.
- **The help page (F1) gains a "Commands" section** generated from the same table.

### 4.2 Matching

A small in-house scorer, about 40 lines, with no new module (answer 5). The query is matched
case-insensitively against the name, then the aliases, then the summary:

1. an exact name or alias;
2. a name prefix;
3. a word start in the name or summary;
4. a subsequence (`rq` → `requests`).

Ties keep the table order. An empty query (`/` alone) lists every command in table order. With
about 45 commands, no index is needed.

**A one-letter alias matches exactly and case-sensitively**, as its key does. busmon's keys pair
letters by case (`r` Requests and `R` recap, `a` all and `A` about, `t` tour and `T` triage, `u`
unread and `U` tokens). A case-insensitive letter would be ambiguous. Every other match ignores case.

**Aliases** (answer 10: the F-key, the letter, and short natural words where unambiguous; for
review):

| Command | Aliases |
|---|---|
| `help` | `f1`, `?` |
| `requests` | `f2`, `r`, `req` |
| `services` | `f3`, `s`, `svc` |
| `answer` | `f4`, `!` |
| `health` | `f5` |
| `plan` | `p` |
| `recap` | `R` |
| `triage` | `T` |
| `tokens` | `U` |
| `settings` | `C`, `config` |
| `projects` | `M` |
| `history` | `H` |
| `about` | `A`, `version` |
| `tour` | `t` |
| `unread` | `u` |
| `follow` | `f`, `autoscroll` |
| `all` | `a` |
| `compose` | `I` |
| `sleep` | `Z` |
| `filter` | — |
| `quit` | `q`, `exit` |
| `write` | `@` |
| `master` | `w` |
| `set` | — |
| `reload`, `pause`, `resume`, `suspend`, `backup` (slice 2) | — |
| `self-repair reset` (slice 2) | — (`reset` alone would collide with the settings page's reset) |
| `priority` (slice 2) | `prio` |

`filter` has no `/` alias: in the line, `//` already means a literal `/` (answer 6).

### 4.3 Keys in the line

| Key | With the list open | Without |
|---|---|---|
| ↑ ↓ | choose a row | (unchanged) |
| Tab | complete the chosen name; on an argument, complete it from `arg.Complete` | (unchanged: `@agent`) |
| Enter | if ↑↓ moved the selection since the last edit, the **chosen row** wins; otherwise the **typed line** runs when it parses; otherwise the chosen (top) row | send (unchanged) |
| Esc | close the list, keep the text; Esc again leaves the line (unchanged) | (unchanged) |
| Ctrl+P | (from the panels or a page) open the line with `/` typed | — |

- A line that starts with `//` is sent as text that starts with `/` (answer 6).
- `@coder /something` is unchanged: only a **leading** `/` opens the palette.
- Typing or deleting clears "moved", so the typed line counts again. Example: `/r` + Enter runs
  `requests` (the exact alias `r`). `/r`, ↓, Enter runs the second row.
- **Ctrl+P inside busmon** is free: busmon binds Ctrl A, C, E, O, S, T, U, V, W, Y and Z. The key
  decoder gains `keyCtrlP`: the legacy byte 0x10, and the kitty form `CSI 112;5u`.
- **Ctrl+P outside busmon: holder and attach.** herdr is gone in the next version, so no
  multiplexer prefix applies.
  - **The holder** never reads the person's keyboard. busmon talks to it over its socket, and only
    `agentbus attach` forwards keys to it.
  - **Attach** is full-screen. busmon runs `agentbus attach` through `tea.ExecProcess` (herdr-exit
    spec §4.6) and reads no key until attach returns. Attach intercepts only `attach.prefix`
    (default F12) and passes Ctrl+P to the agent's terminal, which is the person's answer 2. busmon's
    Ctrl+P is therefore never seen during attach, whatever the prefix is set to.
- **Ctrl+P in the terminal emulators.** In raw mode Ctrl+P is the byte 0x10. It is not flow control
  (that is Ctrl+S and Ctrl+Q, and busmon already uses Ctrl+S).
  - **macOS** (Terminal.app, iTerm2, Ghostty, kitty, WezTerm): app shortcuts use Cmd, so Ctrl+P
    reaches busmon. Ghostty's command palette is Cmd+Shift+P.
  - **Linux** (GNOME Terminal, Konsole, Ghostty, kitty, Alacritty): terminal shortcuts use
    Ctrl+Shift, so Ctrl+P reaches busmon. Ghostty's palette is Ctrl+Shift+P.
  - **Windows Terminal** binds its command palette to Ctrl+Shift+P, so Ctrl+P would pass through.
    Windows is out of scope (the person, 2026-11-11), so this is recorded, not tested.
  - **The known exception is the VS Code integrated terminal on Linux and Windows**, where Ctrl+P is
    Quick Open and VS Code keeps it. On macOS Quick Open is Cmd+P. tmux and screen bind no Ctrl+P by
    default.
  - Wherever Ctrl+P does not arrive, `i` then `/` is the same palette, so Ctrl+P is a shortcut, never
    the only way in.
- **How busmon finds `agentbus`** (class C, slice 2): the existing `agentbusBeside` (`pick.go`).
  1. It looks for the `agentbus` next to busmon's own executable (`os.Executable`, symlinks
     resolved, since setup.sh links both).
  2. Otherwise it runs the bare name `agentbus`, which `exec.Command` resolves on PATH with
     `exec.LookPath`. That works on Linux and macOS alike. Since Go 1.19, `exec` refuses a program
     found through a relative PATH entry such as `.`.
  3. The argv goes straight to `exec.Command`, with no shell, as `Z` and the Projects page do today.
  - On Windows the file beside busmon would be `agentbus.exe`, which `agentbusBeside` does not look
    for, so it would fall back to PATH. That is out of scope with Windows itself.

### 4.4 Safety

- Class C runs only the argv of its table row with typed arguments checked first. Agent and role
  names go through `bus.ValidName`, durations through the parser `pause` already uses, and the
  priority through its three words. Nothing typed reaches a shell.
- Class C never offers what is not the person's (agents' commands, master's `swap`, `deliver` or
  `pilot`). The person asks master with `/master`.
- A failed parse runs nothing and says why on the flash line.

## 5. Testing

- **Pure seam:** `paletteMatch(query, table) []row` and `parseCommandLine(line, table)
  (command, args, error)`. Table tests cover the rank order, aliases, `//`, the leading-only `/`,
  the argument checks, and an unknown command.
- **Route test:** every class A row, run through `runPanelAct` on a test model, changes the same state as its key does through `modeKey`, `quit` included (it returns `tea.Quit`).
- **Registry guard:** every class A `Act` and every Run row's `Key` exists in `panelBindings`, and every class C argv names a
  command in the `agentbus` `commands` table and uses only flags that entry or `globalFlags` declares (`--as`, `--for`,
  `--yes`, `--fresh`, `--project`). This is a test in the style of
  `TestEveryDispatchedCommandIsInTheTable`.
- **Model tests** on the existing input tests (`tea_input_test.go`, `keys_test.go`):
  - `/req` + Enter opens Requests exactly as F2 does, compared on the resulting model state;
  - the Enter rule: `/r` + Enter opens Requests (not recap); `/r`, ↓, Enter runs the second row;
    `/r`, ↓, then typing `e`, then Enter runs the typed line again;
  - `/R` + Enter opens the recap (case-sensitive letter alias);
  - Ctrl+P from a page opens the line with `/`;
  - Esc keeps the text;
  - Ctrl+P is ignored while the compose window, the settings line editor or a confirmation has the
    keyboard;
  - class C (slice 2) stops at the confirmation and runs a fake `agentbus` on PATH, as the Projects
    page tests do.

## 6. Out of scope

- A palette inside the compose window (`I`): there `/` stays text (answer 1).
- Recent commands first (answer 9), for slice 1.
- Windows (the person, 2026-11-11).
- Actions on the selected row, and page-local keys.
- Commands that are agents' or master's.
- Persisted command history.
- Editing a setting's value from the line: `/set` only jumps to it.
- Any change to the bus protocol: no new message type.

## 7. Answers to section 7 (the person, 2026-11-13)

The questions were asked in r1–r4; the person answered them on the bus (to master, 2026-11-13
21:43). The design above follows these answers.

1. **Input:** the one-line input (`i` or `:`) only. In the compose window (`I`), `/` stays text.
2. **Ctrl+P:** yes, from the panels and every page, except while the compose window, the
   settings line editor or a confirmation has the keyboard. It is also excluded during
   full-screen attach to an agent, where Ctrl+P goes to the agent's terminal (§4.3).
3. **First slice:** class A and class B, plus the Run rows `filter <words>`, `tour` and `set`.
   Class C comes in a second step.
4. **Class C:** yes. Each row shows its exact argv before `y` (§4.1).
5. **Matching:** the in-house scorer of about 40 lines, with no new module (§4.2).
6. **Leading `/`:** `//` sends a literal `/`.
7. **Settings:** `/set <key>` jumps to the key on the settings page. There is no line editor.
8. **Key hint on each row:** yes, with Run rows linked explicitly to their key (§4.1).
9. **Recent commands first:** no, for the first slice.
10. **Names:** English verbs and page names, with the F-key and the letter as aliases, plus short
    natural aliases where unambiguous. The list is in §4.2, for review.

The person's corrections are made in this head:
- the Ctrl+B check is replaced by the holder and attach check (§4.3);
- `--project` appears once in every row (§4.1);
- Run rows carry an explicit `Key` (§4.1);
- the Enter rule and its test (§4.3, §5);
- "Four facts" (§3);
- Ctrl+P across terminals, and how busmon finds `agentbus` (§4.3).

## Appendix A. Inventory

### A.1 busmon panel actions (`panelBindings`, run by `modeKey` then `panelKey`, `cmd/busmon/tea_update.go`)

| Key | Action | What it does | Argument | Palette |
|---|---|---|---|---|
| F1 `?` | ActHelp | the help page | — | `help` |
| F2 `r` | ActRequests | Requests page | — | `requests` |
| F3 `s` | ActServices | Services page (cards, logs) | — | `services` |
| F4 `!` | ActAsk | answer the open questions | — | `answer` |
| F5 | ActHealth | Health page | — | `health` |
| `w` | ActAskOrWrite | answer, or write to master | — | `master` |
| `p` `P` | ActPlan | the plan drawer | — | `plan` |
| `R` | ActRecap | the project recap | — | `recap` |
| `T` | ActTriage | triage what waits for you | — | `triage` |
| `U` | ActTokens | tokens page | — | `tokens` |
| `C` | ActSettings | settings page | (key) | `settings`, `set <key>` |
| `M` | ActProjects | Projects page | — | `projects` |
| `H` | ActHistory | History page | — | `history` |
| `A` | ActAbout | About page | — | `about` |
| `t` (help) | ActTour | the first-launch tour (handled in `readerKey` only) | — | `tour` (a Run, §4.1) |
| `u` | ActNextUnread | next unread for you | — | `unread` |
| `f` | ActFollow | auto-scroll on or off | — | `follow` |
| `Z` | ActSleep | suspend or resume the project (asks first) | — | `sleep` |
| `/` | ActFilter | open the filter line (@agent, thread or words) | — | `filter`; `filter <words>` is a Run (§4.1) |
| `i` `:` | ActCompose | the one-line input | — | (the palette lives here) |
| `I` | ActComposeOpen | the compose window | — | `compose` |
| `@` | ActComposeAt | the line, prefilled with @ | agent | `write <agent>` |
| `1`–`6`, ←→, tab | ActTab*, ActNextPanel | views, tabs, panel focus | — | no (navigation) |
| ↑↓ j k pgup pgdn g G | ActUp… ActLast, ActLive | move in a list | — | no |
| enter `v` | ActOpen | open the selected row | row | no |
| `y` | ActCopy | copy the selected row | row | no |
| `d` del | ActDelete | remove the selected agent or request | row | no |
| `a` | ActShowAll | show the long-silent agents | — | `all` |
| `o` | ActFold | Requests: fold the answered threads | — | no (page-local) |
| `+` `-` | ActPriorityUp/Down | a request's priority | row | class C `priority <task> <level>` |
| `d` (Work) | ActShowDone | the plan with done tasks | — | no |
| esc, `q` | ActEscape, ActQuit | back; quit (`modeKey` returns `tea.Quit`, not `panelKey`) | — | `quit` |

Other tables (`keys.go`): input, filter, ask, compose, compose file (`composeFileBindings`, the @ popup), picker, overlay (pages),
settings, notify setup, projects, project delete and tour. They are page-local and stay keys.

### A.2 `agentbus` commands (`cmd/agentbus/commands.go`, `commands`)

The person's commands, in the palette as class C, with their exact argv in §4.1:
- `reload --as`;
- `pause --for`;
- `resume --yes`;
- `suspend --yes`;
- `backup --project`;
- `self-repair reset --as`;
- `request priority --as`.

`answer --as` and `identity set` are the person's too, but busmon already does them on its pages.

Agents' and master's commands stay out of the palette, which reaches them through `/master <text>`:

- reporting: `status`, `report`, `reports`, `notify`, `background`, `handoff`;
- messaging: `cmd`, `reply`, `subscribe`, `listen`, `thread`, `history`, `merge-queue`;
- reviews: `challenge`, `verdict`, `verdicts`, `gate`;
- coordination: `pilot`, `agents`, `pane`, `bind`, `usage`, `budget`, `board`, `request` (other
  than priority);
- lifecycle: `shutdown`, `refresh`, `swap`, `retire`, `swaps`, `up`, `start`, `down`;
- missions and delivery: `dispatch`, `missions`, `mission`, `plan`, `deliver`;
- services and data: `service`, `projects`, `tokens`, `restore`;
- configuration: `config`, `notifications`, `quota`, `models`, `roles`, `mem`, `term`;
- information: `limits`, `version`, `help`;
- internal: `setup`, `plugin`, `peer`, `statusline`.

### A.3 Bus message kinds (`docs/architecture.md`, Stream layout)

- **Streams:** `{p}:status`, `{p}:report` (note or auto), `{p}:notify` (`for=person`) and
  `{p}:cmd`.
- **`{p}:cmd` types:** `directive`, `reply`, `challenge`, `verdict`, `blocked`, `ask` and
  `handoff`.
- **What the person sends from busmon:** a directive or a broadcast (the line, the compose window),
  the answer to an ask (the ask panel) and a reply (a message page).

The palette adds no new kind.

### A.4 Self-repair watcher (`cmd/agentbus/selfrepairact.go`, `selfrepairchore.go`)

- **Automatic acts, fenced on the dispatch lease:** relaunch a dead agent in a new pane, mark a
  crash loop, and report to master once per episode.
- **The person's command:** `agentbus self-repair reset <agent> --as <person>`, which becomes
  class C `self-repair reset <agent>`.

### A.5 Settings (`config/keys.go`, 74 keys; the C page)

The groups are display, attention, identity, notifications, services, backup and projects, plus
dispatch, health, history, bus, delivery, suspend and models, shown under their page groups. The
palette only jumps to a key (`set <key>`, completing on the key names). The value is written by the
settings page (`config.Set`, `config.Unset`) as today.
