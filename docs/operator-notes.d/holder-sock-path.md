- **Holder socket path, per OS (P0-2, `holder/rundir.go`).** The holder's socket path limit is
  this OS's `sun_path` less its NUL: 107 bytes on Linux, 103 on macOS (and on any other OS). It was
  macOS's 103 everywhere, so on Linux a dirty build's long key (a 105-byte path) was refused: the
  holder never listened and the dispatcher waited for it. `agentbus up` now checks the path before
  it starts anything (`holder.SocketPathFits`, which creates nothing). A path too long for this OS
  refuses up with the path and the fix (a shorter `XDG_RUNTIME_DIR` or `TMPDIR`): `nothing
  started`. A socket name of bounded length, whatever the build key, is the next step.
