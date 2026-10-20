---
id: 2026-10-19-herdr-plugin
project: agent-bus-monitor
scope: project
kind: decision
status: active
created: 2026-10-19
last_verified: 2026-10-20
author: claude
confidence: verified
---

## Finding/decision

Since v0.20, agent-bus is itself a herdr plugin, `netbja.agentbus`, and herdr-plus is no longer
installed or needed. Each part of the decision with its reason:

- **Go, not Rust.** The plugin is the two binaries this repository already builds
  (`./agentbus plugin …`, `./busmon --pick`), declared in `herdr-plugin.toml` with no `[[build]]`.
  v0.20 had no `[[events]]`; v0.21 adds exactly two, `workspace.created` and `workspace.focused`,
  both running `./agentbus plugin workspace-event` (the relaunch hook; see the v0.21 spec).
  A second toolchain would add nothing: every fact the picker needs
  (templates, `bus.Projects`, `agentbus up`) is already Go.
- **Link, not install.** `setup.sh` runs `herdr plugin link` on this checkout, so the plugin root
  is the checkout, nothing is downloaded and `./setup.sh update` updates the plugin. setup owns
  only what it wrote: it unlinks only the registration it recorded, replaces only its own
  unedited key-binding block, and never uninstalls a herdr-plus an older setup installed
  (`status` reports it).
- **Our own templates directory.** Project templates live in `~/.config/agentbus/projects`
  (`AGENTBUS_PROJECTS_DIR`, else `$XDG_CONFIG_HOME/agentbus/projects`), resolved by
  `agentbus roles projects-dir`. They no longer depend on another plugin's config directory.
- **No migration code.** Old templates are moved by hand, once (the exact `mv` is in the
  CHANGELOG and the README). A fallback to herdr-plus's directory would keep a dead dependency
  alive for one move.
- **The picker never provisions.** `busmon --pick` lists templates with the broker's state
  (`present`, `absent`, `?`) and opens only a project the broker confirms, through
  `agentbus up <p>`. An `absent` project needs `agentbus up <p> <path>` by hand: choosing a
  directory and starting services is a decision for a person, not for a list. The picker and
  `agentbus plugin` write nothing to Redis and sit above the project-required exit.

## When it applies

Changing the plugin manifest, the picker, `agentbus plugin`, the herdr parts of `setup.sh`, or
where templates live. Also when someone proposes reinstalling herdr-plus, adding a migration, or
letting the picker create a project.

## Evidence

- Spec: `docs/superpowers/specs/2026-10-19-herdr-plugin-design.md` and its review
  `docs/superpowers/specs/2026-10-19-herdr-plugin-review.md`. Both are local and gitignored: they
  are not in the repository, so another checkout will not have them.
- Spike of 2026-10-20 (herdr 0.9.3), run by hand on the host: plugin actions and panes receive
  `HERDR_SESSION`, `HERDR_SOCKET_PATH`, `HERDR_BIN_PATH` and `HERDR_PLUGIN_*`; `--env` reaches the
  pane; the working directory is the plugin root. This is why entry points are `./agentbus` and
  `./busmon` and panes are opened without `--cwd`.
- PR #90 (merged): the groundwork, session from `HERDR_SOCKET_PATH`, `HERDR_BIN_PATH`, templates
  directory. The plugin itself is the PR of branch `feat/v020-herdr-plugin`.
- Committed: the [manifest](../../../herdr-plugin.toml), the [picker model](../../../projlist/projlist.go),
  [setup's tests with fake herdr](../../../tests/installer_test.sh), and the earlier
  [project services decision](2026-10-14-project-services.md) this builds on.

## Limits / what would invalidate it

The spike is a single herdr version (0.9.3); the manifest asks for `min_herdr_version = "0.9.0"`,
so a change in how herdr passes the environment or the cwd to plugin panes breaks the entry
points. If herdr drops `plugin link`, the install step must change. The spec and review are not
versioned: re-derive from the code if they are lost.
