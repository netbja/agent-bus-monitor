- **`EmptyInput` is `Ready` on the current input box** (herdr exit 5d, the architect's ruling of
  2026-11-13): no separate `empty_input` pattern. Each client's Ready is anchored to the box's end
  (claude: the empty `❯` line directly above the closing rule; kimi, grok: directly above the
  bottom border; agy already was), so a second input line or text the client returned reads not
  empty (`<client>-multiline-typed`, `<client>-returned`, DERIVED until a person records them).
  `Signature.EmptyInput` adds the Dialog veto to Match's Modal one; `InputHolds(screen, token)`
  reads a token (`agentbus#<nonce>`, `/clear`) in the current input box only, also with its
  blanks and box borders removed (a wrapped line): from the box's start to the line the client's
  `box_end` matches (claude, agy: the closing rule; kimi, grok: the bottom border; codex: the first
  line out of its shaded block, read raw: an ANSI client's `box_end` sees the escapes, since a blank
  line inside codex's input is shaded too: reviewer-codex), never what a client draws under it (claude's slash menu lists `/clear` while the
  input holds `/cl`: foureyes); a box with no end on screen holds nothing. Mode 2004 is the holder's to check beside it.
  The anchoring reaches today's herdr dispatcher too: it can only wake less, never type into text.
