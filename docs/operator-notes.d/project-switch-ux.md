- **busmon, after a project switch (M, o; project-switch-ux, PR 1).**
  - **Back:** `B` goes back to the project the switch left. The switch passes `--from=<p>`, so `B`
    toggles between the two.
  - **Stopped project:** when the project you switched to has no running dispatch, a banner under the
    header says "stopped project · read-only", with `S`, `M` and `B`.
  - **`S` starts its services:** it runs `agentbus service start` (dispatch, then missions), after a
    y. The agents stay down. It is the person's act, refused from an agent's environment, as `X`
    is.
  - **The stopped project is read-only:** while the banner shows, every write is refused with
    "stopped project: read-only, S starts services". That covers sends, answers, priorities,
    ideas, forget, resume, agentbus runs, settings, attach and the Projects page's delete. `S`,
    its y/n and navigation still work. It lifts as soon as dispatch runs.
  - **`o` alone still starts nothing.**
