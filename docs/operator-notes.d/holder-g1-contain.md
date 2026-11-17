- **The holder contains its screen emulator (holder hardening G1, part 1; spec §7.1).** Each terminal's
  x/vt emulator runs in its own goroutine (`holder/emu.go`, `emuLoop`) behind a 4 MiB queue. The read loop
  never waits on it: tail, viewers and `readSeq` move on. Its lock is `t.emu`; `t.mu` never runs it.
  - A full queue (a gap) or a recovered panic (`run`, with its lock released) latches the screen
    `invalid`.
  - No progress for 10 s with bytes waiting latches it `stuck`. That goroutine is abandoned, and no
    second one starts.
  - A read while the emulator is behind, past 200 ms, is `lagging` (not latched).
  - Screen, settle (submit) and a viewer's paint use the emulator only when caught up (`view`,
    `paint`, `parsedSeq == readSeq` under its lock).
  - `TermInfo.Screen` / `ActivityInfo.Screen` say the state, `ScreenGen` is raised on a latch, and
    `ScreenReq` refuses anything but ok with code `screen` (`termhost.ErrScreenUntrusted`). The
    dispatcher then reads an inspection error and waits: no wake, `/clear` or submit.
  - Nothing restores `ok` in place: the terminal is replaced (reload).
  - While the screen is latched, the read loop answers DA1, DA2 and DSR 5 with x/vt's own strings,
    never a cursor position.
  - An untrusted screen's viewer is painted a notice, then the live output.
  - Part 2 brings health `holder` on invalid/stuck and `agentbus reload --replace-broken`.
  - `go test -race ./holder` passes. `./screen` fails on x/vt's known `Emulator.Close`/`Read` race,
    on dev too; the holder never calls `Close`, so the emulator goroutine is not in that path.
