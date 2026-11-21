- **A client's own MCP servers are not background work (background-work census, 2026-11-21).**
  - **The rule:** a client profile may declare `direct_helpers = true` under `[background]`. The
    client's direct children that are not a shell (sh, bash, dash, zsh, fish, ksh) are then its
    helpers: its MCP stdio servers and its daemons. They never count, whatever their CPU, and
    neither does anything under them (the `node` an `npx`-started server runs, codegraph's watcher):
    the census names each process's branch, the client's direct child whose subtree holds it.
  - **What still counts:** work the agent starts runs under the client's shell, so a dev server it
    left running still blocks `auto-clear` and `pop-retire`, and still gets the long-skip notice.
  - **Who declares it:** Claude Code does, as checked on this host. Every agent's
    `codebase-memory-mcp` is a direct child of `claude`, and each tool command is
    `/bin/bash -c source …shell-snapshots… && …`. This ends the first real false positive: docs's
    `pop-retire` skipped for 2 h on `process 'codebase-memory'`.
  - **Who does not, and why:**
    - **Codex** has the same shape (its MCP servers and `codex-code-mode` are direct children, its
      commands are `/bin/bash -c …`). But a one-command `bash -c` or `bash -lc` execs the program in
      place (checked: `bash -c 'sleep 3'` leaves only `sleep`, a direct child), so a dev server would
      read as a helper. It does not declare it, and its process source is not authoritative anyway.
    - **Grok, Kimi and agy** are not checked.
