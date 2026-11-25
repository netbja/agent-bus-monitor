- **The audit trail, step 1: the stream and its reader (audit spec, the person's Q1 90 days, Q3 B hash
  chain).** `{p}:audit`, one writer `bus.Audit`: a closed action list (approve, answer, merge, push,
  deliver, force, ci-handoff, update, reinstall, quota-pause, pause, resume, suspend, retire,
  retro-apply), outcome ok, refused or failed, an actor that is a bus name, target, ref and a detail
  of at most 300 runes, no control character; anything else is refused and nothing written. Target,
  ref and detail are each replaced whole by `[redacted]` on a credential shape, the line still
  written. Each entry carries the previous entry's hash (SHA-1, computed in the append script, so
  concurrent writers chain in order); `agentbus audit verify` checks the chain (exit 3, naming the
  entry that no longer follows) and says what it cannot see: a cut of the tail, and the head the
  retention trims. Every append trims by age, `MINID` at Redis `TIME` minus `audit.retention`
  (90 days; the person's alone, config.toml: an agent must not shorten the trail). The stream
  survives `busmon --reset`, is a record kind (backups keep it), `--delete` removes it. A trail, not
  a proof: any user allowed to append may trim with `XADD … MAXLEN 0` (the ACL suite asserts it).
  `agentbus audit [--action] [--actor] [--since] [--json]`, `audit export --since <d>` (JSON Lines,
  oldest first). Nothing writes to it yet: the call sites are step 2, busmon's list step 3.
