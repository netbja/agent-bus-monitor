- **busmon, three fixes for the person** (busmon-three-fixes):
  - `/resume` on a project neither paused nor suspended asks nothing: it says there is nothing to
    resume (no red "Run agentbus … resume --yes? y / n"). An unreadable pause state still asks.
  - `G` (live) on Activity with auto-scroll off says so and names the setting (`display.follow`, `/follow`
    or Shift+C turn it on); it still jumps once to the newest lines.
  - The Requests view (`r`, `/requests`) is titled "Requests", with the tab it came from as the way back
    ("esc back to Work"), instead of the tab bar; in the narrow layout the tab row is not drawn under it,
    and a click where a tab used to be switches nothing.
