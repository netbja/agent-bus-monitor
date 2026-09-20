package testredis

import (
	"os"
	"testing"
)

func TestAmbientRedisDoesNotOptIn(t *testing.T) {
	t.Setenv("AGENTBUS_TEST_REDIS_URL", "")
	t.Setenv("REDIS_URL", "redis://production.invalid:6380")
	var probe *testing.T
	reached := false
	t.Run("requires_explicit_endpoint", func(t *testing.T) {
		probe = t
		Configure(t)
		reached = true
	})
	if reached || !probe.Skipped() {
		t.Fatal("ambient REDIS_URL authorized integration tests")
	}
	if os.Getenv("REDIS_URL") != "redis://production.invalid:6380" {
		t.Fatal("skip modified ambient configuration")
	}
}

func TestDedicatedEndpointOverridesAmbient(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://production.invalid:6380")
	t.Setenv("AGENTBUS_TEST_REDIS_URL", "unix:///tmp/isolated-test.sock")
	Configure(t)
	if os.Getenv("REDIS_URL") != "unix:///tmp/isolated-test.sock" {
		t.Fatal("dedicated endpoint did not override ambient configuration")
	}
}
