# site/ — the agent-bus presentation pages

Two static pages, English (`index.html`) and French (`fr.html`), sharing `assets/style.css` and
`assets/site.js`. No build step is needed to *serve* them, no CDN, no web font service and no
tracking: they work offline, from `file://` too. GitHub Pages is not enabled by this folder.

## Preview

```bash
python3 -m http.server -d site 8080     # then open http://localhost:8080/
```

Opening `site/index.html` directly in a browser works as well.

## How the pages are made

`index.html` and `fr.html` are **generated** by one script so the two languages never drift:

```bash
python3 site/tools/build.py
```

- `tools/build.py` holds the text of both languages (`t("English", "Français")`), turns busmon's
  golden renders into HTML, and rewrites the screen colour classes at the marked place in
  `assets/style.css`.
- `tools/diagrams.py` draws the six figures as inline SVG. Every colour is a CSS class
  (`.dia .r-bus`, `.l-svc`, ...) resolved from variables, so light and dark need no second drawing.
  Each figure has `role="img"`, a `<title>` and a `<desc>`, a legend, labels masked over their line
  and arrow markers.

The generated HTML is committed; run the script after editing either tool file and commit the result.

## Where each section comes from

| Section | Source in this repository |
|---|---|
| Hero pitch, quick start, requirements | `README.md` (intro, Quick start, First run), `go.mod`, `setup/preflight.go` |
| What it does (13 cards) | `README.md` "What you get", `CHANGELOG.md`, `cmd/agentbus/commands.go` (each command shown exists in the table) |
| Fig. 1 who does what | `CLAUDE.md` Architecture and "How the bus is actually consumed" |
| Fig. 2 message and wake line | `CLAUDE.md` (wake line text, `subscribe --drain`), `dispatch/` |
| Fig. 3 decision flow | `dispatch/decide.go` (`Decide`, rules 1 to 9) |
| Fig. 4 4-eyes | `CLAUDE.md` (verdict ledger, roll-up rule, `challenge`, `verdict`, `verdicts` exit codes), `docs/memory/decisions/2026-11-02-forge-delivery.md` |
| Fig. 5 restart-safe layers | `docker-compose.yml` (`--appendonly yes`, volume), `CLAUDE.md` (stateless services, fenced writes, `FencedCmdOnce`) |
| Fig. 6 suspend/resume | `docs/releases/v0.27.0.md`, `CLAUDE.md` (suspend, resume, `suspend.auto_resume`) |
| Screens | `cmd/busmon/testdata/golden/{dashboard,expanded,reader,health}-140x40-dark.ansi` |
| Security stance | `docker-compose.yml`, `CLAUDE.md` (connection, password, tests, notifications, fencing) |
| Release line, licence, author | `CHANGELOG.md`, `LICENSE`, `buildinfo` (`Author`, `License`, `Year`) |

The screens are the real golden renders of busmon's dark theme at 140×40 columns, converted from
their ANSI colours to HTML spans (the colours are busmon's own palette, `cmd/busmon/theme.go`). They
show test-fixture data and are labelled "example data". The hero shows the first 13 rows of the
dashboard render.

## Fonts and licences

All fonts are self-hosted in `assets/fonts/` as Latin subsets in woff2 (made with fontTools'
`pyftsubset`), under the SIL Open Font License 1.1; the licence texts are alongside.

| Font | Use | Licence file |
|---|---|---|
| Instrument Serif (regular, italic) | headlines | `OFL-InstrumentSerif.txt` |
| Geist (regular, bold) | body | `OFL-Geist.txt` |
| Geist Mono (regular, bold) | commands, labels | `OFL-GeistMono.txt` |

Terminal screens use the system's monospace font on purpose: Geist Mono lacks the box-drawing
corners and some symbols busmon draws, and a fallback glyph would break the character grid.

## The logo

The three files of the owner's chosen concept ("A, visor bus", from the branch
`claude/bold-darwin-7wl394`, `docs/assets/logo-concepts/`) are copied unchanged into
`assets/logo/`:

- `a-visor-lockup.svg`: its symbol paths are inlined in the header with `fill` set from the page's
  ink variable, so it reads on both themes. Its wordmark is live text in the original
  ("to outline before delivery"), so the header sets it in Geist Bold instead of DejaVu Sans.
  The geometry of the symbol is untouched.
- `a-visor-symbol-color.svg`: the favicon (amber body, readable on light and dark tabs).
- `a-visor-symbol.svg`: kept for reference, not referenced by the pages.

## Behaviour

- Theme: follows `prefers-color-scheme`; the header button stores a manual choice in
  `localStorage` (every access wrapped in try/catch).
- Motion: a packet on the hero's bus line and a reveal on scroll, both off under
  `prefers-reduced-motion`.
- The language switch links the two pages; there is no automatic redirect.

## Accessibility and print

- Keyboard: a skip link to `<main>`; every horizontally scrolling block (terminal screens, figures,
  install and step commands) takes the focus so it can be scrolled with the arrows. The focus ring
  is drawn inward where its box clips (`overflow`), so it is never cut.
- Names: the GitHub button keeps its label for assistive technology when only its icon shows; the
  language switch reads "FR — Français" / "EN — English"; the theme button is a toggle
  (`aria-pressed`) that also follows a system theme change; the copy button reports "copied"
  through a `role="status"` region.
- Reflow: no horizontal page scroll at 320 CSS px (an inline `code` token wider than the column
  wraps there, and only there).
- Print: always the light tokens, every reveal shown, header, ribbon and copy buttons left out, the
  dark screens kept with `print-color-adjust: exact` and sized to fit 140 columns, external link
  targets printed after the link.

## Contrast of the terminal screens

`build.py` computes the contrast of every foreground/background pair drawn in the screens and, for
any pair under 4.5:1, lifts the foreground towards white (or, for the dark text of a pill, lightens
the pill) until it reaches 4.5:1, keeping hue and order. It prints the table on each run. Lifted:
`#4A5563` 2.50 to `#757E88`, `#496A86` 3.32 to `#627F97`, `#316461` 2.82 to `#5A8381`, `#4A7469`
3.60 to `#5E837A`, `#6F6751` 3.37 to `#837C69`, `#7D8A99` 3.68 to `#8F9AA7`; the dim pill fill
`#4A7469` under dark text 3.60 to `#5E857B`. Every other colour already passed (lowest `#7B6FD6`
4.58). The site's own tokens (accents on paper and card, muted text) are 4.57:1 or more in light
and 6:1 or more in dark.
