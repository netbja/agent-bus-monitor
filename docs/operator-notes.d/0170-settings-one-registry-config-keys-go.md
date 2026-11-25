- **Settings: one registry, `config/keys.go`** (config-file, spec
  `docs/superpowers/specs/2026-10-29-config-file-design.md`): every tunable's file key, env name,
  default, today's env rule (use, skip to the next source, or invalid) and typed file rule; per key, env
  over `config.toml` over the legacy `busmon.toml` (busmon's keys only) over the default; warnings name
  file, line and key and never echo a value; no secret and no connection in the file. Readers resolve
  through `config.Resolver`; `TestNoEnvLiteralOutsideTheRegistry` fails on a registry env name written
  as a literal elsewhere. busmon's settings are
  migrated (`loadBusmonSettings`), and so is the person's name (`personName` in `identity.go`: the files
  only, `config.toml` then `busmon.toml`; `identity set` writes `config.toml`; `ask` and the plugin keep
  their raw env rule through `config.EnvName`), and so are the services and writers (`config.Machine(getenv)`:
  env, that environment's `config.toml`, `busmon.toml`; `serviceQuota`/`Tokens`/`Clear`, `dispatch.PromptGrace`,
  `bus.ContextAlert`, `runawayThresholdsFromEnv`; the writers' caps per call through `bus.capFiles`, which
  reads no file in a test binary (`inTestBinary`, `testing.Testing`), so the person's own file never changes a test).
  `config.Path` reads `XDG_CONFIG_HOME`/`HOME` through
  the injected getenv only, absolute paths only, so a test's environment never reaches the real home;
  `config` does not import `bus` (bus will read its caps through it),
  so `ValidName` is repeated there, held equal by a test. busmon writes `config.toml` from its settings page
  (`C`, `ovSettings`, spec `docs/superpowers/specs/2026-11-01-busmon-config-page-mockups.md`, the person's answers:
  Display, Attention and Identity first, busmon's own display keys applied at once): `config.Set`/`Unset`
  (`config/write.go`: the key's own file rule, the smallest edit, its line replaced or added under its table or the
  table appended, the edited text read back as exactly that change, refused when the file does not parse, a temporary
  file renamed, 0600 in 0700), each key's `Group` (`config/group.go`, held by a test), the rows (`settingsGroups` in
  `settings_page.go`: `settingFrom` default, config.toml, elsewhere (a flag, the env or busmon.toml, read-only but
  busmon.toml's) or refused, `Set` when config.toml sets it to other than its default, the source in `agentbus config`'s
  words) and the interaction (`settings_ui.go`, settings-panels, its own key table `ModeSettings`/`settingsBindings`, reads
  and writes in commands): two panels below one row of top margin (about-trim) that carries the focus line (settings-focus-line, `settingsFocusLine`: `focus: groups`, `tabs` or `keys` and the key that moves it, muted with the focused word plain and bold, so it reads the same without colour; the hint, then `focus:`, give way on a narrow column; `keepSettingInView` scrolls to the top whenever the groups or tabs have the focus or the first key is selected, so the line is on screen then; a heavy accent bar `━` marks the focused panel's top edge: the sidebar's part of that row or the keys' column after the text on a wide page, behind the text above the tabs or the full-width row between the tabs and the keys on a narrow one, settings-focus-bar), the groups a sidebar (`settingsSideW`) beside the selected group's keys from a page
  of `settingsSideMin` (92) cells, a tab bar above them narrower (`settingsTabs`, tabBar's blocks, the far tabs giving way
  to an ellipsis); a value as a control (`settingControl`: `● on` ok / `○ off` muted, a choice `‹ v ›`, the rest plain, in one
  column after the group's longest label and a 3-cell gutter, settings-focus: `settingsLabelCol`, `settingsGutter`; elsewhere
  muted, refused the attention colour), `•` (`GlyphSet`) before a `Set` key; the selected key's detail
  zone (`settingsDetail`: label, file key, `from` source, default, read by, when a change applies; `settingHelp`). The focus (settings-focus,
  `settingsPage.keys`) is on the groups or the keys, the focused side lit (the group in the accent, the key's `▶` in the
  accent, the narrow tab a full pill) and the other muted (the tab dimmed by `frameFade`): wide, `↑↓` choose the group (its
  keys follow, the selection back to the first), `→` or Enter go to its keys, `←` back; narrow (the tab bar), `←→` step the
  tabs (wrapping), `↓` or Enter go to the keys, `↑` on the first key and `←` come back; Tab toggles; `↑↓` move among the keys
  (kept in view, `selLine`); the editor takes keys first, so on an open choice `←→` change its value, Enter writes, Esc
  closes it leaving the file as it is. Enter on a key flips an on/off at once, opens a choice or a line editor checked by the kind then the rule; `d` asks `y`, naming what
  applies next; `D` asks `y` naming the group's keys config.toml holds a line for (`settingsResettable` over `FileHeld`: a refused line
  and one a flag or the env shadows too, which the confirmation says still wins, never a `PersonOnly` key, said to stay), unset one by one then read back once; theme, fade, bell and the reminder and recap
  timings apply at once through `applySettings` and `shellConfig.retheme`, the rest at busmon's next start). `agentbus config`
  (`configcmd.go`, above the project-required exit, no broker) prints each value and its source;
  `--project` reads that project's env file for what its services see (only registry keys and the two
  per-project facts, never another line). The local LLM peer's endpoints are not registry keys:
  `[llm.<name>]` tables of `config.toml` only (`config/llm.go`, `File.Endpoints`; `url`, `model`,
  `remote`), each taken whole or dropped with a warning (a secret, user info, a query, an unknown
  key; a host other than `127.0.0.1`, `::1` or `localhost` without `remote = true`); `agentbus
  config` lists them by host and port, never the URL. `agentbus peer` (local-llm-peer step 1, package
  `peer/`) is the local model as a read-only test peer: `examples/roles.local-llm.toml` launches it through
  the adapter `scripts/local-llm-peer --spec-file <launch.json>`; the profile's `model` must equal its
  endpoint's (`Resolve`); one receiver lease from before the drain to its exit (`Bus.TakeReceiver`, then
  `Receiver.Drain` and `Deliver` under it, so the dispatcher sees rule 2 throughout); per entry (directive,
  challenge, reply; never a `wake=no` notice nor a reply from another local-llm peer; a tracked request
  accepted, then completed with the answer) a fresh turn (`Turn.Run`: the role prompt `roles/local.md`, the entry and its thread, at most
  8 tool calls, `turn_timeout`), the reply on the entry's thread, a report, its own `working`/`blocked`/`idle`;
  once the reply is sent, a failed report or status is logged, never answered again.
  An answer is cut under `CmdMaxRunes` with a note, and a reply the bus refuses for good is said, reported and
  acked (never turned again); pane lines carry no control but newline and tab, and neither they nor the answer carry a
  rune that reorders or hides text (`stripFormat`/`hidesText`: the bidi controls, the zero-width space, word joiners, the
  soft hyphen, the BOM, the deprecated, annotation, shorthand and musical format controls; the joiners, tag characters and Arabic number signs, which carry meaning, stay); errors carry the status, never the
  endpoint's body; the start's `/models` check has 5 s.
  The client follows no redirect and uses no proxy; only transport errors, 5xx, 408, 429 and 404 are retried
  (`blocked`, the entry kept, every 30 s), any other refusal fails the turn with a reply. Tools: `read_file`,
  `list_dir`, `grep` under the project root (peer-security: the template's `working_dir`, else the launch
  folder's Git top-level, never the cwd itself, an absolute path; the home from `$HOME`, else the passwd entry, else no start; `/`,
  `$HOME` and its ancestors refused, and (peer-residuals) a root at or under `$XDG_CONFIG_HOME` (else `~/.config`) or holding
  agentbus's configuration directory (`configRefusal`); no `..`, no `.git` (any case, like the denies: on a case-insensitive
  volume `.GIT` and `.ENV` open the real files, so names compare case-insensitively everywhere, fail closed),
  symlinks re-checked, then opened component by component from the root with `O_NOFOLLOW` (`openBeneath`), so a
  link swapped in after the checks is refused; directories read 256 names at a time (grep's own walk, list_dir's 501);
  regular files only; grep bounded by the turn and 20000 entries; the role's
  effective `Read(…)` denies with Claude Code's glob meaning, `~/` matched against the resolved home too, a bare
  `Read` denying everything, fail closed),
  `bus_thread`, `bus_agents`; no shell, no write, no verdict. `--check <endpoint>` asks its `/models`. Provider
  `local` is a built-in unlimited quota reader (listed by `agentbus quota` only with an endpoint). Health
  `llm-endpoint` (step 2; `health/llmendpoint.go` pure, `internal/dispatchsvc/llmcheck.go` the reader, every minute) probes
  only the endpoints the registered `local-llm` agents use (the one each peer resolved, `AgentSnapshot.Endpoint`, set by
  its own status with `EndpointFP`, its URL's fingerprint (HMAC under the host key `endpoint-fp.key` beside config.toml,
  0600, made once and linked whole: a bus reader cannot test a guessed path; no key, no fingerprint; a recorded one
  with an unreadable key is said, never ok), probed for the model the peer runs, an endpoint whose
  config.toml model or URL changed since the launch said; a snapshot
  without it matches by model), `GET /models`
  through `peer.Client` (no redirect, no proxy) within 2 s each, side by side: fail on no answer or an error
  status, warn when one answers without its model or a peer has no endpoint, ok when all list theirs; unknown
  with no such agent, an unreadable config.toml or no endpoint at all; for you on warn and fail
  (`agentbus peer --check <endpoint>`); the detail names host and port, never the URL's path; sentinel
  restarts nothing.
