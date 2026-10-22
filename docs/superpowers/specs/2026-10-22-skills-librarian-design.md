# A skills librarian: inventory, search and reviewed updates — design

Status: draft for review (coder, 2026-10-22), revision 2 after foureyes' review of `61bd627`. Task `skills-librarian-spec`, branch
`docs/skills-librarian-spec` from `origin/main` `61031f7`. Spec only; no code in this change.

## The question

Bernard asked how skills are managed when an agent is popped. Master should look at the
available skills when it pops an agent and when it drafts a role. The team needs an inventory
(name, description, source, version/SHA, date, the roles that use each skill), a search, and
updates.

A skill is a prompt that an agent follows with its tools. An untrusted update is therefore code
execution by proxy. The tools Grok suggested are judged below on what could be verified, not on
their descriptions.

## Current state (read from the code and from this host, 2026-10-22)

**Where skills come from.** `rolecfg.skillRoots` (`rolecfg/launch.go`) looks in this order:

1. the repository's `skills/`: `agent-bus`, `agent-bus-docs`, `agent-bus-master`,
   `agent-bus-security`, `agent-bus-sentinel`;
2. `POCOCK_SKILLS_ROOT/engineering` when set;
3. otherwise `$XDG_DATA_HOME/agentbus/skills/mattpocock/skills/engineering`, cloned by
   `setup.sh` from `https://github.com/mattpocock/skills.git` at `SKILLS_REF` (default
   `v1.3.1`), then `~/Tools/herdr-plugins/skills/skills/engineering`.

The pin is a **tag**, and a tag can be moved upstream. setup fetches it with `--depth 1` and
records no commit, so a moved tag would be installed silently.

**How roles get them.**
- `roles.toml` lists each role's skills by name, and `roles install` (`rolecfg/skills.go`, behind
  `scripts/link-role-skills.sh`) links them as symlinks into `~/.claude/skills` (Claude Code) or
  `~/.agents/skills` (other clients). It checks every source before the first write and never
  overwrites a non-symlink.
- At launch, a missing skill only warns (`presentSkills`).
- `roles draft` picks skills from the same roots by shared words (`DraftSkills`, spec
  `2026-10-21-specialist-roles-design.md` addendum E).
- `roles skill-users` names the roles that use a skill.

**What updates exist today.**
- `./setup.sh update` re-fetches the pinned tag.
- It prints `skills changed: …` for repo skills changed by the pull, and the command to tell the
  agents. A running agent keeps the skill copy it loaded until it invokes it again (master's skill,
  "Skill updates").

**What is on this host besides.** `~/.claude/skills` holds 90 entries:

- 16 are our symlinks: 5 into the repo, 9 into the Pocock collection, and 2 into
  `~/.agents/skills`.
- The other 74 are directories of unknown provenance (`gsd-*` and others), installed by hand or by
  other tools. `gh skill list` reports no `sourceURL` for them.
- Claude Code plugins (`~/.claude/plugins/installed_plugins.json`) carry their own version and
  commit SHA: `superpowers 7.0.1 @2ce8aa15d0`, `claude-md-management 1.0.0 @e30768372b`, …

## The tools proposed by Grok, verified

| Tool | Exists? | Provenance, what it runs | Verdict |
|---|---|---|---|
| `gh skill install/update --all` | **Yes**, in gh 2.102.0 (installed), marked *preview* ("subject to change without notice"); GitHub changelog 2026-04-16 | GitHub's own CLI. `install` copies a repository's `skills/*/SKILL.md` folders into each agent's directory and **writes tracking metadata into the SKILL.md frontmatter**. `update` compares tree SHAs and re-downloads; **`--all` updates without prompting**. `--pin <tag\|sha>` exists; `preview` renders a skill at a ref without installing it | Useful read-only: `gh skill preview <repo> <skill>@<sha>` and `gh skill list --json`. Not as our installer: it edits files, `update --all` adopts without review, the preview status gives no stable interface, and an update into one of our symlinked skill folders would write through the link into this checkout |
| `agent-skills-updater` | **Not found** under that name (not on npm). The nearest is `skill-updater` (oimiragieo/agent-studio), a *skill* that rewrites other skills from web research | An agent rewriting prompts from fetched content | **Rejected**: self-modifying prompts from the web are the risk this spec guards against |
| `skillmd` / `skillmds` registry | **Yes, on npm** (`npm view`, 2026-10-22):<br>- `skillmds` 1.3.3, from skillmd.com: an MCP server (`skillmds`) plus a `skillmd` CLI to lint, scan, search, install and publish skills. github.com/skillmds/skillmd, a single maintainer (bloodyhill), modified 2026-09-19.<br>- A separate `skillmd` 0.1.3, by another single maintainer (parkerbrown), with an empty description: apparently a placeholder.<br>SKILL.md itself is the format (agentskills.io specification, which `gh skill` follows) | A third-party registry plus an installer, run through npm; an MCP server would put the registry's content into agents' context | **Rejected**: an unvetted registry is the source of the untrusted updates this spec guards against; a single-maintainer package adds a supply chain |
| `agent-skills-cli` | **Yes, on npm**: 1.2.0, github.com/Karanjot786/agent-skills-cli, a single maintainer, modified 2026-10-11. It installs binaries `skills` and `agent-skills`, the first clashing with vercel-labs' npm `skills` CLI. Near names: `agentskills-cli` (.NET, NuGet), `@tech-leads-club/agent-skills` | A third-party package manager; `npx` runs the package's current code at each call | **Rejected**: a second package manager adds a supply chain without adding review, and its `skills` binary shadows another tool's |
| `skills-update` GitHub Action | **Not found**. Only community workflows calling scripts | — | **Rejected** |
| `mattpocock/skills` | **Yes**: MIT, about 300k stars, latest release `v1.3.1` (2026-10-04), already our pinned external collection | Markdown skills; some ship scripts | **Kept**, pinned by commit (below) |

## Requirements

1. **Inventory.** For every skill an agent of this host can load:
   - name and description (from the `SKILL.md` frontmatter);
   - **source** and **trust tier** (below);
   - version: tag and commit SHA for a Git source, version and SHA for a plugin;
   - a **content hash** for every skill;
   - date: commit date, else modification time;
   - the roles that use it (`Manifest.RolesUsing`) and the client directories it is linked into.
2. **Search** by words over name and description, ranked like `DraftSkills`, showing the tier.
3. **Updates are proposals, never adoptions.**
   - Every external source is pinned by **commit SHA**.
   - A newer version is shown as a diff of the skills the roles use.
   - It is adopted only through a reviewed change of the pin (a PR, 4-eyes, and the security
     reviewer when a skill gains scripts or tool permissions).
   - Nothing third-party is adopted automatically.
4. **Pop-time lookup.**
   - Master checks that the role it pops covers the task and searches for a better-fitting
     *trusted* skill.
   - It never installs a new third-party skill at pop time.

**Trust tiers** (the inventory's `tier` column):

| Tier | What | May a role use it? |
|---|---|---|
| `repo` | this repository's `skills/`, reviewed here | yes |
| `pinned` | an external collection at a commit recorded in the lock file (below) | yes |
| `plugin` | a Claude Code plugin's skill, at the plugin's recorded SHA | yes for Claude Code roles, by its plugin name; read-only for us |
| `local` | anything else found in a client directory (unknown provenance) | not listed in `roles.toml`, not suggested by master, unless the person adopts it into `repo` or `pinned` through a reviewed change |

**The tiers are advisory, not isolation.** Claude Code loads every skill in `~/.claude/skills`
into every session; this very session lists `gsd-*` and `cate-theme`. A tier governs what
`roles.toml` lists, what `check` verifies and what master suggests, **not what an agent can
invoke**. Real isolation would need each role to see only its own skills:

- a per-role skills directory that the client is pointed at, if the client supports one; or
- a separate client configuration directory per role (Claude Code's `CLAUDE_CONFIG_DIR`, which
  also separates settings, sessions and credentials, so it needs its own login and its own
  transcript paths for the quota and usage readers).

That is open question 4, not part of this design.

## Options

| | Option | Verdict |
|---|---|---|
| A | A small `agentbus skills` in Go: `list`, `search`, `check`, `outdated` and `diff`, over the launcher's own roots and the client directories, with a lock file for external sources | **Recommended**: read-only by default, no new dependency, tested like `roles` |
| B | Wrap `gh skill` | Not now: it is preview, it edits `SKILL.md`, it does not know our roots or roles, and `update --all` adopts. Its read-only `preview` stays a manual review aid |
| C | A librarian *role* (an agent that tracks and proposes updates) | Not now: an LLM reading third-party prompts in order to change our prompts is the injection path this design closes. It can come later on top of A's diffs, proposing only and never applying |

## Recommended design (A)

### `skills.lock.json` (in the repository)

```json
{
  "sources": {
    "mattpocock": {
      "repo": "https://github.com/mattpocock/skills.git",
      "ref": "v1.3.1",
      "commit": "<full sha>",
      "root": "engineering",
      "skills": { "tdd": "<git tree sha of engineering/tdd>", "implement": "…" }
    }
  }
}
```

- **`setup.sh` installs the `commit`, not the tag.** It fetches the commit, checks it out, and
  fails loudly when the tag in `ref` no longer points at it: a moved tag is a finding, never an
  update. Fetching a bare commit relies on the server accepting a SHA in `want` (GitHub does, for
  reachable commits). If a server refuses, setup fetches the tag and checks that it resolves to
  the locked commit, failing the same way. `SKILLS_REF` stays an override for a person testing another ref; it prints that the lock
  is bypassed.
- **Per-skill tree SHAs pin the content the roles use.** Only the skills named in `roles.toml` are
  listed. A local edit, or a different checkout, is visible to `skills check`.

### Commands (no project, no broker, like `roles` and `mem`; above the project-required exit)

| Command | Does | Network |
|---|---|---|
| `agentbus skills list [--json] [--tier t]` | The inventory: name, tier, source, version/commit, date, content hash, roles, linked into | none |
| `agentbus skills search <words…> [--trusted]` | Ranked by shared words with name and description (the `DraftSkills` scorer), tier shown; `--trusted` keeps `repo` and `pinned` only (a plugin's SHA changes outside our review) | none |
| `agentbus skills check` | Every role skill resolves, matches its lock tree SHA, and is linked where its role's client looks; exit 1 on drift | none |
| `agentbus skills outdated` | Newer tags of each locked source (`git ls-remote --tags`; no checkout, no code run) | read-only |
| `agentbus skills diff <source> --to <ref>` | Fetches the ref into a temporary bare repository, prints the diff of the locked skills plus any new skill, and **flags** new or changed executable files, scripts, hooks, `allowed-tools`/frontmatter permission changes and network-fetch instructions. It writes nothing outside the temporary directory | read-only fetch |
| `agentbus skills lock <source> --to <ref>` | Rewrites the lock entry (commit and tree SHAs) **in the working tree only**, for the PR that adopts it | read-only fetch |

- **Parsing is defensive.** Frontmatter is parsed with a size limit, and only `name` and
  `description` are read, kept to one line and sanitised. A `SKILL.md` is read, never executed. No
  command follows a link out of a skill directory to read other files.
- **Plugins are read, never touched.** Their skills are listed from `installed_plugins.json`
  (name, version, SHA, path); the plugin manager remains the plugin manager.

### Update policy

1. **Propose.** `skills outdated` runs in sentinel's daily review. A newer tag earns master one
   line, no action.
2. **Show the change.** master (or the person) runs `skills diff`. Flagged content (scripts,
   permissions, fetches) requires the security reviewer as well as foureyes.
3. **Adopt by PR only.** `skills lock … --to <ref>` on a branch, review, merge by the person. Then
   `./setup.sh update` installs the new commit and prints `skills changed`. Master then sends the
   `--no-wake` re-invoke directives to the roles `skill-users` names (as today).
4. **Never:** an automatic update; an unpinned branch; `gh skill update --all`; a `local`-tier skill
   in a role without the person's yes and a reviewed change.

### Master's pop-time lookup (agent-bus-master skill, "Specialists" and "Draft a role")

- Before `agent-spawn <role>`, run `agentbus skills search <task words> --trusted`. It offers repo
  and pinned skills only; a plugin skill is suggested only while `skills check` shows no drift of
  its SHA.
- When a trusted skill outside the role fits the task clearly better, say so in the request to the
  role ("use the `<skill>` skill"). Only for Claude Code roles, and only when it is already
  installed for that client. Do not edit the manifest at pop time.
- A lasting addition to a role is a `roles.toml` change by PR.
- No trusted skill fits: draft a role (`roles draft`, which already uses only the launcher's
  roots), or ask the person. Never install a new third-party skill to cover a pop.

### Security

- A skill is instructions with the agent's tool access. Adoption therefore carries a code
  review's weight: a pinned commit, a reviewed diff, the person's merge.
- No command here runs anything from a skill. The only network use is `ls-remote` and a fetch into
  a temporary bare repository.
- **A moved tag fails** the install. Content drift fails `skills check`. The `local` tier is kept out
  of roles and suggestions; this is advisory, since Claude Code still loads it (see "The tiers are
  advisory").
- Our symlinked skills are never handed to a tool that writes into skill folders: `gh skill` is
  documented in the master skill as read-only (`preview`, `list`).

## Tests

- **Inventory** over fixture roots: repo, pinned, plugin JSON and local directories give the right
  tiers. A hostile frontmatter (control characters, a 1 MB description, a missing `---`) is
  sanitised and bounded. A symlink out of a skill directory is not followed.
- **Content hash** is stable across runs and changes with any file. Its tree SHA equals git's for a
  committed skill.
- **`check`:** a drifted file, a missing link, and a skill whose lock SHA differs each exit 1 with
  the skill named.
- **`search`:** ranking equals `DraftSkills` on the same inputs; `--trusted` keeps only `repo` and `pinned`.
- **`outdated` and `diff`** against a local bare fixture repository (no internet in tests):
  - new tags are listed;
  - the diff flags a new script, an exec bit, a changed `allowed-tools` and a new `curl …| sh` line;
  - nothing is written outside the temporary directory.
- **`lock`** rewrites only its entry.
- **`setup.sh`** (installer tests, fake git): the lock commit is checked out; a tag pointing
  elsewhere fails the install with a message; `SKILLS_REF` prints the bypass.
- **Skills test:** the master skill's pop section names `agentbus skills search … --trusted` and
  forbids installing at pop time.

## Open questions for review

1. **Where the lock lives:** in this repository, as proposed (one lock for the tooling's own role
   skills). A project with its own `roles.toml` and its own sources could later carry its own
   lock next to it. Proposal: one lock now.
2. **Plugin skills in roles:** allowed for Claude Code roles at the plugin's recorded SHA. The
   plugin manager updates plugins on its own schedule, though. Proposal: `skills check` reports a
   plugin SHA change for a skill a role uses, as a finding for review.
3. **The 74 `local` skills on this host:** list them as `local`, and leave them where they are.
   The person decides one by one whether any deserves adoption.
4. **Real per-role isolation of skills** (a per-role skills directory, or `CLAUDE_CONFIG_DIR` per
   role with its login and transcript-path consequences): needed if a `local` skill must truly be
   out of an agent's reach. Proposal: not now; the advisory tiers plus review cover what a role is
   *told* to use.

Agreed in review: 1 (one lock), 2 (a plugin SHA change is a finding) and 3 (list and leave).

Changes from revision 1 (foureyes, review of `61bd627`):

- **Tool rows corrected:** `agent-skills-cli` (npm 1.2.0) and `skillmds`/`skillmd` (npm) do exist.
  They are still rejected, on their stated grounds.
- **Tiers stated as advisory**, with what real isolation would need.
- **Pop-time suggestions** are limited to repo and pinned skills, plus plugins without SHA drift.
- **The bare-commit fetch** dependency is stated, with a fallback.
