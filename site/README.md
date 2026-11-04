# site/

A static presentation page for agent-bus: `index.html` (English), `fr.html` (French), `style.css`,
`site.js`. Plain HTML and CSS, a few lines of vanilla JavaScript (a Copy button on code blocks), no
build step, no framework, no font, no CDN, no analytics. It works from `file://`. Its only external
links go to `github.com/netbja/agent-bus-monitor`. Nothing here publishes it: no Pages setting and no
workflow is part of this change.

## Preview

```bash
python3 -m http.server -d site
# then open http://localhost:8000/ (or open site/index.html directly)
```

## Where each claim comes from

| Section | Source in the repository |
|---|---|
| Hero (pitch, one container, two binaries, no polling, no tokens while idle) | `README.md` intro |
| Author, licence, copyright line | `LICENSE`, `buildinfo/` (`Author`, `License`, `Year`, `Source`) |
| What it does: status, directives, subscribe | `README.md` "What you get"; `CLAUDE.md` "Stream layout" |
| Tracked requests | `docs/AGENT-BUS-GUIDE.md` (`request send/accept/block/done`) |
| 4-eyes, ledger | `README.md` "Use it"; `docs/AGENT-BUS-GUIDE.md` (challenge, verdict, verdicts); `CLAUDE.md` "The verdict ledger is the audit source of truth" |
| Wake dispatcher | `README.md` "Bootstrap a team"; `CLAUDE.md` "How the bus is actually consumed" |
| Plan view | `README.md` "busmon panes" (Plan tab); `CLAUDE.md` (`plan/`) |
| Suspend and resume | `docs/releases/v0.27.0.md`; `CHANGELOG.md` |
| Backups | `docs/OPERATIONAL-GUIDE.md` (Backups); `CLAUDE.md` "Backups" |
| Notifications | `README.md` "Notifications on your phone and desktop" |
| Questions to a person, quotas, health, tokens | `README.md` "What you get" and "Keeping a team going" |
| busmon text block and `busmon --list` | `README.md` "busmon panes" (the 140-column layout, copied as is) and "Use it" |
| How it works, diagram | `README.md` "Components"; `CLAUDE.md` "Architecture" and "Stream layout" |
| Quick start | `README.md` "Quick start"; `docs/OPERATIONAL-GUIDE.md` "Start a new project" |
| Everyday commands | `README.md` "Use it" |
| Requirements | `docs/OPERATIONAL-GUIDE.md` step 1; `go.mod`; `docker-compose.yml` |
| 4-eyes flow | `docs/AGENT-BUS-GUIDE.md`; `CLAUDE.md`; `README.md` "Pull requests and forges" |
| Security stance | `README.md` "Run the broker", "Watching a remote bus over SSH"; `docs/testing-redis.md`; `CLAUDE.md` ("Notifications", "Settings") |
| Release v0.27.0 | `CHANGELOG.md`, `docs/releases/v0.27.0.md` |

Keep the two languages in step: a change to one page's claims is a change to the other's.
