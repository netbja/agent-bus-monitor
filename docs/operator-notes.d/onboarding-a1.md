- **A plan task can carry a check (onboarding step a1).** `docs/plans/*.json` takes `check` on a task (one line, 1
  to 200 printable characters: no control, format or bidi character) and `checks: "required"` on the plan, which makes every agent task
  carry one. busmon's task page shows it in full, and says so when it cannot draw a character as written. Nothing enforces it yet: done still derives as before. Ship
  this reader first: an older `agentbus` or busmon refuses a plan using these fields, so write none until the
  release holding it is installed.
