package attention

import (
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// serviceStale: a running service publishes its state every 5 s; past this
// age its process is gone (killed, crashed, machine off) without a word.
const serviceStale = 20 * time.Second

// serviceRunning are the states a live process refreshes.
var serviceRunning = map[string]bool{"starting": true, "ready": true, "waiting-holder": true, "holder-behind": true, "waiting-git": true, "idle": true, "busy": true}

// ServiceFact is what busmon's header shows for one service kind (moved
// from busmon, status-summary step 2: the summary's merge row reads it too). legacy is the
// tab-mode fact ("up"/"down" from the lease, "" when nothing to show): it
// stands when there is no record, and wins when another instance holds the
// lease. A running state not refreshed is "down"; failed, gave-up and stopped
// stay, they are final until the next agentbus up.
func ServiceFact(rec bus.ServiceRecord, has bool, legacy string, now time.Time) string {
	switch {
	case !has:
		return legacy
	case serviceRunning[rec.State]:
		if now.Sub(time.UnixMilli(rec.At)) <= serviceStale {
			return rec.State
		}
		if legacy == "up" {
			return "up"
		}
		return "down"
	case rec.State == "standby":
		// The process lives and polls the lease (every 15 s, inside the stale
		// window). While the other instance holds it, dispatch is up there.
		if legacy == "up" {
			return "up"
		}
		if now.Sub(time.UnixMilli(rec.At)) <= serviceStale {
			return "standby"
		}
		return "down"
	case legacy == "up":
		return "up" // a final state while another instance runs
	case rec.State == "failed", rec.State == "gave-up", rec.State == "stopped":
		return rec.State
	}
	return "down"
}
