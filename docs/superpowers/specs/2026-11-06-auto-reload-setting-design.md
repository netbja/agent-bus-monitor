# Auto-restart on a new client version — design

Status: spec only, no code until the person's go. Task `auto-reload-setting` (v0.29).
Base: `origin/main` at af1c52c.

## What the person asked

A settings entry like "Auto-restart Claude new version: true|false". When a new client version is
installed and nothing is in progress, the agents restart on it by themselves.

## What exists

- **The setting exists already**: `sentinel.reload` (`config/keys.go:244`), `on` | `propose` |
  `off`, default `on`, group Services, env `AGENT_BUS_RELOAD`, per project through `agentbus up
  --reload` (the services' env file). `reloadPolicy` (`cmd/agentbus/reload.go:622`) layers the
  caller's environment, then the project's env file, then `config.toml`, then `on`. This project
  runs it at `on (default)` (`agentbus config`).
- **The reload exists**: `agentbus reload <role>` (`runReload`) checks everything, stops the agent
  with SIGTERM, relaunches it in its tab on its own profile, resuming its Claude Code session, and
  proves the new process runs the installed client. It types into no pane.
- **Who reloads whom** (`runReload`, lines 134-148): with no `--as`, sentinel reloads any agent
  but itself, and master only when no other driver holds the pilot lease. Master reloads only
  sentinel. `propose` refuses both and needs a person (`--as`). `off` refuses everyone but a
  person.
- **The trigger**: the dispatcher's observe pass sees an agent on an older client than installed
  (`{p}:observed` `update`) and sends sentinel **one** directive per update episode
  (`proposeReloads`, `dispatch/clientupdate.go:110`, once-mark `update:<client>:<episode>`).
- **sentinel's skill** (`skills/agent-bus-sentinel/SKILL.md`, "Client update") says "Reloading
  them is your job": sort the named agents into ready and later, reload each ready one with
  `agentbus reload`, master last, then report. Only under `propose` does it send master a
  proposal instead; under `off` it reloads nothing.
- **master's skill** ("Client update"): reload sentinel once it has reloaded the others and is
  idle; relay what sentinel cannot do.

## Why `on` did not reload on 2026-11-06

The policy was `on`, yet sentinel proposed. The bus history shows why:

```
Fri 11-06 07:23  dispatch → sentinel  directive  client update: claude-code 2.1.319 installed; still on an
  older one: coder (2.1.316), foureyes (2.1.316), master (2.1.316), sentinel (2.1.316). Propose a reload
  (agent-bus-sentinel skill, Client update): propose only, never restart or type.
Fri 11-06 07:23  sentinel → master  directive  Claude Code 2.1.319 is installed … Ready to reload now …: foureyes.
Fri 11-06 20:21  master → sentinel  directive  [the person] approved …: reload the agents onto Claude Code 2.1.319 now …
```

**The dispatcher's directive tells sentinel "propose only, never restart"** (`clientupdate.go:135`),
and sentinel obeyed the directive over its skill. That text dates from v0.22 (ea6bc34,
2026-10-22), when reloading was the person's. lifecycle-3b (3adf643, 2026-10-28) made reloading
sentinel's job and rewrote both skills, but the directive was not changed. Nor was the health
check's remediation, which says the same thing (`health/health.go:107`, `client-updates`:
"Propose a reload to the person through master …; never restart a client"). The tests pin
neither text against the policy.

So: **today's `on` does not reload by itself.** The command and the skills would; the one
message that starts the episode forbids it. 13 hours passed between the update and the reload of
the first agent, and that one waited for the person.

A second gap, smaller: sentinel's skill says the agents still `later` "are retried on your next
wake". There is no such wake for the same episode: the directive is sent once
(`FencedCmdOnce`), and `client-updates` only warns, which never wakes sentinel (a warning is not
a failure). Only the daily review, or a new update, wakes it. An agent busy at 07:23 waits until
the next day.

## Design

### (d) The fix, so `on` does what it says

1. **The directive follows the policy.** `proposeReloads` writes the action for the project's
   policy, read as `agentbus reload` reads it (`reloadPolicy`: the dispatcher's own environment
   is the services' env file, so the same layers apply):
   - `on`, sentinel in the team: `client update: … Reload the ready ones now (agent-bus-sentinel skill, Client update): agentbus reload <agent>, one at a time, master last; never type into a pane.`
   - `propose`: today's text, `Propose a reload … propose only, never restart or type.`
   - `off`: `… Reload nothing; report the agents to master once.`
   The episode mark is unchanged. The policy is read **at each send**, never once at service
   start, or a change on the C page would wait for a restart of the services. `reloadPolicy`
   lives in `cmd/agentbus` today; it moves to a shared package (`service`, beside `ReloadEnv`) so
   the dispatcher and `agentbus reload` read it the same way. The dispatcher's environment is the
   services' env file as of its start, so a value set there by `up --reload` still needs
   `up --reload` again (and the restart it does); `config.toml`, which the C page writes, is read
   each time. A change after the send applies to the next episode (`agentbus reload` checks the
   policy again anyway).

   **A team without sentinel** (reviewer-codex): `proposeReloads` then sends the directive to the
   escalation target (`l.cfg.EscalateTo`, master by default; `clientupdate.go:123-126`), and
   nobody there may reload the others: `runReload` lets master reload only sentinel, and an agent
   without `--as` nothing at all. So under `on` with no sentinel snapshot the directive says the
   limit and asks for the person, as `propose` does:
   `client update: … No sentinel in this project, so nothing reloads by itself: ask the person to run agentbus reload <agent> --as <name> for each idle agent, one at a time; never type into a pane.`
   `propose` and `off` keep their own texts whoever receives them. No retry hint (3, below) is
   sent without sentinel: the person, not an agent, does the reloads. The C page's detail line
   says it: "on needs sentinel in the team; without it, you are asked".
2. **The health remediation** of `client-updates` stops saying "propose": one static text that
   covers every policy, "sentinel reloads the outdated agents (agentbus reload), master last;
   under propose, or with no sentinel in the team, the person is asked through master; under off
   nothing is reloaded." The
   remediation is shown to people and agents; it must not contradict the policy.
3. **Retry the later ones.** The hint is only for agents that were **not** ready when the
   episode's directive went out. When it sends that directive, the dispatcher also sets the
   per-agent once-mark `update:<client>:<episode>:<agent>` for every named agent ready at that
   moment, in the same fenced script, so an agent sentinel is reloading from the first directive
   never gets a second one. Later, on each pass, an agent of the episode that is still outdated,
   now ready, and has no mark gets one directive to sentinel (`client update: <agent> is ready
   now …`), and its mark is set with it. The ready
   test the dispatcher can make without running code: declared `idle`, not observed busy, no
   `accepted` board task, no open gate on either side, no swap or reload intent, not on a
   fallback. It is a hint only: sentinel runs `agentbus reload`, which decides. Open question 3
   asks whether this belongs in this task.
4. A test pins the directive's text for each policy (`dispatch/clientupdate_test.go`), and that
   no message the dispatcher sends to sentinel for an update says "propose" under `on`.

### (a) What the person sees on the C page

`sentinel.reload` keeps its three values, because `propose` is a real third behaviour (the
person approves each reload) and it is the per-project `up --reload` value too. On the C page it
becomes a **choice** (←→, like Theme and Icons: `settingChoices`), with a label:

```
Services
  Auto-restart on update   on   propose   off
```

The label is 22 cells: the label column is capped at `settingsLabelW` (26), so "Auto-restart on
a new client version" (36) would be cut. The detail line says the rest: `a new version of an
agent's client (Claude Code, Codex, Grok) is installed. on: sentinel restarts idle agents on the installed
version, master last · propose: you approve each restart · off: nothing restarts`.

A plain true/false would hide `propose`, and a project set to `propose` by `up --reload` would
read as neither. Open question 1 offers the bool form anyway.

The label applies to every client, not only Claude Code: the episode is per client (Codex, Grok
too). A Codex agent has no session to resume, so `on` never restarts it fresh by itself: the
skill already says `--fresh` only when master asks. The detail line says "resumes its
conversation; an agent with nothing to resume is left for you".

### (b) "Nothing in progress"

Exactly what `agentbus reload` already refuses on, so one rule holds in one place
(`reloadOpenWork` and the checks around it in `runReload`):

- the agent declares `idle`, and its pane is not observed running a turn (fresh observation);
- no open challenge on either side, no mission attempt (`openWork`);
- no board task it owns in state `accepted`;
- its own open ask only for `--fresh` (a resumed session keeps the conversation);
- no wake being typed into its pane;
- not on a fallback profile (`swap --back` first);
- master: no other driver holds the pilot lease.

The command checks all of it before the reload intent is written, and again after it, right
before SIGTERM. Nothing new is needed; the spec only names it on the C page's detail line
("idle, no review, task or mission in flight").

### (c) master's own reload, and sentinel's

- **master** is reloaded by **sentinel**, last, when every other ready agent is done and no other
  driver holds the pilot lease. `runReload` allows it today; only the directive stopped it. The
  person's directive of 2026-11-06 ("master LAST (I will tell you when)") came from master, not
  from the code: under `on`, sentinel does not wait for master's word (open question 2).
- **sentinel** cannot reload itself (`runReload` refuses). Master reloads it once sentinel has
  reported and is idle (master's skill). Under `on` that is automatic in the sense that master
  acts on sentinel's report without asking the person.
- Reloading master interrupts nothing in flight by construction (idle, no open work), but it
  does relaunch the pilot: the order is sentinel's other agents, then master, and master
  reloads sentinel after its own relaunch.

## Tests (first, red)

- `dispatch`: `TestProposeReloadsFollowsThePolicy` (on / propose / off text, one per episode as
  today; the policy read at each send, a `config.toml` change seen without a restart);
  `TestProposeReloadsWithoutSentinelAsksThePerson` (no sentinel snapshot, policy `on`: the
  escalation target gets the person's text, never "reload the ready ones now"; no retry hint);
  `TestRetryOnlyAgentsLaterAtTheEpisode` if (d)3 is kept (an agent ready at the first directive
  gets no hint; one later gets exactly one).
- `service`: `ReloadPolicy` moved, its layers unchanged (the existing `reloadPolicy` tests move
  with it).
- `health`: the one remediation text names all three policies and no longer says "never restart
  a client".
- `busmon`: the C page shows the label, ←→ cycles on/propose/off, the file gets the value;
  golden for the settings page.
- `skills`: sentinel's skill text is unchanged in substance; the "retried on your next wake"
  sentence names the retry directive.

## Out of scope

- A new key, or a bool next to `sentinel.reload`.
- Reloading for anything but a client update (a changed role prompt keeps master's "propose to
  the person").
- Restarting a Codex or Grok agent without a session to resume.

## Open questions (for the person)

1. **The C page form**: a three-way choice `on / propose / off` under "Auto-restart on update"
   (proposed), or a bool (on = `on`, off = `propose`), with `off` left to the CLI and
   `up --reload`?
2. **master's reload under `on`**: sentinel reloads master by itself, last (proposed, as the code
   and skills say), or master always tells sentinel when?
3. **The retry of later agents** (d)3: in this task, or a follow-up? Without it, an agent busy at
   the update is reloaded only at sentinel's next wake (the daily review or another update).
4. **The default stays `on`?** It is `on` today, but `on` never acted until this fix: with it,
   every project on the default starts reloading idle agents by itself after an update.
