- **busmon shows which project it opened (busmon-project-badge, part 1).** For its first 3 s
  (`projectBadgeFor`, from the first poll) the project's name in the header is a pill in the accent
  colour (reverse video under NO_COLOR, the pill's own fallback). When neither `-p` nor
  `AGENT_BUS_PROJECT` named the project (busmon took the last one opened on this broker), the bottom
  line says `last project opened: <p> (no -p) · M to switch` once. A switch from the Projects page
  re-executes busmon with `--project`, so it gets the badge and no notice. Only main sets
  `shellConfig.badge`: tests and goldens start without it. Part 2 (a per-project accent colour) follows.
