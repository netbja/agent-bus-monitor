- **`agentbus update` reads the release's roles through the release's own binary.** The snapshot and the
  skills check ask the agentbus built in the update's temp dir (`agentbus roles skill-plan --json`, read
  leniently), so a key a release adds to `roles.toml` no longer stops the update from an older binary
  (v0.33 to v0.34 failed: `unknown fields in Codex options: reviewer`). A new binary without that command
  (an older release) leaves the running one to parse the tree, as before. This holds from the release that
  carries it on: an update from v0.34 or older still parses with the old loader, so a new `roles.toml` key
  waits one release after it. The release gate's `TestReleaseUpdateFromPrevious` builds the previous
  release and checks exactly that read.
