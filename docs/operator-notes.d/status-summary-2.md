- **`agentbus summary` (status-summary step 2, §3.1, §3.3): where the project stands, read
  only.** `agentbus [--project p] summary [--all] [--json]` prints one line per section: For you
  (counts per kind from `attention`, the definition busmon's For you uses, plus the reports of the
  last 24 hours that ask for the person, never "unread"), Merge (each approved head, its CI red
  checks, or "CI not read (ci.loop off)", never "green"; "not computed" while the dispatch
  service has not computed the queue), Blocked (count, the first two with owner and reason, `+N
  more`), Plan (done/total, in progress, the first task waiting for the person, or "no plan
  loaded"), Team (counts by state, `0 failed` shown; anomalies from the `agent-launch`,
  `self-repair` and `idle-implementer` health records and the quota holds, never recomputed),
  Health (fail, then warn), then the sources line `↳ …`. A section that cannot be read is `? <reason>`
  and the exit is 1; usage is exit 2. Lines are clipped to the terminal, or 100 columns. `--json`
  carries `"v": 1`; `--all` needs no project and puts the projects waiting on the person first.
  The observer may run it. `agentbus status` with fewer than two positionals is now a usage
  error, exit 2, pointing to `summary`.
