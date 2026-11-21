- **busmon's Plan view, after the person's two reviews (views-redesign §4.1, §4.2).**
  - **The drawer:** `p` and `P` open it under For you, which takes the whole width with only its
    own rows (`shellDrawer`).
  - **The board:**
    - one header line (the plan, a bar, done/total, what is left and what is for the person), then a
      rule (`planHeader`);
    - only the groups with open tasks are expanded (`▼`), and every finished group folds into one
      line, `▶ N groups done · M tasks`, an item Enter or `d` opens;
    - no blank lines;
    - the rows are aligned (glyph, title, owner, state word, age since the task's newest fact).
  - **Colours:** bars and state words take the state's colour (`planStateWord`, `planGroupState`):
    orange waits for the person, blue in progress or in review, green approved, grey done, red
    blocked. `‖` marks a task waiting on another.
  - **The strip:** the selected task's detail is a strip under a rule at the bottom (`planStrip`).
    It shows at every tier but the narrow one: in the tab through `listState.SetBottom`, and in the
    drawer through `pageRows`.
  - **Sizing:** each frame shrinks to its content.
    - The tab's free height goes to Activity under it, shown but not focusable (`fitPlanTab`, the
      layout's `Below`).
    - The drawer's free height goes to the active tab under it (`fitDrawer`, the layout's
      `Drawer`), but not when that tab is the Plan.
  - **Lists:** a `ListItem` with `Wide` is drawn from the gutter. The tab removes a task row's mark
    with `dropCells` (two cells), because `FitLine` can merge the mark with a blank glyph.
