- **Health lists lost agents (cleaning-up-agents step 1c).** A new check, `agents-lost` (the dispatcher, every minute,
  a warn in hand), names every agent without a terminal that owes something or is required, with its reason:
  `2 lost: coder2 (required: self-repair relaunches it); docs (owns skill (accepted))`. It counts those lost
  without a holder event too (a client crash, a pane closed by hand). Stopped and parked agents are never listed;
  with no holder answering nothing is judged. Until the merge-queue pass reads unpushed branches and stashes
  (step 2), an agent without a terminal that owes nothing else reads `unknown: losable work`, so it is listed.
