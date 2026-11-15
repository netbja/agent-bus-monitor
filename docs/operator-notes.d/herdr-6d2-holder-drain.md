- **`agentbus holder drain <project> [--ignore-background]` (herdr exit step 6d2, `holderdrain/`).**
  Finishes an older holder build's drain on the person's word, never automatically. A shell that
  carries an agent's identity (`AGENT_BUS_AGENT` or `AGENTBUS_TERM_ID`) is refused. Nothing is done
  while any holder socket of the project does not answer (its terminals would be unseen). Each agent
  whose live terminal is on an old build, in name order, one at a time, is moved by the reload
  protocol (`agentbus reload`'s, on the holder):
  1. the reload intent, so the dispatcher neither wakes nor clears it, and a wake being typed is
     waited out (10 s);
  2. the same idle process re-checked;
  3. the old terminal closed and its program gone, so two clients never run one session;
  4. the new terminal started on the current build with its `{p}:resume` session;
  5. its proof within 3 min: a verified status whose pid and start time are the new terminal's
     program or one of its descendants;
  6. the swap ledger's `reloaded` entry and a `relaunch` event.
  A start that fails or a missing proof stops the drain (exit 1); the ledger records it with the way
  back (`agentbus up <p> --resume`), and an unproved new terminal is left running. An agent that
  declares or shows `working`, is `blocked`, has a person active, a typed unsent line, an
  unreadable terminal, a swap in progress, a process the bus does not prove in that terminal, or
  background work running or unknown (spec 4.3b, judged over a 10 s CPU window after a first look)
  is skipped and named; exit 3 means "run it again later". Codex, Grok, Kimi and agy have no
  authoritative background source, so they always read unknown: the person drains them with
  `--ignore-background`, which lifts that check only. Only when every holder still answers and
  nothing is left on an old build does it run `service gc` and write `holder-drained`.
  `termhost.Term` now carries the terminal's client profile (`Profile`), which the look judges with.
