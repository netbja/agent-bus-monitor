package health

import (
	"fmt"
	"strings"
)

// MergeQueueInput is the cache as the check reads it (bus imports health, so
// the reader converts bus.MergeQueueRecord).
type MergeQueueInput struct {
	ItemsAt    int64
	Reason     string
	Items      []MergeQueueEntry
	Unapproved int
}

// MergeQueueEntry is one item: its PR, or its branch under the git fallback.
type MergeQueueEntry struct {
	Number        int
	Title, Branch string
}

// EvaluateMergeQueue judges the merge queue's cache (merge-queue spec §5):
// ok when a successful pass found nothing waiting, warn (for you) with the
// items waiting for the person's merge, unknown when it was never computed,
// cannot be read or the last pass failed. Never fail: it wakes nobody.
func EvaluateMergeQueue(rec MergeQueueInput, readErr string) Outcome {
	switch {
	case readErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(readErr)}
	case rec.ItemsAt == 0 && rec.Reason == "":
		return Outcome{Status: Unknown, Detail: "not computed yet (the dispatch service computes it every 5 minutes)"}
	case rec.Reason != "":
		return Outcome{Status: Unknown, Detail: truncateDetail("the last pass failed: " + rec.Reason)}
	case len(rec.Items) == 0:
		return Outcome{Status: OK, Detail: "nothing waits for a merge"}
	}
	var parts []string
	for _, it := range rec.Items {
		if it.Number > 0 {
			parts = append(parts, fmt.Sprintf("#%d %s", it.Number, it.Title))
		} else {
			parts = append(parts, it.Branch)
		}
	}
	d := fmt.Sprintf("waiting for your merge: %s", strings.Join(parts, "; "))
	if rec.Unapproved > 0 {
		d += fmt.Sprintf(" (%d open PRs not on an approved head)", rec.Unapproved)
	}
	return Outcome{Status: Warn, Detail: truncateDetail(d)}
}
