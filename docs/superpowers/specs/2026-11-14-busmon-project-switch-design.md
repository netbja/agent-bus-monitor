# busmon: switch projects without leaving it

Status: r1, spec note only. Base: origin/main `c7e1400c`. Author: architect, for coder's question
(task `busmon-project-switch-note`), from the person's idea of 2026-11-14. Built by coder in herdr
exit step 7b, after attach (7a).

## 1. Why

Today one busmon shows one project. To look at another, the person opens a second herdr pane or
terminal. After the herdr cutover, busmon (list, preview, attach) is the person's only console,
so it must change project itself.

## 2. Decisions

### 2.1 The gesture: both, one action

- **Projects page (M):** `o` opens the selected project in this busmon. `enter` keeps its meaning
  (the detail). `o` is free on that page today (its keys: `j k pgup pgdn home end enter Z x b d
  del R esc F1 q`). The page's hint line shows `o open here`.
- **Palette:** `/project <name>`, with the broker's projects as the argument's completions, under
  the palette's Enter rule (§4.3.1 of the palette spec): a known name runs, a bad name runs
  nothing and the flash says why, the text stays.
- Both call one action, `ActSwitchProject`, with the target name.

### 2.2 How it switches: busmon re-executes itself

The switch **ends the TUI and execs busmon again** with the new project. It is not an in-process
reset.

- **Why.** The project is woven through the running process: the tail goroutine and its cursors
  (`main.go`), the `bus.Bus` the shell reads and writes through, the cache path, the history
  directory, `trustOf`, `ideasOf`, the `@` file source, and every page's state. An in-process
  reset has to find all of them, now and in every later change, and one missed field shows, or
  worse writes, the wrong project's data: a directive sent to project A while the screen shows
  B. A fresh process makes that impossible by construction, and costs one backfill read (as a
  start does).
- **Order.**
  1. Refuse first, with a flash and nothing changed, when:
     - the target is the current project ("already on <p>");
     - the target has no keys on this broker (below, 2.4);
     - a draft stands: a compose window, a line being typed, or an ask answer
       ("a draft is open: send it or clear it first"). Drafts are in memory only, and a switch
       never drops one silently.
  2. Save the current project's local cache (read marks, snoozes, `last_visit`) through the
     model's `saveCache`, as quit does (`tea_signals.go` sets `LastVisit`).
  3. Return `tea.Quit`; `main` gets the target from the model after `prog.Run` returns, with the
     terminal already restored.
  4. `syscall.Exec` of `os.Executable()` with the rebuilt argv (below) and the same environment.
  5. If the exec fails, print the error and the command to type (`busmon --project <p> …`) on
     stderr and exit 1. Nothing else to undo: the old project's cache is already saved.
- **The argv is rebuilt from the parsed options, not edited as text.** busmon's flags go through
  Go's `flag` package, which accepts many spellings (`-p x`, `-p=x`, `--p x`, `-project x`,
  `--project=x`…), so text surgery would miss one. `parseFlags` already knows which flags were set
  (its `set` map). The new argv is `--<long>=<value>` for each set flag, then
  `--project=<target>`. Left out: `project` itself, and the one-shot actions, even though they
  never reach the TUI: `reset`, `delete`, `yes`, `list`, `pick`. Kept: `host`, `as`, `theme`,
  `limit` and any later flag, by default. `--project` wins over `AGENT_BUS_PROJECT`, so the
  environment needs no change.
- **Attach.** While busmon runs an attach (`tea.ExecProcess`), it reads no keys, so there is no
  switch; after detach the person is back in the same project.

### 2.3 What is reset and what is kept

| | Switch |
|---|---|
| Poller, tail, Activity feed and its cursor | new, as at a start (backfill of `--limit`) |
| Pages, filters, selection, open popups, flash | new: the default view of the new project |
| Read marks, snoozes, last visit (local cache) | per project: the old one's saved, the new one's loaded |
| Drafts | none can exist: a draft refuses the switch (2.2) |
| Identity (`--as`, `AGENT_BUS_HUMAN`), settings, theme, tour marker | the machine's: kept |
| Broker connection (`-H`) | the same host (2.4) |

### 2.4 Other brokers: same broker only

- busmon switches **only among the projects of the broker it is connected to** (`-H`, else
  `REDIS_HOST`). That is the list M already shows, and the one `/project` completes.
- A project busmon lists from a template but with **no keys on this broker** is not opened: the
  flash says "not on this broker: `agentbus up <p>` first". busmon never creates a project by
  reading it, the same rule as the picker ("a project with no keys is never re-created from
  here").
- A project on **another broker** is reached by starting busmon with `-H <host>`, as today. No
  reconnect, no list of brokers: that would be a broker registry, beyond this note.
- The Projects page's `?` for another host's counts ("not fresh", `countsFresh`) is about counts
  on this broker, read for projects whose services run elsewhere. It is enough; a switch to such a
  project works, and its own page then reads live.

### 2.5 How busmon starts

- **The project resolves in this order:** `--project`, then `AGENT_BUS_PROJECT`, then **last
  used**, then the Projects picker (`busmon --pick`'s screen, which already lists them), where
  `enter` opens one in this busmon by the same exec.
- **Last used:** the project busmon last opened against that broker, on this machine. busmon
  writes it **at start**, once the project is open, not at quit, so a crash or a closed terminal
  still records it. One small file in busmon's cache directory, one line per broker host:
  `<host> <project>`, written by copy and rename. A last-used project that has no keys on the
  broker any more is skipped, to the picker.
- `noProjectText` is then shown only when the broker cannot be reached to check anything.
- The CLAUDE.md rule "the project is required" stays true for writes: busmon always runs with
  exactly one resolved project. The rule's list of commands that sit above the project-required
  exit gains nothing; the exit moves to after the last-used and picker steps.

## 3. Tests

- **argv:** for each spelling Go's `flag` accepts (`-p x`, `-p=x`, `--project x`,
  `--project=x`, `-project x`), the rebuilt argv holds one `--project=<target>`; `-H`, `--as`,
  `--theme`, `--limit` come back as `--<long>=<value>`; `reset`, `delete`, `yes`, `list`, `pick`
  are never carried, even if set; re-parsing the rebuilt argv gives the same options but the
  project.
- **Exec seam:** `main` calls an `execFn(path, argv, env)` that a test replaces: the switch calls
  it once, after the program quit and after the old project's cache file was written (its `read`
  list and `last_visit` on disk).
- **Refusals** (no exec, no cache write, flash text): the same project; a project with no keys;
  an open compose window; a typed line; an open ask answer.
- **Both gestures** produce the same action and target: M with `o` on a row, `/project <name>`;
  `/project` with a bad name runs nothing and keeps the text (palette Enter rule case 2); its
  completions are the broker's projects.
- **Exec failure:** the error and the command to type on stderr, exit 1.
- **Start order:** `--project` beats the environment, which beats last used, which beats the
  picker; a last used with no keys goes to the picker; last used is per broker host; the file is
  written at start and survives a kill of busmon.
- **No cross-project write:** after a switch to B, a directive sent from the new process goes to
  `{B}:cmd` (a disposable Redis, two projects).

## 4. Out of scope

- Several projects in one busmon screen, or a split view: one project per busmon, as today.
- A broker registry or switching brokers inside busmon.
- Keeping drafts across a switch.
