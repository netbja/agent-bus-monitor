- **A report names its addressees, and a reply goes to its sender (busmon-reply-target).**
  `agentbus report --to <name>` now records the addressees on the report entry itself (field `to`,
  additive; older reports have none), so busmon's page shows "To: bernard", not "broadcast". In
  busmon, `i` or `r` on a message replies to its sender even when an unsent draft to another agent
  is saved: that draft is held and opens again once the reply is sent (before, the draft opened in
  the reply's place, its recipient shown as if it were the reply's).
