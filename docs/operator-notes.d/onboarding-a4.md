- **A person's task with a check is done only on their typed `passes` (onboarding step a4).** Ask the one question
  with `--task <task>` and the id `check-<16 hex>` (`plan.PersonCheckQID`: SHA-256 of the plan, the task id and the
  check), options `passes` and `fails`. `plan done <task>` recomputes the id from the active plan, takes the newest
  ask about the task holding it whose answer is the person's typed `passes` (`bus.TypedApproval`: the kept record
  says `passes`, not withdrawn, not the asker's own, and the typed reply entry agrees), and records the entry and
  the id; otherwise it refuses ("the person has not answered passes to its current check (id check-…)"). A record
  is kept 7 days: after that, ask again. Changing the check in a reloaded plan un-does the task (another id), and a
  board-delivered person task with a check is not done without that record. `plan load` refuses a mission task
  whose check is not its `missions.json` test, or whose `missions.json` it cannot read.
