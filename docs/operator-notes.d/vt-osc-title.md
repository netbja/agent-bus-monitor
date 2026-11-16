- **A title no longer hides an agent's screen (vt-osc-title, 2026-11-16).** x/vt v0.1.0 honours 8-bit C1 controls
  inside a string sequence: a byte 0x80-0x9F in an OSC, DCS, APC, PM or SOS payload ends or breaks it, and the rest
  is printed. Claude Code's turn-end title `✳ <project>:<agent>` (E2 9C B3, 0x9C is ST) landed on its input line,
  its ready signature failed, and the dispatcher deferred `agent unknown` until someone attached. `screen`'s
  `stringFilter` (strfilter.go), before x/vt and stateful across chunks and writes, drops every OSC 0/1/2 title
  and every string sequence whose payload holds such a byte (OSC 9 and 777 notifications with a glyph, an APC or
  DCS); everything else passes byte for byte. The holder tells every terminal it starts `TERM=xterm-256color` and
  `COLORTERM=truecolor` (what x/vt emulates), after its own environment and before the role's (a role's `TERM`
  wins): a service runs with `TERM=dumb`, and Claude Code under it drew its dim placeholder and prompt suggestions
  unstyled, which the ready signature reads as typed text. Agents started before the fix keep `TERM=dumb` until
  they are started again. The health check `screen-unknown` (For you, `agentbus attach
  <agent>`) names each agent declared idle whose screen the dispatcher has read `unknown` for 10 minutes
  (`health.ScreenUnknownAfter`), among the clients with a ready signature only: such an agent cannot be woken.
