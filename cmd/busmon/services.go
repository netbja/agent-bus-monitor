package main

import (
	"github.com/netbja/agent-bus-monitor/attention"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// serviceFact is what the header shows for one service kind (attention's
// ServiceFact: the merge queue's unknown row reads the same words).
func serviceFact(rec bus.ServiceRecord, has bool, legacy string, now time.Time) string {
	return attention.ServiceFact(rec, has, legacy, now)
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
