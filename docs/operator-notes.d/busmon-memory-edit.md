- **busmon's Memory page writes, the person's own acts (person-memory step 4, part b).** `a` opens the
  compose window: its first line `topic type importance` (prefilled with the selected topic and
  `preference standard`; `keep-both` or `supersedes=<id>` after it for a second decision on a topic),
  the rest the person's words, whitespace collapsed to one line; Ctrl+S remembers them with busmon as
  the source (`said: <date> by <you> from busmon`). A refusal (a topic that already holds a decision,
  a credential shape, a bad filing) opens the window again with the text kept. `x` on a topic's page
  forgets the selected observation after `y` (any other key keeps it; git history keeps the text
  until `agentbus mem person purge`). `e` hands the terminal to `$VISUAL`, else `$EDITOR`, else `vi`
  on the topic's file; on its return the whole memory is checked (`Store.CommitEdit`): clean, the file
  is committed as `edit <topic> (by hand, from busmon)`; not, nothing is committed, the file stays as
  saved and the bottom line names the first finding. All three are refused in an agent's environment
  (`AGENT_BUS_AGENT` or a registered agent among busmon's ancestors) and on a machine that is not the
  memory's host, and write nothing.
