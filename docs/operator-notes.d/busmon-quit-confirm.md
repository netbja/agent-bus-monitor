- **`q` asks before busmon quits (busmon-quit-confirm).** The confirm dialog, "Quit busmon?" with
  `[ Quit ] [ Stay ]`, Stay focused: Enter stays, Tab then Enter quits, `y` quits; `n` or Esc stay; any
  other key leaves it open; a click on a button activates it, a click outside stays. Ctrl-C still quits
  at once, dialog or not, and so does a typed `/quit` in the palette. The setting `display.confirm_quit`
  (Display group, Shift+C "Ask before q quits", default on) turns it off: `q` then quits at once.
