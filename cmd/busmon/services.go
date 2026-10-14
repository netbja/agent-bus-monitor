package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/rivo/tview"
)

// serviceStale: a running service publishes its state every 5 s; past this
// age its process is gone (killed, crashed, machine off) without a word.
const serviceStale = 20 * time.Second

// serviceRunning are the states a live process refreshes.
var serviceRunning = map[string]bool{"starting": true, "ready": true, "waiting-herdr": true, "idle": true, "busy": true}

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

// servicesPanel is the Services overlay: per kind the state, its age, detail,
// last error, host and log, and the log's tail when it is on this machine.
func servicesPanel(inst *bus.ServicesInstall, recs map[string]bus.ServiceRecord, host string, now time.Time, width int) string {
	var sb strings.Builder
	rule := strings.Repeat(GlyphDash, 2)
	if inst == nil && len(recs) == 0 {
		sb.WriteString(tag("gray", rule+" no project services recorded "+rule) + "\n")
		sb.WriteString(tag("gray", "   This project runs dispatch and missions as herdr tabs, or not at all. With a\n"+
			"   service manager (systemd --user, launchd), agentbus up starts them as user\n"+
			"   services that outlive the workspace.") + "\n")
		return sb.String()
	}
	if inst != nil {
		fmt.Fprintf(&sb, "%s\n", tag("gray", fmt.Sprintf("%s %s services on %s, set up %s ago %s", rule, Preview(inst.Manager), Preview(inst.Host),
			humanAge(now.Sub(time.UnixMilli(inst.At))), rule)))
	}
	for _, k := range service.Kinds {
		r, ok := recs[k]
		if !ok {
			fmt.Fprintf(&sb, "\n%s %s\n", tag("aqua", pad(k, 9)), tag("gray", "no state published (not started since agentbus up)"))
			continue
		}
		state := serviceFact(r, true, "", now)
		role := map[string]string{"failed": "red", "gave-up": "red", "waiting-herdr": "yellow", "starting": "yellow", "down": "gray", "stopped": "gray"}[state]
		if role == "" {
			role = "green"
		}
		fmt.Fprintf(&sb, "\n%s %s  %s\n", tag("aqua", pad(k, 9)), tag(role, Preview(state)),
			tag("gray", fmt.Sprintf("last update %s ago, pid %d on %s", humanAge(now.Sub(time.UnixMilli(r.At))), r.PID, Preview(r.Host))))
		if state == "down" {
			fmt.Fprintf(&sb, "   %s\n", tag("gray", "its process is gone; last said: "+Preview(r.State)))
		}
		if r.Detail != "" {
			fmt.Fprintf(&sb, "   %s\n", tview.Escape(clip(Preview(r.Detail), width-4)))
		}
		if r.Error != "" {
			fmt.Fprintf(&sb, "   %s %s\n", tag("red", "last error:"), tview.Escape(clip(Preview(r.Error), width-16)))
		}
		fmt.Fprintf(&sb, "   %s %s\n", tag("gray", "log:"), tview.Escape(Preview(r.Log)))
		if r.Host != host || r.Log == "" {
			fmt.Fprintf(&sb, "   %s\n", tag("gray", "the log is on "+Preview(r.Host)+": agentbus --project <p> service logs "+k+" there"))
			continue
		}
		lines, err := service.TailLog(r.Log, 12)
		if err != nil {
			fmt.Fprintf(&sb, "   %s\n", tag("gray", Preview(err.Error())))
		}
		for _, l := range lines {
			fmt.Fprintf(&sb, "   %s\n", tag("gray", tview.Escape(clip(Preview(l), width-4))))
		}
		// A panic, or what was printed before the log opened.
		crashFile := strings.TrimSuffix(r.Log, ".log") + ".crash.log"
		if crash, err := service.TailLog(crashFile, 6); err == nil && len(crash) > 0 {
			fmt.Fprintf(&sb, "   %s %s\n", tag("red", "crash output:"), tview.Escape(Preview(crashFile)))
			for _, l := range crash {
				fmt.Fprintf(&sb, "   %s\n", tview.Escape(clip(Preview(l), width-4)))
			}
		}
	}
	sb.WriteString("\n" + tag("gray", "agentbus down <p> stops them; agentbus --project <p> service status|logs|restart") + "\n")
	return sb.String()
}
