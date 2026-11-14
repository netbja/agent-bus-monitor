- **The holder's foundations** (herdr exit step 5a, package `holder`; nothing uses it yet). One
  function, `holder.ProjectDir`, is the run directory for the holder, the supervisor and every
  client: `$XDG_RUNTIME_DIR`, else `$TMPDIR/agentbus-<uid>`, else `/tmp/agentbus-<uid>`; each level
  (the fallback root, `agentbus`, the project) is made 0700 by `Mkdir` (which never follows a
  symlink), then read once with `Lstat`: a symlink, another owner (`owned by uid N, not you: another
  user created it; remove it or set TMPDIR`) or a mode other than 0700 is refused. `SocketPath`
  adds `holder-<key>.sock` and refuses a path past 103 bytes (macOS's 104 with its NUL).
  `service.BuildKey` is the one build key (security S7): lowered, reduced to `[a-z0-9.]`, cut to
  40, `.` and 10 hex digits of the raw id's SHA-256. Frames are a 4-byte big-endian length
  counting a type byte (control JSON or raw data) and the payload, at most 1 MiB. Control frames are
  closed sets: `DecodeControl` reads one object, refuses a duplicate key, a case-folded key
  (`encoding/json` alone would take `Kind` for `kind`), any field beyond its op's, and checks every
  value (submit `{kind, term, nonce, count}` with no text or name, start `{role, profile?,
  resume_session?, cols?, rows?}` with no argv, env or cwd). `PeerOf` reads the peer's pid and uid
  from the kernel (`SO_PEERCRED`; macOS `LOCAL_PEERPID` and `LOCAL_PEERCRED`), `CheckSameUser`
  refuses another uid both ways, and `InTree` reads a peer as inside a terminal when an ancestor is
  the holder by pid and start time; a chain that does not reach pid 1 within 32 ancestors is
  in-tree too (an unknown is refused the holder's writes, never let through).
  Step 5b, the server (`holder.Server`; still nothing uses it): one per project, a subreaper
  (`PR_SET_CHILD_SUBREAPER`, Linux), its socket 0600, a socket that still answers refused as
  another holder of the build. A connection is the same uid (`CheckSameUser`), then `hello` in the
  holder's protocol (else a refusal and the close), then one `reply` per control frame. `Start`
  builds the command itself (`TemplateLaunch`: `env ROLES_TOML=<manifest> <agent-launch> <role>
  <project> [--profile p] [--resume id]`, the role in the manifest, the profile its own or a
  `fallback_profiles` one, a resume only for a client whose resume is verified), runs it on a new
  PTY (never reused) with `AGENTBUS_TERM_ID` and `AGENTBUS_HOST`, and reports its pid and start
  time. The holder always reads the output into the emulator (and a 1 MB raw tail) and writes the
  emulator's answers to queries (DSR, DA) back, viewer or not. A terminal ends with its process
  group: on Linux the wait loop learns the leader's exit with `waitid(WNOWAIT)`, leaving it unreaped
  (so its pid, and its group's number, cannot be reused), ends what is left of the group (SIGHUP,
  SIGKILL after the grace: a child that ignores SIGHUP never outlives its terminal, reviewer-codex),
  then reaps it. The group always gets that SIGKILL: the `/proc` scan only spares the grace (a group
  that looks empty is killed at once), it never decides that nothing is left, since one scan can miss
  a process forked while it reads (reviewer-codex). `Close` is SIGHUP to the group, SIGKILL after the grace, and only while the leader is
  unreaped: a terminal that ended is never signalled again, by one Close or many, since its group's
  number may be another terminal's now (reviewer-codex). A descendant that left the group (setsid)
  is not reached; off Linux (no `/proc`) the leftovers are not ended at the leader's exit. A terminal's own pid is reaped by its `Wait`, so its exit
  status is kept; the reaper (on SIGCHLD, and a 5 s sweep) reaps only exited children it did not
  start, never `wait(-1)`. An in-tree peer is refused `start`, `close`, `submit` and an attach
  that types (`in_tree`); `submit` answers `later` until step 5d. `holder.Client` checks the
  holder's uid before it says hello. x/vt answers a program's queries asynchronously, so the holder
  pumps `Replies()` every 10 ms (`replyLoop`), not only after an output: a program waiting on its
  DSR answer writes nothing more. The subreaper is the process's, so every server's terminal pids
  live in one registry (`track`), held from the fork until the pid is recorded, and one reaper reads
  it.
  Step 5c, attach (spec 4.5, 4.6): an `attach` frame turns its connection into the viewer's: the
  reply, a first paint (`screen.Render`), then the output as data frames through the viewer's own
  `outputFilter` (security S3: OSC 52 unless `Config.OSC52`, OSC 8 links with their text kept,
  window reports `CSI 11/13-16/18-21 t`, `DCS $ q` and `+ q`, OSC 4/10-19 `?` queries, DA, every DSR
  request `CSI … n`, DECRQM `CSI … $ p`, XTVERSION `CSI > q`, kitty graphics queries (APC `G … a=q`),
  `CSI ? u`; a sequence cut across reads is read as one, and one past 1 MiB is dropped
  whole). A viewer's queue holds 256 chunks; one that falls behind is repainted from the emulator,
  never blocking the program. From the viewer: data frames are its input (an observer's are
  dropped), written under the terminal's input lock; `focus`, `resize` and `detach` control
  frames. A viewer is focused from its attach until a focus-out; an observer never is. The viewer's
  input is read as a stream (a sequence cut across frames is one): any key starts a draft and an Enter
  (CR, kitty `CSI 13 u`) ends it; SGR and X10 mouse reports, focus reports and the viewer terminal's
  replies (a CSI ending in R, n, y, c or t; any DCS, OSC, APC string) are no typing. A lone ESC waits
  100 ms for the rest of a sequence; nothing follows, it was the Escape key, counted at its own time
  (resolved by the next frame, when Activity is read, or at once on a focus-out or a detach). The most recently active non-observer sets the size. `Activity` reports
  the viewers, the focused ones, the last typing and the draft.
  Step 5d, submit (spec 4.2): the frame is `{kind, term, nonce, count}`; the holder composes the
  line (`termhost.WakeLine` with its own project and the terminal's role, or `/clear`) and judges
  the screen with the terminal's client signature (`Command.Client`, mapped to its agentstate
  profile; none: `not_ready`, never prompted). Under the input lock, which every viewer byte also
  takes (so the lock is the hold during a submit and bytes follow the CR in order): no person
  (`person_active`: a draft however old, typing within `person_quiet`, a typing viewer not proven
  unfocused, a viewer's lone ESC still inside its 100 ms wait), not left behind (`left_behind`),
  `EmptyInput` with mode 2004 on a settled screen (no output for 80 ms, else unsettled: `not_ready`), the same again after the hold, the line as a
  bracketed paste only, then the line's own token (`agentbus#<nonce>`, `/clear`) in the input box
  within `submit_wait` (2 s, the settling included: never longer): CR. Not seen: no CR, the terminal left behind, and the viewers' bytes
  stay held after the lock (`not_landed`): a queue of at most 1 MiB (past it, every further byte is refused until the hold ends,
  counted in the notice's `refused` and never delivered; the holder never stops reading a viewer,
  so its `release`, `discard` and `detach` always get through, spec r25), a `hold` notice to each typing viewer (and one that attaches), `release` delivers them
  in order, `discard` drops them; a settled screen whose input reads empty lifts the mark and
  delivers them (`leftBehindLoop`, every 250 ms). Tests drive a fake client drawn as agy's box.
  Not wired yet: `submit_wait` and `person_quiet` are `holder.Config` fields only (no
  `config/keys.go` entry, no production caller); they are registered as `dispatch.submit_wait`
  and `dispatch.person_quiet` when the holder's main lands (step 6).
  Step 6a, the adapter (`holder.Host`, `holder/host.go`; nothing calls it yet): `termhost.Spec`
  is `{project, name (the role), profile, resume_session, size}` — no argv, env or cwd (S2a). A
  `TermID` is `<build key>/<terminal id>`: `Start` goes to the current build's socket, `List`
  and `Describe` read every holder socket of the project (an older build listed as draining),
  and the other calls reach the terminal's own holder. `Submit` sends the line's kind, nonce and
  count only, and refuses a wake line built for another project or agent than the terminal's
  (the holder would type its own). It passes the termhost contract on a real test holder
  (`TestHostContract`, a fake client driven by files).
