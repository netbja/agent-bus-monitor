- **No more writes from `anonymous` (cmd-identity-anonymous, 2026-11-16).** From a shell, `agentbus` writes as
  `AGENT_BUS_AGENT`, else as the person: `AGENT_BUS_HUMAN`, else the name `agentbus identity set` recorded in
  config.toml. With neither, a command that writes is refused with that fix; before, it was sent `from anonymous`,
  and master's answer went to nobody (an `anonymous: no snapshot` escalation followed). `report --to`, `reply` and
  `cmd` refuse `anonymous` as an addressee. Inside an agent's terminal with `AGENT_BUS_AGENT` unset, a write is still
  refused, an inherited `AGENT_BUS_HUMAN` included. A CLI entry sent as the person is not marked `human`: busmon alone
  writes those. The cron `daily-review-trigger.sh` sends as the recorded name.
