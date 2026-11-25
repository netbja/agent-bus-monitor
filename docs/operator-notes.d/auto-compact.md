- **Auto-compact: `/compact` when no hand-off comes (the person's answer A).** An agent whose context
  fill stayed at `dispatch.context_urgent` (90 %) for 10 minutes with no hand-off declared gets the
  fixed line `/compact` typed into its pane by the dispatcher, once per context episode, Claude Code
  only. The pane guards are the auto-clear's: its verified process, ready and idle, not focused,
  not blocked, no wake being typed, no swap, its provider not held. The order is the clear's: the
  `compact:<agent>` mark is reserved first, the pane inspected again, then the line typed; master
  gets one wake=no notice: owed in `compact-typed:<agent>` once the line is typed, delivered
  (`compact-notice:<agent>`) on a later pass if master was being swapped, even after the fill fell.
  The hold's start is `compact-since:<agent>` (`<session>|<Redis ms>`); a reading below urgent
  restarts it, and the episode's end (a reading below the alert) deletes it and `compact:<agent>`. A `/compact` left unsent in the input tells the person,
  and nothing is typed again in that episode. `dispatch.auto_compact` (on by default) is the
  person's: busmon or config.toml, no env variable, read again each minute. The holder's submit
  frame takes kind `compact`; an older holder refuses it (bad frame) and types nothing, so
  restart the holder with the dispatcher. A declared hand-off stays the auto-clear's.
