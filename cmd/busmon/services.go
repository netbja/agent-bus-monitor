package main

import (
	"github.com/netbja/agent-bus-monitor/attention"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// serviceStale: a running service publishes its state every 5 s; past this
// age its process is gone (killed, crashed, machine off) without a word.
const serviceStale = 20 * time.Second

// serviceRunning are the states a live process refreshes.
var serviceRunning = map[string]bool{"starting": true, "ready": true, "waiting-holder": true, "holder-behind": true, "waiting-git": true, "idle": true, "busy": true}

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

// unknownWhy is why what the dispatcher computes (health, the quota
// readings, the merge queue) is unknown, from its state (serviceFact's
// words): "project stopped" when its services were stopped, "dispatch
// <state>" when it is down or gave up; "" when it runs or its state is
// unknown, and a "?" then keeps its old meaning (project-switch-ux item 2).
func unknownWhy(dispatch string) string { return attention.DispatchWhy(dispatch) }

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
	case "waiting-holder":
		glyph, role, text = GlyphBlocked, RoleAttention, "waiting for the holder"
	case "holder-behind":
		glyph, role, text = GlyphBlocked, RoleAttention, "no holder of its build"
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

// serviceLogBody is a service's log page (Enter or l on its F3 card), as
// first drawn: its header, the lines read back (services_log.go) and the
// crash file.
func serviceLogBody(r bus.ServiceRecord, kind, host string, width int) []Line {
	_, b := svcLogPage(serviceLogView(r, kind, host, width), width)
	return b
}

// serviceLogView reads a service's log for its page: the local log read back
// to svcLogVisible lines a person reads, plus the crash file; a remote one is
// named, never read.
func serviceLogView(r bus.ServiceRecord, kind, host string, width int) svcLogView {
	v := svcLogView{kind: kind}
	var b body
	var f fields
	f.add("log", S(RoleText, Preview(r.Log)))
	b.fields(&f, width)
	if r.Host != host || r.Log == "" {
		b.seg(RoleMuted, "the log is on "+Preview(r.Host)+": agentbus --project <p> service logs "+kind+" there").text("\n")
		v.head = b.Lines()
		return v
	}
	lines, err := readSvcLog(r.Log, svcLogVisible, svcLogMaxBytes)
	if err != nil {
		b.seg(RoleMuted, Preview(err.Error())).text("\n")
	}
	v.head, v.lines = b.Lines(), lines
	// A panic, or what was printed before the log opened.
	crashFile := strings.TrimSuffix(r.Log, ".log") + ".crash.log"
	if crash, err := service.TailLog(crashFile, 20); err == nil && len(crash) > 0 {
		var c body
		c.text("\n").seg(RoleError, "crash output:").text(" " + Preview(crashFile) + "\n")
		for _, l := range crash {
			c.seg(RoleError, clip(Preview(l), width)).text("\n")
		}
		v.crash = c.Lines()
	}
	return v
}
