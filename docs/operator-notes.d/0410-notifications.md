- **Notifications** (spec `docs/superpowers/specs/2026-11-03-notifications-design.md`, package `notify/`): the
  dispatch service's chore (`internal/dispatchsvc/notifychore.go`, `dispatch.NotifyConfig`, 30 s, only while
  `HoldsDispatch`) tells the person what waits, through ntfy, Discord and the desktop. Targets and their secrets are
  in `notify.toml` beside config.toml (`notify.Load`: refused whole when open to others or not the person's, its folder
  writable by no one else, opened once `O_NOFOLLOW` and judged by fstat; per target https, http only `insecure` on a
  private host, ntfy.sh by default and never `self_hosted`, a topic of 12-64 safe characters, Discord's hosts only;
  warnings carry no value; a `Target` prints only kind and name). Its one writer is `notify.Edit` (notify-guided-setup
  §4, `notify/edit.go`): by table, every other byte kept (every `[..]` or `[[..]]` line is a boundary, only a
  `[kind.name]` part is editable, any other is kept verbatim; the comments right above a header are its table's);
  the targets after an edit must be those before it changed by the edit's own ops, every other target with the same
  values (a rename keeps its own), or nothing is written; values
  judged by `parse` (Load's rules) and TOML-encoded
  before any write; a refused, unparsable or swapped-folder file never touched; `flock` on `notify.toml.lock`, size
  and mtime compared at the rename (`ErrChanged`); a 0600 temp file renamed over it, the folder synced; loaded again,
  and the old bytes put back (or a new file removed) when an added or renamed target does not load. `notify.Explain`
  turns a send's `SendError` into the setup's plain sentence (a TLS failure is the reason `certificate`), and
  `internal/agentcaller` is the agent-caller rule both binaries share. Its CLI callers (step 2,
  `cmd/agentbus/notifysetup.go`): `notifications setup` (questions on `/dev/tty`, refused when stdin is no terminal),
  `add ntfy|discord|desktop` (a drawn topic, `notify.DrawTopic`; a token or webhook from a file, `-` or
  `--webhook-stdin`, never an argument, and an unknown flag is a usage error that echoes nothing; `http` only with
  `--insecure`), `remove` (asks on the terminal, or `--yes`; `notify.on` goes off only on the person's yes), `rename`
  (the header, `notify.targets` and the `notify-last.json` record, `Last.Rename`) and `show-topic`; `add` turns
  `notify.on` on and lists the target, then tests it (exit 0 delivered, 2 written but failed, 1 nothing written, a
  malformed command line included). A
  topic, its link and its QR (`notify.QR`, half blocks drawn black on white, rsc.io/qr, L, up to version 6) go only
  to `/dev/tty`: without one, stdout says to run `show-topic` on a terminal. All five refuse an agent first
  (`refuseAgent`: `AGENT_BUS_AGENT`, an agent ancestor, an unreadable broker) and every role denies them
  (`rolecfg.NotifyWriteDenies` in the shared rules and a drafted role's own; the launch adds the `--project`/`-p`
  spellings, `NotifyProjectDenies`). They also refuse any global `--project` or `--host`, before or after `notifications`, an empty value included (main
  compares the arguments before and after `globalArgs`)
  (reviewer-codex: `agentbus --host x notifications add …` would escape the plain deny), so the plain deny covers
  every spelling that runs; `status` and `test` keep accepting them. Every role also denies its
  Read/Edit and `Bash(agentbus notifications test*)` (no wildcard before the words: it would deny prose), and the
  command refuses `AGENT_BUS_AGENT` or a process under a registered agent's verified pid in any project (so on Linux
  it needs the broker); a backup secret. The settings are `notify.*` (group Notifications, config.toml only, list keys
  `Key.List`). Events (`notifyfacts.go`, never an agent's text in a summary line): open asks, `waiting`
  declarations not answered (`bus.WaitAnswered`, stale-waiting: an answered one is no event and no longer
  silences a prompt; the cmd stream is read only while an agent is declared waiting), plan tasks waiting for the person (episode `plan:<id>`, first seen in memory), team-stall at fail
  (a stall only; the stall guard's own notice is never an event), other fails, for-you warns, `for=person` notices,
  prompts by the desktop ring's rule. `notify.Decide` is pure: a target's first pass records everything standing as
  `baseline` and sends nothing; then the due events without a field (delay, kinds, pause = fail only, quiet hours =
  `quiet_exempt` only, past `max_per_hour` fail/stall only), one message per target, `full` detail (`notify.Scrub`) only
  to a private target (desktop or `self_hosted`); fields pruned when their fact or target is gone, never by age.
  `{p}:notified` (`bus/notified.go`, fenced; runtime, ignored by `HasKeysBesidesDispatch`) holds kind, episode, target
  and time only; a field is written after that target's success (at least once). A pass with any failed read sends
  nothing and records the source in `<state>/notify-last.json` (host-wide, under the flock `notify.lock`: latest 20
  outcomes per target apart from the counted hour; a send's hour reserved under the lock, given back if it fails).
  The dispatcher's own prompt ring is skipped (`PromptConfig.Skip`) only for an episode that began after a listed
  desktop target's baseline. Health `notifications` (5 min, for you, never fail); busmon's C page shows the group, then
  `Set up ntfy|Discord|the desktop`, then one selectable line per target (`notifyTargets`, never its URL). Enter on a
  set-up line opens the guided setup (notify-guided-setup step 3, `notify_setup.go`, `ovNotifySetup` in its own
  `ModeNotifySetup` table): four steps, nothing written before the last, which writes through `notify.Edit` (an edit
  after `e` removes the old table in the same edit), lists the target and turns `notify.on` on (`nsList`), then tests
  (`nsSend`) and says the outcome by `notify.Explain`. A field being edited takes every key but tab, enter and esc
  (esc puts its value back); a paste goes whole into it, a newline ends it; masked fields draw `••••` and a count. The
  code is `notify.QR` in cells drawn black on white (`Seg.QR`; reverse under NO_COLOR), beside the steps when the
  page has 26 rows and is not narrow, else on `c`. A target line opens its page: `t`, `n` (rename, the record moves),
  `x` (modal y/n; when that leaves no target listed, a second modal asks to turn `notify.on` off, which only y does),
  `s` (the topic and code until any key, that key consumed). A step with one field takes a paste without a tab first. `?` on step 1 opens the old read-only template page
  (`settings_notify_assist.go`). The page refuses an agent: `AGENT_BUS_AGENT`, an agent ancestor, or agents that
  cannot be read (`nsAgentCaller`, `internal/agentcaller`; busmon's tests stub it in `TestMain`, never the broker). One HTTP client: no redirect, no proxy, 10 s, a 429 waited once (seconds
  or a date, at most 60 s), a 5xx retried twice; errors name the target and status, never the URL or body.
