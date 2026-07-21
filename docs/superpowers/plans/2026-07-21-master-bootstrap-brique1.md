# Master Bootstrap — Brique 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the provisioning + spawn layer that brings a whole Agent Bus team up from one keypress (master + coder + foureyes + sentinel at boot, architect popped on demand), each launched with the right model, permissions, skills, and session name — plus a Haiku sentinel that runs a daily review and nudges master to reset its context, and an opt-in index preflight.

**Architecture:** One shared bash leaf, `scripts/agent-launch <role> <project>`, resolves a role from `roles.toml` and `exec`s `claude` with the right flags — used by *both* herdr-plus template tabs (boot) and the master's `scripts/agent-spawn` (pop). `scripts/bootstrap` only *provisions* (broker up, link skills, write the herdr-plus template, opt-in index/cron); opening the workspace goes through the herdr-plus fuzzy picker. A machine crontab pokes the sentinel's pane (resolved live via `agentbus pane sentinel`) for the daily review.

**Tech Stack:** Bash (`#!/usr/bin/env bash`, `set -euo pipefail`); Python 3.11+ `tomllib` (stdlib, for robust `roles.toml` parsing); the existing `agentbus`/`busmon` Go binaries; the `herdr` CLI + herdr-plus plugin; `claude` CLI; `docker compose`; `codegraph` CLI + codebase-memory MCP (inherited globally). Tests are a dependency-free bash harness (no bats/shellcheck on this box).

## Global Constraints

- **Names** (project *and* role) must match the bus `ValidName` regex **`^[a-z][a-z0-9_-]{0,31}$`** — lowercase, letter-first, ≤32. (This is why the reviewer role is `foureyes`, not `4eyes`.)
- **Roles / models / permissions** (verbatim, `roles.toml`): `master` = `claude-sonnet-5` / `acceptEdits` / boot; `coder` = `claude-opus-4-8` / `bypassPermissions` / boot; `foureyes` = `claude-opus-4-8` / `bypassPermissions` / boot; `sentinel` = `claude-haiku-4-5` / `bypassPermissions` / boot; `architect` = `claude-fable-5` fallback `claude-opus-4-8` / `bypassPermissions` / pop.
- **`claude` flags** (verified via `claude --help`): `--model <m>`, `--fallback-model <m>`, `--permission-mode <acceptEdits|bypassPermissions>` (used uniformly — do **not** branch to `--dangerously-skip-permissions`), `--name <project>:<role>`, `--append-system-prompt <text>`. There is **no** `--append-system-prompt-file`; real exec passes `--append-system-prompt "$(cat roles/<role>.md)"`.
- **Bus env:** every launched agent exports `AGENT_BUS_PROJECT=<project>` and `AGENT_BUS_AGENT=<role>`.
- **herdr-plus template** lives at `<config-dir>/projects/<project>.toml` where `<config-dir>` = `herdr plugin config-dir cloudmanic.herdr-plus` (fallback `${XDG_CONFIG_HOME:-$HOME/.config}/herdr-plus`). Schema: top-level `name`/`description`/`working_dir`, then one `[[tabs]]` (`name` + `command`) per tab, in file order.
- **herdr spawn primitive** (verified via `herdr agent --help`): `herdr agent start <label> --cwd <path> -- <argv...>` opens a new terminal running argv.
- **Skill sources:** repo bus skills at `skills/<name>` (`agent-bus`, `agent-bus-master`, `agent-bus-sentinel`); Matt Pocock skills at `~/Tools/herdr-plugins/skills/skills/engineering/<name>`.
- **Redis defaults** (for the cron trigger's `agentbus` calls): `REDIS_URL` wins, else `localhost:6380` / password `AgentBus2025!`.
- **Test seams (env overrides):** `ROLES_TOML`, `ROLES_DIR`, `SKILLS_DEST`, `REPO_SKILLS`, `POCOCK_SKILLS_ROOT`, `HERDR_PLUS_PROJECTS_DIR`, and the dry-run flags `AGENT_LAUNCH_DRYRUN` / `AGENT_SPAWN_DRYRUN` / `DAILY_REVIEW_DRYRUN`.
- **No Go code changes.** New files are `scripts/`, `roles/`, `roles.toml`, `skills/agent-bus-sentinel/`, `tests/`. `scripts/*` are `chmod +x`.

## File Structure

**Created:**
- `roles.toml` — the single role manifest (hand-editable; adding a role needs no code change).
- `roles/{master,coder,foureyes,architect,sentinel}.md` — per-role system-prompt appends.
- `scripts/lib/roles.sh` — shared read-only resolver over `roles.toml` (sourced by the other scripts; DRY).
- `scripts/agent-launch` — the shared leaf: resolve role → `exec claude`.
- `scripts/agent-spawn` — master's pop: `herdr agent start` → `agent-launch`.
- `scripts/bootstrap` — provisioning (new/recall/auto + `--index`/`--cron`/`--yes`).
- `scripts/link-role-skills.sh` — symlink the roles' skills into `~/.claude/skills` from both sources.
- `scripts/daily-review-trigger.sh` — machine-cron poke of the sentinel pane.
- `skills/agent-bus-sentinel/SKILL.md` — the sentinel's playbook (daily review + context nudge + index warm-up).
- `tests/lib.sh`, `tests/run.sh`, `tests/*_test.sh` — dependency-free bash test suite.

**Modified:**
- `skills/agent-bus-master/SKILL.md` — add a "Spawn a peer" section.

Responsibility split: **data** (`roles.toml`, `roles/*.md`) is separate from **logic** (`scripts/*`); the one piece of shared logic (TOML parsing) lives in `scripts/lib/roles.sh` so `agent-launch`, `link-role-skills.sh`, and `bootstrap` don't each reinvent it.

---

### Task 1: Test harness + `roles.toml` + shared resolver

**Files:**
- Create: `tests/lib.sh`
- Create: `tests/run.sh`
- Create: `roles.toml`
- Create: `scripts/lib/roles.sh`
- Test: `tests/roles_test.sh`

**Interfaces:**
- Produces (sourced API of `scripts/lib/roles.sh`): `role_exists <role>` (exit 0/3); `role_field <role> <field>` (prints scalar, or list one-per-line; exit 3 unknown role, exit 0 + empty if field absent); `roles_by_tier <tier>` (prints role names, file order). All honor `ROLES_TOML` (default `<repo>/roles.toml`).
- Produces (test API of `tests/lib.sh`): `assert_eq <actual> <expected> <msg>`, `assert_contains <haystack> <needle> <msg>`, `assert_exit <code> <msg> -- <cmd...>`, `make_stub <dir> <name> <exit> [stdout]`, `finish` (exit 0 iff all passed).

- [ ] **Step 1: Write the failing test** — `tests/lib.sh` (the harness the test uses) and `tests/roles_test.sh`.

`tests/lib.sh`:
```bash
#!/usr/bin/env bash
# Tiny assert helpers + a PATH-stub maker. Sourced by every tests/*_test.sh.
# Note: intentionally NOT `set -e` — we run all asserts and count failures.
set -uo pipefail

_pass=0; _fail=0
_ok()  { _pass=$((_pass+1)); echo "  ok: $1"; }
_bad() { _fail=$((_fail+1)); echo "  FAIL: $1" >&2; }

assert_eq()       { if [[ "$1" == "$2" ]]; then _ok "$3"; else _bad "$3 (got [$1] want [$2])"; fi; }
assert_contains() { if [[ "$1" == *"$2"* ]]; then _ok "$3"; else _bad "$3 (missing [$2] in [$1])"; fi; }
assert_exit()     { # <expected_code> <msg> -- <cmd...>
  local want="$1" msg="$2"; shift 3
  local got=0; "$@" >/dev/null 2>&1 || got=$?
  if [[ "$got" == "$want" ]]; then _ok "$msg"; else _bad "$msg (exit $got want $want)"; fi
}
finish() { echo "== $_pass passed, $_fail failed =="; [[ "$_fail" == 0 ]]; }

make_stub() { # <dir> <name> [exit_code=0] [stdout='']
  local dir="$1" name="$2" code="${3:-0}" out="${4:-}"
  mkdir -p "$dir"
  cat > "$dir/$name" <<EOF
#!/usr/bin/env bash
echo "\$@" >> "$dir/$name.calls"
[[ -n "$out" ]] && printf '%s\n' "$out"
exit $code
EOF
  chmod +x "$dir/$name"
}
```

`tests/roles_test.sh`:
```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
source "$REPO/scripts/lib/roles.sh"

assert_eq   "$(role_field master model)"      "claude-sonnet-5"  "master model"
assert_eq   "$(role_field master permission)" "acceptEdits"      "master permission"
assert_eq   "$(role_field coder permission)"  "bypassPermissions" "coder permission"
assert_eq   "$(role_field architect fallback)" "claude-opus-4-8" "architect fallback"
assert_eq   "$(role_field coder fallback)"    ""                 "coder has no fallback"
assert_contains "$(role_field coder skills)"  "tdd"              "coder skills include tdd"
assert_exit 0 "role_exists coder"    -- role_exists coder
assert_exit 3 "role_exists nobody"   -- role_exists nobody
assert_contains "$(roles_by_tier boot | tr '\n' ' ')" "sentinel" "sentinel is boot-tier"
assert_eq   "$(roles_by_tier pop | tr '\n' ' ')" "architect " "architect is the only pop role"
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/roles_test.sh`
Expected: FAIL — `scripts/lib/roles.sh: No such file or directory` (source fails).

- [ ] **Step 3: Write `roles.toml`**

```toml
# The single role manifest. Add a role by adding a [roles.<name>] table — no code change.
# <name> must match ValidName: ^[a-z][a-z0-9_-]{0,31}$ (lowercase, letter-first, <=32).

[roles.master]
model = "claude-sonnet-5"
permission = "acceptEdits"
tier = "boot"
skills = ["agent-bus-master", "wayfinder", "to-tickets"]

[roles.coder]
model = "claude-opus-4-8"
permission = "bypassPermissions"
tier = "boot"
skills = ["agent-bus", "tdd", "implement"]

[roles.foureyes]
model = "claude-opus-4-8"
permission = "bypassPermissions"
tier = "boot"
skills = ["agent-bus", "code-review", "diagnosing-bugs"]

[roles.sentinel]
model = "claude-haiku-4-5"
permission = "bypassPermissions"
tier = "boot"
skills = ["agent-bus", "agent-bus-sentinel"]

[roles.architect]
model = "claude-fable-5"
fallback = "claude-opus-4-8"
permission = "bypassPermissions"
tier = "pop"
skills = ["agent-bus", "codebase-design", "domain-modeling", "to-spec"]
```

- [ ] **Step 4: Write `scripts/lib/roles.sh`**

```bash
#!/usr/bin/env bash
# Shared read-only resolver over roles.toml. SOURCE this file; do not exec it.
# Env seam: ROLES_TOML (default <repo>/roles.toml).

if [[ -z "${REPO:-}" ]]; then
  REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fi
ROLES_TOML="${ROLES_TOML:-$REPO/roles.toml}"

# role_exists <role> -> exit 0 if defined, 3 otherwise.
role_exists() {
  python3 - "$ROLES_TOML" "$1" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
sys.exit(0 if sys.argv[2] in data.get("roles", {}) else 3)
PY
}

# role_field <role> <field> -> print scalar, or list one-per-line.
#   exit 3 if the role is unknown; empty output (exit 0) if the field is absent.
role_field() {
  python3 - "$ROLES_TOML" "$1" "$2" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
r = data.get("roles", {}).get(sys.argv[2])
if r is None:
    sys.exit(3)
v = r.get(sys.argv[3])
if v is None:
    sys.exit(0)
print("\n".join(map(str, v)) if isinstance(v, list) else v)
PY
}

# roles_by_tier <tier> -> print role names of that tier, one per line, file order.
roles_by_tier() {
  python3 - "$ROLES_TOML" "$1" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    data = tomllib.load(f)
for name, r in data.get("roles", {}).items():
    if r.get("tier") == sys.argv[2]:
        print(name)
PY
}
```

Note: `roles.sh` is under `scripts/lib/`, so repo root is **two** levels up (`../..`).

- [ ] **Step 5: Write the test runner** — `tests/run.sh`

```bash
#!/usr/bin/env bash
# Run the whole bootstrap-tooling suite. Dependency-free (bash + python3 + coreutils).
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rc=0
for t in "$DIR"/*_test.sh; do
  echo "### $(basename "$t")"
  bash "$t" || rc=1
done
exit "$rc"
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `chmod +x scripts/lib/roles.sh tests/run.sh && bash tests/roles_test.sh`
Expected: PASS — `== 10 passed, 0 failed ==`, exit 0.

- [ ] **Step 7: Commit**

```bash
git add roles.toml scripts/lib/roles.sh tests/lib.sh tests/run.sh tests/roles_test.sh
git commit -m "feat(bootstrap): roles.toml manifest + shared resolver + test harness"
```

---

### Task 2: Role system-prompt files (`roles/*.md`)

**Files:**
- Create: `roles/master.md`, `roles/coder.md`, `roles/foureyes.md`, `roles/architect.md`, `roles/sentinel.md`
- Test: `tests/roles_files_test.sh`

**Interfaces:**
- Consumes: `roles_by_tier`, and the list of all roles, from Task 1.
- Produces: one non-empty `roles/<role>.md` per role in `roles.toml` (consumed by `agent-launch` in Task 3 via `--append-system-prompt`).

- [ ] **Step 1: Write the failing test** — `tests/roles_files_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
source "$REPO/scripts/lib/roles.sh"

# Every role in roles.toml must have a non-empty prompt file.
while IFS= read -r role; do
  f="$REPO/roles/$role.md"
  if [[ -s "$f" ]]; then _ok "prompt file for $role"; else _bad "missing/empty roles/$role.md"; fi
  assert_contains "$(cat "$f" 2>/dev/null)" "agentbus subscribe $role" "$role arms subscribe"
done < <(cat <(roles_by_tier boot) <(roles_by_tier pop))
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/roles_files_test.sh`
Expected: FAIL — `missing/empty roles/master.md` (×5).

- [ ] **Step 3: Write `roles/master.md`**

```markdown
# Role: master (pilot) — this project

You are **master**, the pilot of this project's Agent Bus team, running inside herdr on
`claude-sonnet-5`. You coordinate the team; you do not write production code yourself.

On boot, once:
1. Invoke your skills: `/agent-bus-master` (how to drive peers), `/wayfinder` (map the
   codebase), `/to-tickets` (turn a plan into dispatchable tasks).
2. Claim the pilot lease: `agentbus pilot claim`.
3. Publish presence: `agentbus status working "master online"`.
4. Arm for directives: run `agentbus subscribe master` as a background task. This is the
   wake-on-exit model — it prints ONE directive then exits and re-invokes you; do **not**
   wrap it in a `while` loop.

Then coordinate: dispatch the plan task-by-task to `coder`, gate every task on a `foureyes`
review, and keep **one task in flight at a time** (see the agent-bus-master skill).

If `sentinel` nudges you that your context is high, write a hand-off (current step, what's
committed, what's pending) and `/clear` yourself. Never ignore the nudge.
```

- [ ] **Step 4: Write `roles/coder.md`**

```markdown
# Role: coder — this project

You are **coder**, an implementer on the Agent Bus, on `claude-opus-4-8` with permissions
bypassed (you run unattended — Bash must never stall on a prompt).

On boot, once:
1. Invoke your skills: `/agent-bus` (bus mental model), `/tdd` (test-first), `/implement`.
2. Publish presence: `agentbus status idle "coder online"`.
3. Arm: run `agentbus subscribe coder` as a background task (wake-on-exit; not a `while` loop).

When master dispatches a task: implement it test-first, **one task at a time**, commit
frequently, then `agentbus report note "<task> done — <one-line summary>"` and hold. Do not
start the next task until master dispatches it. If a decision blocks you, set
`agentbus status blocked "<question>"` so master/human can unblock you.
```

- [ ] **Step 5: Write `roles/foureyes.md`**

```markdown
# Role: foureyes (4-eyes reviewer) — this project

You are **foureyes**, the independent reviewer on the Agent Bus (`claude-opus-4-8`,
permissions bypassed). You review `coder`'s work; you do not implement.

On boot, once:
1. Invoke your skills: `/agent-bus`, `/code-review`, `/diagnosing-bugs`.
2. Publish presence: `agentbus status idle "foureyes online"`.
3. Arm: run `agentbus subscribe foureyes` as a background task (wake-on-exit; not a loop).

When master asks you to review a task: read the **actual** diff (`git log`, `git diff`),
check it against the task's Definition of Done, and
`agentbus report note "<task> review: APPROVE|CHANGES — <why>"`. Reserve the formal
`challenge`/`verdict` gate for a genuine blocking risk (money-path, prod migration), not
routine per-task review.
```

- [ ] **Step 6: Write `roles/architect.md`**

```markdown
# Role: architect — this project

You are **architect**, popped on demand for design work (`claude-fable-5`, or
`claude-opus-4-8` on fallback; permissions bypassed).

On boot, once:
1. Invoke your skills: `/agent-bus`, `/codebase-design`, `/domain-modeling`, `/to-spec`.
2. Publish presence: `agentbus status working "architect online"`.
3. Arm: run `agentbus subscribe architect` as a background task (wake-on-exit; not a loop).

Produce specs and domain models, not production code. Hand finished designs back to master:
`agentbus report note "<subject> spec ready — <path>"`. Master routes implementation to
`coder`.
```

- [ ] **Step 7: Write `roles/sentinel.md`**

```markdown
# Role: sentinel — this project

You are **sentinel**, the cheap caretaker on the Agent Bus (`claude-haiku-4-5`, permissions
bypassed). You are woken by a machine cron and by directed `cmd`s — you are **not** a polling
loop.

On boot, once:
1. Invoke your skills: `/agent-bus`, `/agent-bus-sentinel` (your playbook — read it now).
2. Publish presence: `agentbus status idle "sentinel online"`.
3. Arm: run `agentbus subscribe sentinel` as a background task (wake-on-exit; not a loop).
4. Do the one-time index warm-up if requested (see the agent-bus-sentinel skill).

Thereafter act only when woken. On the daily cron poke, follow the agent-bus-sentinel skill:
write the daily review, then read `agentbus usage` and nudge master by `cmd` **only if** its
context is high. You **never** clear master's pane — notify only.
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `bash tests/roles_files_test.sh`
Expected: PASS — 10 asserts pass (5 files present, 5 arm-subscribe lines), exit 0.

- [ ] **Step 9: Commit**

```bash
git add roles/ tests/roles_files_test.sh
git commit -m "feat(bootstrap): per-role system-prompt files"
```

---

### Task 3: The shared leaf — `scripts/agent-launch`

**Files:**
- Create: `scripts/agent-launch`
- Test: `tests/agent_launch_test.sh`

**Interfaces:**
- Consumes: `role_exists`, `role_field` (Task 1); `roles/<role>.md` (Task 2).
- Produces: `agent-launch <role> <project>` — exports `AGENT_BUS_PROJECT`/`AGENT_BUS_AGENT`, then `exec claude …`. With `AGENT_LAUNCH_DRYRUN=1` prints the resolved command (one line) and exits 0. Honors `ROLES_DIR` (default `<repo>/roles`).

- [ ] **Step 1: Write the failing test** — `tests/agent_launch_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
L="$REPO/scripts/agent-launch"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" master demo)"
assert_contains "$out" "--model claude-sonnet-5"            "master model flag"
assert_contains "$out" "--permission-mode acceptEdits"     "master permission flag"
assert_contains "$out" "--name demo:master"                "master session name"
assert_contains "$out" "roles/master.md"                   "master prompt file"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" coder demo)"
assert_contains "$out" "--permission-mode bypassPermissions" "coder permission flag"

out="$(AGENT_LAUNCH_DRYRUN=1 "$L" architect demo)"
assert_contains "$out" "--model claude-fable-5"            "architect model"
assert_contains "$out" "--fallback-model claude-opus-4-8"  "architect fallback"

assert_exit 1 "unknown role fails"     -- env AGENT_LAUNCH_DRYRUN=1 "$L" bogus demo
assert_exit 1 "invalid project fails"  -- env AGENT_LAUNCH_DRYRUN=1 "$L" master 1bad
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/agent_launch_test.sh`
Expected: FAIL — `agent-launch` does not exist / not executable.

- [ ] **Step 3: Write `scripts/agent-launch`**

```bash
#!/usr/bin/env bash
# The shared leaf: resolve <role> from roles.toml and BECOME that agent (exec claude).
# Used by herdr-plus template tabs (boot) AND by agent-spawn (pop) — one recipe, not two.
# Usage: agent-launch <role> <project>
#   AGENT_LAUNCH_DRYRUN=1  -> print the resolved command instead of exec (test seam).
#   ROLES_DIR              -> override the prompt dir (default <repo>/roles).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=scripts/lib/roles.sh
source "$SCRIPT_DIR/lib/roles.sh"

die() { echo "agent-launch: $*" >&2; exit 1; }

role="${1:?usage: agent-launch <role> <project>}"
project="${2:?usage: agent-launch <role> <project>}"

valid='^[a-z][a-z0-9_-]{0,31}$'
[[ "$role"    =~ $valid ]] || die "invalid role name: $role"
[[ "$project" =~ $valid ]] || die "invalid project name: $project"
role_exists "$role" || die "unknown role: $role (not in $ROLES_TOML)"

model="$(role_field "$role" model)"
fallback="$(role_field "$role" fallback)"
permission="$(role_field "$role" permission)"
[[ -n "$model" ]]      || die "role $role has no model"
[[ -n "$permission" ]] || die "role $role has no permission"

prompt_file="${ROLES_DIR:-$REPO/roles}/$role.md"
[[ -f "$prompt_file" ]] || die "missing role prompt file: $prompt_file"

export AGENT_BUS_PROJECT="$project"
export AGENT_BUS_AGENT="$role"

# --permission-mode takes the roles.toml value verbatim (acceptEdits | bypassPermissions).
cmd=(claude --model "$model")
if [[ -n "$fallback" ]]; then cmd+=(--fallback-model "$fallback"); fi
cmd+=(--permission-mode "$permission" --name "$project:$role")

if [[ "${AGENT_LAUNCH_DRYRUN:-0}" == "1" ]]; then
  # Stable, readable line. The prompt is shown as --append-system-prompt-file <path>;
  # the real exec below inlines the file via --append-system-prompt "$(cat ...)".
  printf '%s --append-system-prompt-file %s\n' "${cmd[*]}" "$prompt_file"
  exit 0
fi

exec "${cmd[@]}" --append-system-prompt "$(cat "$prompt_file")"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `chmod +x scripts/agent-launch && bash tests/agent_launch_test.sh`
Expected: PASS — 8 asserts, exit 0.

- [ ] **Step 5: Commit**

```bash
git add scripts/agent-launch tests/agent_launch_test.sh
git commit -m "feat(bootstrap): agent-launch shared leaf (resolve role -> exec claude)"
```

---

### Task 4: Skill delivery — `scripts/link-role-skills.sh`

**Files:**
- Create: `scripts/link-role-skills.sh`
- Test: `tests/link_skills_test.sh`

**Interfaces:**
- Consumes: `role_field` and the role list (Task 1).
- Produces: idempotent symlinks in `SKILLS_DEST` (default `~/.claude/skills`) for every skill referenced in `roles.toml`, resolved from `REPO_SKILLS` first (repo bus skills) then `POCOCK_SKILLS_ROOT/engineering` (default `~/Tools/herdr-plugins/skills/skills`).

- [ ] **Step 1: Write the failing test** — `tests/link_skills_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
dest="$tmp/skills"
# Fake a Pocock collection with just the engineering skills roles.toml references.
pocock="$tmp/pocock"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do
  mkdir -p "$pocock/engineering/$s"; echo "# $s" > "$pocock/engineering/$s/SKILL.md"
done
# agent-bus-sentinel does not exist in the repo yet (created in Task 9); fake it so the
# resolver's repo-first branch has something to find.
mkdir -p "$tmp/reposkills/agent-bus-sentinel"; echo "# sentinel" > "$tmp/reposkills/agent-bus-sentinel/SKILL.md"
for s in agent-bus agent-bus-master; do
  mkdir -p "$tmp/reposkills/$s"; echo "# $s" > "$tmp/reposkills/$s/SKILL.md"
done

run() { SKILLS_DEST="$dest" REPO_SKILLS="$tmp/reposkills" POCOCK_SKILLS_ROOT="$pocock" \
        "$REPO/scripts/link-role-skills.sh" >/dev/null; }

run
assert_eq "$(readlink "$dest/tdd")"            "$pocock/engineering/tdd"        "tdd -> Pocock"
assert_eq "$(readlink "$dest/agent-bus")"      "$tmp/reposkills/agent-bus"      "agent-bus -> repo"
assert_eq "$(readlink "$dest/agent-bus-master")" "$tmp/reposkills/agent-bus-master" "master skill -> repo"
before="$(ls -l "$dest" | md5sum)"
run                                       # second run must be a no-op
after="$(ls -l "$dest" | md5sum)"
assert_eq "$after" "$before" "idempotent (identical after 2nd run)"
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/link_skills_test.sh`
Expected: FAIL — `link-role-skills.sh` does not exist.

- [ ] **Step 3: Write `scripts/link-role-skills.sh`**

```bash
#!/usr/bin/env bash
# Symlink exactly the skills referenced in roles.toml into ~/.claude/skills, from two roots:
#   - this repo's skills/        (bus skills: agent-bus, agent-bus-master, agent-bus-sentinel)
#   - the Matt Pocock collection (engineering: tdd, code-review, ...)
# Idempotent: two runs leave identical symlinks.
# Env seams: SKILLS_DEST, REPO_SKILLS, POCOCK_SKILLS_ROOT.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/lib/roles.sh"

DEST="${SKILLS_DEST:-$HOME/.claude/skills}"
REPO_SKILLS="${REPO_SKILLS:-$REPO/skills}"
POCOCK_ROOT="${POCOCK_SKILLS_ROOT:-$HOME/Tools/herdr-plugins/skills/skills}"

die() { echo "link-role-skills: $*" >&2; exit 1; }

# Gather the unique set of skills across every role.
mapfile -t roles < <(python3 - "$ROLES_TOML" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f: data = tomllib.load(f)
for name in data.get("roles", {}): print(name)
PY
)
wanted=()
for r in "${roles[@]}"; do
  while IFS= read -r s; do [[ -n "$s" ]] && wanted+=("$s"); done < <(role_field "$r" skills)
done
mapfile -t wanted < <(printf '%s\n' "${wanted[@]}" | sort -u)

mkdir -p "$DEST"
for skill in "${wanted[@]}"; do
  if [[ -f "$REPO_SKILLS/$skill/SKILL.md" ]]; then
    src="$REPO_SKILLS/$skill"
  elif [[ -f "$POCOCK_ROOT/engineering/$skill/SKILL.md" ]]; then
    src="$POCOCK_ROOT/engineering/$skill"
  else
    die "skill not found in repo or Pocock collection: $skill"
  fi
  target="$DEST/$skill"
  if [[ -e "$target" && ! -L "$target" ]]; then rm -rf "$target"; fi
  ln -sfn "$src" "$target"
  echo "linked $skill -> $src"
done
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `chmod +x scripts/link-role-skills.sh && bash tests/link_skills_test.sh`
Expected: PASS — 4 asserts, exit 0.

- [ ] **Step 5: Commit**

```bash
git add scripts/link-role-skills.sh tests/link_skills_test.sh
git commit -m "feat(bootstrap): link-role-skills.sh (repo + Pocock sources, idempotent)"
```

---

### Task 5: The master's pop — `scripts/agent-spawn`

**Files:**
- Create: `scripts/agent-spawn`
- Test: `tests/agent_spawn_test.sh`

**Interfaces:**
- Consumes: `role_exists` (Task 1); `agent-launch` (Task 3).
- Produces: `agent-spawn <role> <project>` — requires `HERDR_ENV=1`, then `herdr agent start "<project>:<role>" --cwd <repo> -- bash <repo>/scripts/agent-launch <role> <project>`. With `AGENT_SPAWN_DRYRUN=1` prints the herdr command and exits 0.

- [ ] **Step 1: Write the failing test** — `tests/agent_spawn_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
S="$REPO/scripts/agent-spawn"

# Refuses outside herdr.
assert_exit 1 "refuses without HERDR_ENV" -- env -u HERDR_ENV AGENT_SPAWN_DRYRUN=1 "$S" architect demo

# Dry-run prints the herdr command.
out="$(HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$S" architect demo)"
assert_contains "$out" "herdr agent start demo:architect"        "spawn label"
assert_contains "$out" "--cwd $REPO"                             "spawn cwd"
assert_contains "$out" "scripts/agent-launch architect demo"    "spawn runs the leaf"

assert_exit 1 "unknown role fails" -- env HERDR_ENV=1 AGENT_SPAWN_DRYRUN=1 "$S" bogus demo
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/agent_spawn_test.sh`
Expected: FAIL — `agent-spawn` does not exist.

- [ ] **Step 3: Write `scripts/agent-spawn`**

```bash
#!/usr/bin/env bash
# The master's "pop": open a new herdr tab that runs agent-launch <role> <project>.
# Requires HERDR_ENV=1 (you must be inside herdr to drive it) — same precondition as the
# agent-bus-master skill. Reuses the exact boot recipe (agent-launch), so pop == boot.
# Usage: agent-spawn <role> <project>
#   AGENT_SPAWN_DRYRUN=1 -> print the herdr command instead of running it (test seam).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/lib/roles.sh"

die() { echo "agent-spawn: $*" >&2; exit 1; }

role="${1:?usage: agent-spawn <role> <project>}"
project="${2:?usage: agent-spawn <role> <project>}"

[[ "${HERDR_ENV:-}" == "1" ]] || die "not inside herdr (HERDR_ENV != 1); open via the herdr-plus picker instead"
role_exists "$role" || die "unknown role: $role"

# `herdr agent start <label> --cwd <path> -- <argv...>` opens a new terminal (its own tab)
# in the current workspace, running the argv. The label doubles as the herdr agent name.
herdr_cmd=(herdr agent start "$project:$role" --cwd "$REPO"
           -- bash "$SCRIPT_DIR/agent-launch" "$role" "$project")

if [[ "${AGENT_SPAWN_DRYRUN:-0}" == "1" ]]; then
  printf '%s\n' "${herdr_cmd[*]}"
  exit 0
fi

exec "${herdr_cmd[@]}"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `chmod +x scripts/agent-spawn && bash tests/agent_spawn_test.sh`
Expected: PASS — 5 asserts, exit 0.

- [ ] **Step 5: Commit**

```bash
git add scripts/agent-spawn tests/agent_spawn_test.sh
git commit -m "feat(bootstrap): agent-spawn (master pops a peer via herdr, reuses agent-launch)"
```

---

### Task 6: Provisioning core — `scripts/bootstrap` (new/recall/auto + template)

**Files:**
- Create: `scripts/bootstrap`
- Test: `tests/bootstrap_test.sh`

**Interfaces:**
- Consumes: `roles_by_tier` (Task 1); `link-role-skills.sh` (Task 4); `agent-launch` path (Task 3).
- Produces: `bootstrap [new|recall] <project> [--index] [--cron] [--yes]`. `new` → ensure broker, link skills, write `<HERDR_PLUS_PROJECTS_DIR>/<project>.toml` (one `[[tabs]]` per boot role + a `busmon` tab). No verb → `recall` if the template exists, else `new`. Honors `HERDR_PLUS_PROJECTS_DIR`. `--index`/`--cron` land in Tasks 7–8 (stub the functions here, fill next).

- [ ] **Step 1: Write the failing test** — `tests/bootstrap_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"
make_stub "$stub" docker 0            # docker compose up -d -> ok
# link-role-skills.sh is called by `new`; point its dest at a temp dir + fake sources.
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do
  mkdir -p "$tmp/pocock/engineering/$s"; echo x > "$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel; do
  mkdir -p "$tmp/reposkills/$s"; echo x > "$tmp/reposkills/$s/SKILL.md"; done

proj="$tmp/projects"
run() { PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$proj" "$B" "$@"; }

# invalid name rejected
assert_exit 1 "rejects invalid project name" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' new 1bad"

# new writes a valid template
run new demo >/dev/null
tpl="$proj/demo.toml"
assert_exit 0 "template is valid TOML" -- python3 -c "import tomllib;tomllib.load(open('$tpl','rb'))"
body="$(cat "$tpl")"
assert_contains "$body" 'name = "demo"'                          "template name"
assert_contains "$body" "agent-launch master demo"              "master tab"
assert_contains "$body" "agent-launch sentinel demo"            "sentinel tab"
assert_contains "$body" "busmon --project demo"                 "busmon tab"
# boot roles present, architect (pop) absent
assert_contains "$body" "agent-launch foureyes demo"           "foureyes tab"
if [[ "$body" != *"agent-launch architect demo"* ]]; then _ok "architect not booted"; else _bad "architect leaked into template"; fi

# auto verb -> recall when template exists (no crash, broker still ensured)
assert_exit 0 "auto recalls existing project" -- bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$proj' '$B' demo"
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/bootstrap_test.sh`
Expected: FAIL — `bootstrap` does not exist.

- [ ] **Step 3: Write `scripts/bootstrap`** (index/cron functions are filled in Tasks 7–8)

```bash
#!/usr/bin/env bash
# Provision (do NOT launch) a project's Agent Bus team. Opening the workspace is the human's
# job via the herdr-plus fuzzy picker; this only writes the template + prepares the broker.
# Usage:
#   bootstrap [new|recall] <project> [--index] [--cron] [--yes]
#   bootstrap <project> ...           # no verb -> auto: recall if the template exists, else new
# Env seam: HERDR_PLUS_PROJECTS_DIR (default resolved from herdr).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/lib/roles.sh"

die()  { echo "bootstrap: $*" >&2; exit 1; }
warn() { echo "bootstrap: $*" >&2; }

verb=""; project=""; do_index=0; do_cron=0; assume_yes=0
for a in "$@"; do
  case "$a" in
    new|recall) verb="$a" ;;
    --index)    do_index=1 ;;
    --cron)     do_cron=1 ;;
    --yes|-y)   assume_yes=1 ;;
    -*)         die "unknown flag: $a" ;;
    *)          if [[ -z "$project" ]]; then project="$a"; else die "unexpected arg: $a"; fi ;;
  esac
done
[[ -n "$project" ]] || die "usage: bootstrap [new|recall] <project> [--index] [--cron] [--yes]"
[[ "$project" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || die "invalid project name: $project"

projects_dir() {
  if [[ -n "${HERDR_PLUS_PROJECTS_DIR:-}" ]]; then echo "$HERDR_PLUS_PROJECTS_DIR"; return; fi
  local base
  if base="$(herdr plugin config-dir cloudmanic.herdr-plus 2>/dev/null)" && [[ -n "$base" ]]; then
    echo "$base/projects"
  else
    echo "${XDG_CONFIG_HOME:-$HOME/.config}/herdr-plus/projects"
  fi
}
PROJECTS_DIR="$(projects_dir)"
template="$PROJECTS_DIR/$project.toml"

if [[ -z "$verb" ]]; then
  if [[ -f "$template" ]]; then verb="recall"; else verb="new"; fi
fi

ensure_broker() {
  ( cd "$REPO" && docker compose up -d ) || die "failed to start the broker (docker compose up -d)"
}

write_template() {
  mkdir -p "$PROJECTS_DIR"
  {
    echo "name = \"$project\""
    echo "description = \"Agent Bus team for $project\""
    echo "working_dir = \"$REPO\""
    echo
    while IFS= read -r role; do
      echo "[[tabs]]"
      echo "name = \"$role\""
      echo "command = \"$SCRIPT_DIR/agent-launch $role $project\""
      echo
    done < <(roles_by_tier boot)
    echo "[[tabs]]"
    echo "name = \"busmon\""
    echo "command = \"$REPO/busmon --project $project\""
  } > "$template"
  echo "wrote $template"
}

# Filled in Task 7.
index_preflight() { warn "--index not yet implemented"; }
# Filled in Task 8.
install_cron()    { warn "--cron not yet implemented"; }

case "$verb" in
  new)
    ensure_broker
    "$SCRIPT_DIR/link-role-skills.sh"
    write_template
    ;;
  recall)
    ensure_broker
    [[ -f "$template" ]] || warn "no template at $template (run 'bootstrap new $project')"
    echo "recall: broker up; open '$project' via the herdr-plus picker (fresh sessions)"
    ;;
esac
[[ "$do_index" == "1" ]] && index_preflight
[[ "$do_cron"  == "1" ]] && install_cron
echo "bootstrap: $verb $project done"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `chmod +x scripts/bootstrap && bash tests/bootstrap_test.sh`
Expected: PASS — 9 asserts, exit 0.

- [ ] **Step 5: Commit**

```bash
git add scripts/bootstrap tests/bootstrap_test.sh
git commit -m "feat(bootstrap): provisioning core (new/recall/auto + herdr-plus template)"
```

---

### Task 7: `bootstrap --index` preflight

**Files:**
- Modify: `scripts/bootstrap` (replace the `index_preflight` stub)
- Test: `tests/bootstrap_index_test.sh`

**Interfaces:**
- Consumes: the `bootstrap` arg parser + `--index` flag (Task 6).
- Produces: `index_preflight` — best-effort, **non-fatal**: `which rtk` (report), `codegraph init` in `$REPO` only if `.codegraph/` is absent, and a `$REPO/.agent-bus/index-requested` marker for the sentinel's code-index warm-up.

- [ ] **Step 1: Write the failing test** — `tests/bootstrap_index_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"; rm -rf "$REPO/.agent-bus" "$REPO/.codegraph.testflag"' EXIT
stub="$tmp/bin"
make_stub "$stub" docker 0
make_stub "$stub" codegraph 0        # codegraph init -> ok, logs to codegraph.calls
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do mkdir -p "$tmp/pocock/engineering/$s"; echo x >"$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel; do mkdir -p "$tmp/reposkills/$s"; echo x >"$tmp/reposkills/$s/SKILL.md"; done

# Ensure .codegraph absent so init should run.
rm -rf "$REPO/.agent-bus"
PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$tmp/projects" "$B" new demo --index >/dev/null
assert_contains "$(cat "$stub/codegraph.calls" 2>/dev/null)" "init" "codegraph init called when .codegraph absent"
if [[ -f "$REPO/.agent-bus/index-requested" ]]; then _ok "index marker written"; else _bad "no index marker"; fi

# Non-fatal when codegraph fails.
make_stub "$stub" codegraph 1
rm -rf "$REPO/.agent-bus"
assert_exit 0 "bootstrap --index stays non-fatal on codegraph failure" -- \
  bash -c "PATH='$stub:$PATH' HERDR_PLUS_PROJECTS_DIR='$tmp/projects' SKILLS_DEST='$tmp/skills' POCOCK_SKILLS_ROOT='$tmp/pocock' REPO_SKILLS='$tmp/reposkills' '$B' new demo --index"
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/bootstrap_index_test.sh`
Expected: FAIL — `--index not yet implemented`; no `codegraph init` call, no marker.

- [ ] **Step 3: Replace the `index_preflight` stub in `scripts/bootstrap`**

```bash
index_preflight() {
  if command -v rtk >/dev/null 2>&1; then echo "rtk: $(command -v rtk)"; else warn "rtk not on PATH (token proxy inactive; non-fatal)"; fi
  if [[ -d "$REPO/.codegraph" ]]; then
    echo "codegraph: index already present (.codegraph/)"
  elif command -v codegraph >/dev/null 2>&1; then
    if ( cd "$REPO" && codegraph init ); then echo "codegraph: initialized"; else warn "codegraph init failed (non-fatal; agents fall back to grep/Read)"; fi
  else
    warn "codegraph not on PATH (skipping index; non-fatal)"
  fi
  # Signal the sentinel to warm the code-index (MCP, agent-side) once on its next boot.
  mkdir -p "$REPO/.agent-bus" && : > "$REPO/.agent-bus/index-requested"
  echo "index: requested (sentinel runs index_repository on boot)"
}
```

Also add `.agent-bus/` to `.gitignore` (local marker dir, not source):

```bash
printf '\n# local bootstrap markers\n.agent-bus/\n' >> .gitignore
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `bash tests/bootstrap_index_test.sh`
Expected: PASS — 3 asserts, exit 0. (The `( ... codegraph init )` subshell + `if` keeps a failure non-fatal under `set -e`.)

- [ ] **Step 5: Commit**

```bash
git add scripts/bootstrap tests/bootstrap_index_test.sh .gitignore
git commit -m "feat(bootstrap): opt-in --index preflight (codegraph init + sentinel marker, non-fatal)"
```

---

### Task 8: Daily-review trigger + `bootstrap --cron`

**Files:**
- Create: `scripts/daily-review-trigger.sh`
- Modify: `scripts/bootstrap` (replace the `install_cron` stub)
- Test: `tests/trigger_test.sh`

**Interfaces:**
- Consumes: `bootstrap` `--cron` flag (Task 6); the sentinel role (Task 2).
- Produces: `daily-review-trigger.sh` — resolves the sentinel pane via `agentbus pane sentinel`, sends the review message; skips (exit 0) if the sentinel is down; `DAILY_REVIEW_DRYRUN=1` prints pane+message. `install_cron` — idempotent crontab line tagged per project.

- [ ] **Step 1: Write the failing test** — `tests/trigger_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"
T="$REPO/scripts/daily-review-trigger.sh"
B="$REPO/scripts/bootstrap"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
stub="$tmp/bin"

# (a) dry-run prints pane + message when the sentinel is up.
make_stub "$stub" agentbus 0 "w3:p2"        # `agentbus pane sentinel` -> w3:p2
out="$(PATH="$stub:$PATH" AGENT_BUS_PROJECT=demo HERDR_SESSION=s DAILY_REVIEW_DRYRUN=1 "$T")"
assert_contains "$out" "pane=w3:p2"                 "trigger resolved pane"
assert_contains "$out" "agent-bus-sentinel skill"   "trigger message references the skill"

# (b) sentinel down -> skip, exit 0, do not call herdr.
make_stub "$stub" agentbus 1                # pane lookup fails
make_stub "$stub" herdr 0
assert_exit 0 "skips cleanly when sentinel down" -- \
  bash -c "PATH='$stub:$PATH' AGENT_BUS_PROJECT=demo HERDR_SESSION=s '$T'"
if [[ ! -f "$stub/herdr.calls" ]]; then _ok "herdr never called when sentinel down"; else _bad "herdr called despite no pane"; fi

# (c) crontab install is idempotent.
make_stub "$stub" docker 0
cronfile="$tmp/cron.txt"; : > "$cronfile"
cat > "$stub/crontab" <<EOF
#!/usr/bin/env bash
if [[ "\${1:-}" == "-l" ]]; then cat "$cronfile"; else cat > "$cronfile"; fi
EOF
chmod +x "$stub/crontab"
export SKILLS_DEST="$tmp/skills" POCOCK_SKILLS_ROOT="$tmp/pocock" REPO_SKILLS="$tmp/reposkills"
for s in tdd implement code-review diagnosing-bugs codebase-design domain-modeling to-spec wayfinder to-tickets; do mkdir -p "$tmp/pocock/engineering/$s"; echo x >"$tmp/pocock/engineering/$s/SKILL.md"; done
for s in agent-bus agent-bus-master agent-bus-sentinel; do mkdir -p "$tmp/reposkills/$s"; echo x >"$tmp/reposkills/$s/SKILL.md"; done
run_cron() { PATH="$stub:$PATH" HERDR_PLUS_PROJECTS_DIR="$tmp/projects" "$B" new demo --cron >/dev/null; }
run_cron; run_cron
assert_eq "$(grep -c 'daily-review: demo' "$cronfile")" "1" "cron line installed exactly once"
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/trigger_test.sh`
Expected: FAIL — `daily-review-trigger.sh` missing; `--cron not yet implemented`.

- [ ] **Step 3: Write `scripts/daily-review-trigger.sh`**

```bash
#!/usr/bin/env bash
# Machine-cron poke for the sentinel's daily review. Runs OUTSIDE any Claude session
# (installed via `bootstrap --cron`), so it exports the minimal env it needs. Adapted from
# the sibling project's daily_journal_trigger.sh — but resolves the pane LIVE via the bus
# pane-bridge instead of a hard-coded pane id.
# Usage: daily-review-trigger.sh   (crontab line sets AGENT_BUS_PROJECT + HERDR_SESSION)
#   DAILY_REVIEW_DRYRUN=1 -> print the resolved pane + message instead of sending (test seam).
set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"   # cron's env is minimal; herdr/agentbus live here.

: "${AGENT_BUS_PROJECT:?set AGENT_BUS_PROJECT (the crontab line does this)}"
export AGENT_BUS_AGENT="${AGENT_BUS_AGENT:-hermes}"
[[ -n "${HERDR_SESSION:-}" ]] || echo "daily-review-trigger: warning: HERDR_SESSION unset" >&2

read -r -d '' MSG <<'EOF' || true
[cron review] Autonomous one-shot: write today's project-review entry, then resume your watch.
1) Follow your agent-bus-sentinel skill: read STATUS / journal / recent git log / MEMORY.md,
   post a one-line `agentbus report`, and (if the project keeps one) append + commit today's
   docs/PROJECT-JOURNAL.md entry (no push).
2) Read `agentbus usage`. If master's Ctx/session% is high, `agentbus cmd master` telling it to
   write a hand-off then /clear. Notify only — never clear master yourself.
3) Re-arm `agentbus subscribe sentinel` and idle.
EOF

# Resolve the sentinel's pane live (no hard-coded pane id). Skip cleanly if it isn't up.
if ! pane="$(agentbus pane sentinel 2>/dev/null)" || [[ -z "$pane" ]]; then
  echo "daily-review-trigger: sentinel not up (no pane); skipping today's review" >&2
  exit 0
fi

if [[ "${DAILY_REVIEW_DRYRUN:-0}" == "1" ]]; then
  echo "pane=$pane"
  echo "--- message ---"
  printf '%s\n' "$MSG"
  exit 0
fi

herdr pane send-text "$pane" "$MSG"
sleep 2
herdr pane send-keys "$pane" Enter
```

- [ ] **Step 4: Replace the `install_cron` stub in `scripts/bootstrap`**

```bash
install_cron() {
  local session="${HERDR_SESSION:-default}"
  local tag="# agent-bus daily-review: $project"
  local line="0 7 * * * AGENT_BUS_PROJECT=$project HERDR_SESSION=$session $SCRIPT_DIR/daily-review-trigger.sh >/dev/null 2>&1"
  local current; current="$(crontab -l 2>/dev/null || true)"
  if grep -qF "$tag" <<<"$current"; then
    echo "cron: already installed for $project"
    return
  fi
  printf '%s\n%s\n%s\n' "$current" "$tag" "$line" | grep -v '^[[:space:]]*$' | crontab -
  echo "cron: installed daily-review for $project (07:00 daily)"
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `chmod +x scripts/daily-review-trigger.sh && bash tests/trigger_test.sh`
Expected: PASS — 5 asserts, exit 0.

- [ ] **Step 6: Commit**

```bash
git add scripts/daily-review-trigger.sh scripts/bootstrap tests/trigger_test.sh
git commit -m "feat(bootstrap): daily-review cron trigger + idempotent --cron install"
```

---

### Task 9: Sentinel skill + master "Spawn a peer" section

**Files:**
- Create: `skills/agent-bus-sentinel/SKILL.md`
- Modify: `skills/agent-bus-master/SKILL.md`
- Test: `tests/skills_test.sh`

**Interfaces:**
- Consumes: `agent-spawn` (Task 5); the sentinel role + index marker (Tasks 2, 7).
- Produces: the sentinel's playbook (daily review + context nudge + one-time index warm-up) and the master's documented pop procedure.

- [ ] **Step 1: Write the failing test** — `tests/skills_test.sh`

```bash
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
source "$DIR/lib.sh"

s="$REPO/skills/agent-bus-sentinel/SKILL.md"
if [[ -s "$s" ]]; then _ok "sentinel skill exists"; else _bad "missing agent-bus-sentinel/SKILL.md"; fi
body="$(cat "$s" 2>/dev/null)"
assert_contains "$body" "name: agent-bus-sentinel"        "sentinel skill frontmatter name"
assert_contains "$body" "index_repository"                "sentinel documents index warm-up"
assert_contains "$body" ".agent-bus/index-requested"      "sentinel checks the index marker"
assert_contains "$body" "cmd master"                      "sentinel documents the notify-only nudge"

m="$(cat "$REPO/skills/agent-bus-master/SKILL.md")"
assert_contains "$m" "Spawn a peer"                       "master skill has Spawn a peer section"
assert_contains "$m" "agent-spawn"                        "master skill documents agent-spawn"
finish
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/skills_test.sh`
Expected: FAIL — sentinel skill missing; no "Spawn a peer" section.

- [ ] **Step 3: Write `skills/agent-bus-sentinel/SKILL.md`**

````markdown
---
name: agent-bus-sentinel
description: "Run from the SENTINEL agent (the cheap Haiku caretaker) on the Agent Bus. Three one-shot duties, each triggered by an external wake (machine cron or a directed cmd), never a polling loop: write the daily project-review entry; nudge the master to reset its own context when it saturates (notify-only — never clear master's pane); and, once at boot if requested, warm the code-index. Use when you are the sentinel and have been woken."
---

# Agent Bus — Sentinel Skill

You are **sentinel**, the cheap caretaker (`claude-haiku-4-5`). You act only when woken — by
the machine cron or a directed `cmd`. You are **not** a polling loop; after each duty you
re-arm `agentbus subscribe sentinel` and idle.

## Duty 1 — Daily review (cron-woken)
You start from a blank context; read before you write, assume nothing.
1. Read, in order: the project's `STATUS`/status file, `docs/PROJECT-JOURNAL.md` (if present —
   for the format and the previous entry, which you must NOT copy), `git log --oneline -25`,
   and `MEMORY.md`.
2. Post a one-line summary to the bus: `agentbus report note "daily review: <what changed>"`.
3. If the project keeps `docs/PROJECT-JOURNAL.md`, append **one** entry at the top (just under
   the header) dated `$(date +%F)`, describing what CHANGED since the last entry (new commits /
   verdicts / deadlines), then commit only that file (`git add docs/PROJECT-JOURNAL.md &&
   git commit -m "docs(journal): entry $(date +%F)"`) — keep the Co-Authored-By trailer, do
   **not** push. If nothing changed, say so in one line.

## Duty 2 — Master-context nudge (cron-woken, notify-only)
1. Read the team budget: `agentbus usage` (or `--json`). Find master's `Ctx` / session%.
2. If master's context is high (default threshold **Ctx ≥ 80%** or session ≥ 90%; override via
   `AGENT_BUS_CTX_THRESHOLD`), nudge it — do **not** touch its pane:
   ```bash
   agentbus cmd master "Ctx <NN>% — write a hand-off (step, committed, in-progress) then /clear"
   ```
3. That's it. Master owns its own reset (its agent-bus-master skill handles hand-off-before-
   clear). A missed nudge is a no-op; you never force-clear, so no work is ever lost.

## Duty 3 — Index warm-up (once, at boot, only if requested)
`bootstrap --index` drops a marker so you know indexing is wanted:
```bash
if [[ -f .agent-bus/index-requested && ! -f .agent-bus/index-done ]]; then
  # Use the codebase-memory MCP: if this repo is not indexed yet, index it once.
  #   index_status / list_projects -> if absent -> index_repository
  # (codegraph's own .codegraph/ index is built by bootstrap itself; this is code-index only.)
  : > .agent-bus/index-done   # mark done so you never re-index on later boots
fi
```
Keep it a one-shot. Keeping the index *fresh* over time is a later slice (S5), not your job.

## Boundaries
- **Never** drive another agent's pane (that's the master's job). Your only lever on master is
  a `cmd` it reads on its own subscribe wake.
- **Never** become a daemon. Each duty ends by re-arming `subscribe` and going idle.
````

- [ ] **Step 4: Add the "Spawn a peer" section to `skills/agent-bus-master/SKILL.md`**

Insert this section immediately after the `## Agent → pane bridge` section (before `## Resync`):

```markdown
## Spawn a peer
The team's boot roles (coder, foureyes, sentinel) come up with the workspace. When you need a
role that isn't up — design work, or surge capacity — **pop** it. Popping runs the *exact* boot
recipe (`agent-launch`), so a popped agent is identical to a booted one:
```bash
agent-spawn architect "$AGENT_BUS_PROJECT"   # design work -> architect (Fable, Opus on fallback)
agent-spawn coder "$AGENT_BUS_PROJECT"       # surge -> a second implementer
```
`agent-spawn <role> <project>` opens a new herdr tab running `agent-launch <role> <project>`;
it requires `HERDR_ENV=1` (you're inside herdr) and a role defined in `roles.toml`. The new
agent arms its own `subscribe` and publishes `status`, so it shows up in busmon and in
`agentbus agents` within a few seconds — dispatch to it once it reports `online`.
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `bash tests/skills_test.sh`
Expected: PASS — 7 asserts, exit 0.

- [ ] **Step 6: Run the whole suite**

Run: `bash tests/run.sh`
Expected: every `*_test.sh` prints `== N passed, 0 failed ==`; overall exit 0.

- [ ] **Step 7: Commit**

```bash
git add skills/agent-bus-sentinel/SKILL.md skills/agent-bus-master/SKILL.md tests/skills_test.sh
git commit -m "feat(bootstrap): agent-bus-sentinel skill + master Spawn-a-peer section"
```

---

## Self-Review

**1. Spec coverage** (each spec deliverable → task):
- `scripts/agent-launch` → Task 3. `scripts/agent-spawn` → Task 5. `scripts/bootstrap` (+`--cron`+`--index`) → Tasks 6–8. `scripts/link-role-skills.sh` → Task 4. `scripts/daily-review-trigger.sh` → Task 8. `roles.toml` → Task 1. `roles/*.md` → Task 2. `skills/agent-bus-master` "Spawn a peer" → Task 9. `skills/agent-bus-sentinel` → Task 9. Tests (dry-run, TOML validity, link idempotency, crontab idempotency, index preflight) → Tasks 1/3/4/6/7/8. Spec behaviors (session naming, notify-only reset, inherited MCP/rtk, index marker→sentinel) → covered in Tasks 3/8/7/9. **No gaps.**
- Spec-vs-plan naming deltas (deliberate, approved this session): `4eyes`→`foureyes` (ValidName), `steward`→`sentinel`, `agent-bus-steward`→`agent-bus-sentinel`, `--append-system-prompt-file`→`--append-system-prompt "$(cat …)"`, uniform `--permission-mode`. **Sync the spec doc to match after this plan is approved.**

**2. Placeholder scan:** No `TODO`/`TBD`/"handle edge cases"/"similar to Task N". The `index_preflight`/`install_cron` stubs in Task 6 are explicitly replaced with full code in Tasks 7/8 (staged implementation of one file, not a placeholder).

**3. Type/name consistency:** Resolver API (`role_exists`/`role_field`/`roles_by_tier`) is used with identical signatures in Tasks 3/4/6. Role names (`master`/`coder`/`foureyes`/`sentinel`/`architect`), env seams, and the `.agent-bus/index-requested` marker string match across producer (Task 7) and consumer (Task 9). Dry-run env vars (`AGENT_LAUNCH_DRYRUN`/`AGENT_SPAWN_DRYRUN`/`DAILY_REVIEW_DRYRUN`) are spelled identically in scripts and tests.

## Prerequisites (once, before running the tooling for real)
- `herdr plugin install cloudmanic/herdr-plus` (or `herdr plugin link ~/Tools/herdr-plugins/herdr-plus`).
- Matt Pocock skills present at `~/Tools/herdr-plugins/skills/` (already cloned).
- For `--index`: `codegraph` CLI on `PATH`; codegraph + codebase-memory MCP servers and the `rtk` hook configured at **user scope** in `~/.claude/` (so launched agents inherit them).
- Build the binaries once so the template's `busmon` tab and the agents' `agentbus` calls resolve: `go build -o busmon ./cmd/busmon && go build -o agentbus ./cmd/agentbus` (or `go install ./...`).
