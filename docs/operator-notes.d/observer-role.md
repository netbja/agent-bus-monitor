- **The observer role (the person, 2026-11-25).** A session agentbus does not launch, such as claude-ao
  (the Orchestrator), declared in roles.toml as `[roles.<name>] kind = "observer"`, with no other key:
  no profile, terminal or tool rules; start, pop, relaunch and self-repair never see it (it is kept
  out of the launch order), and `Resolve` refuses it. **Not declared in this repository's roles.toml yet:** the previous
  release's `agentbus update` reads the new roles.toml with its own loader, which refuses the kind, so
  the repository declares `[roles.observer]` one release after update-old-binary ships (a test pins
  it). Until then the person adds `[roles.observer] kind = "observer"` to their own manifest and sets
  `ROLES_TOML` to it, with `AGENT_BUS_AGENT=observer`, in that session: its entries carry its own name, never the person's. The CLI then
  allows only reads (agents, board, request, reports, thread, history, verdicts, asks, swaps,
  health, quota, usage, budget, tokens, models, merge-queue, audit, version, limits, help) and
  refuses every other command before the bus, also to a process under a registered observer whatever
  identity it claims (its ancestry is read only for a write, only when the manifest declares an observer) (`cmd`, `report`, `status`, `request send/accept`,
  `deliver`, `verdict`, `pop`, `retire`, `config set`, `subscribe`...). With `broker.acl` off that
  refusal is a convention: a process can still write Redis directly. With it on, the identity
  connects as the new `observer` ACL user (as does any process under a registered observer) (`-@all +@read +@connection -@dangerous +time %R~*`: every
  key read, nothing written, no script; TIME for the reads that date what they read), under any ancestry; an install with ACL on gets that user's
  password at the next `./setup.sh acl off` then `on`; until then
  `rotate-redis-password` refuses, naming its missing password file. A manifest that cannot be read limits nobody (as before).
  kind is the mechanism the external-wake spec's `kind = "external"` joins later.
