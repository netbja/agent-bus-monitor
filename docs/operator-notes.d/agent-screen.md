- **`agentbus -p <p> screen <agent>` prints an agent's last screen text, read-only.** It reads
  through the project's holders (any build) and never types. Options: `--lines N` (default 40,
  at most 200) and `--json`. From inside an agent's terminal (`AGENTBUS_TERM_ID`), only master's
  and sentinel's terminals may run it. The person in a shell outside the terminals may too (the
  person's answer to Q2). Escape sequences and control characters are dropped, so nothing the
  program printed acts on the reader's terminal. An untrusted screen (the holder's emulator
  resynchronising) prints no lines. The caller is identified by `AGENTBUS_TERM_ID`, as `deliver`
  does: it is a team rule, not a security boundary (agent-screen-blocked §2).
