- **busmon's Plan view after the person's review (views-redesign §4.1).**
  - **Drawer:** `p` and `P` open it under For you, not to its right. For you then takes the whole
    width with its own rows only (`shellDrawer`), and the drawer takes the whole width below it.
  - **Detail:** the side panel (`drawPlanSide`, 34 columns) is gone. The selected task's detail is
    three rows at the bottom of the Plan frame (`planDetail`, `planDetailRows`), in the tab
    (`listState.SetBottom`) and in the drawer (`pageRows`). It shows the rule, then state, for you,
    owner, prio, review and PR, then after, unblocks and reason.
  - **Groups:** a finished group is one grey line (`✓ <title>  n/n done`) with no bar, and finished
    groups follow one another. A group's bar is in its state's colour (`planGroupRole`):
    - orange when a task waits for the person;
    - blue when one is in progress or in review;
    - the text colour when nothing has started.

    Without colour, the header's words say the same.
