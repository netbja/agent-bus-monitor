- **`plan done` on a task with a check reads the merged head and checks it (onboarding step a3).** Master never
  types the head: `plan done <task>` reads it from the linked PR through the forge (GitHub's `headRefOid` with state
  MERGED, GitLab's `sha` with state merged; Bitbucket and none have no reader, so link such a delivery with
  `plan link --ref <branch>@<sha>`), from a ref's sha, or from a `merge: false` request's full `--commit`. It records
  done, and that head, only when the deciding approval quoted the check with exit 0 at exactly that head; otherwise
  it refuses and says what to ask for: "the merged head <sha7> was not approved with the check (approved: <sha7> or
  none); ask a reviewer to run the check at <sha7>", "check failed at …", or "cannot read PR <n>'s merged head: …"
  (fail closed). A task without a check, a person's task and a mission record done as before.
