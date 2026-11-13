# busmon command palette: `/` in the input line, Ctrl+P anywhere

Status: draft, spec only, for the person's decisions (§7). Base: origin/main `af6dffa0`.
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
- **Ctrl+P** opens the same line, with the `/` already typed, from the panels and from any page.
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
| busmon panel actions (open a page, triage, sleep, follow, quit…) | `cmd/busmon/keys.go` `panelBindings`; all run through `panelKey` (`tea_update.go`) | ~30 | **yes**: class A (§4.2), no argument |
| busmon actions on the selected row (copy, delete, `+`/`-` priority, fold, enter) | same table, `Panels:` limited | ~12 | **no**: they need a selection, which a typed line does not have |
| busmon page-local keys (reader, settings, projects, notify setup, ask panel, compose, tour) | the other 13 tables in `keys.go` | ~60 | **no**: they belong to a page that has the keyboard |
| the person's `agentbus` commands (`reload --as`, `pause`, `resume`, `suspend`, `backup`, `self-repair reset`, `request priority`, `answer`, `identity set`) | `cmd/agentbus/commands.go` (`commands`, the one CLI table) | 9 | **yes**: class C (§4.2), an argument, run as `agentbus` argv, with `y` |
| agents' and master's `agentbus` commands (`status`, `report`, `subscribe`, `challenge`, `verdict`, `deliver`, `bind`, `swap`, `retire`, `pilot`, `dispatch`, `missions`, `mission`, `up`, `shutdown`…) | same table | ~55 | **no**: an agent's identity and duties. The person asks master instead: `/master <text>` (class B) |
| bus message kinds | `{p}:status`, `{p}:report` (note, auto), `{p}:notify`, and the `{p}:cmd` types `directive`, `reply`, `challenge`, `verdict`, `blocked`, `ask`, `handoff` (architecture.md "Stream layout") | 4 streams, 7 cmd types | only what the person already sends: a directive (`/write <agent>`), the answer to an ask (`/answer`) |
| the self-repair watcher's acts (relaunch a dead agent, mark a crash loop, report to master) | `cmd/agentbus/selfrepairact.go`, `selfrepairchore.go` | 3 automatic acts, 1 command | the watcher acts by itself; only its **reset** is a person command (`/self-repair reset <agent>`, class C) |
| settings (Shift+C) | `config/keys.go`, 74 keys in groups; written by `config.Set` / `config.Unset` | 74 | **yes, as a jump**: `/set <key>` opens the settings page on that key (Q7); the value is still edited there |

Two facts from the walk shape the design:

- **The key table is already a registry.** Each binding has `Keys`, `Hint`, `Help` and an `Act`.
  Every panel action is handled by one function (`panelKey`). A palette command that names an
  `Act` and calls that function runs *exactly* what the key runs. No second handler can drift.
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
	Act     Action   // class A: the key's action, run through panelKey
	Run     func(m *Model, args []string) tea.Cmd // class B and C
	Confirm bool     // class C: shows the argv it will run, y runs it
}
type arg struct {
	Name     string                  // "agent"
	Complete func(m *Model) []string // the names the panel shows, roles, plan tasks, settings keys
}
```

- **Class A** (an `Act`, no argument): `requests`, `services`, `health`, `help`, `plan`, `recap`,
  `triage`, `tokens`, `settings`, `projects`, `history`, `about`, `tour`, `unread`, `follow`, `all`, `compose`, `sleep`
  (it asks first, as the key does), `filter <words>` (it applies the filter directly), `quit`.
- **Class B** (writes a message the way the line already does): `write <agent> <text>` is the same
  as `@agent text`; `master <text>` is a directive to master, the way to ask for an agent's or
  master's action; `answer` opens the ask panel (as `!`).
- **Class C** (runs `agentbus` after `y`). The exact argv each row builds, checked against the
  usages in `commands.go`. `--as <identity.as>` goes only to the three commands that take it, and
  `--yes` goes to those that ask for it, because busmon's `y` is the confirmation (as `Z` passes
  `--project <p> suspend --yes`, `sleep.go`):

  | Palette | argv after `agentbus --project <p>` |
  |---|---|
  | `reload <role> [fresh]` | `reload <role> [--fresh] --as <person>` |
  | `pause <dur>` | `pause --for <dur>` |
  | `resume` | `resume --yes` (the same run as `Z` on a suspended project) |
  | `suspend` | `suspend --yes` (the same run as `Z`) |
  | `backup` | `backup --project <p>` (as the Projects page's `b`) |
  | `self-repair reset <agent>` | `self-repair reset <agent> --as <person>` |
  | `priority <task> <low\|normal\|high>` | `request priority <task> <level> --as <person>` |

  `suspend` and `resume` are `Z` (`sleep`) by name: the palette rows call the same run, so the two
  cannot differ.

  With no `identity.as` set, the three `--as` rows are refused before `y`, naming the setting. They
  never fall back to a default name. The registry test (§5) parses every row's argv against the
  flags of its `commands` entry, so a wrong flag fails the suite.

  The line under the list shows the exact argv before `y`. The outcome lands as a flash line, and
  failures keep their stderr, as `Z` does.
- The row's key hint is derived, not typed: the first binding in `panelBindings` whose `Act`
  equals the command's `Act`.
- **The help page (F1) gains a "Commands" section** generated from the same table.

### 4.2 Matching

A small in-house scorer, about 40 lines, with no new module (Q5). The query is matched
case-insensitively against the name, then the aliases, then the summary:

1. an exact name or alias;
2. a name prefix;
3. a word start in the name or summary;
4. a subsequence (`rq` → `requests`).

Ties keep the table order. An empty query (`/` alone) lists every command in table order. With
about 45 commands, no index is needed.

### 4.3 Keys in the line

| Key | With the list open | Without |
|---|---|---|
| ↑ ↓ | choose a row | (unchanged) |
| Tab | complete the chosen name; on an argument, complete it from `arg.Complete` | (unchanged: `@agent`) |
| Enter | run the line as typed when it parses; otherwise take the chosen row | send (unchanged) |
| Esc | close the list, keep the text; Esc again leaves the line (unchanged) | (unchanged) |
| Ctrl+P | (from the panels or a page) open the line with `/` typed | — |

- A line that starts with `//` is sent as text that starts with `/` (Q6).
- `@coder /something` is unchanged: only a **leading** `/` opens the palette.
- Ctrl+P is free today: busmon binds Ctrl A, C, E, O, S, T, U, V, W, Y and Z, and herdr's prefix is
  Ctrl+B.

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
- **Registry guard:** every class A `Act` exists in `panelBindings`, and every class C argv names a
  command in the `agentbus` `commands` table and uses only flags that entry or `globalFlags` declares (`--as`, `--for`,
  `--yes`, `--fresh`, `--project`). This is a test in the style of
  `TestEveryDispatchedCommandIsInTheTable`.
- **Model tests** on the existing input tests (`tea_input_test.go`, `keys_test.go`):
  - `/req` + Enter opens Requests exactly as F2 does, compared on the resulting model state;
  - Ctrl+P from a page opens the line with `/`;
  - Esc keeps the text;
  - class C stops at the confirmation and runs a fake `agentbus` on PATH, as the Projects page tests do.

## 6. Out of scope

- A palette inside the compose window (`I`), unless Q1 says otherwise.
- Actions on the selected row, and page-local keys.
- Commands that are agents' or master's.
- Persisted command history.
- Editing a setting's value from the line: `/set` only jumps to it.
- Any change to the bus protocol: no new message type.

## 7. Questions for the person

1. **Which input?** The person's request said "key I", but in busmon **`I` is the compose
   window** (several lines, Ctrl+S sends), while **`i` or `:` is the one-line input**.
   *Recommended:* the one-line input only, since a command is one line and `:` already means
   "command" to vim users. In the compose window, `/` stays text.
2. **Ctrl+P everywhere?** *Recommended:* yes, from the panels and every page, but not while the
   compose window, the settings line editor or a confirmation has the keyboard.
3. **First slice?** *Recommended:* class A and class B only (pages, triage, follow, write, master),
   then class C in a second step once the first has been used.
4. **Class C at all?** Running `reload`, `pause`, `resume`, `suspend`, `backup`,
   `self-repair reset` and `priority` from busmon, as the person, after `y`. *Recommended:* yes,
   each one showing its argv before `y`.
5. **Fuzzy matching:** an in-house ranking of about 40 lines (*recommended*: light, no dependency;
   `pick.go` chose "deliberately not fuzzy" for projects) or `sahilm/fuzzy` (a new module, better
   at long scattered queries we do not have).
6. **A message that starts with `/`:** `//` sends one literal `/` (*recommended*), or no escape at
   all (such a message would need the compose window).
7. **Settings:** `/set <key>` opens the settings page on that key (*recommended*), or the line
   edits the value directly (a second editor beside the settings page's).
8. **Show the key on each row?** *Recommended:* yes; the palette should teach the keys.
9. **Recent commands first** when only `/` is typed, kept in memory until busmon exits?
   *Recommended:* no for the first slice; table order is predictable.
10. **Names:** English verbs and page names (`requests`, `health`, `reload`), with the F-key and the
    letter as aliases (`f2`, `r`). Is that enough, or does the person want other aliases?

## Appendix A. Inventory

### A.1 busmon panel actions (`panelBindings`, run by `panelKey`, `cmd/busmon/tea_update.go`)

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
| `t` (help) | ActTour | the first-launch tour | — | `tour` |
| `u` | ActNextUnread | next unread for you | — | `unread` |
| `f` | ActFollow | auto-scroll on or off | — | `follow` |
| `Z` | ActSleep | suspend or resume the project (asks first) | — | `sleep` |
| `/` | ActFilter | filter Activity by @agent, thread or words | words | `filter <words>` |
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
| esc, `q` | ActEscape, ActQuit | back, quit | — | `quit` |

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
