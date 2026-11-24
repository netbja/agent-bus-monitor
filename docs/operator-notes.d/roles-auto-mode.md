- **Every Claude Code role starts in auto mode but security and infra** (roles-auto-mode, the person
  2026-11-24): coder, coder2, foureyes, architect (and `claude-fable`), sentinel, deploy and docs move
  from `bypassPermissions` or `acceptEdits` to `auto`; security and infra stay `manual`, master was
  already `auto`. Ask rules still prompt and deny rules still block; Claude Code's classifier decides
  the rest, and after 3 blocks in a row (20 in a session) it asks in the pane, which prompt-notify
  rings: a worker can now stop on a prompt where bypass never stopped. Workspace trust is still not
  pre-accepted (Claude Code has no flag or setting for it; first-run-trust option (b) stands), and
  Codex ignores a `-c projects."<dir>".trust_level` override (tried on codex 0.169). reviewer-codex
  runs on `gpt-6-luna`, read-only, with `reviewer = "auto_review"` (Codex's `approvals_reviewer`: its
  automatic review answers escalations, the person no longer does; announced as elevated at launch),
  and every Codex agent's status line shows the tokens used (`tui.status_line`).
