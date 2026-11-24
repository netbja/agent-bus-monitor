- **`q` asks before busmon quits (busmon-quit-confirm).** A popup, "Quit busmon?": `y` quits; `n`,
  Enter or Esc stay; any other key leaves it open, and a click does nothing. Ctrl-C still quits at once,
  popup or not, and so does a typed `/quit` in the palette. The setting `display.confirm_quit` (Display
  group, Shift+C "Ask before q quits", default on) turns it off: `q` then quits at once.
