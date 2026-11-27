- **A plan task can carry a check (onboarding step a1).** `docs/plans/*.json` takes `check` on a task (one line, 1
  to 200 characters, no control character) and `checks: "required"` on the plan, which makes every agent task
  carry one. busmon's task page shows it in full. Nothing enforces it yet: done still derives as before. Ship
  this reader first: an older `agentbus` or busmon refuses a plan using these fields, so write none until the
  release holding it is installed.
