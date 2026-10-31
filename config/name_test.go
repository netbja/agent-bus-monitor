package config_test

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

func TestValidNameIsTheBusRule(t *testing.T) {
	for _, s := range []string{"charlie", "c", "a-b_9", "", "Charlie", "9a", "a b", "abcdefghijklmnopqrstuvwxyz0123456", "abcdefghijklmnopqrstuvwxyz012345"} {
		if got, want := config.ValidNameForTest(s), bus.ValidName(s); got != want {
			t.Errorf("%q: config %v, bus %v", s, got, want)
		}
	}
}
