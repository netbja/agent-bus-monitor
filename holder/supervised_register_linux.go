package holder

import "github.com/netbja/agent-bus-monitor/service"

// Supervised is service.Detect's manager where none runs — on Linux only,
// where a supervisor's start time is read from /proc: elsewhere nothing could
// tell a live supervisor from a reused pid, and Detect keeps its answer of
// before (no manager).
func init() {
	service.Unmanaged = func(paths service.Paths, getenv func(string) string) service.Manager {
		return Supervised{Paths: paths, Getenv: getenv}
	}
}
