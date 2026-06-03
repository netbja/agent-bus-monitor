package main

import (
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/bus"
)

// watchOutcome is why one subscribe tick ended. It maps to the exit-code +
// rearm-sentinel contract every runtime branches on.
type watchOutcome int

const (
	outcomeCmd       watchOutcome = iota // delivered an addressed cmd
	outcomeHeartbeat                     // idle window elapsed; re-arm
	outcomeTransient                     // recoverable error; re-arm
	outcomeFatal                         // misconfiguration; do NOT re-arm
)

// rearmSentinel returns the final machine-readable stdout line and the process
// exit code for a subscribe tick. The contract every runtime follows: re-arm
// iff the line carries rearm=1 (every outcome except fatal). ref/from populate
// the cmd line; msg the error/fatal line.
func rearmSentinel(o watchOutcome, ref, from, msg string) (line string, code int) {
	switch o {
	case outcomeCmd:
		return fmt.Sprintf("__AGENTBUS__ event=cmd rearm=1 ref=%s from=%s", ref, from), 0
	case outcomeHeartbeat:
		return "__AGENTBUS__ event=heartbeat rearm=1", 64
	case outcomeTransient:
		return "__AGENTBUS__ event=error rearm=1 msg=" + msg, 75
	default:
		return "__AGENTBUS__ event=fatal rearm=0 msg=" + msg, 1
	}
}

// printCmd writes one delivered cmd entry in the human-readable form agents
// already parse. Shared by the one-shot and --loop handlers.
func printCmd(out io.Writer, e bus.Event) {
	ref := ""
	if e.Ref != "" {
		ref = " ref=" + e.Ref
	}
	fmt.Fprintf(out, "[%s %s->%s%s] %s\n", e.Type, e.From, e.Target, ref, e.Message)
}
