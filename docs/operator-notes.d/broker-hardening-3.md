- **The broker's runtime is a setting, `broker.runtime` (docker or podman; broker-hardening step 3).**
  - **What follows it:** every caller of the broker's container names its binary through
    `internal/brokerrt`, with `<runtime> compose` as the compose command:
    - the installer: preflight, the owner and volume checks, status, uninstall and the rotation;
    - the `redis-password` and disk health checks;
    - busmon's Redis page and Services card;
    - `scripts/bootstrap`'s `ensure_broker`, which reads it through the new `agentbus config get
      broker.runtime`, and stops rather than guess when it cannot.
  - **Under podman:**
    - `redis-password` filters the broker's container by name, because podman has no `publish=`
      filter;
    - the disk check measures podman's storage root, only when `podman info` says the service is
      not remote (a podman machine is not measured).
  - **Without systemd:** nothing new is needed, since `agentbus up` starts the broker. Under
    rootless podman, a reboot needs the person's own `agentbus up`.
  - **The guard:** `TestNoRuntimeLiteral` fails on a new `"docker"` literal in the Go code, or a
    script line calling docker (`busmon-demo.sh` aside).
