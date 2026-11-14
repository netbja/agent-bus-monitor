- **`agentbus term` needs no project and no broker** (`termrec` package, herdr exit step 3):
  `record` runs a client under a PTY (`github.com/creack/pty`) and writes
  `agentstate/testdata/raw/<client>/<fixture>.jsonl` (`--out` elsewhere; `--force` replaces).
  A quiet wait needs the client to have written something, then counts from the last output or
  input (a resize is no I/O), so it never ends before a slow prompt or between a key and its
  echo; a client that stays silent until `max` gets a `quiet-timeout` mark. The header names the
  binary, not its path. A client still running when the script ends gets SIGTERM, then SIGKILL 2 s later (an
  interactive shell ignores SIGTERM). Without an emulator (step 4) the only judgement is on bytes:
  `dump --between` says what the client wrote between two marks. Tests run a fake client (the
  test binary under `TERMREC_FAKE`), never a real one.
