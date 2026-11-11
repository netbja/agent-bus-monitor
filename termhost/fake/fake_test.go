package fake

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/contract"
)

// The fake passes the contract every adapter passes.
func TestFakeContract(t *testing.T) {
	contract.Run(t, func(*testing.T) contract.Harness { return New() })
}

var _ termhost.Host = (*Host)(nil)
