- **Login expiry, part 2: the check and the warning (login-updates r9 §3.4-3.5,
  `dispatch/loginexpiry.go`).** The dispatcher reads each Claude Code agent's expiry with
  `loginexpiry.Read`, from the agent's own `claude_config_dir` (`~/.claude` when unset): once an
  hour, once a minute for a directory whose login has expired, and once per pass for agents
  sharing a directory. Only clients whose profile declares `[login] source` are read (Claude Code).
  Per agent: `ok`, `warn` (within `dispatch.login_expiry_warn`, default `72h`; `0` turns warnings
  off), `fail` (passed) or `unknown` with the reader's reason. A `warn` tells the person once per
  agent and expiry (`login-expiry:<agent>`, episode the expiry in ms):
  `coder's Claude login expires Thu 15:40 (2 days): run /login in its pane.` The new `login-expiry`
  health check (every 5 minutes) reports the loop's last results, worst first, and reads no file;
  it never notifies the person. An expired login holds its agent (part 3, `login-expiry-3a.md`).
