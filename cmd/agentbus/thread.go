package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// threadReport renders a correlation thread: a header plus one chronological
// line per :cmd entry (oldest→newest, as bus.Thread returns them). The entry
// whose id == threadID (the root) is marked "(root)"; an empty message renders
// nothing (not `""`). Pure — no Redis.
func threadReport(threadID string, evs []bus.Event, now time.Time) string {
	var sb strings.Builder
	if len(evs) == 0 {
		fmt.Fprintf(&sb, "thread %s  (no entries)\n", threadID)
		return sb.String()
	}
	fmt.Fprintf(&sb, "thread %s  (%d entries)\n", threadID, len(evs))
	for _, e := range evs {
		fmt.Fprintf(&sb, "  %-9s %-9s %s→%s",
			humanAge(now.Sub(time.UnixMilli(idMS(e.ID)))), e.Type, e.From, e.Target)
		if e.Message != "" {
			fmt.Fprintf(&sb, "  %q", e.Message)
		}
		if e.ID == threadID {
			sb.WriteString("  (root)")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// idMS parses the millisecond component of a Redis stream id ("<ms>-<seq>").
// The bus package's splitID is unexported, so the CLI parses it locally.
func idMS(id string) int64 {
	s := id
	if i := strings.IndexByte(id, '-'); i >= 0 {
		s = id[:i]
	}
	ms, _ := strconv.ParseInt(s, 10, 64)
	return ms
}
