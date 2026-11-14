- **`setup.sh` is the installer; tests of it must stub `docker` and `herdr`.** `install`,
  `update`, `uninstall`, `status`. Since installer-default it is a thin script that builds
  `agentbus` and runs `agentbus setup` (package `setup/`, every external command through
  `setup.Runner`); the bash installer stays one release as `scripts/setup-legacy.sh`, the fallback
  (`tests/installer_legacy_test.sh` runs the same suite on it), removed in the next, and run only
  when invoked by name (the thin `setup.sh` never execs it); `--force` only warns and is ignored, in
  both installers, and is dropped with the bash one. It stamps `buildinfo.Version` (from
  `git describe` when it is a `v*` tag, else `dev`) and `buildinfo.Repo` via ldflags; `agentbus up`
  finds `scripts/bootstrap` through `AGENTBUS_REPO`, then `buildinfo.Repo`, then the binary's
  directory. A copy of the repository uses the same compose project and container name as the live
  broker, so an unstubbed `uninstall --yes` in a test stops the production Redis.
  `tests/installer_test.sh` puts fakes first on `PATH`; keep it that way. Since v0.20 setup
  links *this checkout* as the herdr plugin `netbja.agentbus` (`herdr plugin link`, the plugin
  root is the checkout, no download; its one `[[build]]` exits 1 with how to install, so
  `herdr plugin install` from herdr's plugin list registers nothing, `TestPluginBuildRefusesInstall`,
  plugin-listing); herdr-plus is no longer installed, and a
  herdr-plus an older setup installed is reported by `status`, never removed. setup owns only
  what it wrote: it unlinks only the registration it recorded, and replaces only its own,
  unedited key-binding block.
