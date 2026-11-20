- **The dispatch service's runtime is `internal/dispatchsvc` (dispatchsvc move, 2026-11-20).** Its health-check readers, stall
  checks, chores and reboot judge left `cmd/agentbus` whole, unchanged; both binaries keep their paths. The CLI reaches it
  through `Deps`, `Config`, `ForegroundConfig`, `Runtime` and `MergeQueueLive` only. `TestGuard` fails when the package
  imports the CLI or the screen, calls the mission runner, imports `os/exec` in a new file, runs a program not on its
  list or any shell, or imports a module package not on its list; a file path in an older note under `cmd/agentbus` for
  one of these files now reads `internal/dispatchsvc`.
