- **A task with a check is done only with the check's proof (onboarding step a2).** Its approving verdict, recorded
  with `--head <sha>`, must quote the run in one line, `check: <the check> => exit 0 at <that sha>` (whitespace
  collapsed). The head must also be the task's result: the merge queue's head for a merge task, the request's full
  `--commit` for a `merge: false` task (a short sha counts as none). Otherwise the task stays in review as
  `approved, check not run`, `check failed`, or `approved, head changed` ("re-review: approved at …, now …"), and a
  recorded done counts only at the approved head (`plan done` records it from step a3). busmon keeps a verdict's
  evidence lines and shows the deciding one on the task page, or "check not run yet". Tasks without a check derive
  as before; a person's task with a check is unchanged until step a4.
