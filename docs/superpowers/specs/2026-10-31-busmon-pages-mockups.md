# busmon pages: one list pattern, text mock-ups

Status: mock-ups for discussion. Plan v0.26. Text only, no code; master puts the questions to
Bernard and a spec per page follows the answers.

## Why

Each page grew its own shape. F5 lists every check as a four-line block, unknown and failing
alike, so the header's `! 7` says something is wrong without saying whether the person can do
anything. Requests repeats a long sentence on every group. The U page spells out the same reason
on nine rows. Bernard asked for **one standard list pattern** across the pages, so a page is read
the same way wherever it is.

## The pattern

```
<title> ──────────────────────────────────── <counter summary> ─
  <sym> <name>            <short state>                    <age>
  <sym> <name>            <short state>                    <age>
  ── <folded group> · N  (key shows them) ──
```

- **One line per item.** Status symbol (coloured by its role) + name + short state + humanised age
  (`40s`, `12m`, `2h`, `10d`). Raw ids, ISO timestamps, full paths and remediation text live **only**
  in the expanded detail (Enter, as Activity and For you already do).
- **A counter summary in the title**, the same words as the symbols: `2 to do · 5 ok · 1 watched`.
- **Order:** what the person must do first, then what is in hand, then what is fine. Done or old
  items are folded into one last line (`o` shows them), as Requests already does past 24 h.
- **Filters on single keys**, the same letters on every page where they apply: `o` folded items
  shown, `a` all (also the quiet ones), `/` text filter. Page-specific keys are listed in its footer.
- **Symbols from the closed set only** (`theme.go`): `▲` act or failing, `◆` waits on someone,
  `●` live or unread, `○` quiet or read, `✓` fine or done, `·` nothing to say. `?` (not measured,
  as today's `?N`) is a plain ASCII marker, not a glyph of the set, as it already is in the header. Colour is never the only signal (`NO_COLOR` keeps the symbol and the word).
- **Ages:** `humanAge` as today; past 7 days a day count (`10d`); never a timestamp on a row.

Sizes: each page is shown at **80x24** (the narrow tier: one view at a time, the page fills the
lower zone) and **140x40** (the wide tier: the page over the lower zone). The 140-column boxes
are drawn about 105 columns wide here so they read in a review; on screen the page spans the
width and the age column sits at the right edge. Light and dark are not shown: the roles carry the
colour.

---

## 1. Health: F5 and the header

### What the person should understand

A check is in one of four places, and the page says which:

| Place | Meaning | Examples |
|---|---|---|
| **For you** | Only the person can fix it; the row says the exact command | a tracked secret, the old Redis password, Redis behind in its major, an install behind, a service out of its restart budget |
| **In hand** | Something is already dealing with it; nothing to do but watch | a service failure sentinel restarts, an agent past its context threshold handing off, a client update sentinel proposes, runaway work master looks at |
| **Fine** | measured ok | everything that is ok |
| **Not measured** | no current measurement (`?`: the dispatcher is down, the check has not run yet, or its evidence is stale); never counted as fine, by the standing rule | a check after a restart, every check while the dispatcher is down |

Which place a check goes to is a fixed property of the check and its status (a table in the
spec, next to `Remediation`), never guessed from text.

### Header

Today: `health !7`, red when one of them fails and in the attention colour when they only warn,
whatever the seven are. Proposed:

```
  health ▲ 2 to do (F5)        two checks only the person can fix (attention colour; red if one fails)
  health ● 3 in hand           nothing for the person; muted
  health ✓                     every check measured ok, none unknown
  health ?3                    no problem, but 3 checks not measured (muted, as today's ?N; never ✓)
```

`to do` wins over `in hand`, and both over `?N`; the compact header keeps the short form (`▲2`,
`●3`, `?3`). Bernard's own
words were `! 7 a voir (F5)`. The UI is in English by standing rule, so the proposal is `2 to do
(F5)`: **question 1**.

### F5 at 140x40

```
╭─ Health ────────────────────────────────── 2 to do · 3 in hand · 11 fine · 1 not measured ─ esc back ─╮
│  For you                                                                                               │
│  ▲ Tracked secret-like files   a private-key path is tracked            git rm --cached <path>    3h   │
│  ▲ Redis version               7.4.1, 7.4.2 is out in its major          docker compose pull redis 2d   │
│  In hand                                                                                               │
│  ● Context fill                coder at 85 %, told to hand off           (dispatch)               12m   │
│  ● Client updates              claude 2.1.4 installed, 2 agents on 2.1.3 (sentinel proposes)      40m   │
│  ● Runaway work                docs silent 35m while working             (master looks)            5m   │
│  Not measured                                                                                          │
│  ? Model drift                 no current measurement                                             2h   │
│  ── fine · 11  (a shows them) ──                                                                       │
│                                                                                                        │
│                                                                                                        │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ j k select   enter detail   a all   y copy command   esc back
```

Enter on a row expands it in place (today's four lines: since, detail, remediation, the note):

```
│  ▲ Redis version               7.4.1, 7.4.2 is out in its major          docker compose pull redis 2d   │
│  │ since       2026-10-29 08:00                                                                        │
│  │ detail      broker 7.4.1 (INFO), latest of major 7: 7.4.2, checked 08:00                            │
│  │ what to do  docker compose pull redis, then docker compose up -d redis, from the checkout           │
│  │ never       sentinel never restarts or upgrades the broker                                          │
│  │ ↵ full text                                                                                         │
```

`y` on a For-you row copies its command, never the detail.

### F5 at 80x24

```
 agent-bus-monitor  ● bus  ● disp  health ▲2                              15:04
╭─ Health ──────────────── 2 to do · 3 in hand · 11 fine · 1 ? ─╮
│ For you                                                            │
│ ▲ Tracked secrets   private-key path tracked                  3h   │
│     git rm --cached <path>                                         │
│ ▲ Redis version     7.4.1, 7.4.2 out                          2d   │
│     docker compose pull redis                                      │
│ In hand                                                            │
│ ● Context fill      coder 85 %, handing off                  12m   │
│ ● Client updates    2 agents on 2.1.3                        40m   │
│ ● Runaway work      docs silent 35m                           5m   │
│ ? Model drift       not measured                              2h   │
│ ── fine · 11  (a shows them) ──                                    │
╰────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter detail  a all  y copy  esc back
```

At 80 the command goes on its own line under a For-you row (it is the one thing to read whole).

---

## 2. Requests

Today every group repeats its section's sentence (`no reply on their thread`, `untracked: the
protocol records no acceptance…`). Proposed: the sentence moves to the detail, a row is one short
line, pending first, done older than 7 days folded.

### 140x40

```
╭─ 1 Activity  2 Board  3 Requests  4 Plan ─────────────── 1 owed by you · 3 waiting · 9 gated ─╮
│  Owed by you                                                                                    │
│  ◆ master → you        ask        review lot 1? (2 questions)                             4m    │
│  You wait on                                                                                    │
│  ○ you → master        directive  message 21 (+20 older)                                  5m    │
│  Tracked                                                                                        │
│  ◆ master → coder      request    busmon-group-a · accepted                               1h    │
│  Reviews gated                                                                                  │
│  ◆ coder → foureyes    challenge  config-cmd · config-services (+7)                       1h    │
│  Between agents                                                                                 │
│  ● coder → ui-kimi     directive  coder to ui-kimi · unread                              12h    │
│  ○ sentinel → master   directive  6 in thread · read                                     48m    │
│  ── notices · 22, nobody owes an answer  (enter lists them) ──                                  │
│  ── done or answered · 77, older than 7 d folded  (o shows them) ──                             │
╰─────────────────────────────────────────────────────────────────────────────────────────────────╯
```

- The counter summary replaces the per-section counts (`owed by you · 0` disappears when 0).
- Names: `you` for the person on rows (the header already names them).
- Read/unread is the symbol (`●` unread by its target, `○` read), not a word, except where it is
  the state itself.
- The detail (Enter) keeps today's thread view and gets the moved sentence as its first line.

### 80x24

```
╭─ Requests ─────────────── 1 owed · 3 waiting · 9 gated ─╮
│ Owed by you                                              │
│ ◆ master→you      ask   review lot 1?               4m   │
│ You wait on                                              │
│ ○ you→master      dir   message 21 (+20)            5m   │
│ Tracked                                                  │
│ ◆ master→coder    req   busmon-group-a             1h    │
│ Reviews gated                                            │
│ ◆ coder→foureyes  chal  config-cmd (+8)             1h   │
│ Between agents                                           │
│ ● coder→ui-kimi   dir   unread                     12h   │
│ ── notices 22 · done 77  (o) ──                          │
╰──────────────────────────────────────────────────────────╯
```

---

## 3. Tokens: the U page

Three zones (accounts, projects, agents), bars relative to the largest row of their zone, the
top consumers bold, short grey reasons. **ntcharts is not needed**: the sparkline and the bars are
already our glyphs (`▁`–`▆`, `▇`, `█` and the eighths).

### 140x40

```
╭─ Tokens ───────────────────────────── today · anthropic 62 % 5h · xai held 40m ─ esc back ─╮
│  Accounts                                                                                    │
│  anthropic  5h  ██████▎    62 %  ▁▁▂▃▄▅▆▇  resets 2h                                         │
│  anthropic  7d  ████▏      41 %            resets 3d                                          │
│  xai        5h  ██████████ 100 %           held 40m                                           │
│  Projects                       in      out    7 days                                         │
│  agent-bus-monitor          **~1.5M**  ~289k  ▃ █▆     ███████████████████  ~5.1M             │
│  blog                           0        0  ▁         ▌                     170k              │
│  wiki                           ·        ·  no dispatch service                               │
│  Agents, agent-bus-monitor      in      out                                                    │
│  foureyes     anthropic     **1.2M**   206k  ████████████████████                             │
│  coder        anthropic       345k      83k  █████▊                                           │
│  8 more       ?             no proved session  (a lists them)                                  │
│                                                                                               │
│  in = input + cache writes · out = output · ~ still counting · no cost, by rule              │
╰───────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ scroll   s sort   f provider   d period   a all   esc back
```

(`**…**` marks bold in this text; the page uses `Bold`, never stars.)

- **Unproved sessions** fold into one grey line, `8 more · no proved session`, listed with `a`;
  each listed row says `no proved session` in grey, never the long sentence.
- **Optional keys:** `s` sort (in, out, name), `f` cycles a provider filter, `d` the period (today,
  7 days). Each optional, **question 3**.

### 80x24

```
╭─ Tokens ──────────────── today ─╮
│ Accounts                         │
│ anthropic 5h ██████▎  62 % 2h    │
│ anthropic 7d ████▏    41 % 3d    │
│ xai       5h █████████ held 40m  │
│ Projects          in     7 days  │
│ agent-bus-mo… **~1.5M** ███ 5.1M │
│ blog                 0 ▌   170k  │
│ wiki        no dispatch service  │
│ Agents              in      out  │
│ foureyes  **1.2M**  206k ███████ │
│ coder       345k     83k ██      │
│ 8 more · no proved session  (a)  │
╰──────────────────────────────────╯
```

---

## 4. Recap (R)

Groups by state, one short line per task, the boundary notes folded into one line.

### 140x40

```
╭─ Since your last visit (5h ago) ────────── 3 for you · 2 done · 1 failed · 4 in progress ─ esc back ─╮
│  For you                                                                                             │
│  ◆ master asks   review lot 1? (2 questions)                                                   4m   │
│  ◆ coder waits   review this                                                                  30m   │
│  Done                                                                                                │
│  ✓ config-file   change integrated · master                                                    2h   │
│  ✓ pr:7          approved by foureyes                                                          3h   │
│  Failed                                                                                              │
│  ▲ dispatch      service exited, restarted by sentinel                                         4h   │
│  In progress                                                                                         │
│  ● coder         busmon-pages-mockups                                                          5m   │
│  ● foureyes      reviewing config-cmd                                                         12m   │
│  ── history limits · 6 notes  (enter lists them) ──                                                 │
│  ── ask master for a summary  (s) ──                                                                │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

### 80x24

```
╭─ Recap · 5h ─── 3 you · 2 done · 1 failed ─╮
│ For you                                     │
│ ◆ master asks  review lot 1?           4m   │
│ ◆ coder waits  review this            30m   │
│ Done                                        │
│ ✓ config-file  integrated              2h   │
│ ✓ pr:7         approved                3h   │
│ Failed                                      │
│ ▲ dispatch     exited, restarted       4h   │
│ In progress                                 │
│ ● coder        busmon-pages-mockups    5m   │
│ ── history limits 6 · summary (s) ──        │
╰─────────────────────────────────────────────╯
```

---

## 5. Activity: a For you section

When the For you panel is not on screen (the narrow tier, or a page over it), Activity's top keeps
what is for the person, pinned above the feed, with the same rows as For you:

```
╭─ 1 Activity  2 Board  3 Requests  4 Plan ──────── For you 2 · 7 events · auto-scroll on ─╮
│  For you                                                                                   │
│  ● ask    master    review lot 1? (2 questions)                                       4m   │
│  ◆ gate   coder     by charlie  look at b1                                                 │
│  ──────────────────────────────────────────────────────────────────────────────────────── │
│    14:24  architect            status     done · spec merged                               │
│    14:50  ♙ Charlie → coder    directive  review lot 1                                     │
│    15:02  coder                status     working · busmon-charm-b: b1 shell               │
╰────────────────────────────────────────────────────────────────────────────────────────────╯
```

At most 3 pinned rows (`+N more · F1? R`); none when For you is already on screen in the wide and
medium tiers, so nothing shows twice. **Question 5:** pinned always, or only in the narrow tier?

---

## 6. A new health check: GitHub reachable

`github` (Interval 15m, Deadline 10 s), run by the dispatch service only when the project's Git
remote is on github.com. It answers one question: **can the project's services reach GitHub, by
each transport, from where they run?** It says only what it measured: reachable by HTTPS, by SSH,
by neither, or not judged. It stores and prints no credential; what each probe does with one is
stated row by row below.

| Probe | How | reached when |
|---|---|---|
| SSH | `ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com`: it connects with the service's own SSH identities (its keys or agent socket); BatchMode never prompts for a passphrase | GitHub answers: "successfully authenticated" (exit 1), or "Permission denied" (exit 255 after the server's reply), the latter with its authentication unknown |
| HTTPS | `git -c credential.helper= ls-remote https://github.com/<owner>/<repo>` with `GIT_TERMINAL_PROMPT=0`: a **reachability** probe, no helper and no prompt, so no credential at all | GitHub answers within the deadline: exit 0 whatever refs come back (an empty repository lists none and is still reachable; no `--exit-code`), or an authentication refusal from GitHub (a private repository), with its authentication unknown |

Each probe has one **network result**: **reached** (it connected and GitHub answered, whatever the
answer) or **failed** (no DNS, no route, timeout, refused, reset). An answer that is not a success
still means GitHub was reached: an SSH "permission denied" or an HTTPS "authentication required"
is **reached**, its authentication **unknown**, noted apart in the detail, never in the status.

| SSH | HTTPS | Status |
|---|---|---|
| reached | reached | **ok** |
| reached | failed | **warn** (`reachable by SSH only`) |
| failed | reached | **warn** (`reachable by HTTPS only`) |
| failed | failed | **unknown** (`reachable by neither`: offline or blocked), a **warn** once it has lasted an hour |
| no GitHub remote | | **unknown**, not run |

A probe that cannot run at all (no `ssh` or `git` on the service's `PATH`, or the exec fails) is
neither reached nor failed: it reads **unknown** for that probe, and the row is read with the other
probe alone (the other reached: **ok** is not claimed, the status is **unknown** with the reason;
the other failed: **unknown**).

There is **no fail** status: neither probe can prove that GitHub itself refuses this host. The
check never opens a fail episode, so sentinel is never woken by it.

Three limits, each said in the check's detail:

- **Authentication and reachability are apart.** HTTPS judges reachability only:
  `-c credential.helper=` empties the helpers (store, gh, libsecret), so it never asks for a stored
  token; `GIT_TERMINAL_PROMPT=0` alone would only stop the terminal prompt. A private repository
  answers "authentication required": reached, authentication unknown. SSH connects with whatever
  identity the service already has, and nothing else.
- **The service's context, not the person's shell.** Under `systemd --user` or launchd there is
  usually no `SSH_AUTH_SOCK`, and the service's keys may not be the person's. An SSH "permission
  denied" is reached, authentication unknown: it proves the network, not the key, and it is never
  a failure.
- **A network outage is not the person's problem to fix here.** Both transports failing reads
  unknown whatever DNS says (a resolver cache can answer for github.com on an offline laptop),
  and a warn only after an hour.

Place (section 1): **For you** when warn, with one remediation line for every case:
`Check your git remote and credentials; try: git ls-remote <your remote URL>`. The check makes no
claim about pushing.

```
│  ▲ GitHub reachable   reachable by HTTPS only            check your git remote and credentials   15m │
│  ? GitHub reachable   reachable by neither: offline or blocked                                   2m  │
```

---

## Small separate item: PageUp / PageDown in the compose window

In the compose window (`I`), PageUp and PageDown move the cursor a window height up or down,
as the reader's pages already scroll. Today `composeBindings` has no page keys, so a long draft can
only be walked line by line. No other key changes.

---

## Questions for Bernard

1. The header in English, `health ▲ 2 to do (F5)`, by the standing English-UI rule, or the
   French `a voir`?
2. Health: is the split (For you, In hand, Fine, Not measured) right, and should `In hand` count
   in the header at all, or only show on F5?
3. U page: which of `s` sort, `f` provider, `d` period are wanted?
4. Requests: fold done threads after 7 days (proposed) or keep today's 24 h?
5. Activity's pinned For you section: always, or only when the For you panel is not on screen?
6. GitHub check: warn when only one transport reaches GitHub, and when both have failed for an
   hour (proposed), with no fail status at all? Is an hour right?
