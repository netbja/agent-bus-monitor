package lifecycle

import "github.com/netbja/agent-bus-monitor/dispatch"

// HostBootID is this host's boot id, "" when it cannot be read (no /proc):
// the identity then carries no host, and readers on other hosts prove
// nothing from it.
func HostBootID() string {
	id, _ := dispatch.BootID()
	return id
}
