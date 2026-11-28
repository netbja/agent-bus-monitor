- **A plan can be adopted only through the person's yes (onboarding step b1).** `agentbus plan draft <path>@<commit> [--build <path>]`
  (master or the architect, never another agent or the person's shell) records the file and its `BUILD.md` in
  `{p}:plan:draft` and asks the person "Adopt plan …? id plan-…", with no recommended option; `plan draft show`
  says whether the yes holds, `plan draft --drop` deletes the draft. While a draft is recorded, `plan load` loads
  only that file at its commit, once the person typed yes, and refuses any other; a plan with `checks: "required"`
  loads only as an approved draft. A draft over an active plan is refused until step b2.
