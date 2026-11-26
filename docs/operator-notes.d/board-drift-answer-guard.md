- **The answer guard (board-drift step 6, spec §8, check `person-unanswered`).** A directive the person typed in
  busmon (`human=yes`, `typed=yes`, from `identity.as`, not `wake=no`) that its agent has not answered on the bus
  (a `reply` or a `report --to <person>` after it) for `dispatch.answer_after` (default 10m, 0 = off) warns and
  wakes the agent once with the answer's command (mark `answer-wake:<agent>`); past twice that the person is told
  once (`answer-person:<agent>`, a notice that rings as a `blocked` event: "Answer guard: " is not on the
  notifications' skip list). The warn itself stays In hand and never rings. One answer covers every earlier
  directive; a directive composed in the person's name (not typed) or older than the retained cmd history is not
  judged. With no `identity.as` the check reads unknown. The dispatcher never answers, types or resends.
