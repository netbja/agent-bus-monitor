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
    drawer through `pageRows`. Its last line holds the keys, said once (`planStripKeys`): the tab's
    frame has no footer then, and with a fold selected the strip keeps its rule and its keys.
  - **Sizing:**
    - The tab takes the lower zone's full height (plan-tab-r3): no Activity under it. What arrives
      in Activity meanwhile is counted on its tab, `1 Activity · 3 new` (`tabNotes`, from the feed's
      `seq` when Activity was last drawn, `activitySeen`: never while the idle view or a page
      covers it, `activityOnScreen`); a too narrow bar drops the note first.
      One blank row sits between the tab bar and the header line.
    - The drawer's free height goes to the active tab under it (`fitDrawer`, the layout's
      `Drawer`), but not when that tab is the Plan.
  - **Releases column (plan-tab-r3 item 5):** from 130 columns the Plan tab has a column on its
    right (`withReleases`, the layout's `Side`, a quarter of the zone between 32 and 44 cells,
    after the Plan in the Tab order); `V` shows it below 130 and hides it from 130. Its rows: next
    with the decided ideas, the loaded plan highlighted, then the release tags newest first with
    their date and their plan's planned task count (a plan file holds no completion, so never
    "done"). Enter opens a release's plan read only (`ovRelease`). The source is the project's
    checkout on this machine (`projectCheckout`): `git for-each-ref` on `vX.Y.Z` tags and
    `docs/plans/*.json` decoded leniently, read every 5 minutes at the requests' cadence
    (`readReleasesDue`). A tag `vX.Y.0` closes plan `vX.Y`; a patch closes none. A busmon with no
    checkout here lists next and the current plan only.
  - **Lists:** a `ListItem` with `Wide` is drawn from the gutter. The tab removes a task row's mark
    with `dropCells` (two cells), because `FitLine` can merge the mark with a blank glyph.
