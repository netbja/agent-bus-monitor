- **One approval for a small change, per project** (single-approval step 2, spec
  2026-11-21-single-approval-design §3.1, §4; the person's §6 answers).
  - **The settings**, in `config.toml`'s `[project.<name>.delivery]` (then `[delivery]`, then the
    default; never an env variable): `single_approval` (`off`, or `small`), `small_max_lines` (150),
    `small_max_files` (5) and `same_provider_ok` (`off`, `docs`, `small`) are the person's:
    `agentbus config set` refuses them. `same_provider_ok` is read from the project's table only (a
    `[delivery]` line is ignored with a warning). `never_small` (globs added to deliver's defaults) only
    narrows the gate, so agents may set it.
  - **With `single_approval = "small"`,** `deliver` accepts one current approval on the head when:
    - no reviewer's latest verdict on the subject is a reject (`reject` or `changes`), on any head:
      an objection is answered by the one who raised it (§6 Q8);
    - the change is small (`size:` in `--dry-run`);
    - the approver is of another provider than the author (an empty provider is never "another"), or
      of the same one and the change is in `same_provider_ok`'s class: `docs` (every path `docs/**`
      or `*.md`) or `small`.
  - **The PR body and the bus** carry `single approval (small: N lines, M files; approved by R)`, or
    `single approval, same provider (small docs: …)`; `--dry-run` prints `approval: …`.
  - **Refusals** say which rule failed. Above the limits, two approvals or the person's yes with
    `--single-approval "<the person's yes, ask <id>>"`, as before. With two approvals nothing is read.
