- **Every `go test` the skills, `CLAUDE.md` and `docs/testing-redis.md` give runs through
  `agentbus run --cap` with its own `-timeout`** (agent-resource-limits step 2b): the suite
  `agentbus run --cap -- go test ./... -count=1 -timeout 25m`, a mutant `--mem 4G --time 5m`.
  `TestSkillGoTestLinesAreCapped` fails on a `go test` in a fenced block of those files, or in an
  inline span of a skill, without both. Needs step 2a's `run --cap` in the installed agentbus.
