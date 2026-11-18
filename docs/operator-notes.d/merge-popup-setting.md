- **A delivered PR is a For you row within seconds, and only once both reviewers approved it.**
  `agentbus deliver` refuses a head that two reviewers did not approve (`--single-approval <reason>`
  in an emergency: the reason goes in the request's body and on the bus). After a delivery it marks
  the merge queue due (`{p}:mergequeue:due`, the broker's time). The dispatcher reads the mark every
  5 s and passes at once, the 5-minute pass staying the fallback, so the PR is a row in For you
  before the person can merge it. The queue's base is the project's, as deliver's (the template's
  `[delivery] base`, else `delivery.base`). The reminder popup (`waiting.popup_after`, Shift+C)
  lists merges with the other decisions; 0 turns it off.
