- **`dispatch.background_ignore`: the clients' own helpers are never background work**
  (background-ignore-helpers). A process named in the list (default `codebase-memory` and
  `codex-code-mode`, the clients' own helpers; config.toml only) never holds a hand-off's `/clear`, a
  pop's retire or the holder drain, whatever its CPU or where it runs. A shell whose every child is
  such a helper is its wrapper and is ignored too. A shell running anything else, or nothing, still
  counts, as does any unlisted process (`go test`, a dev server). The list is read at each look, so
  a change applies without a restart. Generic names (`MainThread`, every node or Python process;
  `import`; `bash`) are left out on purpose: they would hide real work. Add one only knowingly.
