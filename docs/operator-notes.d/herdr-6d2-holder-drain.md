- **`agentbus holder drain <project> [--ignore-background]` (herdr exit step 6d2, `holderdrain/`).**
  Finishes an older holder build's drain on the person's word, never automatically. For each
  agent whose live terminal is on a build other than the current one, in name order, one at a
  time:
  - it starts the agent's new terminal on the current build, with its `{p}:resume` session and
    profile;
  - it waits up to 3 min for the agent's proof: a verified status whose pid and start time are the
    new terminal's program or one of its descendants, as the holder reports them;
  - then it closes the old terminal (a `relaunch` event). An unproved new terminal is closed, the
    old one kept, and the drain stops (exit 1).
  An agent that declares or shows `working`, is `blocked`, has a person active, a typed unsent
  line, an unreadable terminal, or background work running or unknown (spec 4.3b, judged over a
  10 s CPU window after a first look) is skipped and named; exit 3 means "run it again later".
  Codex, Grok, Kimi and agy have no authoritative background source, so they always read unknown:
  the person drains them with `--ignore-background`, which lifts that check only. When nothing is
  left on an old build it runs `service gc` and writes `holder-drained`. `termhost.Term` now
  carries the terminal's client profile (`Profile`), which the look judges with.
