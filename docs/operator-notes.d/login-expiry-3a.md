- **Login expiry, part 3a: an expired login holds its agent (login-updates r9 §3.6,
  `dispatch/loginhold.go`).** On `fail` the dispatcher writes `hold-login:<agent>` in `{p}:quota`
  (fenced, its value the expiry in ms). While that field exists the agent gets no wake. One agent
  is held, not the provider, so other Claude agents keep working.
  - **What it had:** an agent with work (an accepted task, or `working`) gets a `{p}:held` record:
    provider `login`, since, tasks, branches, last report. This is run-budget §4's record, built
    here first.
  - **Notices, once per expiry:** the person (`login-expired:<agent>`):
    `coder's Claude login expired Thu 15:40: run /login in its pane. Its work is held (master told).`
    Master (`run-held:<agent>`): `coder is held (Claude login expired Thu 15:40, released after
    /login): accepted wiki-3 (feat/wiki-3). Reassign it, or wait.` Master's own expiry sends no
    directive, and the person's notice names master.
  - **Release:** the next read after /login (within a minute) finds a later expiry still ahead.
    The agent gets `Your Claude login is renewed. Work interrupted by it: …`, then its `{p}:held`
    field and its hold field are deleted, the hold field last. The same expiry, an older one, or an
    `unknown` read keeps the hold. An agent swapped to a client with no login expiry is released
    once the swap is over, with no resume message (the swap resumes it). `retire --forget` deletes both.
  - **Readers:** `bus.HeldReason(marks, agent, provider)` (`quota`, `login` or `""`) is read by:
    the dispatcher's wake skip, its clear and its pop-end judgement; `CannotAct`, whose reason now
    reads `master is held: its Claude login expired`; and team-stall (`t1 (coder, login held)`),
    quiet-agent, idle-implementer and idle-master (`held: its login expired`).
  - **busmon:** shows `coder: Claude login expired Thu 15:40, held` under the Accounts cards and in
    the agent's detail.
  - **Self-repair and pop facts (part 3b):** self-repair holds a login-held agent (hold kind
    `login`, "its Claude login expired: /login in its pane releases it") and never relaunches it, since
    a new process would hit the same expired login. `ReadPopFacts` gives `LoginHeld`, so a login-held
    pop is never judged done ("its login expired (held)").
