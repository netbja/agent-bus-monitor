# Shared-memory validation and delivery status

This record describes the prepared worktree, not a deployed service.

## Composition

Base: a2c2fa1. Memory structure and checker by Codex, plus the exact boot changes
from Claude's e90ea6f and c5f02a3, applied as a working-tree diff. The shared
reference and the boot changes must be integrated together. PR38 disconnect
policy is separate and is not silently included in this memory diff.

## Independent local checks

- `python3 scripts/check-memory.py`: PASS.
- `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -p
  'test_memory_check.py'`: 13 regression cases PASS (including optional-reference suppression and date checks).
- Shell checks: skills_test.sh 11/11, roles_files_test.sh 18/18,
  model_ids_test.sh 2/2. These inspect local fixtures/documents; no Redis.
- `git diff --check`: PASS.

No Go runtime source is changed by this memory slice. A full Redis integration
run is not claimed here; the final release must validate its exact merged tree
on an isolated broker. Fresh-context adoption by each client, VDR availability,
and opening the vault in Obsidian remain explicit follow-up checks, not inferred
from the presence of files.

## Delivery limits

The sandbox refused git add (index.lock read-only), so the combined memory diff
is not committed in this worktree. GitHub API access via gh also failed.
The separate boot commits were published by Claude; they must not be merged
alone with missing memory targets. The v0.6.0 release notes are preparation, not
a created GitHub release or an authorization to merge pending branches.

The separate Redis-test opt-in guard remains outside this slice, uncommitted
and awaiting full isolated validation. No live-bus groups, cursors or pending
entries were changed. Existing Todo_Kimi.txt and historical brain notes were
not modified.

## Independent review by Claude

Claude independently ran the initial checker and its nine regressions. He found
that optional-file could hide broken links without visible fallback and that
future/inverted dates passed. Both findings were reproduced and corrected; the
expanded 13-test suite passes locally, and the actual vault reports zero optional
missing files. Claude completed final independent re-review: six probes covered
internal-link suppression, skill-to-memory suppression, invisible fallback,
visible fallback with OPTIONAL reporting, future dates and inverted dates. He
reports all expected outcomes, 13 passing tests, zero live-vault exceptions, and
restoration of his temporary probes. No objection remains on structure/checker.
