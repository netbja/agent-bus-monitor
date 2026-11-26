- **Retros are in the skills now, and busmon starts one (retro step 3).** The agent-bus skill says how an agent runs
  `retro-<task>` (evidence, root cause, at most three proposals, `retro propose` or `retro none`), the master skill
  how master routes a retro once its ask closes (`retro route`, `retro withdraw`; an ended pop's retro comes to
  master), and the docs skill how docs applies the person's approved rows (`retro show`, one commit per proposal
  with `Retro:` and `Approved-By:`, the record under `docs/memory/retros/`, `retro close`). In busmon, `/retro
  [task]` runs `agentbus retro request` after y, on the task named or the request selected on Requests.
