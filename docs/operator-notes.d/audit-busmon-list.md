- **The audit trail, step 3: busmon's Audit list (audit spec §4).** `a` on the History page (`H`)
  opens the Audit list (`audit_page.go`, `ovAudit`): the project's last 200 entries
  (`bus.AuditLast`, `XREVRANGE … COUNT 200`), newest first, read once in a command when it opens;
  time, action, actor (`(person)` when a person's entry caused the act), outcome (refused and failed
  in colour), target and, from 100 columns, the detail. `tab` shows one action, then the next one
  present, then all; `enter` (or a second click) opens an entry in place, its `ref` first, then
  target, detail, who and id. Read-only: the page writes nothing. Older entries:
  `agentbus audit --since <d>`. A failed read, or no broker, is said on the page.
