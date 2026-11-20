# busmon: restart a service from Services (F3), and a log page that reads

Status: r1, spec only, no code. §6's questions are the person's: nothing in §3-§5 that depends on
them is built before they answer. Base: origin/dev `b83d4b7b`. Author: coder, from master's directive
of 2026-11-20 (plan v0.31 task `services-restart-buttons`). It answers the open question
[2026-11-17-services-view](../../memory/open-questions/2026-11-17-services-view.md) (the person's item 18).

## 1. What exists

- **The cards.** F3 (`cmd/busmon/services_cards.go`) shows one card per service: dispatch, missions,
  Redis. The holders are listed below the cards (`services_holders.go`), and that list only reads:
  "busmon never stops a holder". Enter or `l` opens a log page. The only restart is the footer's
  advice: `agentbus --project <p> service status|logs|restart`, to type in a shell.
- **The command.** `agentbus --project <p> service restart <kind>` (`cmd/agentbus/servicecmd.go`)
  runs Stop, resets the restart budget, then Start, and prints `<kind>: restarted`. `service restart
  holder` refuses unless `--kill-agents` is given, because the restart kills every agent the holder
  holds.
- **Who may run it.** The command refuses no caller. The sentinel role relies on that: it may run
  `agentbus service restart <kind>` when the budget allows (`skills/agent-bus-sentinel/SKILL.md`).
- **busmon already runs agentbus for the person.** The Projects page's `x` asks
  `Stop the services of <p> here? … y / n`, then runs `agentbus down <p>` through `startRun`
  (`sleep.go`). That path allows one run at a time, appends the output to the project's log in a
  session of its own, and shows the last line as the notice until the run exits. The idea actions
  refuse when busmon runs in an agent's environment (`ideas_actions.go`: `AGENT_BUS_AGENT` set).
- **The log page.** `serviceLogBody` (`services.go`) shows the last 200 raw lines, plus the crash
  file. The logs on this host as of 2026-11-20:
  - `dispatch.log` holds 1780 lines. 1775 are JSON decisions with no time field (`{"action":…,
    "agent":…,"reason":…,"rule":…}`), and 922 of those are `"action":"none"`. The other lines are
    timestamped `service:` text.
  - `missions.log` is timestamped text only.

  The page shows no time for a decision, and the routine `none` lines bury the rare
  `escalate`, `blocked` and `defer` ones.

## 2. Out of scope

- **The views redesign** (coder2, spec 2026-11-17: Board, Plan, Work, IDEAS). This spec touches
  only F3's files: `services_cards.go`, `services.go`, a key in `keys.go` and the log writer of
  dispatch decisions. It does not touch the Board, Plan, Work or ideas files.
- **New process paths.** busmon runs the existing `agentbus` command, never systemd, launchd or
  docker itself.
- **Remote services.** A service whose record names another host is not restarted from here
  (§3).

## 3. Restart: the same path as the Projects page's `x`

- **The key, on the selected card: `X`** (free today; `r` and `R` are taken globally). The footer
  shows it as a word only on a card it applies to: `X restart`.
- **It asks first.** `Restart dispatch of <p>? agentbus --project <p> service restart dispatch. y / n`
  - the exact command is in the question;
  - `y` runs it through `startRun`, with the same rules as `x`: one run at a time, the output in
    the project's log, the last line as the notice;
  - any other key cancels.
- **As the person, never an agent.** busmon refuses before asking when `AGENT_BUS_AGENT` is set
  (the ideas rule), or when a registered agent is among its ancestors (`agentcaller.Refuse`, the
  `--delete` rule). The message names the command to run from the person's shell.
  - The `agentbus service restart` command itself keeps accepting every caller, so the sentinel's
    documented repair still works (§1).
- **Refused, with the reason in the notice, never sent:**
  - the service is on another host (`record.Host` is not this host): `restart it on <host>`;
  - nothing is installed for that kind here;
  - another run is in progress;
  - the card is a holder (§6 Q1) or Redis (§6 Q2), until the person answers.
- **After the run.** The card is read again at once, instead of waiting for the next tick: its
  `since` and state show the new process. A non-zero exit becomes an attention notice, with the
  command's last output line.

## 4. A log page that reads

Each line is drawn on its own row, unchanged except for a level and a time:

- **Level.** For a dispatch decision: `escalate`, `blocked` and `unblocked` are attention; `defer`
  and `prompt` are info; `none` is muted. For text lines: a `failed`, `error` or `panic` word is
  an error, and anything else is info. The crash file is an error, as today.
- **Format of a decision.** Fields in a fixed order: `action  agent  reason  (rule N)`. The
  `high_id` and `latency_ms` fields are left out, and the raw file keeps them.
- **Routine lines folded.** `none` decisions are hidden by default, and `a` shows them (the same
  page key as Health's `a`, all). The frame label counts them:
  `412 lines · 922 routine hidden (a)`.
- **Time.** A text line already has its time. A decision line has none (§1). This spec proposes
  that the dispatcher add `"ts"` (the time it was written) to each decision line. That is an
  additive field, and readers that ignore unknown fields keep working. Older lines show no time.
  Whether to add it is §6 Q3.
- `f` filters by a word, such as an agent or an action (the same page key as History's `f`). Esc
  clears the filter. `/` and `n` stay the palette and next-item keys they are on every page.

## 5. Steps (after the answers)

1. The log page (§4). Its test reads a fixture log mixing JSON decisions and text lines, and
   checks the levels, the folding count and the field order.
2. The restart key (§3). Its tests cover:
   - the question carries the exact argv;
   - `y` runs that argv through a fake runner;
   - each refusal of §3 sends nothing;
   - under `AGENT_BUS_AGENT` nothing is asked.

One PR per step from origin/dev.

## 6. Questions for the person

- **Q1. The holder.** Restarting the holder kills every agent's terminal, and the command requires
  `--kill-agents` for that. Should busmon offer it?
  - (a) no: holders stay read-only in busmon, and the footer keeps naming the command;
  - (b) yes, behind a second question that names the agents it kills.
- **Q2. Redis.** The broker's container (`agent-bus-redis`) serves every project on this host, and
  restarting it cuts every busmon and agent off for a moment. Today no `agentbus service` kind is
  Redis. Should its card offer a restart?
  - (a) no;
  - (b) yes, through a new `agentbus` command, which would be specced separately.
- **Q3. Time on dispatch decisions.** Should the dispatcher write a `ts` field on each decision
  line (§4)? Without it, the page can only say the order of decisions, not when they happened.
- **Q4. The key.** Is `X` right for restart, or do you want a different key?
