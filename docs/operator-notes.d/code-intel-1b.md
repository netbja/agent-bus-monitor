- **A request's work branch, one owner per branch (code-intel step 1b).** `agentbus request accept <task> --branch <b>`
  declares the branch you work on; `agentbus request branch <task> <b>` declares it later, by the target, while
  accepted. It lands in `request.work_branch`, apart from review's `branch`. A branch another agent's open request
  (accepted, review or blocked) declares, as its work branch or its review branch, refuses the accept or the
  declaration with `branch <b> belongs to <agent>'s request <task>`, and nothing is written; the board is read in the
  same script, so two agents never both own one branch. The code-intel relay's rename (a later step) reads it.
