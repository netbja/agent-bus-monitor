- **The automatic update, step 4e: the updater's unit (quiet-update-auto §4.1).** On a managed
  build, `agentbus up` of any project writes and starts the updater when its unit is missing:
  `agentbus-updater.service` (systemd, `KillMode=process`) or `dev.agentbus.updater` (launchd,
  `AbandonProcessGroup`, its output in `<state>/agentbus/updater.log`), running
  `<bindir>/agentbus service run updater --env <config>/agentbus/updater.env`. That env file is
  written from up's shell like a project's (PATH, the broker, the Go variables a build needs), and
  the loop puts it in its environment, so each pass inherits it. One per host: a running updater
  is never rewritten or restarted (an update never restarts it), a stopped one is started, and a
  host updated by `agentbus update` gets it at its next `up`. A developer build writes none. Not
  yet: `services.manager = none` (up says so), `setup install` and `setup uninstall`, its line in
  `service list` and `service status`, and the updater health check (step 4f).
