package mergequeue

import (
	"reflect"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// HealthInput is the record as the health check reads it: its time, reason,
// unapproved count and each item's number, title and branch (dispatchsvc P6,
// moved from cmd/agentbus mergequeuecmd.go's mergeQueueInput).
func TestHealthInput(t *testing.T) {
	rec := bus.MergeQueueRecord{ItemsAt: 42, Reason: "gh down", Unapproved: 3, Source: "gh", Base: "dev",
		Items: []bus.MergeItem{{Number: 7, Title: "a", Branch: "fix/a"}, {Number: 9, Title: "b", Branch: "fix/b"}}}
	want := health.MergeQueueInput{ItemsAt: 42, Reason: "gh down", Unapproved: 3,
		Items: []health.MergeQueueEntry{{Number: 7, Title: "a", Branch: "fix/a"}, {Number: 9, Title: "b", Branch: "fix/b"}}}
	if got := HealthInput(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("HealthInput = %+v, want %+v", got, want)
	}
	if got := HealthInput(bus.MergeQueueRecord{}); got.Items != nil {
		t.Errorf("an empty record: %+v", got)
	}
}
