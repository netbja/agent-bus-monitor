- **Holder socket name, bounded (P0-2 B, `holder/rundir.go`).** A holder now listens at
  `<rundir>/agentbus/<project>/h-<id>.sock`, where `id` is the first 16 hex digits of its build
  key's sha256: 23 bytes whatever the key, so a dirty build's long key and a 32-byte project fit
  under `/run/user/<uid>`. Before listening, it writes `h-<id>.key` beside the socket (0600, the key
  in full), and its shutdown removes both. Every reader goes through `holder.ProjectSockets` or
  `SocketFor` and takes both names: the host's socket list and its calls, holder health
  (draining, leftovers), `attach`, busmon's projects page. A holder of an older build keeps its
  `holder-<key>.sock` after an upgrade and is still found, reached and drained. A new-format
  socket whose key file is missing, not a regular file, or names another key is ignored.
