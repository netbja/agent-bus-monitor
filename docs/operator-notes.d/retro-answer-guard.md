- **`agentbus answer` checks the process tree (retro step 2, spec §5).** Besides `AGENT_BUS_AGENT` and
  `AGENTBUS_TERM_ID`, an answer is now refused when a registered agent is among the caller's ancestors
  (`agentcaller.Refuse`, as `retro request` and `trust` do), and when the ancestry cannot be read; a host
  with no ancestry keeps the variable rule alone. A retro's approval is the person's typed answer, so an
  agent that unsets both variables must not answer in the person's name. Run `agentbus answer` from your own
  shell, or answer in busmon. The limit stays the one person-memory states: the same user can still write
  the stream directly until the broker ACL is on.
