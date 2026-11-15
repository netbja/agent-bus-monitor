- **busmon attach (herdr exit step 7b2, `cmd/busmon/termpreview.go`).** An agent's page (enter on
  the Agents panel) shows a read-only **Screen** section: the last 30 text lines of the agent's
  terminal on the project's holders that `agentbus attach` picks (the current build's holder first, its newest live one), read through `holder.Host` List and Screen every
  second while the page is on top (never under another page, never Submit), every line through
  `Preview` so no byte the program printed reaches busmon's terminal; no section while herdr hosts
  the agent, `not read: <why>` when the holder fails. `t` on the Agents panel or on the agent's page
  runs `agentbus --project <p> attach <agent>` through `tea.ExecProcess` (the `agentbus` beside
  busmon); detach (the prefix then `d`) returns to busmon, and a refusal (a bound or unverified
  prefix, no terminal) shows agentbus's last line in the status line. Spec 4.6's `enter`/`a` were
  taken (detail, silent agents): `t` instead.
