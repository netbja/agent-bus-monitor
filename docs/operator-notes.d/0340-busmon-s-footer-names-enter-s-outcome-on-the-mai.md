- **busmon's footer names Enter's outcome on the main screen** (busmon-row-actions, spec
  `docs/superpowers/specs/2026-11-06-busmon-queue-triage-design.md` §5): `rowHint` (`rowhint.go`) gives,
  from the selected row's key and its `Expanded` state, the page Enter opens (`answer`, `agent`,
  `thread`, `task`, `PR`, `health`, `request`, `notices`, `full text`), the change in place
  (`expand`, `fold`, `unfold`; a Requests fold reads `ListItem.Expanded`, since `reqOpen.folded`
  is inverted by name), or `rowNoPair` (`merge:unknown`, or no row selected: an empty key), which leaves the `enter` pair out.
  `pageHint` answers with it when no page is open, in `ModeShell` only (`shellHint`; the picker
  keeps `open`), and `footerHintsOn` puts the `enter` pair first in `ModeShell`. A key outside the
  table keeps `detail`. A new row-key prefix needs its line in `rowHint`:
  `TestRowHintEveryRowAndItsOutcome` presses Enter on every selectable row of five fixtures (the
  Requests ones from `reqShell`, all closed and all open), again on an in-place row after its
  change, and holds the word to what Enter did and to every row-key kind being met.
