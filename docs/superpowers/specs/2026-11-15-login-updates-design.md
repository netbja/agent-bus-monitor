# Login expiry and client updates

Status: r8, 2026-11-15. Author: architect. Asked by the person through master (thread
1794745325922-0; scope agreed on report 1794745470782-0). r7: §3 rewritten on the person's decision
(thread 1794762002143-0).

**What stays from r6 (merged, PR 523):** §2, the after-expiry notices (rule 3e, all four clients;
not built yet), unchanged. §4, the Codex update line, unchanged (built, PR 527). §3, the banner
notice, is **replaced** by r7's §3 (never built).

Three small slices: tell the person when an agent's login **has expired** (all four clients), warn
them **before** a Claude Code login expires, and show one line in busmon when a **newer Codex** is
published. Constraints from the person: simple, fast, reliable. **No credential is read except the
one expiry field of §3 (r7), no poller is added, nothing is typed into a pane.**

## 1. What exists, what is missing

| Fact | Today (main 42e20e52) |
|---|---|
| A login that has expired | Nothing. The api-recovery spec (2026-10-22, rule 3e) specs an `auth` notice; no code for it exists (no `login` or `authentication_failed` match in `dispatch/`, `agentstate/`, `quota/`). An expired agent reads as a stuck agent. |
| A login about to expire | Nothing. Only Claude Code warns ("Your login expires in N days · run /login to renew"); Codex, Grok and agy refresh silently and fail only on revocation or a failed refresh. The banner shows only at an interactive start (the person, r7); `.credentials.json` holds the login's expiry (§3). |
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

## 3. Before expiry: Claude Code, from its credentials' expiry field (r7)

r7 replaces r6's banner notice. The person decided it (2026-11-15, thread 1794762002143-0): the
CLI shows "Your login expires in N days" only when an interactive session starts, so an agent that
has run for days never shows it. This also lifts, **for this one field only**, r6's rule that no
pass reads a credential file.

### 3.1 Source

- `claudeAiOauth.refreshTokenExpiresAt` in `.credentials.json` under the agent's Claude config
  directory (3.2). **Verified 2026-11-15 on Linux** by extracting only that field
  (`jq '.claudeAiOauth.refreshTokenExpiresAt'`, the file never printed): a JSON number, **Unix
  milliseconds**, 2.5 days ahead that day, which matches the CLI's "expires in 3 days".
  `expiresAt` beside it is the short access token's and is never read.
- The field comes from no official documentation. If it is missing or not a number, the status is
  `unknown` with that reason, never a guess. **A value implausible as milliseconds** (before
  2020-01-01, or more than two years after now) is `unknown: not a time`, never `fail`: should the
  CLI ever write seconds, no agent is held (foureyes on r7).
- **macOS**: the credentials are in the Keychain. Read-only:
  `security find-generic-password -s <service> -w`, its output parsed for that one field like
  the file. The service name (`Claude Code-credentials` for the default directory, believed to
  carry a suffix for another `CLAUDE_CONFIG_DIR`) is **verified on a Mac before the build**; until
  then the macOS reader reports `unknown: keychain entry not verified`. A Keychain access dialog
  or a refusal under launchd is `unknown` with its reason, and is not retried before the next
  hour.
- Windows is out of the project (the person, 2026-11-11). The file reader would work there as is.

### 3.2 Each agent's config directory

- The agent's own `CLAUDE_CONFIG_DIR` is read from its environment by both writers of the
  identity, exactly where `client_fp` is: the status publish (`cmd/agentbus/main.go`) and `bind`
  (`cmd/agentbus/bind.go`, from the pane's environment), so that a bound agent is not left on
  `~/.claude`. It goes into a new snapshot field, `claude_config_dir`, which is set only with a
  proof, like `client_fp`. Unset means the default, `~/.claude`. Agents
  may use different directories.
- The dispatcher uses the path only when it is absolute and clean, and the file is a regular file
  owned by the dispatcher's own user. Anything else is `unknown`. Agents that share a directory
  are read once per pass.
- The agents' processes run on the dispatcher's host (local panes), so the path is a local one.
- `/proc/<pid>/environ` is never read: it would hold every other secret in the agent's
  environment.

### 3.3 Reading only that field

- One function per platform, returning `(expiry time, reason)`.
  - It reads at most 64 KiB and decodes into a struct holding only `refreshTokenExpiresAt`.
  - The reason is from a closed set: `no file`, `not owned`, `unreadable`, `no field`,
    `not a number`, `not a time`, `keychain: <exit status>`.
  - It never wraps or prints the decoder's error, the file's bytes or the Keychain's output. Those
    bytes stay in the function's local buffer.
- **Never logged, copied, stored or put on the bus**: any token, any other field, the file's
  content. What leaves the function is a number and a reason. What leaves the dispatcher is the
  agent's name, the expiry time and the status.

### 3.4 The check

- **The dispatcher reads the field itself**, in its own loop (a new `dispatch/loginexpiry.go`,
  holding the lease token), because the notices and the hold are fenced writes and a health check's
  `Run` gets no token and never writes (foureyes on r7). Cadence, per config directory: **once an
  hour**, and **once a minute for a directory whose agent is held** (3.6), so the release follows the
  person's /login within a minute. There is no new service, no systemd timer and no cron.
- `now` is Redis `TIME`, for the status, the hold, the episode and what busmon shows.
- The results stay in the loop's memory as structured per-agent values (agent, status, expiry,
  reason). 3.5 and 3.6 act on those values, never on a text.
- A health check, `login-expiry` (`Interval: 5m`), **reports** them. It reads no file; it gets the
  loop's last results through a `HealthConfig.LoginExpiry` function, as `StallNotify` passes
  values the other way.
- It looks at every agent whose client is Claude Code. Each client's profile declares whether it
  has a fixed login expiry, as `[login] source = "claude-credentials"` in
  `agentstate/clients/claude.toml`. No other profile declares one, so Codex, agy, Kimi and Grok
  are not checked (they refresh themselves). A later client opts in with its own source.
- Per agent:
  - `ok`: the expiry is beyond the threshold.
  - `warn`: the expiry is within `dispatch.login_expiry_warn` (default 3d).
  - `fail`: the expiry has passed.
  - `unknown`: the reason from 3.3.
- The check's own status is its worst agent's status. Its detail lists each agent with its exact
  expiry in the host's zone (`coder: Thu 19 Nov 15:40 (warn)`).
- The definition sets `Person` to none. The person hears through 3.5, once per agent, never again
  through the health notices.
- `dispatch.login_expiry_warn = 0` turns warnings off: no `warn`, no warning notice. `fail` and
  its hold (3.6) remain, because an expired login stops the agent whatever the setting.
- An expiry can be noticed up to an hour late. §2's after-expiry notice (the transcript) covers
  that hour.

### 3.5 Notices: one per agent and episode

`FencedNotifyOnceForPerson`, on the person's channels (ntfy, Discord) as today. The **episode is
the expiry value**, so a new /login that gives a later date starts a new one:

- warn, mark `login-expiry:<agent>`:
  `coder's Claude login expires Thu 15:40 (2 days): run /login in its pane.`
- fail, mark `login-expired:<agent>`:
  `coder's Claude login expired Thu 15:40: run /login in its pane. Its work is held (master told).`

§2's account-wide transcript notice can also fire on an expiry. The two texts say the same thing
and each fires once, which is accepted.

### 3.6 Expired: a hold for that agent

The agent is treated like a screen hold (run-budget §4), but per agent, not per provider: other
Claude agents with another directory still work.

- On `fail`, a fenced field `hold-login:<agent>` is written in `{p}:quota`. Its value is the expiry
  (ms).
  - While it exists, the dispatcher sends that agent **no wake**, the same skip as a held
    provider.
  - The dispatcher writes `{p}:held` for it (run-budget §4's "What a held agent had", with
    provider `login`).
  - Master gets one directive per episode (mark `run-held:<agent>`, episode the expiry):
    `coder is held (Claude login expired Thu 15:40, released after /login): accepted wiki-3
    (feat/wiki-3). Reassign it, or wait.`
- **Release**: the field reads a later expiry that is still in the future. The hold field and the
  `{p}:held` field are deleted, and the agent gets the resume directive that `release` sends
  today. `retire --forget` also deletes both.
- Master expired: master gets no wakes, and the person's fail notice names master. There is no
  project pause: other agents finish what they hold. §2's transcript notice may reach the person
  first; the two say the same thing.
- **Every reader that excuses a held agent learns the login hold** (foureyes on r7). Today each one
  matches only `hold:<provider>`. A new `bus.HeldReason(marks, agent, provider) string` returns
  `quota` (from `hold:<provider>`), `login` (from `hold-login:<agent>`) or `""`, and each reader uses
  it:

  | Reader | With a login hold |
  |---|---|
  | the dispatcher's wake skip (where `l.quota.held` is read) | no wake |
  | `cmd/agentbus/stallchecks.go` `heldProviders` (stall checks, `team-stall`, `quiet-agent`, `idle-implementer`, `idle-master`) | excused, as a quota hold |
  | `stallchecks.go` around `dispatch.CannotAct` | the agent counts as held, so cannot act |
  | `cmd/agentbus/selfrepairchore.go` (relaunch decision) | **no relaunch**: a new process would hit the same expired login |
  | `dispatch/popfacts.go` (`f.Held`) | `Held = true`: a held pop is never judged done |
  | `cmd/busmon/quota.go` | a line `coder: Claude login expired Thu 15:40, held` |
- If run-budget §4 is not built first, this step builds `{p}:held`'s writer as that spec defines
  it. It is one function, which run-budget then reuses.

### 3.7 Not done

Running `claude` or any client to ask its login state; refreshing a token; reading `accessToken`,
`refreshToken`, `expiresAt` or any other field; reading another client's auth file.

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

- `updates.check` gates §4. §2 needs no switch.
- New (r7): `dispatch.login_expiry_warn` in `config/keys.go`, a Duration, default `72h`, `0` = no
  warning (3.4). Its env is `AGENT_BUS_LOGIN_EXPIRY_WARN`, `ReadBy: dispatch`, and it gets a row on
  busmon's C page (`Login expiry warning`).

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
- Login expiry (r7), with fixture credential files written by the test:
  - far future → `ok`;
  - 2 days → `warn` and one notice, none on the next pass;
  - expired → `fail`, a hold field, a `{p}:held` record, one directive to master, no wake;
  - a later date → a new episode, the hold released, the resume directive sent;
  - a missing file, a missing field, a string value, a file owned by another user, a relative
    path → `unknown` with that reason;
  - `0` → no warn and no warning notice, while `fail` still holds;
  - two agents sharing a directory → one read;
  - a non-Claude agent → not checked.
- Unit: the value is read as milliseconds; a seconds-sized value, a value before 2020 and one more
  than two years ahead are each `unknown: not a time`, and none holds an agent.
- Cadence: a held agent's directory is re-read within a minute, and a later future expiry releases
  it then; an unheld one is read once an hour; `now` comes from the fake broker's `TIME`.
- Readers, one test each with only `hold-login:<agent>` set: no wake; no stall alarm (team-stall,
  quiet-agent, idle-implementer, idle-master); `CannotAct`; no self-repair relaunch; a held pop
  is not retired; busmon's quota page shows the line.
- The health check reports the loop's results and opens no file (a fake reader counts calls).
- **No token anywhere**: the fixture's `accessToken` and `refreshToken` hold sentinel strings. After
  ok, warn, fail, release and every `unknown` case, the sentinels appear in none of these: the
  captured log, every stream entry and hash field the fake broker holds, the health detail, the
  notices, any error's text. The same check runs on the macOS reader with a fake `security` that
  prints a sentinel-filled JSON.
- macOS: a fake `security` that exits non-zero or prints nothing → `unknown: keychain …`; an
  unverified service name → `unknown: keychain entry not verified`.
- Notices: two passes the same day send one notice per client; the next day sends again; ten panes
  in one pass send one notice naming them.
- busmon: newer published → line; equal or older → none; offline → none and cached for the day;
  `updates.check` off → no request (fake transport counts calls); no Codex agent → no request.
- Profile parse: `auth` compiles; `[login] source` accepts only a known source; only claude.toml
  declares one.

## 7. Out of scope

- The rest of api-recovery (transient retries, outage).
- Any automatic /login, token refresh, or `claude setup-token` (a long-lived token would remove the
  expiry for agents; that is the person's choice about credentials).
- Installing any client, and "update available" for Grok, agy, Claude Code.
- Windows (out of the project), and other clients' login expiry until a profile declares a source.
- De-duplicating notices across projects on one host: each project's dispatcher sends its own (one
  per day each).

## 8. Footprint (r7, for the build)

- `bus/stream.go`: `AgentIdent.ClaudeConfigDir` and the `claude_config_dir` snapshot field, set
  with a proof. Also `bus/ident.go` and `bus/handoff.go`, where `ClientFP` is carried.
- `cmd/agentbus/main.go` (status publish) and `cmd/agentbus/bind.go`: they read `CLAUDE_CONFIG_DIR`.
- `agentstate/profile.go` and `agentstate/clients/claude.toml`: the `[login] source` key.
- A new reader package, `loginexpiry/`: the file and Keychain readers of 3.3, pure, with their tests.
- `health/health.go`: the `login-expiry` definition.
- `dispatch/loginexpiry.go`: the reads, their cadence, the notices (3.5), the hold and the release
  (3.6); `HealthConfig.LoginExpiry`; the wake skip; `{p}:held`'s writer if run-budget §4 is not
  built first.
- `cmd/agentbus/healthchecks.go`: the check, reporting the loop's results.
- `bus/`: `HeldReason`; its readers `cmd/agentbus/stallchecks.go`, `cmd/agentbus/selfrepairchore.go`,
  `dispatch/popfacts.go`, `cmd/busmon/quota.go`.
- `config/keys.go`: `dispatch.login_expiry_warn`.
- `cmd/busmon` settings: the C page row.
- `docs/operator-notes.d/login-expiry.md`: one bullet.

