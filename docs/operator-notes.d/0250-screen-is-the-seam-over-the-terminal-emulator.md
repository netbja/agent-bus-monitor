- **`screen` is the seam over the terminal emulator** (herdr exit step 4; `screen/BAKEOFF.md` has
  the bake-off and its numbers). Production is `charmbracelet/x/vt`; gostty (a native library) is
  only the test oracle, behind the `gostty` build tag (`go test ./screen -tags gostty -run
  TestWriteOracle -update-oracle` rewrites `screen/testdata/oracle`), and
  `TestProductionLinksNoOracle` fails if a binary links it. The bake-off itself runs with `-tags
  bakeoff`. Plain and ANSI are rendered by the seam, one SGR sequence per attribute and color, so
  agentstate's signatures read the same bytes whatever the emulator. x/vt's scrollback is not
  used (about 12 KiB a line, trimmed by copying): scrolled-off lines go to a ring of rendered
  strings, and writes reach x/vt in 4 KiB pieces. Its answers to queries are drained into
  `Replies()`, else a query would block the write.
