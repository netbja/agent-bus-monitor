- **busmon shows an agent's background work (background-work-visible step 3).** One mark per agent after its
  message, the state word unchanged (an agent with only background work still reads `idle`): its declared text
  (`agentbus background`) first; else the tasks its Claude Code hooks reported (`… 3 bg tasks 14m`, the oldest's age
  counted from agentbus's first sight); else the dispatcher's process count, a lower bound (`… bg ≥1? 9m`). `◷`
  follows when it has scheduled wakeups, never counted as running work (the person's `⏰`, drawn one cell wide). A
  record of another pid, past its hour, or an observation of another process or too old shows nothing. The header
  chip names an agent with observed work alone as `bg`. The agent's page gains a Background section: the declared
  text and who set it, one line per task (`subagent Explore · running · 3m`), the source and its last report, the
  program names running under the agent (read live from the holder while the page is open, never stored), and the
  wakeups with their schedule.
