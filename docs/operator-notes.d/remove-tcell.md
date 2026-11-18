- **busmon no longer depends on tcell** (`remove-tcell`): the theme keeps its colours in its own
  `Color`/`Style` (`cmd/busmon/color.go`, tcell's semantics); the goldens are unchanged. `go.mod`
  drops `gdamore/tcell`, `gdamore/encoding` and `golang.org/x/text`.
