- **The relay of host commands (herdr exit §4.2, step 10a; off until the cutover).** After the cutover the holder
  refuses `Start`, `Close` and `Submit` from inside an agent's terminal, so a host command run there sends the
  dispatcher a host directive on `{p}:cmd` (`type host`, target `dispatch`, `wake=no`): a closed set of names,
  `{op, agent, profile, ignore_background, from}`, ops `up pop relaunch reload swap suspend resume retire drain`
  (`bus.HostOp.Validate`: an agent exactly when the op takes one, a profile only for swap and pop, every name a
  `ValidName`), never argv or text. The dispatcher's own goroutine (`dispatch/hostops.go`, beside the
  reconciliation) reads them every 250 ms while it holds the lease: one outside the set gets `refused: <why>`;
  otherwise `bus.FencedHostAccept` (one fenced Lua script, Redis `TIME`) accepts it only within 5 s of its entry
  id and writes the `accepted` reply in the same step, or replies `expired`; a directive already handled is
  `seen`. Accepted ones go to one serial worker (64 queued at most; a full queue fails the operation), joined
  when the loop ends, which marks the operation `running` under the lease (`bus.FencedHostRunning`, only from
  `accepted`) before it runs `HostOpsConfig.Run`, and runs nothing without the lease; it writes one outcome
  reply, `done` (no wake) or `refused`/`failed` (waking), plus a `host-op` event. `{p}:hostops` (runtime) keeps
  each directive's state; a dispatcher starting replies `not-run` to each `accepted` one (never started) and
  `unknown` to each `running` one (it may have acted: check before asking again), tells master, and never runs
  either late. The command side
  (`relayHostOp`) exits 0 on `accepted` and 1 on anything else; with no reply once 5 s have passed by Redis
  `TIME` and a last read, it prints the spec's "the dispatcher did not answer within 5 s … Nothing was done"
  text. Nothing sends a directive and the service gives the relay no `Run` until the cutover (step 10b) wires
  the in-tree callers and the operations on the holder.
