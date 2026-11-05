# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately through GitHub's private vulnerability reporting for this repository:
[Security → Report a vulnerability](https://github.com/netbja/agent-bus-monitor/security/advisories/new).
This is the only reporting channel; there is no security e-mail address.

Useful in a report: the affected version (`agentbus version`, or busmon's About page `A`), the
platform, the steps to reproduce, and what an attacker gains. Do not include real credentials,
broker passwords or another person's data.

## Supported versions

Only the **latest release** receives security fixes. A fix ships as a new release; update with
`./setup.sh update` (busmon shows `↑ vX.Y.Z` in its header when a newer release exists). Releases
are listed in [CHANGELOG.md](CHANGELOG.md).

| Version        | Supported |
|----------------|-----------|
| latest release | yes       |
| anything older | no        |

## Security stance

What follows is what the repository's own documentation ([README.md](README.md),
[CLAUDE.md](CLAUDE.md)) states. It is not a claim of anything beyond it.

### The broker

- **Loopback only.** `docker-compose.yml` binds Redis to `127.0.0.1:6380:6379`, so the bus is not
  reachable from the LAN. The Redis password travels in plaintext, so the bus must never be exposed
  raw over the network; an SSH tunnel is the only supported remote path (`remote-bus.sh`, README
  "Watching a remote bus over SSH").
- **The password.** `./setup.sh install` on a fresh broker writes a random password to
  `~/.config/agentbus/redis.env` (`$XDG_CONFIG_HOME/agentbus/redis.env`; mode 0600 in a 0700
  folder). Compose hands it to Redis as a config on stdin, never on a command line. A password file
  that exists but cannot be read is an error, never the default, and no error quotes its content.
- **The historical default.** An install made before the password file keeps the historical
  default password; health `redis-password` warns until it is rotated with
  `./setup.sh rotate-redis-password` (a person's act).
- **Services.** The project services read the password from the same file. Their env file
  (`~/.config/agentbus/services/<p>.env`, 0600) carries one only if the shell that ran `agentbus up`
  exported it. Never in a unit, an argument or a log.

### The bus is not a boundary against the local user

- Sender identity on the bus is what the caller's environment states (`AGENT_BUS_AGENT`); a local
  process can claim another name. The identity checks, the role checks and the 4-eyes gate are
  coordination and audit tools, not access control.
- **Tool rules are defence in depth, not a boundary.** The per-role allow/ask/deny rules a launch
  passes to an agent's client match command text only; another spelling, a wrapper or a script can
  run the same command, and processes an agent starts (tests, dotenv and similar tools) can still
  read environment files. The shipped Read/Edit denies cover common secret-file names.
- **Health is not a security boundary.** The health checks report; remediation text is static
  guidance.
- The **conductor** (`agentbus missions`) runs the missions' test commands, which is arbitrary code
  from agents' commits, as any CI does. It is the only process that runs repository code; the
  dispatcher never runs code.

### Secrets agent-bus handles

- No reader of quotas or models opens a client's credential; the trust readers return a boolean,
  never file content (`~/.claude.json` can hold an API key).
- `ANTHROPIC_API_KEY` is dropped for every launched client unless explicitly kept.
- `config.toml` holds no secret and no connection. Notification targets and their secrets live in
  `notify.toml`, which is refused whole when open to others; every role denies reading or editing it.
- Backups list secrets without archiving them, unless `--secrets` is given with an `age` recipient;
  they are then encrypted with `age`, never written in clear on disk.
- The local LLM peer has read-only tools confined to the project root, follows no redirect and uses
  no proxy; its errors never carry the endpoint's body.

### Out of scope

- Anything that requires the local user's account: as stated above, the bus and the tool rules are
  not a boundary against it.
- A broker exposed beyond loopback, against the stance above.
- The agents' own clients (Claude Code, Codex, Kimi, Grok, agy) and herdr: report those to their
  own maintainers.

## Disclosure

Please allow time for a fix to be released before disclosing a report publicly.
