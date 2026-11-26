- **`agentbus run --cap`'s command starts with its soft open-file limit raised toward the hard one**
  (agent-resource-limits §6, Linux). The helper execs it with a raw `execve` so that nothing allocates
  under its memory cap, so Go's restore of the original `RLIMIT_NOFILE` soft limit is skipped; an uncapped
  command keeps the original. `TestSkillGoTestLinesAreCapped` now also holds `docs/memory/PROTOCOL.md`.
