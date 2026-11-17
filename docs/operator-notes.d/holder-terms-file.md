- **A restarted holder numbers its terminals on (holder hardening, found by the endurance test).** Each holder
  process numbered its terminals from `t1`, so after a crash, a restart or a reboot the relaunched master
  got `<key>/t1` again, the dead master's pane: self-repair's proof ("a new process in a new pane",
  `replaced`) refused it for 60 s and every relaunch after a holder crash was escalated as failed. The
  holder now records its last number in `holder.Config.TermsFile`, `<state>/<project>/holder.<key>.terms`
  (`holderTermsFile`, set by `holderConfig`), and numbers on from it; an unreadable record numbers from
  `t1`, a failed write is ignored (the process still numbers on). One small file per build is left
  behind by `service gc`. `TestHolderRestartEndToEnd` (release gate) pins the recovery: crash, the
  same socket served again, `holder_restarted` naming both agents, both resumed master first and
  proved, woken again, `ok`; then a holder that never returns fails in For you.
