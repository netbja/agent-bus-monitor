package main

import (
	"context"
	"testing"
)

// G7 (model-detection §3.5): the daily model refresh runs without services:
// it reads no broker (a nil bus would panic) and always refreshes.
func TestModelsRefreshNeedsNoServices(t *testing.T) {
	old := dailyModelsRefresh
	defer func() { dailyModelsRefresh = old }()
	calls := 0
	dailyModelsRefresh = func(context.Context) error { calls++; return nil }
	if err := modelsRefresh()(context.Background()); err != nil || calls != 1 {
		t.Fatalf("calls %d err %v", calls, err)
	}
}
