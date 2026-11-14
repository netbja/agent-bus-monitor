- **`"docs": "<agent>"` in missions.json** makes the conductor send that agent one directive when
  the change completes, written with the `complete` notice in one fenced script
  (`Store.PutChangeWithCmds` → `FencedCmdsWithChange`). The docs agent opens a PR from
  `pr/<change>` and never merges.
