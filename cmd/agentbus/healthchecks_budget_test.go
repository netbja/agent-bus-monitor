package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/service"
)

// The evaluator's budget threshold is the services' own.
func TestServiceBudgetStartsMatch(t *testing.T) {
	if health.ServiceBudgetStarts != service.BudgetStarts {
		t.Fatalf("health.ServiceBudgetStarts %d, service.BudgetStarts %d", health.ServiceBudgetStarts, service.BudgetStarts)
	}
	for _, k := range []string{service.Dispatch, service.Missions} {
		d, _ := health.ByID("services")
		if _, ok := d.Reasons["budget-spent-"+k]; !ok {
			t.Errorf("no reason for kind %s's spent budget", k)
		}
	}
}
