// Package testredis requires an explicit, dedicated broker for integration tests.
package testredis

import (
	"os"
	"testing"
)

// Configure overrides ambient production connection settings before any dial.
// Setting AGENTBUS_TEST_REDIS_URL asserts that this endpoint is isolated and
// disposable. Neither REDIS_URL nor the application's default broker opts in.
func Configure(t testing.TB) {
	t.Helper()
	endpoint := os.Getenv("AGENTBUS_TEST_REDIS_URL")
	if endpoint == "" {
		t.Skip("Redis integration test: set AGENTBUS_TEST_REDIS_URL to an isolated disposable broker")
	}
	t.Setenv("REDIS_URL", endpoint)
}
