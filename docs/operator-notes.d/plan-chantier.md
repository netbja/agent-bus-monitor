- **A plan grouped by chantier (plan-chantier).** `docs/plans/<name>.json` may list `groups` once (`{id, title}`, at
  most 12, the id a `bus.ValidName`, the title one line of 1 to 40 characters) and give a task `group`: each named
  group is listed, and a listed group with no task is refused (a typo would hide it). busmon's Plan board then
  draws one block per group instead of NOW, NEXT and LATER: a header (title, a bar of done cells then approved
  rows still in review, `done/total` counting Done only, `in progress: <id>` by `planInHand`'s rule on the group's
  rows, `waits for you: <id>`, the latter kept when the header is too wide; narrow: the title and the count), its
  open rows in board order, done rows folded into one DONE line until `d` shows them under their group. Order: a
  group waiting for the person, then one with a task in hand, then the rest in list order, `Other` (no group), then
  finished groups as one line. Unknown reads and plans without `groups` draw as before. `plan.Parse` refuses
  unknown fields, so an older `agentbus plan load` refuses a grouped plan and an older busmon fails to parse one:
  add `groups` to a plan only once a release carrying this is installed.
