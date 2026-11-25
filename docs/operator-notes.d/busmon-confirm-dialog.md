- **One confirm dialog for busmon's yes/no questions (busmon-confirm-dialog, the person 2026-11-25).**
  A solid box with a cell of padding over a dimmed screen, the question centred, detail lines under it,
  two buttons on one line (`[ Quit ] [ Stay ]`, `[ Run ] [ Cancel ]`, `[ Forget ] [ Keep ]`...), the safe
  one focused by default and drawn as a pill, a hint line `y / n · esc`. Left, Right and Tab move the
  focus, Enter activates the focused button, `y` answers yes, `n` and Esc no; any other key leaves it
  open (before, the flash-line questions cancelled on any other key). A click on a button activates it,
  outside the box is no. It now asks: q's quit, the recap's summary request, `d`'s forget of an agent or
  close of a thread, Z (suspend, resume; also on the Projects page), the palette's agentbus commands
  (`/resume`, `/pause`, `/reload`, `/backup`, ...), X (restart a service), S (start the services) and the
  Projects page's x (stop the services). Left for later, each in its own form: the Projects page's
  delete (two steps, the name typed), Shift+C's `d`/`D` reset (an inline row), the notification setup's
  y/n fields and `x` remove, and `busmon --reset`/`--delete`'s terminal prompts.
