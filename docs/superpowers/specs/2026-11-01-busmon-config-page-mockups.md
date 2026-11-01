# busmon settings page: text mock-ups

Status: mock-ups for discussion. Text only, no code. Built on the settings registry
(`config/keys.go`) and `agentbus config`, both merged; drawn in the one list pattern of
`2026-10-31-busmon-pages-mockups.md`. Master puts the questions at the end to Bernard; a spec
follows the answers.

## What it is for

`agentbus config` already answers "what value, from where". The page answers the same question
inside busmon, and lets the person change a value **without opening an editor**, for the keys
only the person sets: display, attention, the services' thresholds. Nothing else.

It changes one decision of the config-file spec, which lists "busmon writing settings" and "a
`config set` command" as out of scope (the file is edited by hand). **Question 1** asks whether that
changes now. Everything below assumes yes; with no, the page is the same list, read-only.

## What it never does

- **No secret, no connection.** The registry has no key for `REDIS_URL`, a password or a
  provider credential, so the page cannot show or write one. The connection is one read-only
  line of facts, as `agentbus config` prints it (`REDIS_URL set`, never its value).
- **Never edits env.** A value from the environment, a flag or the project's env file is shown
  and read-only: the page says which variable or flag sets it, and that only unsetting it lets the
  file value apply.
- **Writes only `config.toml`.** Never `busmon.toml` (legacy, read below `config.toml`), never the
  project's env file, `quota.toml`, `models.toml` or `roles.toml`.
- **Never writes a value its own rule refuses.** The check is the registry's file rule, the same
  function the readers use, so the page cannot write what a reader would then ignore.

## Groups

The registry's keys, by theme. The group is the key's first segment, so the page needs no
second table.

| Group | Keys | Read by |
|---|---|---|
| Display | `display.theme`, `display.limit`, `waiting.fade` | busmon |
| Attention | `waiting.popup_after`, `waiting.snooze`, `waiting.bell`, `recap.auto_after`, `updates.check` | busmon |
| Identity | `identity.as` | busmon, `agentbus identity`, ask, plugin |
| Services | `dispatch.*` (context alert, prompt grace, quota on/warn/hold/max age/long hold, tokens, auto clear) | the dispatch service |
| Health | `health.runaway.*` | the dispatch service (health) |
| Bus caps | `bus.report_max`, `bus.report_full_max`, `bus.verdict_max` | each writer, per call |
| Delivery | later (forge support, v0.26 plan) | — |

`waiting.fade` sits in Display, not Attention: it is how a waiting line looks, not when the
person is told.

## The row

One line per key, in the pattern: symbol, name, value, source, and no age (a setting has
none). The source column uses `agentbus config`'s words.

```
  <sym> <name>                <value>                <source>
```

| Symbol | Meaning |
|---|---|
| `●` | set here: `config.toml` |
| `○` | the default |
| `·` | set elsewhere, read-only here: env, a flag, the project's env file, or `busmon.toml` (legacy) |
| `▲` | refused: a `config.toml` value its rule refuses (the warning's line); the reader skips it, so the value column shows what applies instead, `busmon.toml`'s or the default |

The word in the source column always says which, so colour and symbol are never the only
signal (`NO_COLOR`).

A value refused **in the environment** is not a file matter and the page only reports it, per
key as the registry's `Invalid` says: for `health.runaway.*` the runaway check reads unknown,
and the row says so (`▲`, `env <NAME> refused: the runaway check reads unknown`); for
`display.theme` and `identity.as` busmon refuses to start, so a running page never shows that
case (`agentbus config` does). Every other key skips a refused env value to the next source, as
its rule does today, and the row shows the source that applies.

## 140x40

```
╭─ Settings ──────────────────── 4 set here · 2 elsewhere · 1 refused · 18 default ─ esc back ─╮
│  Display                                                                                     │
│ ▶ ● Theme                     dark                        config.toml                        │
│   ● Fade and slide            off                         config.toml                        │
│  Attention                                                                                   │
│   ● Bell                      on                          config.toml                        │
│   · Update check              off                         env AGENT_BUS_NO_UPDATE_CHECK      │
│  Identity                                                                                    │
│   ● Your name                 charlie                     config.toml                        │
│  Services (agent-bus-monitor)                                                                │
│   · Context alert at          75 %                        env file (frozen at up)            │
│   ▲ Quota hold at             95 %                        config.toml line 12 refused        │
│   ── 18 at their default  (a shows them) ──                                                  │
│                                                                                              │
│  Connection   as agentbus config prints it: which variables are set, never a value           │
│  Changes      busmon's own: next start of busmon · services: agentbus service restart        │
╰──────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select   enter change   d unset   a all   / filter   esc back
```

Defaults are folded like the fine checks on F5: the page leads with what someone chose, and
`a` lists every key, the defaults in their groups. The Services group reads what the open project's services see (`agentbus config
--project`): the env file comes first there, so a value frozen at `up` shows `·`, read-only.

Enter on a row opens it in place, as on F5:

```
│ ▶ ● Theme                     dark                        config.toml                        │
│   │ Key:        display.theme  (env AGENT_BUS_BUSMON_THEME, flag busmon --theme)             │
│   │ Values:     auto · dark · light                                                          │
│   │ Default:    auto                                                                         │
│   │ Read by:    busmon, at its start                                                         │
│   │ Change:     ←→ choose, enter writes config.toml, esc leaves it                           │
```

## 80x24

```
 agent-bus-monitor  ● bus  ● disp  health ▲2                              15:04
╭─ Settings ────────────────────── 4 here · 2 elsewhere · 1 refused ─╮
│ Display                                                            │
│▶● Theme              dark          config.toml                     │
│ ● Fade and slide     off           config.toml                     │
│ Attention                                                          │
│ ● Bell               on            config.toml                     │
│ · Update check       off           env                             │
│ Identity                                                           │
│ ● Your name          charlie       config.toml                     │
│ Services                                                           │
│ · Context alert at   75 %          env file                        │
│ ▲ Quota hold at      95 %          config.toml refused             │
│ ── 18 at their default  (a shows them) ──                          │
╰────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter change  d unset  a all  esc back
```

At 80 the source keeps its first word; the variable's name is in the detail (Enter).

## How each kind is changed

One rule for every kind: the edit happens **on the row**, the value is checked by the key's file
rule on every Enter, and nothing is written until Enter. Esc leaves the file untouched.

| Kind | Keys | Shown | Change |
|---|---|---|---|
| on/off | `waiting.bell`, `waiting.fade`, `updates.check`, `dispatch.quota`, `dispatch.tokens`, `dispatch.auto_clear` | `on` / `off` | Enter or space flips it and writes at once (one key, one meaning: no confirm) |
| choice | `display.theme` | the value; the detail lists the others | Enter opens the choice: `←→` move along `auto · dark · light`, Enter writes |
| number | `display.limit`, `bus.*`, `health.runaway.context_growth`, `screen_repeats` | `25` | Enter opens the line editor on the value (the same editor as the input line); Enter writes |
| percent | `dispatch.context_alert`, `quota_warn`, `quota_hold` | `85 %` | as a number; `0` where the key allows it reads `off` |
| duration | `waiting.popup_after`, `waiting.snooze`, `recap.auto_after`, `dispatch.quota_max_age`, `dispatch.quota_long_hold`, `health.runaway.silent_after`, `growth_span` | `humanAge` (`5m`, `8h`) | the editor takes the file's form (`"90s"`, `"5m"`, `"2h"`); `0` where the key allows it reads `off` |
| seconds | `dispatch.prompt_grace` | `30s` | the file holds whole seconds (0 to 3600), so the editor takes a number; `0` reads `off` |
| name | `identity.as` | `charlie` | the editor; the rule is `ValidName` |
| colour | none today | — | the theme is the only colour setting, and it is a choice of three names: the page shows no swatch, so it needs no glyph outside the set and reads the same in 16 colours and none |

`d` **unsets** a key here: it removes the key's line from `config.toml`, after one `y`. What
applies next is the next source, not always the default: for a busmon key still in the legacy
`busmon.toml`, that file's value (the page never writes `busmon.toml`). So the confirmation names
the value and source that will apply, from the resolver run without that line: `unset theme here?
busmon.toml's light will apply (y)` or `… the default auto will apply (y)`. Writing the default's
value instead would hide the legacy file from the person; naming it lets them decide.

`on/off` uses the words, not a switch glyph: `●`/`○` already say *where the value comes from*,
and one symbol must not mean two things on one row.

## Refused values

The editor stays open and the row says why, in the rule's own words (the ones `agentbus config`
prints), nothing written:

```
│ ▶ ● Quota hold at   [ 150 ]   want a number above 0, up to 100                               │
```

A value already refused in the file (a hand edit) shows `▲` with its line, and the next source's
value is what runs (`busmon.toml`'s or the default); the page offers to fix it (Enter) or unset it
(`d`), and never rewrites it unasked.

## Where and how a change is written

- **`config.toml`**, the registry's `config.Path` (`$XDG_CONFIG_HOME/agentbus/config.toml`, else
  `~/.config/agentbus/config.toml`), created `0600` in a `0700` directory if absent.
- **The smallest edit:** the key's own line replaced, or the key added under its table (the table
  appended if missing); every other line, comment and order kept byte for byte. Written to a
  temporary file and renamed, so a crash leaves the old file or the new one, never half.
- **Refused when the file does not parse** (a hand edit in progress): the page says so with the
  line, and writes nothing.
- **Read back after writing**: the row shows the new value only once `config.toml` read again
  gives it; anything else (env still wins, a race with a hand edit) is shown as it is.
- **When it applies:** busmon's own keys at busmon's next start, the services' keys at their next
  start (`agentbus service restart <kind>`), the bus caps at each writer's next call. The page
  says it on the `Changes` line and in the flash after each write; nothing is reloaded live
  (question 3).

## Glyphs and palettes

Only the closed set: `●` `○` `·` `▲` `▶` `│` `─` and the frame. The source word carries the
meaning in no-colour; in 16 colours `●` is the accent, `○` and `·` muted, `▲` attention. No
swatch, no checkbox glyph, no toggle glyph.

## Questions for Bernard

1. May busmon write `config.toml` (it reverses the config-file spec's "edited by hand"), or is
   the page read-only?
2. Which groups belong on the page at first: all of them, or Display, Attention and Identity
   only (the services' thresholds staying in the file)?
3. A change applies at the next start (as the config-file spec decided). Should busmon's own
   display keys (theme, fade, bell) apply at once instead?
4. Which key opens the page: `C`, like `R`, `P` and `U`, or an F-key (`F6`)?
