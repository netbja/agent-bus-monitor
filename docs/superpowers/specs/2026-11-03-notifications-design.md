# Notifications: tell the person on their phone, through ntfy or Discord

Status: spec, not built. Subject `notifications-spec`. Request: master's directive of 2026-11-03,
from Bernard: notifications through ntfy and Discord, each configurable in the settings; later maybe
two-way (Signal, Telegram), so behind a sender interface. Open questions for Bernard at the end.

## Why

What needs the person today reaches them only where they look. busmon shows it (For you, the
health pill, the bell), and the desktop rings for a few cases (`notify-send`: a pane on its client's
prompt, the context alert, an identity mismatch). Away from the desk, nothing reaches them: a
decision waits for hours, a team stalls overnight, a fail goes unseen. The README's Claude Code
`Stop` hook POSTing to a webhook of one's own is the workaround some have built; it knows when a
turn ends, not what is waiting for the person.

This spec makes the project's dispatch service send a short message to targets the person
configures, ntfy and Discord first, for the facts the bus already marks as theirs.

## What is notified

Nothing new is invented: every event is a fact the bus already holds and busmon already shows the
person. The notifier **reads** them; no producer changes.

| Kind | The fact (already on the bus) | Episode (sent once per) |
|---|---|---|
| `decision` | an open ask (`{p}:asks`); an agent declared `waiting`; a plan task waiting for the person (plan-view's For you item) | the ask id; the declaration's `ts`; the task and its waiting state's since |
| `stall` | health `team-stall` at `fail` (the stall guard's episode) | the record's `since` |
| `fail` | any health check entering `fail` | the check and its `since` |
| `blocked` | a dispatcher notice marked `for=person` (`{p}:notify`, `for=person`): an escalation nobody on the bus can act on, the coordinator's own long hold | the notice's stream id |
| `prompt` | an agent's pane waiting on its client's prompt, past the prompt-notify grace (the same episode the desktop rings for) | `pid:since` |
| `for-you` | a health check entering `warn` with its place For you (backup, disk, github...) | the check and its `since` |

Defaults (open question 1): `decision`, `stall`, `fail`, `blocked` and `prompt` on; `for-you` off,
since a for-you warn can wait for the next look at busmon.

Never notified: reports, directives between agents, verdicts, a warn in hand (sentinel's or
master's), anything a person wrote (`human=yes`).

## Where it runs

In each project's **dispatch service**, as a chore beside the reconciliation, like the history and
backup chores: a pass every 30 s, only while the service holds its dispatch lease
(`HoldsDispatch`). It never runs in busmon, never in an agent, never in the CLI. Each project
notifies about itself; the targets are the host's (one file, below), shared by every project.

**Once per episode, across restarts.** Each sent episode is recorded by a fenced write in
`{p}:notified` (a hash: field = kind and episode key, value = Redis `TIME` of the send), *before*
the send, as the prompt-notify mark is: a dispatcher that lost its lease sends nothing, and an
episode is sent at most once even when the service restarts between two passes. A field older than
7 days is pruned by the pass. The key is classified **runtime** in the backup key table (it
describes what this host sent, not the project's record), not in `projectKinds`, ignored by
`HasKeysBesidesDispatch`.

**Not on the bus.** No message, URL, token or delivery result is written to a stream; the person's
phone is not a bus participant. The record above holds kind, episode and time only.

## Dedup, delay and quiet

- **A delay, then a re-check** (`notify.delay`, default `2m`): an event is sent only if it still
  holds after the delay: the ask still open, the declaration still `waiting` with the same `ts`, the
  check still at its status with the same `since`, the notice not yet read... (a `for=person`
  notice's "read" is busmon's local cache, which the service cannot see, so `blocked` has no
  re-check beyond the delay). A decision answered at the desk within two minutes never reaches the
  phone. `0` sends at the next pass.
- **Grouped.** The events of one pass that are due go out as one message per target, one line each
  (at most 10 lines, then "and N more: open busmon"), so a burst is one buzz.
- **A rate limit** (`notify.max_per_hour`, default 6 messages per target per hour): beyond it,
  events are kept and sent in the next allowed message; `fail` and `stall` are never held back by
  it (open question 3).
- **Quiet hours** (`notify.quiet`, `"22:00-07:30"` style, local time; empty = none, the default):
  during them nothing is sent but `fail` and `stall` (open question 3); what was held is sent as one
  message when they end, its lines marked "(during quiet hours)". The record is written when the
  message is sent, not when the event is held.
- **Paused project**: nothing is sent while `{p}:pause` holds (a pause is the person's own act),
  except `fail`.

## The message

Short, the same words busmon uses, and a way back:

```
agent-bus-monitor · 2 waiting for you
• master asks: Merge PR 281 (backup)? (2 questions)
• team-stall: every agent idle, task plan-view-2 ready — busmon F5
```

- **Title**: the project and a count. **Body**: one line per event: who, what, and the busmon key
  to open it (`F5`, `!`, For you).
- **Detail level** (`notify.detail`, open question 2): `summary` (the default) sends no free text
  written by an agent: "master asks a question (2)" instead of the question itself, since the
  target is a third-party service (ntfy.sh, Discord). `full` adds the question's title, a waiting
  agent's status message and the check's detail, each through `health.Redact` (the whole field
  replaced on any credential-like hit) and cut to 120 runes.
- **Never**: a secret, a URL from the bus, a file path under the home, an ask's answer options as
  links. A message is at most 2000 bytes (Discord's limit; ntfy's 4096 is higher).
- **Priority** (ntfy's `Priority`, Discord has none): `fail` and `stall` high, `decision` and
  `blocked` default, `for-you` low.

## Targets and where their secrets live

**The URL and the token are secrets.** A ntfy topic URL is a write capability on a public server,
and a Discord webhook URL is one too. They never go in `config.toml` (which says no secret goes
there), never on the bus, never in a log, a health detail or an agent's environment.

They live in **`$XDG_CONFIG_HOME/agentbus/notify.toml`** (else `~/.config/agentbus/notify.toml`),
mode `0600` in a `0700` folder, owned by the person:

```toml
[ntfy.phone]
url = "https://ntfy.example.org/agentbus-7f3c"   # the topic URL
token = "tk_..."                                  # optional: an access token (Authorization: Bearer)

[discord.team]
webhook = "https://discord.com/api/webhooks/123/abc..."
```

- **Read rules.** A file readable by group or others is refused whole (the health check says so,
  and nothing is sent until the mode is fixed). A symlink is followed only to a file the person
  owns. An unknown key, a URL that is not `https`, a URL with user info, or a webhook not on
  `discord.com`/`discordapp.com` refuses that target with a warning naming the target and the
  line, never the value (as `config.toml`'s warnings do). `http` is accepted only for a host that
  is loopback or a `.local`/private address, with `insecure = true` (a self-hosted ntfy on the LAN).
- **Written by the person only.** agentbus never writes it: no command, no busmon page. The guide
  shows the two tables to copy. The shipped tool rules deny every role `Read`, `Edit` and `Write` of
  `~/.config/agentbus/notify.toml`, and the launch adds the `XDG_CONFIG_HOME` path as the backup
  spec does for `config.toml` (defence in depth: a shell can still read it).
- **In the archive.** `agentbus backup` treats it as a secret like `redis.env`: left out and listed,
  or in `secrets.age` with `--secrets`.
- **Which targets are used** is a setting (below): `notify.targets = ["phone"]`. A name listed but
  absent from the file is a health warning.

## The sender interface

```go
// package notify

type Message struct {
    Project  string
    Title    string
    Lines    []string
    Priority Priority // Low, Default, High
}

type Sender interface {
    Name() string // the target's name in notify.toml
    Kind() string // "ntfy", "discord", ...
    Send(ctx context.Context, m Message) error
}
```

- **ntfy**: one `POST` to the topic URL; the body is the lines, the title in the `Title` header,
  `Priority`, `Tags: agentbus`, `Authorization: Bearer <token>` when a token is set; `Markdown: no`.
- **Discord**: one `POST` of JSON `{"username": "agentbus", "content": ..., "allowed_mentions":
  {"parse": []}}` to the webhook (no `@everyone`, no role pings, whatever the text holds); the
  title bold on the first line.
- **Common to every sender** (one HTTP client in the package): 10 s per request, no redirect
  followed, no proxy from the environment (as `peer.Client`), TLS verified; a `429` waits for its
  `Retry-After` (at most 60 s) once, a `5xx` or transport error is retried twice with 2 s and 8 s;
  any other status fails that send. An error carries the status and the target's name, never the
  URL nor the response body.
- **The outcome** of each send (target, time, ok or the short error) goes to
  `<state>/notify-last.json` (`$XDG_STATE_HOME/agentbus/`, 0600, per target, the latest 20), which
  the health check reads; never the message.
- **Later, two-way** (Signal, Telegram; Bernard's "later maybe"): a `Receiver` beside `Sender`,
  out of scope here. Its rule is written now so the interface does not foreclose it: an answer
  from a phone is a person's act, so it may only enter the bus as busmon's own writes do (an ask's
  answer through `Bus.Answer`, a `human=yes` directive), identified by a pairing the person
  makes at the desk, never by a phone number alone.

The desktop's `notify-send` stays as it is (prompt, context, identity); making it a sender like the
others is open question 6.

## Settings

All in the registry (`config/keys.go`), group **Notifications**, read by the dispatch service
through `config.Machine`:

| Key | Default | Meaning |
|---|---|---|
| `notify.on` | `false` | the chore runs only when true |
| `notify.targets` | `[]` | the names from `notify.toml` to send to |
| `notify.events` | `["decision", "stall", "fail", "blocked", "prompt"]` | the kinds sent |
| `notify.detail` | `summary` | `summary` or `full` |
| `notify.delay` | `2m` | how long an event must hold before it is sent |
| `notify.max_per_hour` | `6` | messages per target per hour (fail and stall excepted) |
| `notify.quiet` | `""` | quiet hours, `HH:MM-HH:MM` local; empty = none |

No secret among them. `notify.targets` and `notify.events` are TOML lists (stored as the backup
spec's `after_env` is: a comma-joined string with a list file rule).

## Commands

```
agentbus notifications status            # each target: configured, mode ok, last sends
agentbus notifications test [<target>]   # one test message now, to one target or every one
```

`notifications` (not `notify`, which is the bus command). Both sit above the project-required exit
and need no broker. `test` sends "agentbus: a test message from <host>", records the outcome, and
prints `ok` or the short error; it is the person's act, never an agent's (the shipped tool rules
deny `Bash(agentbus notifications test*)` to every role: a test reaches a phone).

## Health

A check `notifications` (every 5 minutes, per project, for you, command `agentbus notifications
test`):

- **unknown** when `notify.on` is false (nothing to judge), or `notify.toml` cannot be read;
- **warn** when the file is readable by others, a listed target is missing or refused, or a
  target's latest send failed (the detail: the target's name and the short error, never the URL);
- **ok** when every listed target's latest send succeeded or none was needed yet.

Never a fail: it wakes nobody. A failed send does not lose the event: it is retried at the next
pass until it succeeds or the event no longer holds (the `{p}:notified` field is written only after
a successful send to at least one target; with several targets, the ones that failed are retried
alone, per target).

## busmon

Two screen changes; mock-ups below, at 100 cells.

**The settings page (C)** gains a Notifications group, after Identity. Each key is a row like the
others, editable on the page (none is a secret): Enter flips `notify.on`, chooses the detail with
←→, edits the rest in the line editor with the key's own rule. Under the rows, one muted line per
target from `notify.toml`, read-only, with its last outcome (never its URL):

```
╭─ Settings · 3 set here ────────────────────────────────────────────── esc back ─╮
│  Identity                                                                        │
│  ● Your name                 charlie                         config.toml         │
│                                                                                  │
│  Notifications                                                                   │
│  ● Send notifications        on                              config.toml         │
│  ● Targets                   phone                           config.toml         │
│  ○ Events                    decision, stall, fail, blocked, prompt   default    │
│  ○ Detail                    summary                         default             │
│  ○ Delay                     2m                              default             │
│  ○ At most per hour          6                               default             │
│  ● Quiet hours               22:00-07:30                     config.toml         │
│    phone   ntfy      last sent 14:02 ok           (notify.toml, edit it yourself) │
│    team    discord   not listed in Targets                                        │
╰──────────────────────────────────────────────────────────────────────────────────╯
```

**F5 Health** lists `notifications` like every check; for you when it warns:

```
│  ▲ Notifications       phone: last send failed 14:02 (401 unauthorized)   y copy │
```

No new key, no new panel, no bell: busmon already rings for these facts.

## What it never does

- Put a URL, token or webhook in `config.toml`, on the bus, in a log, a health detail, the
  notified record or an agent's environment; read `notify.toml` from an agent's tool.
- Send anything an agent wrote with `summary` detail; send an answer option as a link.
- Send from busmon, an agent or the CLI (but the person's `notifications test`); send without the
  dispatch lease; send an episode twice.
- Ping a Discord role or `@everyone`; follow a redirect; use an environment proxy.
- Accept an answer from a phone (two-way is a later spec, under the rule above).

## Tests (no network)

Every sender test uses an `httptest.Server` on loopback, or a fake `Sender`: no test reaches ntfy,
Discord or any host.

- **Events**: a fake bus with an open ask, a `waiting` declaration, a `team-stall` fail, a check
  entering `fail`, a `for=person` notice, a prompt episode, a for-you warn: each kind sent once, the
  default events only; `human=yes` entries, reports and in-hand warns never.
- **Episodes**: two passes send once; a restart (a new loop on the same Redis) sends nothing more;
  without the lease nothing is sent nor recorded; a new episode (a new `since`, a new ask) sends
  again; fields older than 7 days pruned.
- **Delay**: an ask answered within `notify.delay` is never sent; one still open is.
- **Grouping and limits**: ten events in one pass are one message; the eleventh line reads "and N
  more"; past `max_per_hour` events wait, `fail` does not; quiet hours hold all but `fail` and
  `stall`, then one message marked "(during quiet hours)" (a fake clock).
- **Detail**: `summary` carries no agent text (an ask titled `pw-SECRET` never reaches the
  server); `full` carries it redacted and cut.
- **Senders** (httptest): ntfy headers and body, Bearer only with a token; Discord's JSON with
  `allowed_mentions` empty; a 302 not followed; a 429 retried once after `Retry-After`; a 500
  retried twice; a 401 not retried; an error string with neither the URL nor the body.
- **notify.toml**: 0644 refused whole; a non-https URL, user info, a foreign webhook host, an
  unknown key refused per target with the line and never the value; `http` only with `insecure`
  on a private host.
- **Backup**: `notify.toml` left out and listed without `--secrets`, in `secrets.age` with it.
- **Tool rules**: every role denies Read/Edit/Write of the file and `notifications test`.
- **Health**: unknown off, warn on a failed send, a bad mode, a missing target; ok after a success.
- **busmon**: the C page rows (golden), the target lines show no URL; F5 row.

## Steps (when built)

1. `notify` package: `Sender`, ntfy and Discord, the HTTP client, `notify.toml` reading.
2. The settings keys, `agentbus notifications status|test`, the tool-rule denies, backup's secret.
3. The chore: events, episodes in `{p}:notified`, delay, grouping, limits, quiet hours.
4. Health `notifications`; busmon's C page group and target lines.

## Open questions for Bernard

1. **Which events by default?** Proposed: decisions waiting, team stall, a fail, a notice only you
   can act on, a pane on its prompt; for-you warns (backup, disk...) off. Add or drop any?
2. **How much text leaves the machine?** Proposed `summary` by default: "master asks a question",
   not the question, since ntfy.sh and Discord are third parties. Do you want `full` by default, or
   only with a self-hosted ntfy?
3. **Quiet hours and the limit**: proposed none by default; when set, everything is held but a fail
   and a stall. Should a fail wake you at night, or wait too?
4. **The delay**: two minutes before sending, so what you answer at the desk never buzzes. Shorter,
   longer, or none?
5. **ntfy server**: ntfy.sh (public, a hard-to-guess topic, optional token) or your own server? A
   public topic without a token is readable by anyone who knows its name: warn about it in health,
   refuse it, or accept it?
6. **The desktop**: keep `notify-send` separate as today, or make it one more target, with the same
   events and rules?
7. **Two-way later**: confirm the rule above (an answer from the phone enters the bus only as
   busmon's own writes do, after a pairing made at the desk).
