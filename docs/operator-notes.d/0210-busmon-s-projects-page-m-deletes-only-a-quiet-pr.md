- **busmon's Projects page (M) deletes only a quiet project** (spec
  `docs/superpowers/specs/2026-11-08-projects-admin-page-design.md` §5). Quiet means: no dispatch or
  conductor lease and no live service record younger than 30 s (`bus.ServiceFreshFor`, by Redis TIME,
  any host; `stopped`, `failed`, `gave-up` and the `installed` marker never count), no active service
  here, no verified agent process alive here, no herdr workspace open here; checked at `d` and again
  just before the delete, and the lease and record part again inside the delete script itself
  (`bus.DeleteProjectIfQuiet`, `deleteIfQuiet`). Agents on another host are seen only through their
  dispatcher, conductor or service records: the broker has no rule that tells a suspended agent from a
  remote live one (the rule `agentbus restore` uses). On a host without `/proc` (macOS) agents are not
  checked at all, and the panel says so (master's choice, never refused); an agent check that errs where
  `/proc` exists refuses. A writer outside the script cannot be fenced: the scan after the DEL reports
  the keys that came back, and busmon --delete exits 1. The outcomes keep their words (`deleteErr`):
  refused and unknown-before-the-script say "nothing deleted" (with the units removed here, if any);
  a lost connection during the script says its outcome is unknown; a failed scan after it says how
  many keys it deleted, never "nothing deleted".
