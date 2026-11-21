- **`dispatch.background_ignore`: the clients' own helpers are never background work**
  (background-ignore-helpers). A process named in the list (default `codebase-memory`,
  `codex-code-mode`, `MainThread`, `import`; config.toml only) never holds a hand-off's `/clear`, a
  pop's retire or the holder drain, whatever its CPU or where it runs. A shell whose every child is
  such a helper is its wrapper and is ignored too. A shell running anything else, or nothing, still
  counts, as does any unlisted process (`go test`, a dev server). The list is read at each look, so
  a change applies without a restart. Note: `MainThread` is every node process's name, so a plain
  `node server.js` started by an agent is ignored too; remove it from the list if that matters.
