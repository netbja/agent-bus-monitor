- **`agentbus run --cap -- <command>` runs a command under a memory and a time cap** (agent-resource-limits
  step 2a). It needs no project and must come first on the line (`agentbus -p x run --cap` is refused):
  every word after `--` is the command's own. The command runs in its own process group with
  `oom_score_adj` +500; at the time cap the whole group is killed (exit 137, `killed at time cap`). The
  memory cap is `RLIMIT_AS` inside a holder or without a user manager (virtual memory: a failing
  allocation, not a kill, so `go test -race` needs `--mem off`, allowed only when the person set
  `limits.command_memory = "off"`), and a `systemd-run --user --scope` in a shell no holder runs. The
  defaults are the person's `limits.command_memory` (8G), `limits.command_time` (30m) and
  `limits.command_cpu` (0, none) in `config.toml`; `--mem`, `--time` and `--cpu` only narrow them.
