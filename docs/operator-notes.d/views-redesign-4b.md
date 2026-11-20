- **A plan task's priority is the person's, on the broker (views-redesign step 4b).** `agentbus plan priority <task>
  high|normal|low --as <person>`, or `+`/`-` on a plan task in busmon (the Plan tab, the drawer `p`, a task's page),
  records it with the plan's facts in `{p}:plan:facts` (one script; `normal` is stored too, so it wins over the plan
  file's `priority`; the task's fact time is kept, since a priority is no progress; a reload keeps it while the task
  stays in the plan). The Plan reads the person's level over the file's. An agent is refused, as for `request
  priority`, and `roles.toml` denies `Bash(agentbus plan priority*)` to agents as a second layer. `request send`
  starts the request for that task at its level, in the same script: the plan task is the one whose `board` field
  names the request's task, else the one with its id and no `board` field. A request already sent keeps its own
  level (the command says so and prints the `request priority` line that moves it; nothing for a done request). The
  v0.29 plan's three `LOW:` titles are now `"priority": "low"`: a running plan takes it at its next `plan load`.
