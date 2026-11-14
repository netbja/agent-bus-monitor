# busmon command palette: `/` and Ctrl+P open it; `f` filters; auto-scroll is a setting

Status: r9, spec only. The person answered §7 on 2026-11-13 (§7 records the answers). r9 folds in
the person's change request of 2026-11-14 (§8). Slice 1 is built (PR 470 merged the spec; `cmd/busmon/palette.go`).
Base: origin/main `8a79182f`.
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
- **`/` belongs to the palette** (r9, §8): `/` on the panels opens the one-line input with `/`
  typed, exactly as Ctrl+P does. The Activity filter line moves to **`f`**.
- **Auto-scroll is a setting** (r9, §8): `display.follow`, on or off, on the settings page (C),
  display group. It has no key and no hint in the bottom bar. `G`/end still jumps to the newest
  line, and the palette's `/follow` flips the setting.

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
- The Activity filter is **not** a category filter. It matches `@agent`, a thread id or free words
  (`filter.go` `parseFilter`). r9 moves its key from `/` to `f`; the line itself is unchanged
  (§8, answer b).
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
  `triage`, `tokens`, `settings`, `projects`, `history`, `about`, `unread`, `all`, `compose`, `sleep`
  (it asks first, as the key does), `filter` (it opens the filter line, as `f` does), and `quit`.
  `follow` is no longer class A: r9 removes `ActFollow` and makes `follow` a Run row (§8.3).

  **The route.** On the panels, a key goes through `modeKey`: `ActQuit` becomes `tea.Quit` there, and
  every other action goes to `panelKey`. A page's own table does not carry these actions;
  `readerKey` handles F5 and help, not F2. So a class A row runs as follows:
  1. It leaves the line and closes any open page, so Ctrl+P from a page ends on the panels.
  2. It calls `runPanelAct(act)`, the panel half of `modeKey` taken out as a function: `ActQuit` →
     `tea.Quit`, else `panelKey(act)`.

  `modeKey` itself calls `runPanelAct` on the panels, so a key and its command still share one path.
  Work's Kanban intercepts only movement actions (`workKanbanKey`), none of them class A.
- **Every row, Run rows included, starts the same way:** it leaves the line and closes every open
  page (the reader stack, `m.rd`), then acts on the panels. A command run from a page with Ctrl+P
  therefore shows its result, never the page it was started from. A model test runs each row from
  an open page and checks that no page is left open, except one the row itself opens.
- **Two rows with a Run of their own**, because no panel action takes their argument or path:
  - `filter <words>` does what the filter line's Enter does (`editFilter`):
    after the shared first step (pages closed), `m.ui.filter = parseFilter(words)`, then focus
    Activity, so the filtered Activity is what the person sees. With no words it is the class A
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
  | `master <text>` | `ActAskOrWrite`, **only while no ask is open** | `w`, or nothing |
  | `answer` | `ActAsk` | `F4 !` |
  | `filter <words>` | `ActFilter` | `f` (r9; was `/`) |
  | `set <key>` | `ActSettings` | `C` |
  | `tour` | none in `panelBindings` (the help page's `t`) | `F1 t`, written in the row |
  | class C rows | none (no key today) | — |

  `w` is not always `master`: `askOrWrite` opens the ask panel while any ask is open, and the
  master composer only when none is. So the `master` row shows `w` only while no ask is open (the
  same `openAsks` test `askOrWrite` makes, read at render). Otherwise it shows no key rather than one
  that does something else. A model test covers both states.

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
| `follow` | `autoscroll` (r9: `f` is the filter's key now, so it is no longer `follow`'s alias) |
| `all` | `a` |
| `compose` | `I` |
| `sleep` | `Z` |
| `filter` | `f` |
| `quit` | `q`, `exit` |
| `write` | `@` |
| `master` | — (`w` is shown as its key only while no ask is open, §4.1; as an alias it would mean two things) |
| `set` | — |
| `reload`, `pause`, `resume`, `suspend`, `backup` (slice 2) | — |
| `self-repair reset` (slice 2) | — (`reset` alone would collide with the settings page's reset) |
| `priority` (slice 2) | `prio` |

`filter` has no `/` alias: in the line, `//` already means a literal `/` (answer 6). r9 gives it the
letter alias `f`, its key.

### 4.3 Keys in the line

| Key | With the list open | Without |
|---|---|---|
| ↑ ↓ | choose a row | (unchanged) |
| Tab | complete the chosen name; on an argument, complete it from `arg.Complete` | (unchanged: `@agent`) |
| Enter | the rule in §4.3.1 | send (unchanged) |
| Esc | close the list, keep the text; Esc again leaves the line (unchanged) | (unchanged) |
| Ctrl+P, and `/` on the panels (r9) | open the line with `/` typed | — |

- A line that starts with `//` is sent as text that starts with `/` (answer 6).
- `@coder /something` is unchanged: only a **leading** `/` opens the palette.
- Typing or deleting clears "moved", so the typed line counts again. Example: `/r` + Enter runs
  `requests` (the exact alias `r`). `/r`, ↓, Enter runs the second row.

#### 4.3.1 The Enter rule (r9: replaces the r8 sentence and §4.4's last line)

Four cases, checked in this order:

1. **The selection was moved with ↑↓ since the last edit:** the chosen row wins. Its name replaces
   the typed name, the typed arguments stay, and the line is parsed again.
2. **The typed name is a complete command name or alias.** Its line runs when its arguments
   parse. If an argument is missing or invalid (`/reload` with no role, `/write` with no agent, `/set nosuch`),
   **nothing runs**: the flash line says what is missing or wrong, and the text stays in the line
   to be fixed.
3. **The typed word is not a name but still matches rows** (a prefix, a word start or a
   subsequence: `/requ`, `/he`, `/rq`): Enter takes the **top row**, the best match, with the typed
   arguments.
4. **The typed word matches no row at all** (`/zzz`): **nothing runs**. The flash says "no command
   matches: / alone lists them", and the text stays.

Case 2 is what §4.4 meant by "a failed parse runs nothing". Case 3 is what r8's "otherwise the chosen
(top) row" meant. They no longer read as two rules. This is the order `paletteEnter` already
follows: `palMoved`, then `parseCommandLine`, whose `errNoCommand` alone falls through to the top
row. Every other error is flashed with the line kept. With no rows, the fall-through (`chosen`)
flashes and runs nothing: that is case 4.
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
  - **The VS Code integrated terminal keeps Ctrl+P on Linux and Windows by default.**
    `terminal.integrated.commandsToSkipShell` defaults to a hard-coded list of commands whose
    shortcuts skip the shell, and Quick Open (`workbench.action.quickOpen`, Ctrl+P there) is in it.
    The VS Code docs' example *removes* it with a leading `-`, commented "Send quick open's keyboard
    shortcut to the shell" (code.visualstudio.com/docs/terminal/advanced, "Keyboard shortcuts and the
    shell"). A person can give Ctrl+P to busmon with
    `"terminal.integrated.commandsToSkipShell": ["-workbench.action.quickOpen"]`. On macOS Quick Open
    is Cmd+P, so Ctrl+P passes. tmux and screen bind no Ctrl+P by default.
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
- A known command with a missing or invalid argument runs nothing and says why on the flash line,
  the text kept (§4.3.1 case 2).

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
  - §4.3.1, one test per case:
    - case 1: `/he`, ↓, Enter runs the second row and not `help`;
    - case 2: `/write` + Enter (no agent) runs nothing, the flash names the missing `agent`, and
      the line still reads `/write`. When class C lands, `/reload` + Enter is checked the same way;
    - case 3: `/requ` + Enter opens Requests (the top row);
    - case 4: `/zzz` + Enter runs nothing, flashes "no command matches", and the line still reads
      `/zzz`;
  - r9 keys: `/` on the panels opens the line with `/` (the same model state as Ctrl+P); `f` opens
    the filter line; `f` on History opens History's filter; `G` with
    `display.follow` off jumps to the newest line and does **not** resume live (new lines are counted
    after it); with it on, `G` resumes live as today; `/follow` flips `display.follow` and its flash says the new value;
  - `/R` + Enter opens the recap (case-sensitive letter alias);
  - the `master` row shows `w` with no ask open and no key with one open;
  - Ctrl+P from a page opens the line with `/`;
  - Esc keeps the text;
  - `/filter coder` run from an open page (Ctrl+P) closes the page and shows Activity filtered;
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

## 8. Change request (the person, 2026-11-14): `/`, `f` and auto-scroll

### 8.1 `/` opens the palette

- On the panels, `/` binds `ActPalette`, the action Ctrl+P already has (`openPalette`): the
  one-line input opens with `/` typed. The panel binding `/` → `ActFilter` goes.
- **On pages** (`overlayBindings`), History's `/` filter (`OnPage`) moves to `f` too, so `/` means
  the palette everywhere it is a key (answer c, recommended). That page's `/` then binds
  `ActPalette` like its `^P` row.
- Inside the one-line input, `/` is text. A leading `/` is what makes the line a command line,
  unchanged. The filter line, the compose window and the picker's type-to-filter are unaffected.

### 8.2 `f` opens the filter line

- `f` binds `ActFilter` on the panels. It is free once `ActFollow` goes. On History it replaces
  `/`. `f` is bound nowhere else on pages, and the settings, projects, notify setup and tour tables
  do not use it.
- The palette's `/filter <words>` stays; its key hint becomes `f`, derived from the binding, and
  `f` becomes its alias.

### 8.3 Auto-scroll becomes the setting `display.follow`

- **Registry:** `display.follow` in `config/keys.go`, a boolean, `ReadBy: "busmon"`, shown on
  the settings page (C) in the display group. It is applied by `applySettings` like
  `display.work_layout`.
- **No key:** `ActFollow` leaves `panelBindings`, so the bottom bar and the help lose `f
  auto-scroll`, and the `case ActFollow` in `panelKey` goes. `G`/end (`ActLive`) still jumps to
  the newest line.
- **What on and off mean** (answer d, recommended):
  - **on**, today's behaviour: Activity scrolls with new lines. **Only a wheel scroll up pauses
    it** for the moment (`listState.ScrollBy` with a negative delta; the title counts `· N new`).
    Moving the selection, clicking or opening a line keeps it on (`listState.Move` never pauses;
    OPERATIONAL-GUIDE "Activity auto-scroll"). Today `f` pauses it too; r9 removes `f`, so the wheel
    is the only way left. `G`/end resumes (`Last` calls `Resume`), and so does ↓ onto the newest line
    or the wheel back to the bottom while paused (A3).
  - **off:** Activity never scrolls by itself. New lines are counted, and the view stays.
    `G`/end jumps to the newest line once and stays put there. A3's automatic resume is off.
  - The title keeps saying which (`auto-scroll on` / `auto-scroll off · N new`), so the setting's
    effect is visible without opening C.
- **Palette:**
  - `/follow` (alias `autoscroll`) becomes a Run row, with `Key` none and the summary "auto-scroll
    on or off (the setting display.follow)". It flips the setting through the same code as Enter on
    that key in the settings page (`config.Set` then `applySettings`), and flashes the new value.
  - `/set display.follow` jumps to the key.
- **What still teaches the old keys, all updated in the same slice:**
  - the bottom hint bar and the F1 help: both derived from `keys.go`, so they follow the
    bindings;
  - the tour: its filter row reads `ky(ModeShell, ActFilter)`, so it follows; no tour row shows
    follow;
  - `README.md`: the screen sample's hint line ("`f auto-scroll` … `/ filter`"), the Activity
    paragraph ("`/` filters"), and History's "`/` filters" row;
  - `docs/OPERATIONAL-GUIDE.md` and `docs/OPERATIONAL-GUIDE.fr.md`: the "Activity auto-scroll"
    paragraph (`f` turns it off), which becomes the setting;
  - the tests: `tea_ported_test.go` "slash filters" (`/` → `ActFilter`, now `ActPalette`), plus any
    `keys_test.go` row on `f` or `/`;
  - `cmd/busmon/main.go`'s header comment ("/ filters").

### 8.4 Questions for the person (answered in busmon)

a. **Default of `display.follow`:** *recommended* **on**, today's behaviour, so nothing changes
   for someone who never opens C.
b. **The filter line once opened:** *recommended* **unchanged**. Enter applies (`parseFilter`:
   `@agent`, a thread id, words), Esc cancels, and the filter shows in Activity's title. Only the
   key that opens it changes.
c. **History's `/`:** *recommended* it moves to `f` like the panels', and `/` opens the palette
   on that page too, so `/` has one meaning wherever it is a key. Alternative: History keeps `/` as
   its filter, and `/` means two things.
d. **`display.follow` off:** *recommended* as in §8.3: no scrolling by itself, `G`/end a one-time
   jump, no A3 resume. Alternative: off only changes the start-up state, and A3 still resumes.

### 8.5 The slice for coder (slice 1b)

One PR from main, before class C (slice 2):

1. `keys.go`:
   - `/` → `ActPalette` on the panels and on pages;
   - `f` → `ActFilter` on the panels and History;
   - `ActFollow` removed, with its `panelKey` case.
2. `config/keys.go`: `display.follow` (bool, default per answer a). `settings.go` reads it, the
   settings page shows it in display, and `applySettings` applies it.
3. Activity's list: the start-up state and the A3 resume read `display.follow` (answer d).
   `G`/end jumps to the newest line. With follow on it resumes live as today (`Last` →
   `Resume`); with follow off it does not resume. The view stays on the newest line, and lines
   arriving after it are counted. So `Last` needs the setting, not just `Resume`.
4. `palette.go`:
   - `follow` becomes a Run flipping the setting, alias `autoscroll` only;
   - `filter` gets the alias `f`, and its hint derives `f`.
5. Text: `README.md`, the two operational guides and `main.go`'s comment (§8.3).
6. Tests:
   - the r9 model tests of §5 (`/`, `f`, History `f`, `G` with follow off, `/follow`);
   - one test per §4.3.1 case (four);
   - the updated "slash filters" row.

The §4.3.1 Enter rule is already what `paletteEnter` does. 1b adds only its four tests, which
check that cases 2 and 4 keep the text.

## Appendix A. Inventory

### A.1 busmon panel actions (`panelBindings`, run by `modeKey` then `panelKey`, `cmd/busmon/tea_update.go`)

| Key | Action | What it does | Argument | Palette |
|---|---|---|---|---|
| F1 `?` | ActHelp | the help page | — | `help` |
| F2 `r` | ActRequests | Requests page | — | `requests` |
| F3 `s` | ActServices | Services page (cards, logs) | — | `services` |
| F4 `!` | ActAsk | answer the open questions | — | `answer` |
| F5 | ActHealth | Health page | — | `health` |
| `w` | ActAskOrWrite | answer while an ask is open, else write to master | — | `master` (the hint `w` only while no ask is open, §4.1) |
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
| `f` | ActFollow → **removed in r9** | auto-scroll on or off | — | `follow` (a Run row flipping `display.follow`, §8.3) |
| `Z` | ActSleep | suspend or resume the project (asks first) | — | `sleep` |
| `/` → **`f` in r9** | ActFilter | open the filter line (@agent, thread or words) | — | `filter`; `filter <words>` is a Run (§4.1) |
| `/` (r9) | ActPalette | the palette: the line with `/` typed, as Ctrl+P | — | — |
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

Other tables (`keys.go`), 13 in all:
- the shell's tab keys (`shellTabs`);
- input, filter, ask, compose, compose file (`composeFileBindings`, the @ popup);
- picker, overlay (pages), settings, notify setup;
- projects, project delete and tour.

They are page-local and stay keys. `shellBindings` and `shellAskBindings` are built from
`panelBindings`, `shellTabs` and `askBindings`, not tables of their own.

### A.2 `agentbus` commands (`cmd/agentbus/commands.go`, `commands`)

The person's commands, in the palette as class C, with their exact argv in §4.1:
- `reload --as`;
- `pause --for`;
- `resume --yes`;
- `suspend --yes`;
- `backup` (the project given once, as the global `--project` before the command, §4.1);
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
