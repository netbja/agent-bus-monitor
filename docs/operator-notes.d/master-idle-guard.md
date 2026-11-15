- **`idle-master`, the stall guard for master with work in hand (master-idle §2-§5, part C).** A health check
  (every minute, `Handler: master`, never a fail) judging the escalation target: idle, `done` or its `waiting`
  answered (`WaitAnswered`) for `dispatch.master_idle_after` (15m, `0` off: `unknown`, `off`), a fresh observation
  not busy (unseen: `unknown`), no wakeable message (`Obligation(...).Wakes()`: the dispatcher's own wake covers
  it), not paused, its provider not held, and something in hand: the merge queue's `undelivered` heads (a pass
  older than three passes, or none, makes that source `unknown`: named beside other items, `unknown` alone), its
  board requests (`requested`, `accepted`, `working`), its check-back once due. An open `waiting` is the
  person's turn (`ok`). The hook (`idle-master` joined the StallWake ids in `dispatch/health.go`) wakes master
  once per episode (8 bytes of SHA-256 of the sorted item keys `head:`/`req:`/`cb:`) through
  `bus.FencedCmdOnceAt`: mark `stall-master` and `stall-master:at` (the entry's id, Redis time) written with the
  entry in one script, so the cmd stream's cap or `busmon --reset` never loses the wake's time. Same episode, the
  wake at least `master_idle_after` old: one notice for the person (`stall-master-person`); the `:at` missing:
  none, the detail says `wake time unknown`; master cannot act (`dispatch.CannotAct`): the notice at once, no
  wake. On any pass, master's check-back set on the same `what` more than `dispatch.checkback_max` (3, `0`
  never) times is one notice (`checkback-person`, episode master + `what` + `first_set_id`).
