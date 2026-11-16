- **Untracked files no longer make a build "+dirty" (build-id-untracked).** Go's `vcs.modified`
  counts untracked files, so notes left in the repository root gave every setup build a "+dirty"
  build id, and that longer key put the holder's socket path over its limit (2026-11-16). setup now
  stamps `buildinfo.Tree` from `git describe --dirty` (tracked files only) and the build id follows
  it; setup says once per install when it builds from modified tracked files. A plain `go build`
  carries no stamp and keeps Go's own view.
