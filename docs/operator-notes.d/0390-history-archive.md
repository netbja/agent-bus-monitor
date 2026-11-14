- **History archive** (spec `docs/superpowers/specs/2026-11-01-history-design.md`, package `history/`): the
  dispatch service's chore (`dispatch/history.go`, its own goroutine beside the reconciliation, 1 min, a 5 s
  pass, 2000 entries shared a page per stream in turn, the round starting one stream further each pass) appends `history.Streams` to `<state>/history/YYYY-MM-DD.jsonl` (UTC day of the id;
  `{"stream","id","f"}`), only while `HoldsDispatch` and under an flock on `history/.lock`; files only, no
  Redis write, so nothing new is fenced. On start (and after a failed write or a deleted cursor) a torn last
  line is truncated and each stream's position is max(`cursor.json`, the files' highest id); no cursor and no
  day file writes the `archive restarted` gap line (`of=*`), a stream trimmed past its position one `gap` line. Every gap line is kept again in `gaps.jsonl` (`ReadGaps`; pruned with the days), and a gap matches time bounds by the stretch it covers (`from`..`to`), not its id, so `--until` never hides a hole.
  Every field goes through `health.Redact` (whole field on any hit; `SafeText` shares `health.Sensitive`).
  `history.keep_days` (90, 0 = off) and `history.max_mb` (256), pruned daily, never today's file. Bus reads:
  `Bus.StreamAfter`/`StreamOldest`. `agentbus history` (`historycmd.go`, after the identity guard and before the
  broker is dialled: it needs the project, not the broker) reads the day files (`history.ReadDays`), then the live
  streams, `history.Merge` by (stream, id), `history.Filter` (`--agent` matches from/target/agent and a verdict's
  author/reviewer; a gap line passes an agent or kind filter it may hide, never `--thread`/`--grep`), newest
  `--limit` (200); says `broker unreachable: archive only` or `no archive on this host: live streams only`.
  busmon's About page (`A`, about-page, `about.go`, `ovAbout`; about-design: centred, the two-row wordmark `aboutLogo` on top (the half blocks, `GlyphUpperHalf` joined the closed set; the name in words below `aboutLogoW` + 2 cells), the subtitle, then the versions as label/value rows in one column under THIS BUILD (busmon, agentbus marked `✓ same build` or `≠ busmon`, protocol, go, the update state marked) and THIS HOST (redis, herdr, plugin), `y` still copying the flat `Lines`; about-credits (about-trim: no author line and no project row under the subtitle; the project stays in what `y` copies): at the foot, `<License> License · Copyright (c) <Year> <Author>` and `buildinfo.Source`, copied by `y` too; the pseudonym and licence are `buildinfo` constants held to `LICENSE` by `TestLicenseMatches`, never read at run time, and nothing more identifying is shown): the versions read in a command (`aboutSources`: the project, busmon, `agentbus version` beside it, protocol, Redis INFO, `herdr --version` through `HERDR_BIN_PATH`, the plugin's `herdr-plugin.toml` version (`AGENTBUS_REPO`, `buildinfo.Repo`, the binary's folder), the update check's cached `latest.json` only, never a network call, Go; `y` copies them), master and coder on a bus line, a packet travelling there and back on the fade schedule only (`animated` reads `aboutPlaying`; the run starts at the first `fadeMsg` after the open, `aboutState.start`, one cell per `aboutCell` 60 ms of frame time), still with fade off, 16 colours or no colour (`aboutMotion`), where `r` (`ActReplay`, OnPage) says there is nothing to replay; else it replays.
  busmon's History page (`H`, `history_page.go`, `ovHistory`): loaded in a command (`historyLoadCmd`: the last 3 days
  under an 8 MiB cap through `history.ReadBack`, which pages **by position** with a `Cursor` (day file, byte offset)
  because a day file is not in id order; `ReadGaps`; the live streams through the writer's optional
  `liveStreamReader`; the three sources kept apart in `histView` and windowed together, `window`), newest first
  under day headings (`historyBody`, short kinds below 80 cells), Enter opens an entry in the message page (`n`/`p`
  there step the page's rows, `historyStep`), `o` (`ActOlder`, OnPage) widens the window by three days (the archive read on from its cursor, the gaps read again, the live entries windowed again), `/` filters it with
  Activity's syntax on its own line kind (`lineHistFilter`, `historyFiltered`), `t` narrows it to the selected
  entry's thread.
