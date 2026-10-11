# roles-go — port the role launcher from Python to Go

Goal: the role manifest code (`scripts/lib/role_config.py`, 323 lines, and the template generator
embedded in `scripts/bootstrap`) moves to Go. That removes the last Python from the project. The
behaviour stays identical: every error message, exit code, dry-run line and generated file.
The Python is the reference: when this brief and `role_config.py` disagree, `role_config.py`
(at `main`) wins.

Rules for every mission:
- Package `rolecfg/` (not `roles/`, which holds the role prompt files). Go 1.26, stdlib, plus
  `github.com/BurntSushi/toml` (added by t1 only).
- No Python, ever. Messages in English. Errors read `agent roles: <message>` when printed by the CLI.
- Work in your own git worktree from the base given in the directive. Commit only inside
  `allowed_paths`. Deliver with `agentbus request done <task> --commit <sha> --branch <b> "<summary>"`.
- Test fixtures go under `rolecfg/testdata/<config|launch|template>/` (one folder per mission).
- Tests never touch the live broker or herdr. The suite unsets `HERDR_ENV`, `HERDR_PANE_ID` and
  `REDIS_URL`; keep it that way.

## t1

**Manifest: load, validate, resolve** (`rolecfg/config.go`, tests, `go.mod`/`go.sum`).

Port `require`, `string`, `keys`, `load` and `resolve` exactly:
- `func Load(path string) (*Manifest, error)`. The path comes from `ROLES_TOML`, else
  `<repo>/roles.toml`; the caller passes it, resolved to an absolute path.
- `func (m *Manifest) Resolve(role string) (Profile, error)`.
- `func (m *Manifest) RolesByTier(tier string) []string`, in manifest order. **Order matters**:
  Python dicts keep the TOML order, so decode with `toml.MetaData.Keys()`, not by ranging over a map.
- `func (m *Manifest) Field(role, name string) (values []string, ok bool, err error)`, following
  `main()`'s `field` action: the role table first, then the profile, then the options. A list
  yields one value per line.

Resolution rules:
- Schema 1 (legacy) and schema 2, with every validation rule of `load` and `resolve`: names,
  tiers, skills, unknown fields, profiles, builtin options (claude-code, codex, kimi-cli),
  `reasoning` against `reasoning_levels`, `env_refs` restrictions, and adapters (the executable
  resolved relative to the manifest's directory).
- `Profile` carries every field `resolve` returns: client, provider, model, reasoning, levels,
  options (map[string]string), env_refs, profile name, role, legacy, skills, manifest, adapter.

Tests in `rolecfg/config_test.go`:
- the repo's `roles.toml` and `examples/roles.mixed.toml` load;
- one case per refusal, with its exact message;
- tier order;
- `Field` lookups.

## t2

**Prompt, cache, command, launch** (`rolecfg/launch.go`, tests).

Port `skill_sources`, `prompt_for`, `cache_file`, `build` and `launch`, using t1's `Manifest`/`Profile`.

**Environment overrides (same as Python):**
- `REPO_SKILLS`, `POCOCK_SKILLS_ROOT`, `ROLES_DIR`;
- `AGENT_LAUNCH_CACHE` / `XDG_CACHE_HOME` (cache dir 0700, sha256 name, atomic write);
- `AGENT_LAUNCH_DRYRUN`, `AGENT_LAUNCH_KEEP_API_KEY`, `HERDR_ENV`/`HERDR_PANE_ID`, `AGENTBUS_BIN`.

**Functions:**
- `func ShellJoin(args []string) string`, equal to Python `shlex.join`: safe characters
  `[A-Za-z0-9@%+=:,./_-]` stay bare, the empty string is `''`, anything else is single-quoted with
  `'"'"'`.
- `func Launch(m *Manifest, role, project string, env []string, stdout, stderr io.Writer, exec func(argv0 string, argv, env []string) error) error`.
  Production passes `syscall.Exec`.

**Launch sequence:**
1. The flag preflight with `<client> --help` (non-dry, non-legacy builtins).
2. Build the command.
3. Inside herdr, the bind child, run as
   `<AGENTBUS_BIN or os.Executable()> --project <p> bind <role> --pid <os.Getpid()>` with a 10 s
   timeout. On failure, print the same warning as Python and continue.
4. The environment edits: AGENT_BUS_* set, `CLAUDE_CODE_SESSION_ID` dropped, `env_refs` copied,
   `ANTHROPIC_API_KEY` dropped for claude-code unless `AGENT_LAUNCH_KEEP_API_KEY=1`.
5. Exec, resolving the binary via `exec.LookPath`.

**Dry run:** it prints the bind line (inside herdr), the command, and the
`profile=… client=… provider=… reasoning=…; skills=…` line, byte for byte as Python. The `reasoning`
value is `client-default` when unset.

Tests: build lines per client (claude-code, codex, kimi-cli, adapter), ShellJoin parity, the bind
child (stub binary), the warning on bind failure, the env edits, and the dry-run output.

## t3

**Skill links and the herdr-plus template** (`rolecfg/skills.go`, `rolecfg/template.go`, tests).

**`func InstallSkills(m *Manifest, env []string, stdout io.Writer) error`**, ported from
`install_skills`:
- `SKILLS_DEST`, else `~/.claude/skills` for claude-code and `~/.agents/skills` for the others;
- refuse a non-symlink target;
- preflight every link before the first write;
- skip a link that is already correct;
- replace atomically: create the symlink in a temp dir under the target's parent, then rename;
- print `linked <name> -> <source>`.

**`func Template(m *Manifest, manifestPath, project, cwd, scriptsDir, repo string, dispatch, missions bool) string`**
must output exactly what the Python heredoc in `scripts/bootstrap` (`write_template`) prints:
- `name`, `description`, `working_dir`;
- one `[[tabs]]` per boot-tier role, in manifest order, whose command is
  `env ROLES_TOML=<manifest> <scripts>/agent-launch <role> <project>`;
- then busmon, dispatch and missions.

Strings are `json.dumps(…, ensure_ascii=False)`: JSON string quoting, non-ASCII kept as is (Go
`json.Marshal` escapes `<`, `>` and `&`, and Python does not, so use an encoder with
`SetEscapeHTML(false)`). Commands are `shlex.join`; if t2 has not merged yet, use a local copy of
`ShellJoin` with the same rules, and t4 deduplicates.

Tests: golden output for a 2-role manifest with dispatch and missions on and off, a path with a space,
a non-ASCII project description, link creation, an idempotent rerun, and refusal of a regular file.

## t4

**Switch the scripts to Go, delete the Python** (`cmd/agentbus/roles.go` + test, `cmd/agentbus/main.go`,
`scripts/**`, `tests/**`, docs).

1. **The subcommand.** Add `agentbus roles <validate|exists <role>|tier <tier>|field <role> <name>|launch <role> <project>|install|template <project> <cwd> <scripts> <repo> <0|1 dispatch> <0|1 missions>>`.
   It needs no project and no broker (place it above the project-required exit, like `mem`). Exit
   codes as Python: 0, 1 on error (`agent roles: …` on stderr), 3 for a missing role in
   `exists`/`field`.
2. **The scripts.** Point `scripts/lib/roles.sh`, `scripts/agent-launch`,
   `scripts/link-role-skills.sh` and `scripts/bootstrap` (its `write_template`) at
   `"${AGENTBUS_BIN:-$REPO/agentbus}" roles …`, so that the launcher `exec`s it.
   - Resolve `ROLES_TOML` to an absolute path without Python (`realpath -m` or `cd`+`pwd`).
   - `tests/run.sh` builds `agentbus` into a temp dir and exports `AGENTBUS_BIN`.
3. **Parity first.** Run `tests/test_role_config.py` against the Go-backed scripts (it is black
   box) and make it pass unchanged.
4. **Then port and delete.** Port its 13 cases to Go tests (in `cmd/agentbus/roles_test.go` or
   `rolecfg/`, building fake clients as small shell scripts). Delete `scripts/lib/role_config.py`,
   `tests/test_role_config.py` and the unittest line in `tests/run.sh`, and remove any
   `python3`/`tomllib` left in `scripts/`.
5. **Docs.** Update README, CLAUDE.md, `install.sh` preflight (python3 no longer required) and
   `docs/memory/decisions/2026-09-27-provider-neutral-security.md`, where it names the Python
   resolver.

`grep -rn python scripts/ tests/ install.sh` returns nothing at the end.
