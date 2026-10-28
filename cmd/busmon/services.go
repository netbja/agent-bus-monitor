package main

import (
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// serviceStale: a running service publishes its state every 5 s; past this
// age its process is gone (killed, crashed, machine off) without a word.
const serviceStale = 20 * time.Second

// serviceRunning are the states a live process refreshes.
var serviceRunning = map[string]bool{"starting": true, "ready": true, "waiting-herdr": true, "waiting-git": true, "idle": true, "busy": true}

// serviceFact is what the header shows for one service kind. legacy is the
// tab-mode fact ("up"/"down" from the lease, "" when nothing to show): it
// stands when there is no record, and wins when another instance holds the
// lease. A running state not refreshed is "down"; failed, gave-up and stopped
// stay, they are final until the next agentbus up.
func serviceFact(rec bus.ServiceRecord, has bool, legacy string, now time.Time) string {
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

// serviceSeg is the header element of a service: glyph, word, state.
func serviceSeg(word, short, state string, compact bool) Line {
	glyph, role, text := GlyphIdle, RoleMuted, state
	switch state {
	case "up", "ready", "busy":
		glyph, role = GlyphWorking, RoleOK
	case "idle":
		glyph, role = GlyphWorking, RoleOK
	case "starting":
		glyph, role = GlyphIdle, RoleAttention
	case "waiting-herdr":
		glyph, role, text = GlyphBlocked, RoleAttention, "waiting herdr"
	case "waiting-git":
		glyph, role, text = GlyphBlocked, RoleAttention, "waiting git"
	case "failed":
		glyph, role = GlyphBlocked, RoleError
	case "gave-up":
		glyph, role, text = GlyphBlocked, RoleError, "gave up"
	case "down", "stopped":
	case "":
		return Line{{Text: "? " + word, Role: RoleMuted}}
	}
	if compact {
		return Line{{Text: glyph + " " + short, Role: role}}
	}
	return Line{{Text: glyph + " " + word + " " + text, Role: role}}
}

// serviceLogBody is a service's log page (Enter or l on its F3 card): the
// log's tail and its crash output when the log is on this machine.
func serviceLogBody(r bus.ServiceRecord, kind, host string, width int) []Line {
	var b body
	b.seg(RoleMuted, "log:").text(" " + Preview(r.Log) + "\n")
	if r.Host != host || r.Log == "" {
		b.seg(RoleMuted, "the log is on "+Preview(r.Host)+": agentbus --project <p> service logs "+kind+" there").text("\n")
		return b.Lines()
	}
	lines, err := service.TailLog(r.Log, 200)
	if err != nil {
		b.seg(RoleMuted, Preview(err.Error())).text("\n")
	}
	for _, l := range lines {
		b.seg(RoleMuted, clip(Preview(l), width)).text("\n")
	}
	// A panic, or what was printed before the log opened.
	crashFile := strings.TrimSuffix(r.Log, ".log") + ".crash.log"
	if crash, err := service.TailLog(crashFile, 20); err == nil && len(crash) > 0 {
		b.text("\n").seg(RoleError, "crash output:").text(" " + Preview(crashFile) + "\n")
		for _, l := range crash {
			b.text(clip(Preview(l), width) + "\n")
		}
	}
	return b.Lines()
}
