// Package dispatchsvc is the dispatch service's runtime: the health checks'
// readers, the stall checks, the chores (merge queue, notifications, backup
// schedule, self-repair, history) and the reboot judge, moved whole from
// cmd/agentbus (docs/superpowers/specs/2026-11-19-dispatchsvc-extraction-design.md).
//
// What judges lives here; what acts on the team through a CLI command stays in
// cmd/agentbus and arrives injected through Deps: the relay's runner
// (HostOps), the pop's end (PopEnd) and the base configuration (Base). The CLI
// calls in through five identifiers only: Deps, Config, ForegroundConfig,
// Runtime and MergeQueueLive. TestGuard holds the wall: no import of the CLI
// or the screen, no mission runner, and only the programs and module packages
// it lists.
package dispatchsvc
