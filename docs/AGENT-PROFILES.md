# Agent execution profiles

`agent-launch <role> <project>` supports Claude Code, Codex and **Kimi CLI**.
Roles and skills describe the work. A profile selects the client, provider, model
and optional native permissions. Boot and `agent-spawn` use the same launcher.
The existing unversioned `roles.toml` keeps its Claude behavior; no migration or
live session restart happens automatically.

## Select a project configuration

Copy [the mixed-team example](../examples/roles.mixed.toml), replace its model
placeholders with IDs/aliases configured on your accounts, and set `ROLES_TOML`
to its absolute path. Python 3.11+ is required.

```sh
ROLES_TOML=/path/to/project/roles.toml AGENT_LAUNCH_DRYRUN=1 scripts/agent-launch coder demo
ROLES_TOML=/path/to/project/roles.toml scripts/link-role-skills.sh
ROLES_TOML=/path/to/project/roles.toml scripts/agent-launch coder demo
```

The dry-run validates the manifest, role prompt and required skill files; it does
not contact a provider, launch a client, write cache files or publish on the bus.
It shows the requested configuration, not a measurement of the model actually
served. At execution, the launcher checks the client's required flags via `--help`.
Authentication and model availability remain the native client's responsibility.

For a homogeneous team, omit each role's `profile` override: every role uses
`defaults.profile`. For a mixed team, select a complete profile per role. Profiles
are not merged: a Codex role cannot inherit Claude permissions or a fallback.
In schema v2, role fields are `profile`, `tier` and `skills`; move legacy `model`,
`permission` and `fallback` into the appropriate profiles. Unknown fields fail.

Bootstrap persists the selected manifest path into generated workspace tabs.
Existing workspace templates require regeneration with `bootstrap new` to change
that selection; recall does not rewrite them. Spawn carries the same manifest into
its new tab and uses the caller's working directory as the project directory.
Do not remove/move a referenced manifest while its workspace template uses it.

## Effort is optional and unset by default

**Leave `reasoning` absent to keep the client's/provider's normal setting.** No
role, including security, imposes an effort level by default. A user can change it
in their native client or opt into a per-profile launch override:

```toml
[profiles.review]
client = "codex"
provider = "openai"
model = "your-compatible-model"
# Optional only; declare the levels verified for this model/provider:
reasoning_levels = ["low", "medium", "high", "xhigh"]
reasoning = "xhigh"
```

`reasoning_levels` is an operator declaration of model support, not an automatic
provider probe. The adapter also checks its own supported values. `standard` is
not translated silently to `medium` or to the absence of a setting. Omit reasoning
to use defaults. There is no cross-provider fallback.

## Native client differences

| Client | Model/provider selection | Role instructions | Explicit effort override |
| --- | --- | --- | --- |
| `claude-code` | `--model`; provider routing remains native environment/auth configuration | Appended system prompt | `--effort`, only supported declared levels |
| `codex` | `--model` and `model_provider` configuration | Additive initial role prompt, preserving existing developer instructions | `model_reasoning_effort`, only supported declared levels |
| `kimi-cli` | `--model` selects an already configured native model alias and its provider | Generated `--agent-file` Markdown wrapping the base prompt | No verified override in the tested CLI; leave absent |

For Codex, `provider` is the native provider ID already configured in Codex. For
Claude and Kimi, it labels the selected native configuration; changing this label
alone does not change an endpoint or account. **Kimi CLI is a client; using a Kimi
model through a different client is a separate configuration.**

Optional native permissions live in `profiles.<name>.options`:

- Claude: `permission` (`default` omits the flag), optional `fallback` model.
- Codex: `sandbox` (default `read-only`), `approval` (default `on-request`).
- Kimi: `permission` (`default`, `yolo`, `auto`, `plan`); default omits the flag.

These names are not portable equivalents. No generic permission bypass is added.
Review-only instructions are not an operating-system sandbox: native permissions
still determine what a client can execute. Configure shell/network permissions
needed for bus reports independently of permission to modify the audited code.

The Claude adapter preserves the historical billing guard: it removes
`ANTHROPIC_API_KEY` unless `AGENT_LAUNCH_KEEP_API_KEY=1`. Native base URL/token
variables remain available. Other clients do not use this guard. Optional profile
`env_refs` maps target environment variable names to existing source variable names
without storing their values in the manifest:

```toml
[profiles.claude.env_refs]
ANTHROPIC_BASE_URL = "TEAM_ANTHROPIC_ENDPOINT"
ANTHROPIC_AUTH_TOKEN = "TEAM_ANTHROPIC_TOKEN"
```

Never put credentials in models, options or prompts. Dry-run does not print
resolved environment values. Process-control and bus identity overrides through
`env_refs` are rejected. Existing user authentication settings are not rewritten.

## Skills and cached launch files

The installer resolves all requested skills and checks collisions before writing
links. Claude uses `~/.claude/skills`; Codex and Kimi use `~/.agents/skills`.
`SKILLS_DEST` overrides the destination for isolated installs. A real file or
folder at a target is preserved and causes an error. Sources remain the repository
and the collection selected by `POCOCK_SKILLS_ROOT`.

Schema v2 also includes required skill source paths in the role prompt, so the
agent can read them directly if native invocation is unavailable. Relative links
must be resolved against each source document's directory. The installer does
not guarantee an agent followed instructions; review the first actual session.

Kimi agent definitions and custom-adapter launch specs are private cached files
under `${XDG_CACHE_HOME:-~/.cache}/agentbus/launch`, overridable with
`AGENT_LAUNCH_CACHE`. Files are content-addressed and retained for session reuse;
remove obsolete files only when those sessions no longer need them. They contain
role instructions/configuration, so keep credentials out of these inputs.

The launcher exports `AGENT_BUS_CLIENT`, `AGENT_BUS_PROVIDER`, `AGENT_BUS_MODEL`,
`AGENT_BUS_PROFILE` and `AGENT_BUS_REASONING` as requested configuration. It clears
an inherited `CLAUDE_CODE_SESSION_ID` so a child cannot report its parent's usage.
The existing collectors cover Claude sources only. Other usage is unavailable,
not zero; retained usage rows are historical observations with their own timestamp.
No new provider quota collector or TUI telemetry is implied by this change.

## Additional clients/providers

Register an executable adapter in a trusted local manifest:

```toml
[adapters.my-client]
executable = "./adapters/my-client"

[profiles.other]
client = "my-client"
provider = "my-provider"
model = "my-model"
```

The executable path is relative to the manifest. The launcher calls it directly,
without a shell, with `--spec-file <path>`. The JSON contains `client`, `provider`,
`model`, `role`, `project`, `profile`, `prompt`, `prompt_path`, `skills`, `options`,
`manifest` and optional `reasoning`/`reasoning_levels`/`env_refs`. The inherited
bus identity and resolved environment are already set. The adapter must validate
native options/capabilities, preserve default effort when absent, load the prompt
and skills, and propagate the client's exit status. It must not log secrets.
The dry-run does not execute extension code. This is trusted local executable
configuration, not a mechanism for executing adapter commands received on the bus.

## Security reviewer

`agent-spawn security "$AGENT_BUS_PROJECT"` starts the on-demand reviewer.
The [role](../roles/security.md) and [skill](../skills/agent-bus-security/SKILL.md)
cover repository vulnerabilities, evidence, severity/confidence and proposed
corrections. Foureyes retains functional correctness and regression review.
Findings with the same root cause reference the existing task; master assigns
corrections. Security does not apply patches or introduce an automatic merge veto.

## Verification and sources

Local help/version inspected on 2026-09-27: Claude Code 2.1.283, Codex 0.157.1,
Kimi CLI 0.41.0. This is a verified CLI surface, not a claimed minimum-version
matrix or a successful authenticated model call. Older Kimi distributions with
YAML agent definitions are not covered by the Markdown adapter.

Official references:

- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-reference)
  for provider selection and effort; [skills](https://learn.chatgpt.com/docs/build-skills)
  for skill discovery.
- [Kimi agent definitions](https://moonshotai.github.io/kimi-code/en/customization/agents.html)
  for Markdown agent files and base-prompt inclusion;
  [Kimi skills](https://moonshotai.github.io/kimi-code/en/customization/skills.html).

Automated tests use fake clients and isolated paths. They check actual argument
boundaries, role instructions, environment, error propagation, effort defaults,
profile isolation, extension specs and installer preflight. No authenticated
inference or live-bus interaction is part of these tests.
