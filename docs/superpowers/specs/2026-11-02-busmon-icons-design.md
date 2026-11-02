# busmon icons — design (spec and text mock-ups, no code)

Status: proposed, 2026-11-02. Branch `spec/busmon-icons`. Request: Bernard, "donner un peu de
couleur". Constraints from master's directive: no new Go dependency (a few constants, not
gonerdfonts); one setting `display.icons = off | nerd | agent-font`, default `off`, hot like the
other display keys and on the C page; icons join `theme.go`'s closed glyph set, their cell width
decided and tested (one cell plus a space); a text fallback always.

## 1. What busmon can and cannot know

busmon cannot see which font the terminal draws with. A font installed on the machine (`fc-list`)
proves nothing: the terminal may use another font, run on another machine over SSH, or map a
range to a font by hand. So nothing is detected and the default is `off`: today's screen, byte
for byte. The person turns icons on, and the C page shows a sample of each mode drawn in that
mode, so the person sees whether their terminal draws it (§6). Their eyes are the only test that
works.

Facts read on this host on 2026-11-02:

| Fact | Evidence |
|---|---|
| `Herdr Agent Icons Max` is installed and covers exactly `U+E1A0–E1BA` and `U+E1C0–E1C5` | `fc-query --format='%{family} %{charset}' ~/.local/share/fonts/HerdrAgentIconsMax-*.ttf` |
| Ghostty's config no longer has a `font-codepoint-map` line for it (the shared note from 2026-10-20 says it had one) | `~/.config/ghostty/config`, all non-comment lines read. Fontconfig fallback may still find the font; not checked live. |
| One Nerd Font file is installed (`FiraCode Nerd Font`, Bold), covering `U+F0001–F1AF0` (Material Design) among others | `fc-query … %{charset}` |
| Nerd Fonts 3.4.0 has **no** maker logo for Claude/Anthropic, OpenAI/Codex, Kimi/Moonshot or Grok/xAI | `glyphnames.json` of the v3.4.0 tag: no key matches `claude|anthropic|openai|kimi|moonshot|grok|xai` (`gemini` only as `md-zodiac_gemini`) |
| The agent font's range overlaps no Nerd Font range (Nerd Fonts use `E000–E00A`, `E0A0–…`, `E200–…`) | both charsets above |

The `Herdr Agent Icons Max` codepoints come from herdr-radar's `lib/logos.js` (personal note
`agent-icon-font`, 2026-10-20); the font itself does not name its glyphs.

## 2. The setting

| Field | Value |
|---|---|
| Key | `display.icons` |
| Values | `off` (default), `nerd`, `agent-font` |
| Env | `AGENT_BUS_BUSMON_ICONS`, so the person can try a mode for one run without writing the file |
| Unknown value | a warning naming file, line and key (never the value, as for every key), then `off`: a wrong value never breaks the screen |
| Legacy (`busmon.toml`) | no: a new key |
| Group | Display (`config/group.go`), with `display.theme` |
| Read by | busmon |
| Applies | at once (`applySettings`, like the theme), no restart |

`off` draws nothing new: every frame equals today's goldens.

## 3. The glyphs

### 3.1 By client, then by provider

An agent's icon comes from its snapshot, keyed differently in each mode, from fixed table keys;
an icon is never read from bus text:

- **`agent-font`**: the `client` first (the logo is the tool's: Codex, Claude Code), else the
  `provider`, else no icon.
- **`nerd`**: the `provider` only. The lettered box names the provider, so the client is not
  read.

The two can differ for a client served by a provider other than its usual one. For example, a
`codex` client on provider `local` draws `U+E1A1` (codex) under `agent-font` and `U+F0B13`
(the `L` box) under `nerd`. `TestIconTables` pins this row (§5).

| Client (`rolecfg.Clients`) | Provider | `agent-font` | `nerd` |
|---|---|---|---|
| `claude-code` | `anthropic` | `U+E1A0` claude | `U+F0B08` md-alpha_a_box |
| `codex` | `openai` | `U+E1A1` codex | `U+F0B16` md-alpha_o_box |
| `kimi-cli` | `moonshot` | `U+E1A6` kimi | `U+F0B12` md-alpha_k_box |
| `grok` | `xai` | `U+E1B1` grok | `U+F0B1F` md-alpha_x_box |
| `agy` (no `rolecfg.Clients` entry today) | `google` | `U+E1B2` agy; by provider alone `U+E1AE` gemini | `U+F0B0E` md-alpha_g_box |
| — | `deepseek` | `U+E1AD` deepseek | `U+F0B0B` md-alpha_d_box |
| — | `xiaomi` | none | `U+F0B14` md-alpha_m_box |
| — | `local` | none | `U+F0B13` md-alpha_l_box |
| anything else | anything else | none | none |

Nerd Fonts has no maker logos (§1), so `nerd` marks the *provider* with a lettered box in the
provider's own colour (`providerRole`). That still says something in the 16-colour and no-colour
palettes, where the colour alone does not. `agent-font` draws the maker's logo.

"None" leaves a blank in the icon cell, so names stay in one column (§4). The person (`♙`) never
gets an icon.

### 3.2 Lifecycle marks: not used

The agent font's `U+E1C0–E1C3` (done, blocked, idle, unknown) would replace the state glyphs
`● ○ ▲ ✓ ♙`. They are not used:

- the font has no mark for `working` or `waiting` (what `E1C4`/`E1C5` draw is not documented),
- the state glyphs carry the attention colours and a closed meaning that the tour, the help and
  the goldens rely on,
- in `nerd` mode no equivalent set exists.

The state column stays as it is in every mode.

### 3.3 Where they live in the code

They live in `theme.go` only, in a second slice beside `UIGlyphs`:

```
// IconGlyphs: drawn only when display.icons asks for them, from fixed tables
// keyed by client and provider; never read from bus text.
var IconGlyphs = []string{iconClaude, iconCodex, …, nfAlphaA, nfAlphaO, …}
```

`TestNoStrayNonASCIILiterals` allows `UIGlyphs ∪ IconGlyphs`. It skips `theme.go` today, so no
other file can write a literal icon either way. The new tests are in §5.

## 4. Cell width: one cell, then a space busmon writes

Measured with busmon's own three methods (`uniseg.StringWidth`, `ansi.StringWidth`,
`ansi.StringWidthWc`, the ones `widthsAgree` compares) on 2026-11-02:

| Codepoints | uniseg | grapheme | wcwidth |
|---|---|---|---|
| `E1A0 E1A1 E1A6 E1B1 E1B2 E1C0 E1C3` | 1 | 1 | 1 |
| `F0E7 E7A8 F120 F0510 F06A9 EE0D F4A5` (Nerd, BMP and plane 15) | 1 | 1 | 1 |

All three agree on one cell: Private Use is East Asian Ambiguous and none of the methods widens
it. A font may still *draw* an icon wider than its cell: a non-"Mono" Nerd Font does, and the
agent font's advance is not documented. So:

1. **An icon is always followed by one blank cell that busmon writes**: the pair is one unit,
   measured 2. A wide drawing spills into that blank, never into a letter, a border or the
   scrollbar.
2. **The pair is all or nothing.** An icon is never cut, never the last cell before a frame's
   border, and never alone without its blank. How room is found depends on the place:
   - **Icon columns** are the Agents rows, the U page's Accounts rows and the U page's Agents
     zone. They exist at 80 cells and wider (the medium and wide tiers). There, the column is
     kept and the flexible column pays: the message on Agents rows, the trailing room on the
     U page. Below 80 cells (the narrow tier) these places have no icon column, and their rows
     are as today.
   - **Inline places** are the header gauges. There, `Fit` drops each pair before any text when
     room is short. In practice the pairs show only in the wide tier (§7.2).
3. **A row with no icon gets two blank cells** in an icon column, so names stay aligned.
4. **Colour:** the icon takes the provider's role (`providerRole(p, fallback)`). In 16 colours
   it takes the provider's ANSI name; with no colour it is plain.

## 5. Tests (what the implementation must add)

- `TestIconsAreSingleCell`: each `IconGlyphs` entry is one rune in a Private Use range
  (`E000–F8FF` or `F0000–FFFFD`), `Width == 1`, `widthsAgree`, and unique.
  `TestGlyphsAreSingleCell` keeps its exact count for `UIGlyphs`.
- `TestIconTables`: the §3.1 tables pinned row by row, with each mode's key, including the
  mismatched row (`codex` on `local`: `agent-font` codex, `nerd` the `L` box). Also: no icon
  column below 80 cells. Every provider in `providerHues` has a
  `nerd` icon. `TestEveryQuotaProviderHasItsColour`'s walk over `quota.Builtins` is extended:
  every built-in provider has a `nerd` icon, or is listed as knowingly without one.
- `TestIconsOffIsToday`: with `display.icons = off`, every existing golden is unchanged. These are
  the goldens as they are, with no `-update`.
- New goldens `dashboard-{80x24,120x30}-dark-{nerd,agent-font}` and
  `tokens-{80x44,120x44}-{nerd,agent-font}`.
- `TestShellAlignmentSuite` and `TestTokensPageAlignment` run in all three modes. Every frame's
  corners and borders are checked, and the icon pair is never split or touching a border.
- The setting: `config` registry row (env rule, file rule, group, ReadBy), `config --json` count,
  C page row, and a hot apply: the same busmon model in mode `nerd`, then `off`, draws the off
  golden.
- Any cache of a surface that shows icons adds the mode to its key. The Activity cache is not
  touched in v1, since Activity gets no icons.

## 6. The C page row

The row follows the C page pattern. `←`/`→` choose the value, as for the theme, and the row's
help line draws one sample per mode **in that mode**:

```
  ○ Icons                      off                    default
    off · nerd A O K X G · agent-font A O K X G     (what your terminal draws is what you get)
```

In the real row, the letters after `nerd` and `agent-font` are the §3.1 glyphs themselves, each
followed by its blank cell. A terminal that shows boxes, tofu or overlaps there is telling the
person to keep `off`.

## 7. Where icons show, before and after

**Reading the mock-ups:** they are built from busmon's real frames: the goldens and the dashboard
fixture drawn at 120×30. In the "after" frames, one icon is shown as one ASCII letter in its cell:
`A` = Claude Code / anthropic, `O` = Codex / openai, `K` = Kimi / moonshot, `X` = Grok / xai. Every
line is exactly 80 or 120 cells, checked by script. Both modes have the same layout; only the
glyph differs.

### 7.1 Agents rows (v1): between the state glyph and the name

80 cells, before:

```
╭─ Agents ─ 10 agents · 1 working · 2 blocked · 1 dormant (a shows) · 1-3 of …─╮
│▶ ♙ foureyes  waiting  6m   —   escalated   need a decision on the width rule ▲
│  ♙ master    waiting  12m  —   pilot       see my ask                        ┃
│  ♙ sentinel  waiting  30m  —   prompt      health ok                         ▼
╰──────────────────────────────────────────────────────────────────────────────╯
```

80 cells, after:

```
╭─ Agents ─ 10 agents · 1 working · 2 blocked · 1 dormant (a shows) · 1-3 of …─╮
│▶ ♙ A foureyes  waiting  6m   —   escalated   need a decision on the width ru…▲
│  ♙ A master    waiting  12m  —   pilot       see my ask                      ┃
│  ♙ A sentinel  waiting  30m  —   prompt      health ok                       ▼
╰──────────────────────────────────────────────────────────────────────────────╯
```

120 cells (wide tier, Agents beside For you), before:

```
╭─ For you ───────────── 4 for you · 2 unread ─╮╭─ Agents ─ 10 agents · 1 working · 2 blocked · 1 dormant (a shows) ·…─╮
│  ● ask    master    review lot 1? (2 ques… 4m││    agent     state     updated queue ctx       flags       message   ▲
│  ♙ wait   foureyes  need a decision on th… 6m││▶ ♙ foureyes  waiting   6m      —     —         escalated   need a de…┃
│  ♙ wait   sentinel  pane prompt            1m││  ♙ master    waiting   12m     —     41k 21%   pilot · ar… see my ask┃
│  ◆ gate   coder     by charlie  look at b1   ││  ♙ sentinel  waiting   30m     —     —         prompt · m… health ok │
│  ● unread dispatch  project paused until … 8m││  ✓ architect done      40m     —     —         escalating… spec merg…│
│                                              ││  ○ coder     paused    2m      —     88k 85%   gate 1 · d… busmon-ch…▼
╰──────────────────────────────────────────────╯╰──────────────────────────────────────────────────────────────────────╯
```

120 cells, after:

```
╭─ For you ───────────── 4 for you · 2 unread ─╮╭─ Agents ─ 10 agents · 1 working · 2 blocked · 1 dormant (a shows) ·…─╮
│  ● ask    master    review lot 1? (2 ques… 4m││      agent     state     updated queue ctx       flags       message ▲
│  ♙ wait   foureyes  need a decision on th… 6m││▶ ♙ A foureyes  waiting   6m      —     —         escalated   need a …┃
│  ♙ wait   sentinel  pane prompt            1m││  ♙ A master    waiting   12m     —     41k 21%   pilot · ar… see my …┃
│  ◆ gate   coder     by charlie  look at b1   ││  ♙ A sentinel  waiting   30m     —     —         prompt · m… health …│
│  ● unread dispatch  project paused until … 8m││  ✓ A architect done      40m     —     —         escalating… spec me…│
│                                              ││  ○ A coder     paused    2m      —     88k 85%   gate 1 · d… busmon-…▼
╰──────────────────────────────────────────────╯╰──────────────────────────────────────────────────────────────────────╯
```

**The cost:** two cells, taken from the message column, the only flexible one. At 120 the
message drops from 10 cells to 8. That is the trade the person accepts by turning icons on; with
`off`, nothing changes. A mixed team reads like this (fixture agents with other clients; 80
cells):

```
│  ▲ O docs      blocked  50m  —   armed       waiting for the PR              ┃
│  ○ X ui-grok   quota    3m   —   swapping    reviewed                        │
│  ● K release   working  7m   —   fallback k… tagging                         ▼
```

### 7.2 Header gauges (v1, wide tier only): before the provider label

120 cells, before:

```
 paused · resumes in 1h52 (lunch)   claude 5h ███▏  62% │ 7d ████▏      41%   grok 5h █████ 100% held 15:44             
```

120 cells, after:

```
 paused · resumes in 1h52 (lunch)   A claude 5h ███▏  62% │ 7d ████▏      41%   X grok 5h █████ 100% held 15:44         
```

At 80 cells the header is unchanged: its second line is already full
(`claude 5h 62% │ 7d 41%`), and the icon pairs are the first thing to go for room, before any
text. The word stays in every tier, so the icon never replaces the text.

### 7.3 U page (v1): Accounts rows and the Agents zone's provider column

80 cells, before:

```
│ Accounts                                                                     │
│   anthropic  5h  ██████▎     62%   7d  ████▏       41%                       │
│   xai        5h  ██████████ 100%  held 40m                                   │
…
│ Agents, agent-bus-monitor           in      out                              │
│   foureyes         anthropic      1.2M     206k  ████████                    │
│   coder            anthropic      345k      83k  ██▍                         │
```

80 cells, after:

```
│ Accounts                                                                     │
│   A anthropic  5h  ██████▎     62%   7d  ████▏       41%                     │
│   X xai        5h  ██████████ 100%  held 40m                                 │
…
│ Agents, agent-bus-monitor             in      out                            │
│   foureyes         A anthropic      1.2M     206k  ████████                  │
│   coder            A anthropic      345k      83k  ██▍                       │
```

120 cells, before:

```
│ Accounts                                                                                                             │
│   anthropic  5h  ██████▎     62%  resets 2h00     7d  ████▏       41%  resets 72h00                                  │
│   xai        5h  ██████████ 100%  held 40m                                                                           │
…
│ Agents, agent-bus-monitor           in      out                                                                      │
│   foureyes         anthropic      1.2M     206k  ████████████████████                                                │
│   coder            anthropic      345k      83k  ██████▏                                                             │
```

120 cells, after:

```
│ Accounts                                                                                                             │
│   A anthropic  5h  ██████▎     62%  resets 2h00     7d  ████▏       41%  resets 72h00                                │
│   X xai        5h  ██████████ 100%  held 40m                                                                         │
…
│ Agents, agent-bus-monitor             in      out                                                                    │
│   foureyes         A anthropic      1.2M     206k  ████████████████████                                              │
│   coder            A anthropic      345k      83k  ██████▏                                                           │
```

On the U page an Accounts row is keyed **by provider** in both modes (an account window belongs
to the subscription, not a client). The Agents zone follows §3.1: under `agent-font`, the client
then the provider; under `nerd`, the provider.

### 7.4 For you kinds: not in v1

For you's kind column already uses glyphs with a meaning (`● ask`, `♙ wait`, `◆ gate`,
`● unread`). An icon there would compete with them. Putting the *sender's* icon before its name
(For you, Activity, Requests) is a possible v2 (open question 1). In v1 these lists are
unchanged.

## 8. What does not change

- `off` (the default): every frame, golden and test is as today.
- The state glyphs, the For you kinds, Activity, Requests, Board, Plan, the pages' `Label:`
  rows, the tour and the help.
- Nothing is installed or detected: setup installs no font, and busmon runs no `fc-list`.
- No new Go dependency: about a dozen string constants in `theme.go`.

## 9. Open questions for Bernard

1. Should the **sender's icon** appear in For you, Activity and Requests (v2)? It costs 2 cells
   in each `from` column.
2. In `nerd` mode, are lettered boxes worth having (Nerd Fonts has no maker logos), or should
   `nerd` be dropped, leaving `off | agent-font`?
3. Ghostty: the `font-codepoint-map` line for `U+E1A0–E1BA,U+E1C0–E1C5` is no longer in
   `~/.config/ghostty/config`. Put it back, or check that Ghostty's fallback draws the agent font
   without it? The C page sample (§6) answers this on screen.
