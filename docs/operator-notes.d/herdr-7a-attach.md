- **`agentbus attach` (herdr exit step 7a, `cmd/agentbus/attach.go`, `attachkeys.go`).** `attach
  <agent> [--observe] [--prefix <key>]` finds the agent's live terminal (its role) among the
  project's holder sockets, the current build first, and streams it raw both ways
  (`holder.Client.ReadFrame`/`SendData`/`SendControl` after an `Attach` call). The way out is the
  prefix, `attach.prefix` (default `f12`; `f1`-`f12`, `ctrl+<letter>`, `ctrl+\`, `ctrl+]`, never
  `ctrl+c/d/h/i/j/m/q/s/z`, one rule in `config/attachkey.go`): `<prefix> d` detaches,
  `<prefix> <prefix>` sends one, `r`/`x` release or discard held keys (only while the holder
  holds some), any other key after it is dropped with a bell; legacy and kitty encodings, split
  reads, a kitty release or repeat of it dropped; a lone ESC goes after 30 ms. Attach asks the outer
  terminal for focus reports (`?1004h`) and turns `ESC[I`/`ESC[O` into `holder.Focus`, never keys:
  the holder's person-active judgement sees a blur (reviewer-codex); a program that enabled focus
  reporting gets none through attach, and one that turns it off (`?1004l` in its output, split
  between two frames or not: `focusWatch` keeps an open sequence across frames) has it turned
  back on at once. In kitty mode the repeat or release of the key that ended a pending
  prefix is dropped too (foureyes). **Gated per
  client**: the client profiles' `[keys]` table records `free`/`bound` per key as step 3 settled
  it (claude and grok: `f12` free, verified offline; codex: `f12` free by its
  `term record codex f12-at-input` probe, 2026-11-16; kimi and agy unknown until that probe is run
  and recorded for them); bound is refused naming the
  free keys, unknown is refused unless `--prefix` names the key (then a warning in the title),
  `--observe` is never gated and never types. Pending prefix and hold notices go to the outer
  terminal's title, never over the program's screen; detach restores the outer terminal
  (`attachRestore`). `TermInfo.Profile` names the terminal's client profile.
