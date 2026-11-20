- **A plan task's priority is the person's, on the broker (views-redesign step 4b).** `agentbus plan priority <task>
  high|normal|low --as <person>` records it with the plan's facts in `{p}:plan:facts` (one script; `normal` removes
  it; the task's fact time is kept, since a priority is no progress; a reload keeps it while the task stays in the
  plan). An agent is refused, as for `request priority`. `request send` starts the request for that task at its
  level, in the same script: the plan task is the one whose `board` field names the request's task, else the one
  with its id; the person's level wins over the plan file's `priority` (step 4a's field). A request already sent keeps its own level
  (the command says so and prints the `request priority` line that moves it). busmon's `+`/`-` on a plan task is
  step 4b's second part, after the Plan tab merges.
