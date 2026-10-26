package main

import (
	"fmt"
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

// servicesPanel is the Services overlay: per kind the state, its age, detail,
// last error, host and log, and the log's tail when it is on this machine.
func servicesPanel(inst *bus.ServicesInstall, recs map[string]bus.ServiceRecord, host string, now time.Time, width int) []Line {
	var b body
	rule := strings.Repeat(GlyphDash, 2)
	if inst == nil && len(recs) == 0 {
		b.seg(RoleMuted, rule+" no project services recorded "+rule).text("\n")
		b.seg(RoleMuted, "   This project runs dispatch and missions as herdr tabs, or not at all. With a\n"+
			"   service manager (systemd --user, launchd), agentbus up starts them as user\n"+
			"   services that outlive the workspace.").text("\n")
		return b.Lines()
	}
	if inst != nil {
		b.seg(RoleMuted, fmt.Sprintf("%s %s services on %s, set up %s ago %s", rule, Preview(inst.Manager), Preview(inst.Host),
			humanAge(now.Sub(time.UnixMilli(inst.At))), rule)).text("\n")
	}
	for _, k := range service.Kinds {
		r, ok := recs[k]
		if !ok {
			b.text("\n").seg(RoleAccent, pad(k, 9)).text(" ").seg(RoleMuted, "no state published (not started since agentbus up)").text("\n")
			continue
		}
		state := serviceFact(r, true, "", now)
		role := map[string]Role{"failed": RoleError, "gave-up": RoleError, "waiting-herdr": RoleAttention, "waiting-git": RoleAttention, "starting": RoleAttention, "down": RoleMuted, "stopped": RoleMuted}[state]
		if role == RoleText {
			role = RoleOK
		}
		b.text("\n").seg(RoleAccent, pad(k, 9)).text(" ").seg(role, Preview(state)).text("  ").
			seg(RoleMuted, fmt.Sprintf("last update %s ago, pid %d on %s", humanAge(now.Sub(time.UnixMilli(r.At))), r.PID, Preview(r.Host))).text("\n")
		if state == "down" {
			b.text("   ").seg(RoleMuted, "its process is gone; last said: "+Preview(r.State)).text("\n")
		}
		if r.Detail != "" {
			b.text("   " + clip(Preview(r.Detail), width-4) + "\n")
		}
		if r.Error != "" {
			b.text("   ").seg(RoleError, "last error:").text(" " + clip(Preview(r.Error), width-16) + "\n")
		}
		b.text("   ").seg(RoleMuted, "log:").text(" " + Preview(r.Log) + "\n")
		if r.Host != host || r.Log == "" {
			b.text("   ").seg(RoleMuted, "the log is on "+Preview(r.Host)+": agentbus --project <p> service logs "+k+" there").text("\n")
			continue
		}
		lines, err := service.TailLog(r.Log, 12)
		if err != nil {
			b.text("   ").seg(RoleMuted, Preview(err.Error())).text("\n")
		}
		for _, l := range lines {
			b.text("   ").seg(RoleMuted, clip(Preview(l), width-4)).text("\n")
		}
		// A panic, or what was printed before the log opened.
		crashFile := strings.TrimSuffix(r.Log, ".log") + ".crash.log"
		if crash, err := service.TailLog(crashFile, 6); err == nil && len(crash) > 0 {
			b.text("   ").seg(RoleError, "crash output:").text(" " + Preview(crashFile) + "\n")
			for _, l := range crash {
				b.text("   " + clip(Preview(l), width-4) + "\n")
			}
		}
	}
	b.text("\n").seg(RoleMuted, "agentbus down <p> stops them; agentbus --project <p> service status|logs|restart").text("\n")
	return b.Lines()
}
