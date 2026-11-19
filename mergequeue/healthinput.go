package mergequeue

import (
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// HealthInput is the record as the health check reads it.
func HealthInput(rec bus.MergeQueueRecord) health.MergeQueueInput {
	in := health.MergeQueueInput{ItemsAt: rec.ItemsAt, Reason: rec.Reason, Unapproved: rec.Unapproved, CI: rec.CI}
	for _, it := range rec.Items {
		in.Items = append(in.Items, health.MergeQueueEntry{Number: it.Number, Title: it.Title, Branch: it.Branch})
	}
	return in
}
