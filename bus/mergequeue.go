package bus

// The merge queue (spec docs/superpowers/specs/2026-11-05-merge-queue-design.md):
// what waits for the person's merge, derived from the verdict ledger and the
// forge (or git), never remembered. The derivation is package mergequeue; the
// dispatch service caches it in {p}:mergequeue (MergeQueueRecord).

// MergeApproval is one independent approve of an item's head.
type MergeApproval struct {
	Reviewer string `json:"reviewer"`
	At       int64  `json:"at"`      // Unix ms (the verdict's)
	Message  string `json:"message"` // what the reviewer wrote: the checks it ran
}

// MergeItem is one change waiting for the person's merge: an open PR (or a
// branch, under the git fallback) whose head is a subject's deciding approved
// head.
type MergeItem struct {
	Number     int             `json:"number,omitempty"` // the PR or MR; 0 under the git fallback
	Title      string          `json:"title,omitempty"`
	URL        string          `json:"url,omitempty"`
	Branch     string          `json:"branch"`
	Head       string          `json:"head"`
	Subjects   []string        `json:"subjects"`
	Approvals  []MergeApproval `json:"approvals"`
	ApprovedAt int64           `json:"approved_at"`    // Unix ms of the deciding approve
	Task       string          `json:"task,omitempty"` // the plan task whose review subject is one of Subjects
}
