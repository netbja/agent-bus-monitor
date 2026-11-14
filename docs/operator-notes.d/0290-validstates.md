- **`ValidStates`** (`working`, `idle`, `blocked`, `waiting`, `done`) is still a hardcoded map in
  `bus.go`. Adding a state means editing that map. `waiting` (v0.12.3) means a *person* must decide:
  busmon sorts it first with ♙, fades the word `waiting` in and out (3 s, `fadeLevel`, ~10 redraws a second only while one waits; true colour only), counts it in the header and rings the bell once per new
  declaration (`tea_signals.go`, after the poll, never in View).
