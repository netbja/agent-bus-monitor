- **An unverified `status` never overwrites a verified identity.** Every `{p}:agents` write goes
  through `putSnapshot` (`bus/ident.go`, one WATCH/MULTI transaction, `mergeSnapshot`): a status
  with no pid keeps the stored pane/pid/pid_start/boot_id/terminal, a verified one replaces them, an
  older entry never regresses state, `bind` changes only the identity, and a verified status
  replaces the identity only if it is newer than `ident_ts` (the last proof). Don't write the
  snapshot with a bare HSET — Codex's app-server env carries another session's `HERDR_PANE_ID`,
  and a blind write would erase the bound pane. One runtime per agent name.
