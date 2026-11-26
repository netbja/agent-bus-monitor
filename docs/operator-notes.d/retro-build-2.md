- **A retro's proposals reach the person (retro step 2, spec §4-§7).** The retro's owner files it with
  `agentbus retro propose <task> --file <retro.md>`: a `Root cause:` line and a table `n | lesson | evidence |
  destination | file | change`, 1 to 3 rows. Each evidence id must be in a stream, the verdict ledger or the
  history archive; each file under `skills/<name>/SKILL.md`, `roles/<name>.md` (destination `skill`), `AGENTS.md`,
  `CLAUDE.md`, `docs/memory/lessons/<note>.md` (`context`) and on `origin/<delivery base>`, or absent there for a
  `new skill`; no field may match a credential shape (the refusal names the field, never the value); a row equal to
  a rejected one (file, lesson in lower case with spaces collapsed) is refused (`agentbus retro rejected --file
  <f>`); over `retro.max_per_day` (project setting, default 3) the next is refused: close it with `retro none`. A
  target past `retro.prune_lines` (default 400) adds a note to the ask. The record goes to `{p}:retros` with each
  file's blob id, and its ask opens from the reserved author `retro` (one open at a time, the rest `queued`; never
  master's slot): one question `p<n>` a proposal, `yes`, `no` or `later`, the answers to master, woken. Only the
  person's typed `yes` approves (`RetroApproved`: the answer's reply now carries `qid`, so a yes binds to its
  question; while the ask's record is kept its choice must be yes too; the reply is read from the cmd stream, then
  the history archive, so one trimmed from the stream or reset away still counts; a yes neither proves is
  `unverified`: not applied, and never put on the rejected list; an unreadable archive stops route, it never
  rejects). Master runs `agentbus retro route <task>` once
  the ask closed (or after `retro withdraw <task>`): outcomes recorded, `no` rows stored as rejected,
  `retro-apply-<task>` sent to docs, the next queued ask opened. docs reads `agentbus retro show <task>` and ends
  with `agentbus retro close <task> --pr <n>` (docs alone closes it). `agentbus ask` refuses the author `retro`, and no role may take the
  name.
