# Notifications: a guided setup that writes notify.toml for the person (notify-guided-setup)

Status: spec, for review (subject `notify-guided-setup`). Not built. v0.29. The person tested ntfy
and asked for it (relayed by master, 2026-11-07): "je ne sais pas quoi mettre dans tous les champs,
cela doit pouvoir se configurer simplement sans avoir besoin de créer les fichiers de config". §11
holds the questions left to them.

## 1. Today, and what goes

The notifications spec (`2026-11-03-notifications-design.md`, "Targets and where their secrets
live") made `notify.toml` the person's file:

- "agentbus never writes it: no command, no busmon page";
- busmon's assistant (`settings_notify_assist.go`, page C → Notifications → `Set up ntfy`) shows the
  steps, a table to copy (`y`, with a topic drawn on the spot) and each target's last test;
- the person then creates the file, sets its mode, pastes the table, and sets `notify.on` and
  `notify.targets`.

That is four places to touch and a TOML syntax to get right, for someone who only wants their
phone to ring. **This spec reverses one rule: agentbus may write `notify.toml`, on the person's own
act only.** Every other rule of that spec stays:

- the file's mode;
- the read rules, and refusing the file whole;
- no secret on the bus, in a log or in `config.toml`;
- summary versus full text, and `self_hosted`;
- the roles' denies;
- the file treated as a secret in backups.

On the person's go, the notifications spec gets one line pointing here. A decision note records
it: "notify.toml: written by agentbus on the person's act, never by an agent".

## 2. Who may write it: the person only

The writer is one function, `notify.Edit(path, func(*Doc) error)` (§4). Two callers reach it:
busmon's guided setup (§5) and the CLI twin (§6). Both refuse an agent first, by the same rule
`agentbus notifications test` already applies (`notificationscmd.go`):

- **busmon** refuses when its own environment carries `AGENT_BUS_AGENT`, or, on Linux, when a
  registered agent is among its ancestors (`agentAncestor`). Its page then reads `setting up a
  target is the person's act: open busmon from your own shell`. busmon is the person's tool, so this
  is a guard against an agent running it in a pane, not a hurdle for the person.
- **the CLI** refuses the same two cases, before reading anything.
- **The tool rules stay as they are.** Every role already denies `Read`, `Edit` and `Write` of
  `notify.toml` (`rolecfg/configdeny.go`). The new subcommands need no rule: they refuse an agent
  themselves, so a deny never has to match prose that mentions them.

## 3. What the person is asked, per kind

The guided setup asks **the minimum**, and everything else has its safe default.

### ntfy (phone)

1. **Which server?**
   - **`ntfy.sh (free, public)`**, the default. Nothing more is asked.
   - **`my own server`**: its address (`https://…`, or `http://` for a loopback, `.local` or
     private address, which then takes `insecure = true`, as Load accepts). Then an optional
     access token, typed masked. Then **"Is this server yours? Then full text may be sent to it"**,
     no by default, which sets `self_hosted`.
2. **The topic is drawn for them.** It is `drawTopic()`, `agentbus-` and 20 letters or digits from
   `crypto/rand`, as the assistant draws it today. It is never asked.
3. **Subscribe on the phone.** The page shows, together:
   - the topic, with `y` to copy it;
   - the subscribe link `https://<server>/<topic>`, with `Y` to copy it;
   - a QR code of that link (§5.3), with one line: `in the ntfy app: + → subscribe to topic → paste
     the topic, or scan the code with the phone's camera`.

   The person presses Enter once subscribed. Nothing on the page claims the phone is subscribed:
   the test (step 5) is the proof.
4. **Write.** The target is named `phone` (else `phone-2`…, `notifyAssistName` today; Tab edits it).
   The file is written (§4), `notify.on` is turned on, and the name is added to `notify.targets`
   (both through `config.Set`, as the C page writes today).
5. **Test.** One message is sent at once, the same as `agentbus notifications test phone`. The page
   shows `delivered: check your phone`, or the failure in plain words (§7). It offers `t` test
   again, `e` edit, `d` remove.

### Discord

1. **The webhook.** The page shows where to make one: `Server settings → Integrations → Webhooks →
   New webhook → Copy webhook URL`. The person pastes it into a masked field. A paste is accepted
   whole, bracketed paste included, and the field shows `https://discord.com/api/webhooks/…`
   **with the rest hidden**. It is checked by Load's rules before anything is written: `https`,
   host `discord.com` or `discordapp.com`, no user info. A refusal names the rule, never the value.
2. **Write and test**, as ntfy's steps 4 and 5. The name defaults to `team`.

### Desktop (notify-send)

Nothing is asked. The page says whether `notify-send` is on `PATH`; without it, the page says
`install libnotify (notify-send) first` and stops. With it, the page writes `[desktop.here]` and
tests, as above.

### Remove and rename

On the C page, every target of the file is a line: its name, its kind and its last test, never a
topic or a URL.

- **`d` (remove)** asks `remove phone? its topic is lost, y/n`. It removes the target's table and
  its name from `notify.targets`. When no target is left listed, it asks `turn notifications off
  too? y/n`.
- **`r` (rename)** edits the name. A new name must be free, and `^[a-z][a-z0-9_-]{0,31}$`, the
  rule Load applies. It renames the table and its entry in `notify.targets`, and moves the
  target's record in `notify-last.json`, so its last test stays.
- **`s` (show the topic)**, ntfy only: the person may need the topic again, on a new phone. The
  page shows the topic and its QR code until a key is pressed. It is never written to a log, the
  bus or the screen's scrollback. §11 Q2.

## 4. Writing the file safely: `notify.Edit`

One writer, in package `notify`, which both callers use.

- **Read, edit, write, check.**
  1. It reads the current bytes, if any, with Load's own refusals. A file that is refused whole
     (open to others, not the person's, a folder others can write) is **never** rewritten or
     chmod-ed: the page says why and what to run (`chmod 600 …`), as health does. A file that
     cannot be parsed is not rewritten either: `notify.toml has an error at line 12: fix it, or
     move it away and start again`.
  2. It edits **by table**: the whole `[<kind>.<name>]` table, from its header line to the next
     header or the end. Every other byte is kept: the person's comments, order and other tables. A
     new target is appended at the end, after one blank line.
  3. Every value is written by the TOML encoder (quoted strings, never pasted text), and only the
     keys this spec knows. A value is checked by Load's rules first, so a value Load would refuse is
     never written.
  4. It writes a temporary file in the same folder:
     - mode `0600`, `O_EXCL`;
     - `fsync`, then `rename` over the file, then `fsync` the folder.
     The folder is created `0700` if missing, and refused, as Load refuses it, if it is not the
     person's or others can write it.
  5. It re-loads the result with `notify.Load`. If the new or renamed target is not there and
     valid, it puts the old bytes back the same way, and the caller says `notify.toml was not
     changed: <why>`.
- **No lost edit.** Between its read and its rename, the writer holds a lock beside the file
  (`notify.toml.lock`, `flock`, the way `notify-last.json` is updated). It also compares the
  file's size and modification time at the rename with those it read. If the person saved the file
  by hand meanwhile, nothing is written: `notify.toml changed while you were editing: open the
  setup again`.
- **No secret leaves the file.** The writer's errors, the pages and the CLI never print a topic, a
  token or a webhook, except the topic on its own page (§3, §5.3). `Target`'s `String` and `Format`
  already print only kind and name. A test asserts no output holds the secret.
- **Backups and history.** The file stays where it is (`~/.config/agentbus/`), outside every
  repository, and backups already treat it as a secret. Nothing is written to the repository, the
  bus, the busmon cache or the shell's history: the CLI reads secrets from a prompt, a file or
  stdin, never from its arguments (§6).

## 5. busmon

### 5.1 Where

Page C (Settings) → group **Notifications**. The three `Set up …` lines stay, and Enter on one now
opens the **guided setup** in place of today's read-only page. Below them, one line per existing
target (§3, remove and rename). The read-only template stays reachable on the setup's first screen
(`?` shows the file's format), for a person who prefers to write the file by hand.

### 5.2 The screens

One reader page, `ovNotifySetup`, with one screen per step; the frame says `step 2/4`. Esc steps
back, and Esc on step 1 closes it. Nothing is written before the write step.

| Step | ntfy | Discord | desktop |
|---|---|---|---|
| 1 | server: `ntfy.sh` / own (↑↓, Enter) | where to make a webhook; the masked field | notify-send found or not |
| 2 | own server only: address, token (masked), "yours?" | name (default `team`) | name (default `here`) |
| 3 | subscribe: topic, link, QR, Enter when done | write | write |
| 4 | name, write, test, the result | test, the result | test, the result |

- **Keys.** They are bound in the page's own mode, which already has its table in `keys.go`:
  - Enter next, Esc back, Tab between fields, ↑↓ between choices;
  - `y` copy the topic, `Y` copy the link;
  - `t` test again, `e` edit, `d` remove, `r` rename.

  `TestNoKeyCollisionPerMode`, `TestTourKeysExist`, and F1 lists them from the table.
- **Masked fields.** They show `••••` and the count of characters. Bracketed paste is taken whole;
  a pasted newline ends the field. The field's value is held in the page only and dropped when the
  page closes. It is never drawn, never copied, never in a golden.

### 5.3 The QR code

- The code encodes the subscribe link, `https://<server>/<topic>`. Opened on a phone, it shows the
  topic's page, from which the ntfy app subscribes. Whether the app's own `ntfy://` link should be
  used instead is §11 Q1.
- It is drawn with half-block characters (`▀`, `▄`, `█`, space): two modules per cell in height, a
  4-module quiet zone, black on white whatever the theme, so a camera reads it in dark mode too.
- A version-3 code (29×29 modules, L correction) holds a link to ntfy.sh. With its quiet zone it is
  37 cells wide and 19 rows tall. A longer own-server link takes the version it needs, up to
  version 6 (41 modules: 49 × 25).
- **The encoder.** A Go QR encoder is a new dependency. Recommended: `rsc.io/qr` (small, no
  further dependency, BSD). It is pinned in `go.sum`, and `go mod verify` passes. §11 Q4.
- **Tiers.**

  | Tier | What the page shows |
  |---|---|
  | wide and medium, with at least 26 rows inside the page | the code beside or under the steps |
  | otherwise | the topic and the link only, with `q` to show the code alone on the whole screen until any key |
  | narrow | the topic and the link; `q` shows the code when the screen is at least 41×25, else `widen the window to show the code` |

### 5.4 Goldens and tests

New goldens, each with a **fixed fake topic** (`agentbus-goldentopic0000000`) and a fixed fake
webhook. Neither is a secret, and the scrub test (`internal/scrub`) accepts them:

- each step of ntfy at wide, medium and narrow;
- the QR screen;
- Discord's masked field after a paste;
- the desktop without notify-send;
- the result line for `delivered` and for two failures;
- the C page's Notifications group with two targets.

The tests are in §9.

## 6. The CLI twin

```
agentbus notifications setup                                # the same questions, on a terminal
agentbus notifications add ntfy [--name phone] [--server URL] [--token-file F] [--self-hosted] [--insecure]
agentbus notifications add discord [--name team] [--webhook-file F | --webhook-stdin]
agentbus notifications add desktop [--name here]
agentbus notifications remove <name> [--yes]
agentbus notifications rename <old> <new>
agentbus notifications show-topic <name>                    # §3 "s", with the QR on a terminal
agentbus notifications test [<name>]                        # unchanged
agentbus notifications status                               # unchanged
```

- `setup` is interactive. It asks what §3 asks and draws the QR on the terminal. It refuses when
  stdin is not a terminal (`use add … instead`).
- `add` is for scripts. **No secret on the command line**: a token or a webhook comes from a
  file (`--token-file`, `--webhook-file`, `-` for stdin) or `--webhook-stdin`, so it is never in
  `ps` or the shell's history. Each command:
  - prints the topic it drew (ntfy) and the subscribe link once, on stdout, with nothing else on
    that line;
  - turns `notify.on` on and lists the target;
  - tests it, unless `--no-test`;
  - exits 0 when written and delivered, 2 when written but the test failed, 1 when nothing was
    written.
- Every writing subcommand refuses an agent (§2), with the message `notifications test` uses today.

## 7. The test's result, in plain words

The send already returns an error. A new `notify.Explain(err) string` turns it into one sentence,
and the raw error is kept for `agentbus notifications status`, never with a URL:

| What failed | What the person reads |
|---|---|
| DNS, connection refused, no route | `the server cannot be reached: check the address, or your network` |
| TLS (a certificate) | `the server's certificate is not valid for this address` |
| timeout | `the server did not answer in 10 s` |
| ntfy 401/403 | `the server refused the token (or this topic needs one)` |
| ntfy 429 | `the server limits this machine for now: try again in a minute` |
| Discord 401/404 | `this webhook no longer exists, or was not copied whole` |
| Discord 429 | `Discord limits this webhook for now: try again in a minute` |
| desktop: notify-send failed | `notify-send failed: is a desktop session running?` plus its exit status |
| any other | `the send failed (<status>)`, the status code only |

A delivery on the forge's side is not a delivery to the phone. On success the page says
`delivered to <server>: check your phone`, not `your phone rang`.

## 8. Privacy and what does not change

- **Summary versus full** (`notify.detail`): the setup never changes it. Its last step shows one
  muted line: `messages say what waits, never an agent's words (notify.detail = summary)`. With
  `full`, it adds `full text goes only to a server you marked yours or to the desktop`.
- **`self_hosted`** is set only by the person's explicit "yes, this server is mine". It is never
  set for `ntfy.sh` (the writer refuses the pair) and never for Discord.
- **The topic is the lock** on a public server: the setup always draws a 20-character one, and
  `add ntfy` takes no `--topic` (§11 Q3).
- **The roles' denies**, the backup's treatment and health `notifications` are unchanged. Health
  keeps warning on a file open to others, a listed target missing or refused, and a failed send.

## 9. Tests

- **`notify.Edit`** (in `t.TempDir()`, never the person's real folder):
  - appending a target keeps every other byte;
  - remove and rename touch only their table;
  - values are encoded, not pasted (a topic with `"` or a newline is refused before writing);
  - a value Load refuses is never written;
  - the file is `0600` and the folder `0700` when created;
  - a file open to others, not the person's, or under a folder others can write, is not touched;
  - an unparsable file is not touched;
  - a concurrent edit (the modification time moved) writes nothing;
  - a failed re-load puts the old bytes back;
  - no error string holds a topic, token or webhook.
- **The agent refusal**: busmon's page and every writing subcommand, with `AGENT_BUS_AGENT` set and
  with an agent ancestor (the cases `TestNotificationsTestRefusesAnAgent*` cover today).
- **The settings**: after `add`, `notify.on` is true and the name is in `notify.targets` once.
  After `remove`, it is gone, and `notify.on` changes only on the person's yes.
- **The record**: `rename` moves the target's `notify-last.json` entry.
- **`Explain`**: one case per row of §7, from a fake server (`httptest`) and a fake `notify-send`
  on `PATH`.
- **The QR**: decoding the drawn half-blocks gives back the link (a test-only decoder, or a
  round-trip through the encoder's matrix). The size per version fits §5.3's tiers.
- **busmon**: the flow through every step, per kind, with a fake sender; Esc back at each step
  writes nothing; the masked field never reaches the grid's text; goldens (§5.4); key-table tests.
- **The CLI**: `add` with a secret only from a file or stdin; `setup` refused without a terminal;
  the exit codes 0, 1 and 2.

## 10. Steps, and out of scope

1. **The writer and the CLI:** `notify.Edit`, `Explain`, `add`, `remove`, `rename`, `show-topic`
   and `setup` on a terminal, with the QR.
2. **busmon's guided setup:** the screens, the masked fields, the QR tiers, the target lines with
   `d`, `r` and `s`, and the goldens.

**Out of scope:**
- other kinds (Slack, email, Telegram): later specs, with the same writer;
- a topic chosen by the person;
- answering from the phone (two-way, a later spec, as today);
- moving `notify.events`, `delay` or `quiet` into the setup (they stay on page C as settings).

## 11. Questions for the person

1. **The QR's link.** Recommended: `https://<server>/<topic>`. It opens in any phone camera and
   shows the topic's page, with a subscribe button in the ntfy app. Or the app's own `ntfy://`
   link, which subscribes in one tap but opens nothing without the app.
2. **Showing a saved topic again (`s`, `show-topic`).** Recommended: yes, on the person's key
   only, for a second phone; never in a log. Or never: a new phone means a new target.
3. **A topic of the person's choice.** Recommended: no. A short or guessable topic on ntfy.sh can
   be read by anyone, so the setup always draws one. Or allow `--topic` with the same 12-character
   minimum Load applies.
4. **A new dependency for the QR** (`rsc.io/qr`). Recommended: yes, pinned. Or no QR: the topic
   and the link only.
5. **Turning notifications on.** Recommended: the setup turns `notify.on` on and lists the target,
   since that is what the person wants when setting one up. Or it only writes the target and asks
   first.
