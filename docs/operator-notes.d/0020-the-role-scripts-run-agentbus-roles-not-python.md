- **The role scripts run `agentbus roles`, not Python.** `scripts/lib/roles.sh`, `agent-launch`,
  `link-role-skills.sh` and `bootstrap`'s template call `${AGENTBUS_BIN:-$REPO/agentbus} roles …`
  (package `rolecfg/`: manifest, launch, skills, template — the port of the removed
  `role_config.py`; its block in `main()` sits above the project-required exit, like `mem`). The
  repo binary must be built first (`./setup.sh install`); `tests/run.sh` and `tests/lib.sh` build one.
  `agent-launch` `exec`s `agentbus`, which binds and then `syscall.Exec`s the client: the pid
  never changes, which is what `bind` relies on. Outputs are kept byte-identical to the Python
  (dry runs, adapter spec JSON via `pyJSON`, template); `tests/roles_scripts_test.sh` covers the
  scripts end to end.
  `coder2` (role-coder2, the person 2026-11-08) is a pop role, `agent-spawn coder2 <p>`: coder's
  profile, skills and deny on `deliver`, held equal by `rolecfg/coder2_test.go`; `roles/coder2.md`
  adds the rules for two implementers (its own worktree per task from `origin/main`, never the
  shared tree, never a package another task has in flight: read the board, the requests and the
  worktrees first). Nothing new enforces the split: the board's claim refuses a task another owner
  holds and only a request's target accepts it (`bus/twocoders_test.go` names both coders,
  concurrent claims included). `roles reviewers` still reads coder's provider: coder2 shares it.
  Specialist roles (spec `docs/superpowers/specs/2026-10-21-specialist-roles-design.md`):
  `roles.toml` is schema 2 since then (profiles; `rolecfg/migration_test.go` keeps every launch
  equal to the last schema-1 manifest, `rolecfg/testdata/roles.v1.toml`);
  `examples/roles.specialists.toml` holds six pop roles with prompts in `roles/`. Verbs:
  `roles reviewers` (models of other providers than coder's that can review; never the author's
  own maker served elsewhere), `roles skill-users <skill>...` (whom to tell when skills change),
  `roles draft <role> --task …` (an inert draft in `.agent-bus/drafts`, skills from the launcher's
  roots, least privileged) and `roles adopt <role> --ask <id>` (needs the project's bus, like
  `doctor`: only after an `adopt` answer not given by the asker; appends to the manifest after
  checking it loads, never overwrites a prompt). `infra` is a pop role (Ansible, Terraform/OpenTofu,
  IT automation), adopted (#187) from the reviewed draft `examples/drafts/infra/` with `roles adopt infra
  --ask <id> --from examples/drafts`: manual profile, approval person; allowed only exact syntax/lint invocations, no wildcard
  (`terraform|tofu fmt -check [-recursive]`, `validate`, `tflint [--recursive]`, `ansible-lint
  --offline`, `yamllint .`); everything else prompts in the pane, and asked as well: what evaluates
  providers or data sources, touches a target or downloads (`plan`, `init`, `refresh`, `get`, `test`, `ansible-playbook --check`/`--diff`,
  ad-hoc `ansible`, `ansible-pull`, `ansible-galaxy`, `tflint --init`) and every change (apply,
  destroy, import, taint, state surgery, force-unlock, workspace) and every `ansible-playbook` run,
  one `terraform *<verb>*` pattern per tool
  so `-chdir=` forms match; denied: state, plan
  and tfvars files, vault passwords, cloud credentials, `output`/`show`/`console`/`state pull|show`,
  `env` (`rolecfg/infra_test.go`, a command table per class; it also holds the manifest to the
  draft's entry byte for byte and `roles/infra.md` to the draft's prompt). A role popped only through
  the person (deploy, infra) is an ask in master's rules; `TestShippedManifestRules` and the
  migration test derive that from `approval`.
