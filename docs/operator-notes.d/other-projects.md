- **busmon's header counts what waits in the other projects (project-switch-ux item 3).**
  - **The counter:** "other: N for you · M merge (O)", summed over this broker's other projects.
    N is the for-you rows other than merges, M the merges waiting. It shows only when something
    waits.
  - **How it reads them:** every 2 minutes, 4 at a time, read-only, and only projects whose dispatch
    holds its lease. A stopped project is read on M and R as before.
  - **What counts:** a read that fails, or is older than the period plus its read time, drops out
    of the sum; it never counts as 0.
  - **`O`** opens the project with the most for-you rows, by the same checked switch, so `B` comes
    back.
