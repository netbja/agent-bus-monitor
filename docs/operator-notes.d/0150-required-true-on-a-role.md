- **`required = true` on a role** (self-repair step 4, `Manifest.RoleRequired`): a boot role whose
  closed tab the self-repair watcher will recreate (step 7; an optional role's is only reported).
  `Load` refuses it on a pop and refuses any value but a boolean; `roles doctor` shows it (`REQ`).
  A new role key reaches the shipped `roles.toml` only after a release that parses it is
  installed: sessions read the main checkout's `roles.toml` with the installed `agentbus`, whose
  older parser refuses the key ("unknown fields in role …") and stops every launch, relaunch and
  swap. `required` itself waited for that release; since then `roles.toml` and
  `examples/roles.mixed.toml` mark every boot role, which `TestShippedManifestsMarkTheBootRolesRequired`
  checks.
