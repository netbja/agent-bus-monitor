- **busmon's header strip is a partition of For you** (busmon-queue-strip, same spec §3): `stripOf`
  (`queue_strip.go`) counts `decide` (ranks `rankAsk`…`rankGate`), `merge` (`rankMerge` but never
  `merge:unknown`) and `unread` (`unreadItems`), so `decide + merge = decisions(forYou)` and the strip
  can never disagree with the panel. A new For you rank must be placed in one chip or none, there.
  The panel's frame label is the same words (`forYouLabel`: `2 decide · 1 merge`).
