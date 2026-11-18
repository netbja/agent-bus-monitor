# A language setting: French or English for what the person reads

Status: spec, r3, for review (subject `language-setting-spec`). r3: reviewer-codex (`auto` reads
the locale with its normal precedence). r2: foureyes (the default is `en`;
the text busmon composes for the bus stays English, and the rule test covers busmon's writers). Not built. Base: origin/dev
`5ec6e134`.
Author: coder, task `language-setting-spec`, from master's directive of 2026-11-18 relaying the
person's idea 17 (`docs/memory/open-questions/2026-11-17-language-setting.md`). The person's
decisions are §7; the build is §8's steps, one PR each from origin/dev.

## 1. What exists today

- **One language, no catalogue.** busmon and agentbus speak English only, written in place:
  - busmon: about 5,000 string literals in 118 files (35,000 lines), drawn through `S(role,
    text)` segments, `Preview`, `DetailText`, `humanAge`;
  - agentbus: about 960 `Fprint` lines, and 270 `Help`/`Summary` lines in its command registry
    (`cmd/agentbus/commands.go`).
  - No `i18n`, `gettext` or `golang.org/x/text` is used.
- **A rule says English.** The UI copy was made English on purpose (the person's earlier
  request). This spec adds French as a choice; it does not change the default.
- **The tests read English.** 518 golden files and 22 overlay captures pin busmon's screens
  word for word.

## 2. What follows the setting, and what never does

**Follows the setting: what only the person reads in busmon.**
- Frame titles, column heads, key hints and the help page.
- The words busmon composes itself: For you lines, a page's row labels, notices, the Services and
  Settings pages, ages (`4m` stays `4m`; `today 17:04` becomes `aujourd'hui 17:04`).
- The state words as busmon *shows* them (`blocked` → `bloqué`). The stored states (`idle`,
  `working`, `blocked`) never change: they are protocol, which agents and scripts compare.

**Never follows it, English always:**
- **Everything on the bus:** reports, directives, asks, verdicts, status messages, notices that
  agents read. One language serves every model, and an agent never has to guess.
- **The text busmon composes for the bus**, which reaches an agent:
  - the compose window's prefills (`@master ` plus `planStartText`, `re: <first line>`);
  - its answers to asks (any words busmon adds to the agent's choice labels);
  - idea states;
  - the views redesign's triage directive (`idea <id>: yes|later|no`).

  These are written in English, never through `T`, even while busmon shows French around them.
- **What agents write:** their reports and messages are shown as written, in whatever language
  they used.
- **The CLI, by default (Q2):** agents and scripts read `agentbus` output and errors and match
  their words (the role skills tell agents what
  a command prints). A French error would break them. Only `--help` could follow
  the setting, and only when the caller is the person (no `AGENTBUS_TERM_ID`, no
  `AGENT_BUS_AGENT`).
- **Logs, the services' state details, operator notes, specs and code comments.**

## 3. Where the strings would live

**One catalogue, keyed by the English text** (recommended):
- `i18n/fr.go` holds a map from each English string to its French one, and `T("...")` wraps a
  string busmon shows.
- English needs no table: a missing French entry falls back to the English text, so a new string
  never breaks anything.
- Strings with values use a format string as the key: `T("%d agents")` → `"%d agents"`.
- A test lists every `T(...)` literal in the code and fails when one has no French entry and is
  not marked as deliberately the same (`T` of a proper name or a glyph).

**Not `golang.org/x/text/message` with its catalog tooling.** It is heavier (a generator, plural
rules), for two languages. Plurals are handled by two keys (`1 agent`, `%d agents`), as busmon
already words them.

## 4. The setting

- `display.language` in the registry (`config/keys.go`), `en | fr | auto`, **default `en`**
  (§1: the default does not change; Q3 may choose `auto`).
  - Env `AGENT_BUS_LANGUAGE` (through `config.EnvName`).
  - Shown in busmon's settings screen (Shift+C), in the Display group.
  - Read by busmon (and agentbus's `--help`, with Q2's yes).
- **`auto`:** the locale's own precedence: the first non-empty of `LC_ALL`, `LC_MESSAGES`,
  `LANG`. French when that value starts with `fr`; English otherwise, `C` and `POSIX` included.
  `LC_ALL=en_US.UTF-8` with `LANG=fr_FR.UTF-8` is English: `LC_ALL` overrides. A test covers
  each conflicting pair.
- **Fallback:** an unknown value is English, with the registry's usual settings warning.
- **Changed while busmon runs:** applied at the next settings reload, as the theme is.

## 5. Tests

- **Every existing golden stays English:** the test harness pins `display.language = en`, as
  `pinLocal` pins the time zone. Not one of the 540 files changes.
- **A small French set:** the dashboard at 80 and 140 columns, the agent page, For you and the
  settings screen, as new goldens. French runs about 20-30% longer than English, so these goldens
  are where truncation and wrapping are seen.
- **The catalogue test** (§3): every `T` literal translated or marked; no French entry left
  without its English key in the code.
- **The rule test:** nothing that writes to the bus or to an agent's terminal calls `T`. It
  scans `bus/`, `dispatch/` and the CLI's bus writers. It also covers busmon's own write paths:
  - the compose prefills;
  - every busmon call into a bus writer (`Send`, `Answer`, idea states, the busWriter
    interface's methods);
  - the strings handed to them, followed back to their literals.

  A `T` on any of these paths fails the test. A second test renders busmon in French, sends a
  prefill and an answer, and checks that the entries written are the English ones.

## 6. Cost and files

**Phased**, so the person sees French early and the long tail follows:

1. **Infrastructure:** the `i18n` package, `T`, the setting, the harness pin and the catalogue
   test. Files:
   - new `i18n/`;
   - `config/keys.go` and `config/group.go` (one key; `keys.go` is the hot file);
   - busmon's settings load;
   - the test harness.

   About half a day.
2. **busmon's frame:** titles, column heads, key hints, the help page, the state words as shown,
   ages and dates. About 300-400 strings, touching most of busmon's view files once. About two
   days.
3. **busmon's pages and composed text:** For you, the agent page, Services, Settings, notices.
   The rest of the user text in the 5,000 literals: many of those are keys, formats and
   protocol, not prose. About a week.
4. **The CLI's `--help`:** with Q2's yes only, about two days.

**Overlap with coder2.** Steps 2 and 3 touch nearly every busmon view file. That is the same
ground as coder2's views redesign (#601), so they must come after it, or between its steps
(Q4).

## 7. Questions for the person

- **Q1.** Which surfaces: busmon only, or busmon and the CLI's help? Recommended: busmon only
  first. The bus and the CLI's output stay English because agents and scripts read them.
- **Q2.** If the CLI is included, should only `--help` follow the setting, and only for the
  person's own shell? Recommended: yes, that limit or none at all. Errors and output stay
  English.
- **Q3.** Should `auto` (follow the system's `LANG`) be the default, or English unless the person
  chooses French? Recommended: English by default, French only when chosen. A machine with a
  French `LANG` would otherwise change busmon's text without anyone asking.
- **Q4.** When: before or after the views redesign? Recommended: step 1 at once (no overlap);
  steps 2-3 after the redesign lands, so the strings are translated once, on the new screens.
- **Q5.** Should the state words be translated (`bloqué`, `en attente`, `inactif`), or kept as
  the English terms the agents and the bus use? Recommended: translated in busmon's columns, with
  the English word on the agent's page. The person reads French, and the page keeps the link to
  what agents say.

## 8. Steps

One PR each, from origin/dev, base dev: §6's steps 1 to 3, and 4 with Q1/Q2's yes.
