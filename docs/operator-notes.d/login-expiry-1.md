- **Login expiry, part 1: the reader and the identity (login-updates r9 §3.1-3.3, `loginexpiry/`).**
  No behaviour changes yet (part 2 runs the check). `loginexpiry.ReadFile` reads one field,
  `claudeAiOauth.refreshTokenExpiresAt` (Unix ms), from `<config dir>/.credentials.json`:
  - the path must be absolute and clean;
  - the file is checked with `Lstat`: a regular file, owned by this user, at most 64 KiB, and the
    same file once opened;
  - it is decoded into a struct holding only that field;
  - a value before 2020 or more than two years ahead is `not a time`, never an expired login.
  What leaves the reader is a time and a reason from one closed set (`invalid path`, `no file`,
  `not a regular file`, `not owned`, `unreadable`, `too large`, `no field`, `not a number`,
  `not a time`, `keychain entry not verified`, `keychain: exit <n>`). No token, other field, byte or
  decoder error is ever wrapped, logged or returned.
  On macOS the reader is `security find-generic-password -s "Claude Code-credentials" -w` (read
  only); it answers `keychain entry not verified` and runs nothing until the service name is
  verified on a Mac.
  An agent's `CLAUDE_CONFIG_DIR` is published with its status and its `bind`, and kept in the
  `claude_config_dir` snapshot field only with a proof, like `client_fp`; `/proc/<pid>/environ` is
  never read. The Claude profile declares `[login] source = "claude-credentials"`; no other client
  does.
