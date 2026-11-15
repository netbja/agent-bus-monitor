# Login expiry and client updates

Status: draft r6, 2026-11-15. Author: architect. Asked by the person through master (thread
1794745325922-0; scope agreed on report 1794745470782-0).

Three small slices: tell the person when an agent's login **has expired** (all four clients), warn
them **before** a Claude Code login expires, and show one line in busmon when a **newer Codex** is
published. Constraints from the person: simple, fast, reliable. **No credential file is read, no
poller is added, nothing is typed into a pane.**

## 1. What exists, what is missing

| Fact | Today (main 42e20e52) |
|---|---|
| A login that has expired | Nothing. The api-recovery spec (2026-10-22, rule 3e) specs an `auth` notice; no code for it exists (no `login` or `authentication_failed` match in `dispatch/`, `agentstate/`, `quota/`). An expired agent reads as a stuck agent. |
| A login about to expire | Nothing. Only Claude Code warns ("Your login expires in N days · run /login to renew"); Codex, Grok and agy refresh silently and fail only on revocation or a failed refresh. |
| A client updated on disk, agent still on the old one | Built: `clientfp` + `dispatch/clientupdate.go`, sentinel reloads (resume) under reload policy `on`. Claude Code updates itself, so its cycle is complete. |
| A newer client published, not installed | Nothing. Codex (npm), Grok (`grok update`), agy (`agy update`) need the person's install step, and nothing tells them. |

## 2. After expiry: one notice, all four clients

This slice is api-recovery's rule 3e on its own, without the rest of that spec (no try counting,
no outage tracking).

### 2.1 Sources, structured before screen

| Client | Source | Why it cannot be an agent's text |
|---|---|---|
| Claude Code | its transcripts, the scan `quota.transcriptRejections` already makes (`~/.claude/projects/*/*.jsonl`, files touched in the last 6 h, 256 KiB tail): an entry with `isApiErrorMessage: true`, `error: "authentication_failed"` and model `<synthetic>` | both fields are set by the client, never by the model; the transcript is not a credential |
| Codex, Grok, agy | the screen, from a captured fixture (2.3); a client-written session log is preferred if the fixture work finds one with a client-set error field, as for Claude | the pattern carries the client's own error style, proven by a spoof fixture (2.2) |

Claude's expiry is one fact for the host's account, not per pane: every Claude agent shares
`~/.claude/`. It holds until a newer **served** answer appears in any transcript (the same
`servedAt` rule the limit backstop uses): a /login in any session clears it.
An expiry followed by no Claude activity leaves the 6 h scan window: it is not re-notified the next
day, which is accepted (one notice already went out, and any new turn writes a fresh error entry).

### 2.2 Screen rule (Codex, Grok, agy)

A new profile list, `[signature] auth = [...]` in `agentstate/clients/<client>.toml`, compiled like
`blocked`. A read counts only when **all** hold:

- the pattern matches within the **raw** bottom region: the same last non-blank lines as
  `bottomLines` (blankness judged on the stripped text, as there), returned **without** `Strip`, so
  the escape sequence below can match (`bottomLines` itself strips: foureyes on r2), above the input
  box;
- the pattern includes the client's own **error style**, the raw escape sequence (before `Strip`)
  the client applies to its error lines as the fixture shows it. A glyph or any printable prefix is
  never enough: a model can print it (reviewer-codex on r1);
- the style is proven unforgeable for that client by a **spoof fixture** (2.3): the same client,
  asked to print the exact error line, its glyph and a literal escape sequence for that style,
  renders a screen the pattern does not match (the client escapes, strips or restyles model
  output). **If the fixture shows no error style, or the spoof fixture matches, that client gets no
  `auth` pattern** (no loosened pattern): its only signal stays the stuck agent, as today;
- **the error is the client's last output**: between the matching line and the input box's top
  there is only blank space. The box's top is the profile's own: the rule above the prompt line
  when `rule_above = true` (Claude), else the prompt line itself (Codex's `›`, as `inputRegion`
  finds it). Any non-blank line in between counts as later output, the client's own status lines
  included: no profile names them today, and a missed notice is the safe failure (foureyes on r4). A ready box is *not* a veto, unlike `BlockedOn`: after an
  auth failure the client returns to an empty, ready box under the error, which is the very case to
  catch. An old error followed by any later answer, tool line or message is no longer last and
  never matches (reviewer-codex on r3);
- **not already reported**: the dispatcher keeps, in memory, per agent, the pid and a hash of the
  matched raw region it last notified for; the same pid with the same region is not counted again.
  So an error left on screen after the person's /login (nothing printed since) notifies once, not
  each day. **The entry is cleared by the first settled read of that agent that does not match**
  (the error is no longer the last output, or the pane shows no error): that ends the episode, so a
  later expiry of the same long-lived process, even with the same text, is a new non-match→match
  episode and notifies (reviewer-codex on r5). A restarted dispatcher may send one more: accepted;
- two settled reads in a row, as the `Blocked` signature (`Episode.Step`'s rule).

A misread costs one notice, nothing else: nothing is retried, held, restarted or typed.

### 2.3 Fixtures

Captured into `agentstate/testdata/screens/<client>-auth-*.txt`, the same way as the other screens.
A "not logged in" screen can be made without touching the person's login: launch the client with an
empty config home in a scratch pane (`CODEX_HOME=<empty dir> codex`; `HOME=<empty dir>` for Grok and
agy). A mid-session expiry screen is captured at its first real occurrence and added then. With
each, a `<client>-auth-spoof.txt`: in a logged-in scratch pane the client is asked to print, as its
answer, the captured error line verbatim with its glyph, then the same line wrapped in the literal
escape sequence of the error style. The fixture README records which case each file is.

### 2.4 Notice

`FencedNotifyOnceForPerson`, `for=person`:

- mark `login-expired:<client>`, episode the date (`YYYY-MM-DD`) of Redis `TIME` in the
  dispatcher host's local zone (a once-mark episode, not a persisted time): at most one notice
  per client per day per project, however many panes show it;
- text, Codex, Grok, agy: `<client> login expired (seen on <agent>[, <agent>…]): run /login in one <client> pane; restart a pane that still fails after it.`
- text, Claude Code: `Claude Code login expired (seen <HH:MM> in a Claude session; the login is the account's, so every Claude agent is affected): run /login in one Claude pane; restart a pane that still fails after it.`
  No agent is named: the transcript scan is account-wide and no transcript is mapped to an agent.
  An expiry seen in a Claude session outside the bus notifies too, rightly: the login is shared.
  (Codex: `codex login`; Grok: `grok login`; agy: as its fixture says.)

The dispatcher's wakes do not change: waking an expired agent costs one failed turn, and adding a
hold would add a state to clear.

## 3. Before expiry: Claude Code only

- Profile key `[signature] login_expiry = '<regex with a (?P<days>\d+) group>'`, Claude only.
- **Status area only**: the pattern is matched on the lines **below the input box's end**
  (`box_end`), where the client draws its own notices and never the conversation, so an agent
  printing the sentence (this repository contains it) never matches. A screen whose box cannot be
  bounded is not read for it. This is the rule the person set for `limit` (run-budget r6).
- **Fixture first.** Before any code, the person's next banner is captured
  (`claude-login-expiry.txt`). If it shows the banner anywhere but below the box (the welcome header,
  for instance), this slice is **not built** and master is told; the pattern is never widened.
- Read on the screens the dispatcher's pass already captures; no extra read. A pass that reads no
  Claude pane sees nothing: the banner lasts days.
- Notice: `FencedNotifyOnceForPerson`, mark `login-expiry:claude-code`, episode the host's local
  date (Redis `TIME`, host zone, as 2.4): **one per day**. Text: `Claude Code login expires in <N> days (seen on <agent>): run /login in one Claude pane at a quiet moment; restart a pane that then fails auth.`

Not done: reading `~/.claude/.credentials.json` or any other client's auth file (a credential; and
its stored expiry is probably the short access token's, not the login's); running `codex login
status` or any client (clientfp never runs a client).

## 4. A newer Codex: one line in busmon

- Next to busmon's daily release check (`cmd/busmon/update.go`), with the same pattern and the same
  opt-out: `updates.check` (and `AGENT_BUS_NO_UPDATE_CHECK=1`), a 24 h cache file
  (`${XDG_CACHE_HOME:-~/.cache}/agentbus/client-codex.json`), a 3 s budget, silent offline, never on
  the UI path.
- Published version: `GET https://registry.npmjs.org/@openai/codex/latest`, field `version` only. The
  request carries no personal data.
- Installed version: the `Label` of a Codex agent's `client_fp` in `{p}:agents` (package.json's
  version). No Codex agent, or no label: no check, no line.
- Shown when published is newer (`buildinfo.Newer`; the coder checks it parses a bare `0.170.0`, else prefixes `v`):
  the header's update item and the About page carry
  `codex <new> available (installed <old>): npm install -g @openai/codex@latest`.
- Never an install, by busmon, the dispatcher or an agent. After the person installs, the existing
  fingerprint flow (1) reloads the Codex agents.
- Grok and agy: no line. No documented public version endpoint is known; asking means running the
  client. Added only if a stable endpoint is found. Claude Code: nothing (it updates itself).

## 5. Settings

None new. `updates.check` gates 4; 2 and 3 are notices with once-marks and need no switch.

## 6. Tests

- Transcript auth: an `authentication_failed` error entry reads expired; a served answer newer than
  it clears it; an older one does not; a text entry whose content says "Login expired" without the
  error fields reads nothing; a file older than 6 h is skipped.
- Screen auth, per client with a fixture: the fixture matches on two settled reads; one read does
  not; the spoof fixture (agent text carrying the exact line, glyph and escape) does not; a
  fresh error directly above a ready empty box **does** match (the real case); an old error line
  followed by a later answer, then the ready box, does not (that exact fixture through the auth
  matcher); the same pid and region on the next day does not notify again; a new pid with the same
  screen does; the same pid after recovery (a settled non-matching read) and a later expiry with the
  same region notifies again.
- Banner: the fixture matches below the box with N parsed; the same sentence above the box (agent
  text) does not; an unbounded box reads nothing.
- Notices: two passes the same day send one notice per client; the next day sends again; ten panes
  in one pass send one notice naming them.
- busmon: newer published → line; equal or older → none; offline → none and cached for the day;
  `updates.check` off → no request (fake transport counts calls); no Codex agent → no request.
- Profile parse: `auth` and `login_expiry` compile; `login_expiry` without a `days` group is a
  build error.

## 7. Out of scope

- The rest of api-recovery (transient retries, outage).
- Any automatic /login, token refresh, or `claude setup-token` (a long-lived token would remove the
  expiry for agents; that is the person's choice about credentials).
- Installing any client, and "update available" for Grok, agy, Claude Code.
- De-duplicating notices across projects on one host: each project's dispatcher sends its own (one
  per day each).
