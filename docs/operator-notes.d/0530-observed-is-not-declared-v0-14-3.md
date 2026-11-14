- **Observed is not declared (v0.14.3).** The services' dispatcher inspects every verified pane
  every `ObserveEvery` (15 s; `Config.Observe`, 0 = off, so the default config and its tests
  inspect nothing) and replaces `{p}:observed` (`bus/observed.go`, fenced, whole hash, `PEXPIRE`
  120 s), after the pause check (a paused project is not observed), within a 5 s budget per pass (inspections and time reads; the final write has its own 2 s bound),
  the interval counted from the pass's end. It only reads panes: no typing, no state change; a pane
  failing `identityOK` is not observed. `{p}:observed` is not in `projectKinds` and
  `HasKeysBesidesDispatch` ignores it, so a write racing `busmon --delete` never revives a project. busmon reads it with the dispatcher facts and adds the flag `busy` (and an `Observed`
  detail row) when the pane runs a turn but the agent does not declare `working`. Never let it
  rewrite or replace the declared state. Client updates (spec
  `docs/superpowers/specs/2026-10-21-client-reload-design.md`): the launcher fingerprints the client
  it resolved (`clientfp`, `AGENT_BUS_CLIENT_FP`, kept in the verified identity as `client_fp`); the
  observe pass re-checks it at most once a minute (or reads the Claude Code `versions/` layout for
  agents without one) and adds `update` and `client_judged` to `{p}:observed`; one directive per
  update episode goes to sentinel when it is registered (a snapshot), else to the escalation target
  (`FencedCmdOnceMarks`, mark `update:<client>:<episode>` = `sent`); health `client-updates` warns;
  busmon flags `update`. Metadata only: the dispatcher never runs a client; sentinel reloads the
  outdated agents with `agentbus reload` (lifecycle-3b, below). Auto-reload (spec
  `docs/superpowers/specs/2026-11-06-auto-reload-setting-design.md`): the directive's text follows
  the reload policy, read at each send (`Config.ReloadPolicy` = `serviceReloadPolicy`, i.e.
  `service.ReloadPolicy` over the env, the project's env file, then `config.toml`): `on` with
  sentinel registered says reload the ready ones now, master last; `on` without one asks the person
  through the escalation target (it may reload no one but sentinel); `propose` keeps the proposal;
  `off` reloads nothing. Never word it "propose only" under `on`: that text, left over from v0.22,
  is why `on` never reloaded until v0.29. Under `on` with sentinel, the episode's send also sets a
  per-agent mark `update:<client>:<episode>:<agent>` for each agent ready then (`reloadReady`:
  declared idle, not observed busy, no `accepted` board task, no gate on either side
  (`ReviewsOf`), no swap or reload intent, not on a fallback, every read succeeded), in the same
  script, with `update:<client>:<episode>:auto=on` saying the episode went out automatically;
  on later passes, only for such an episode and while the policy is still `on`, an agent of the
  episode now ready with no mark gets one hint
  (`client update: <agent> is ready now …`, `retryReloads`), never sentinel about itself. busmon's
  C page shows `sentinel.reload` as "Auto-restart on update" (on / propose / off) in the Services
  group, with self-repair's keys (`servicesOnPage`).
  v0.15: an observation carries `since` (first reading of
  that state by the same pid, carried forward) and a failed inspection keeps the previous reading
  (`Observation.Fresh`, 120 s); a pane seen `blocked` (its client's own menu or permission prompt)
  reads `waiting` with the flag `prompt` (`promptWaiting`), one bell per `since`, unless the agent
  declares blocked/waiting or has an open ask. The dispatcher also rings the desktop once per such episode
  (`dispatch/promptnotify.go`, decision `docs/memory/decisions/2026-10-27-prompt-notify.md`): after
  `AGENT_BUS_PROMPT_GRACE` s (default 30, `0` = off) of the same pid and `since`, with the same
  suppressions plus a pause or a swap, through the context alert's `notify-send` runner (missing =
  a logged note); its only write is the fenced mark `prompt:<agent>` (= `pid:since`) in `{p}:quota`,
  deleted when the pane is no longer blocked or the agent leaves the observation. No bus entry, nothing typed.
  The same runner rings once per episode for an agent deferred for `identity mismatch` while
  messages wait for it (A8, `dispatch/identnotify.go`: mark `ident:<agent>` = the registered
  `pid:pid_start`, deleted once its decision is no longer a mismatch), since that agent's escalation
  goes to master, which helps nobody when master is the one lost. An escalation caused by the pane's state (rules
  7-8, `Decision.Cause` → wake `esc_cause`, or read from the reason of an older record) is
  re-armed once the pane is ready: the loop inspects such a record before `Decide`, tries clear
  and `at` stays. An escalation records the obligation's high id it was for (`WakeRecord.HighID`, chain-reliability):
  a newer message for the agent re-opens the wake with fresh tries (`Decide`'s reset, "new message since the
  escalation") instead of "already escalated" until progress; still one escalation per high id. When the escalation target cannot act (it is the agent, its provider is held,
  its tab is blocked), the escalation is a `notify` for the person only (`recipientCannotAct`).
