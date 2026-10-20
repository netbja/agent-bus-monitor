# CLI help and GNU-style flags — design

Status: implemented on `feat/cli-help` (coder, 2026-10-21); revised to match the code. Task `cli-help`, branch `feat/cli-help`.

## Goal

1. `agentbus help`, `agentbus help <cmd>` and `agentbus <cmd> --help` print a usage line,
   the flags and examples for every subcommand. All three come from **one table**, so the help
   cannot drift from what `main()` dispatches. They need no project and no broker.
2. Both binaries accept conventional GNU flags: long options with `--`, short ones with `-`.
   `busmon -p demo` = `busmon --project demo`; `agentbus -p demo agents -j` =
   `agentbus --project demo agents --json`. Every existing long form keeps working unchanged.

## The table

A small package `cli/` (shared by both binaries, no dependency on `bus`):

```go
type Flag struct {
    Long  string // "project" (without dashes)
    Short rune   // 'p'; 0 = long only
    Arg   string // "" for a boolean, else the value's name: "<project>", "<dur>"
    Help  string
}
type Command struct {
    Name     string   // "subscribe"
    Aliases  []string // "watch"
    Usage    []string // one or more synopsis lines, without the "agentbus " prefix
    Summary  string   // one line for `agentbus help`
    Flags    []Flag
    Examples []string
    NoProject bool    // handled above the project-required exit (mem, roles, up, …)
    Internal  bool    // entry point for scripts/herdr/units: listed apart, no short forms
}
func Expand(globals, flags []Flag, args []string) []string // short → long, --long=v → --long v
func Lookup(cmds []Command, name string) (Command, bool)    // name or alias
func Render(w io.Writer, bin string, c Command)            // usage + flags + examples
func Index(w io.Writer, bin string, globals []Flag, cmds []Command)
```

`cmd/agentbus/commands.go` holds `var commands = []cli.Command{…}` and `var globalFlags`;
`cmd/busmon/flags.go` holds busmon's `[]cli.Flag`. The long comment atop `cmd/agentbus/main.go`
shrinks to a pointer at `agentbus help` (it is the copy that drifted).

## agentbus behaviour

Order in `main()`, all **before** the first `NoProject` block and so before the
project-required exit:

1. `cli.Expand(globalFlags, nil, args)` rewrites the global shorts (`-p`, `-H`), then
   `extractFlag` takes `--host`/`--project` as today.
2. `help`, `--help`, `-h` as `args[0]` → index (exit 0). `help <cmd>` → that command
   (unknown → error listing the names, exit 2).
3. `<cmd> --help` / `<cmd> -h` **as the first argument after the command** → that command's
   help, exit 0. Only that position: a message such as `report coder see --help` is still a
   report. (`board claim --help` is not help: `board --help` lists the board's subcommands.)
4. `cli.Expand(nil, cmd.Flags, args[1:])` for a non-`Internal` command, then the existing
   dispatch, untouched. Global shorts are **not** expanded after the command (foureyes'
   review of `2477a0a`): there `-p` is a message word (`mkdir -p`, `ssh -p`, `git log -p`), and
   since `--project` is read anywhere, expanding it would silently write to another project.

The bare `agentbus --project P` without a command prints the index instead of today's
one-line list, still exit 1.

### Expansion rules (both binaries)

- A token equal to `-x` becomes `--long` when `x` is a global short and the token precedes
  the command, or `x` is a short of this command and the token follows it. Anything else is left as it is: an unknown `-z`, `-n 50` of
  `service logs` (kept as it is today), a negative number, a bootstrap flag passed through
  by `up`.
- `--long=value` for a defined flag that takes a value becomes `--long value`.
- No bundling (`-jq`) and no attached values (`-pdemo`): both are ambiguous next to the
  free-text messages these commands take.
- **Known limit, same class as today:** a message word that is exactly a defined short
  (`report coder fix -t`) is read as that flag, as a word `--to` already is. Shorts are
  only expanded for flags the command defines, which keeps that surface small.
  `--` as end-of-options is out of scope (the parsers do not honour it today); open
  question below.

### Short assignments

Globals, reserved in every command: `-p --project`, `-H --host`, `-h --help`.
(`-h` is help, so host takes `-H`.)

Recurring meanings, used wherever the flag exists: `-j --json`, `-r --ref`, `-f --force`,
`-t --to`, `-q --quiet`, `-b --branch`, `-s --subject|--status|--since` (one per command),
`-e --escalate-to`, `-n --now|--no-wake` (one per command), `-a --as|--auto|--all` (one per command).

| command | shorts |
|---|---|
| report | `-a --auto`, `-t --to` |
| reports, agents, usage, budget, projects, quota | `-j --json` |
| agents forget | `-f --force` |
| cmd | `-r --ref`, `-n --no-wake` |
| challenge, reply | `-r --ref` |
| verdict | `-P --pr`, `-s --subject`, `-r --ref` |
| verdicts | `-P --pr`, `-s --subject`, `-j --json` |
| pilot | `-t --ttl` |
| bind | `-P --pane`, `-i --pid` |
| board | `-j --json`, `-b --branch` |
| shutdown | `-f --force` |
| refresh | `-q --quiet` |
| subscribe / watch | `-s --since`, `-d --drain`, `-l --loop` |
| dispatch | `-s --status`, `-z --horizon`, `-A --escalate-after`, `-e --escalate-to` |
| request | `-j --json`, `-r --ref`, `-t --ttl`, `-c --commit`, `-b --branch` |
| pause | `-f --for`, `-u --until`, `-R --until-reset`, `-t --to`, `-s --status` |
| ask | `-F --file`, `-r --replace`, `-w --withdraw`, `-Q --question`, `-N --note` |
| asks | `-o --open`, `-j --json` |
| answer | `-c --choice`, `-T --text`, `-a --as` |
| swap | `-P --profile`, `-n --now`, `-f --force`, `-b --back`, `-r --reason` |
| retire | `-n --now`, `-r --reason` |
| swaps | `-R --role`, `-j --json` |
| missions | `-s --status`, `-C --repo`, `-e --escalate-to` |
| mission | `-w --wait`, `-f --force`, `-R --recover`, `-r --reason` |
| mem | `-C --root`, `-S --stale-after`, `-x --strict`, `-T --title`, `-s --scope`, `-N --note-project` |
| models | `-j --json`, `-P --provider`, `-r --refresh` |
| roles doctor | `-j --json`, `-u --publish` |
| up | `-a --as`, `-T --tabs`, `-R --resume`, `-n --dry-run`, `-e --escalate-to`, `-S --session` |
| service | `-a --all` |

`up` passes unknown flags to `scripts/bootstrap`; bootstrap's only short is `-y`, which `up`
does not define, so it passes through unchanged. Its `--no-dispatch`, `--no-missions`,
`--index`, `--cron` stay long only.

**Internal, long only, no expansion:** `service run` (`--env`, `--stderr`), `plugin`,
`statusline` (`--then`, `--print-install`), and the
script-facing `roles` verbs (`launch`, `template`, `field`, …; their argv is matched by
position, e.g. `rest[2] == "--profile"`). They are listed under "internal" in `agentbus help`.

Commands with no flags (`status`, `notify`, `thread`, `gate`, `pane`, `listen`, `health`,
`resume`, `down`, `identity`, `limits`, `version`) still get usage and examples.

## busmon behaviour

busmon keeps the `flag` package (it already accepts `-project` and `--project`). Each table
entry is registered under its long name and, when it has one, its short name on the same
variable. `flag.Usage` is replaced by `cli.Render` of the table, so `busmon -h` lists each
flag once as `-p, --project <project>`. `flag.Visit` checks (`limit`, `theme`, `as`) look at
both names.

| short | long |
|---|---|
| `-p` | `--project` |
| `-H` | `--host` |
| `-n` | `--limit` |
| `-r` | `--reset` |
| `-y` | `--yes` |
| `-l` | `--list` |
| `-d` | `--delete` |
| `-t` | `--theme` |
| `-a` | `--as` |
| `-V` | `--version` |
| `-k` | `--pick` |
| `-h` | `--help` |

Back-compat: Go's `flag` keeps accepting `-project`, `--project`, `--project=x`; nothing
existing changes.

## Tests (TDD, seams)

- `cli`: `Expand` (short, `=`, unknown left alone, no bundling, internal untouched);
  `Render`/`Index` against literal expected text for one small command.
- Table integrity, both binaries: no duplicate long or short within a command plus globals;
  every `Short` is a single ASCII letter; no command shadows a global short.
- **Drift test (the requirement):** parse `cmd/agentbus/main.go` with `go/ast`, collect every
  string literal in the `switch cmd` cases and in `args[0] == "…"` tests of `main()`, and
  assert each has a table entry (name or alias) — and the reverse, so a table entry for a
  removed command fails too.
- Flag drift, agentbus: every `"--x"` literal passed to `extractFlag`/`extractBool` in
  `cmd/agentbus/*.go` (non-test) appears as a flag of some command, or in an explicit
  allowlist of strings that belong to other programs (herdr's `--workspace`, `--label`, …).
- Flag drift, busmon: every flag registered on `flag.CommandLine` is in the table.
- Behaviour: `agentbus help`, `help verdict`, `verdict --help`, `-h` run with no project and
  no Redis (exec of the built binary with an empty environment; exit 0, expected header);
  `report x see --help` is not help (parse-level check, no broker).

## Open questions for review

1. `--` as end-of-options: add it now (expansion and the long-flag extractors stop there,
   `--` is dropped)? Proposal: not in this change; it touches every parser.
2. `-n 50` of `service logs` stays the count, not `--now`; fine since shorts are per command.
3. Should `AGENT-BUS-GUIDE.md` §0 trap 2 ("never a single dash") be rewritten in this PR?
   Proposal: yes, one line — shorts exist, long forms remain the documented form for agents.
