package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// holderRow is one holder of the project on this host, for the Services
// page (agent-screen-blocked §4): its manager unit and whether it runs, and
// what it answers. busmon only reads: it never stops a holder.
type holderRow struct {
	Raw     string    // the build
	Unit    bool      // the manager has a unit for it
	Active  bool      // that unit runs
	Answers bool      // its socket answered Describe
	Listed  bool      // its terminals were listed
	Live    int       // its live terminals (agents)
	Since   time.Time // its process's start (zero: unknown)
}

// holderRows merges the manager's units (active by key) with the holders
// that answered (states), sorted by build; since is each answering build's
// process start.
func holderRows(units []service.HolderUnit, active map[string]bool, states []holder.HolderState, since map[string]time.Time) []holderRow {
	byRaw := map[string]*holderRow{}
	for _, u := range units {
		byRaw[u.Raw] = &holderRow{Raw: u.Raw, Unit: true, Active: active[u.Key]}
	}
	for _, s := range states {
		r := byRaw[s.Build]
		if r == nil {
			r = &holderRow{Raw: s.Build}
			byRaw[s.Build] = r
		}
		r.Answers, r.Listed, r.Live, r.Since = true, s.Listed, s.Live, since[s.Build]
	}
	out := make([]holderRow, 0, len(byRaw))
	for _, r := range byRaw {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b holderRow) int { return strings.Compare(a.Raw, b.Raw) })
	return out
}

// realHolders reads project's holders on this host: the service manager's
// holder units and every holder that answers on its socket.
func realHolders(project string) func(ctx context.Context) ([]holderRow, error) {
	return func(ctx context.Context) ([]holderRow, error) {
		var units []service.HolderUnit
		active := map[string]bool{}
		m, _ := service.Detect(runtime.GOOS, service.CommandRunner(service.ManagerBin(runtime.GOOS, os.Getenv)), os.Getenv)
		if hm, ok := m.(service.HolderManager); ok {
			units, _ = hm.HolderUnits(project)
			for _, u := range units {
				active[u.Key], _ = hm.HolderActive(project, u.Key)
			}
		}
		h, err := holder.NewHost(project, holder.BuildID(), os.Getenv)
		if err != nil {
			return nil, err
		}
		states, err := h.Holders(ctx)
		if err != nil {
			return nil, err
		}
		since := map[string]time.Time{}
		for _, s := range states {
			if t, ok := procStartTime("/proc", s.PID); ok && s.PID > 0 {
				since[s.Build] = t
			}
		}
		return holderRows(units, active, states, since), nil
	}
}

// holdersSection is the Services page's holders: one line each (state,
// build, uptime, agents) and its flag; an older build that hosts no agent
// names the stop command (the person's Q5), the dispatcher's own build
// missing is said.
func holdersSection(f svcFacts, now time.Time, width int) []Line {
	rule := strings.Repeat(GlyphDash, 2)
	out := []Line{nil, {S(RoleAccent, rule+" holders "+rule)}}
	if !f.holdersRead {
		why := f.holdersErr
		if why == "" {
			why = "not read yet"
		}
		return append(out, Line{S(RoleMuted, "holders not read: "+Preview(why))})
	}
	current := f.recs["dispatch"].Build
	project := f.project
	if project == "" {
		project = "<p>"
	}
	currentAnswers := false
	for _, r := range f.holders {
		if r.Raw == current && r.Answers {
			currentAnswers = true
		}
		glyph, role, state := GlyphIdle, RoleMuted, "inactive"
		switch {
		case r.Answers:
			glyph, role, state = GlyphWorking, RoleOK, "running"
		case r.Active:
			glyph, role, state = GlyphBlocked, RoleAttention, "not answering"
		}
		l := Line{S(role, glyph+" "), S(RoleText, Preview(r.Raw)), S(role, "  "+state)}
		if r.Answers {
			if !r.Since.IsZero() {
				l = append(l, S(RoleMuted, " "+GlyphNone+" up "+humanAge(now.Sub(r.Since))))
			}
			switch {
			case !r.Listed:
				l = append(l, S(RoleMuted, " "+GlyphNone+" agents not listed"))
			case r.Live == 0:
				l = append(l, S(RoleMuted, " "+GlyphNone+" hosts no agent"))
			case r.Live == 1:
				l = append(l, S(RoleText, " "+GlyphNone+" hosts 1 agent"))
			default:
				l = append(l, S(RoleText, fmt.Sprintf(" %s hosts %d agents", GlyphNone, r.Live)))
			}
		}
		out = append(out, l)
		if r.Answers && r.Listed && r.Live == 0 && current != "" && r.Raw != current {
			out = append(out, Line{S(RoleAttention, "  old build, hosts no agent: stop it, then remove it")})
			out = append(out, wrapSpans([]mdSpan{{text: DetailText(fmt.Sprintf("agentbus --project %s service stop holder %s; agentbus --project %s service gc",
				project, r.Raw, project)), role: RoleMuted}}, width, "    ", "    ")...)
		}
	}
	if len(f.holders) == 0 {
		out = append(out, Line{S(RoleMuted, "no holder of this project on this host")})
	}
	if current != "" && !currentAnswers {
		out = append(out, wrapSpans([]mdSpan{{text: DetailText(fmt.Sprintf("no holder of the dispatcher's build (%s) answers: agentbus up %s starts it", current, project)),
			role: RoleAttention}}, width, "", "  ")...)
	}
	return out
}
