- **"1 approval" in the merge queue** (single-approval step 3a, spec 2026-11-21-single-approval-design
  §4.5): busmon's merge row ends `(1 approval)`, and its page has an `Approvals:` line naming the
  current approver, when the item's head has one current approver: a reviewer's latest verdict on the
  subject, an approve of that head. `bus.CurrentApprovers` (moved from `deliver`) counts it for both,
  and the merge queue's cache gains `approvers` per item (additive; `approvals` still lists every
  approve naming the head, stale and repeated ones included, so its length is no count). A cache
  written by an older dispatch shows nothing until its next pass.
