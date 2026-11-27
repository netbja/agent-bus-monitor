- **Sibling steps run in series, enforced on the board** (sibling-enforce step 1). Each
  `request review --more` with a recorded base lists its branch in the request's `open_steps`. A
  review of another branch, and the target's `request done` (and `report --done`), judge every listed
  step in the agent's checkout with the dispatcher's own test (ancestor or same tree, squash, rebase;
  the remote tip; else the newer of the local tip and the recorded commit, refused when they diverge)
  and are refused while one is not
  merged; a judged merge leaves the list in the same script step, with a `step-merged` audit line.
  `--branch` is required while a step is open. The merge pass no longer closes a request whose last
  review merges with a step still listed (`parallel`): it steps it back to `accepted` (`open`).
  `agentbus request parallel <task> <reason>` (the sender or the person, never the target; audit
  `parallel`) lifts the review refusal, not done's; `--off` forgets no step. `agentbus request step
  <task> <branch> --abandon <reason>` (same people; audit `step-abandoned`) closes a step whose PR
  closed unmerged. busmon's Board and Plan rows show `parallel: <reason>`.
