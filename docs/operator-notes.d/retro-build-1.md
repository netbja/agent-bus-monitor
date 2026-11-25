- **`request done` may send a retro.** After a done, `agentbus request done` counts the task's friction
  (`bus.TaskFriction`: reject verdicts on the owner's work, CI hand-offs to it, `blocked` and
  `wake-not-landed` events and escalations naming it, from its accept to its done). With any, it sends
  one tracked request `retro-<task>` from `agentbus` to the owner and prints `retro due: retro-<task>
  (…)`; a green task, a repeated done and a retro's own done send nothing. The owner reads `agentbus retro
  evidence <task>` and, with no lasting lesson, closes it with `agentbus retro none <task> "<root cause>"`
  (proposals come in the next step). `agentbus retro request <task>` is the person's own trigger, refused
  from an agent's process tree; it goes to master when the owner is no longer registered. Escalations now
  carry an additive `agent` field: friction reads it, never the text. A retro's name is cut to 32
  characters for a long task.
