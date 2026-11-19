package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// The team on the holder (herdr exit, spec docs/superpowers/specs/2026-11-10-herdr-exit-design.md
// §4.2, §4.7): up starts each agent tab's role as a terminal of the
// project's holder, resuming its recorded session through Start's
// resume_session only (the holder rebuilds the command from the role's
// launch template), and leaves a role that already runs alone. The
// template's busmon tab is the person's own window now: up says how to
// open it.

// teamHost is what up starts the team through.
type teamHost interface {
	List(ctx context.Context, project string) ([]termhost.Term, error)
	Start(ctx context.Context, s termhost.Spec) (termhost.Term, error)
}

// openTeam starts every agent tab of tabs whose role has no live terminal:
// on the recorded session when its record holds (resumeChoice), else fresh
// on the tab's own profile, the person told why a record was not used. A
// role already running is left alone. It returns each role's terminal
// (running or started); every start is tried, the failures returned joined.
// dry says what it would start and starts nothing.
//
// A role with a swap intent (a swap or a self-repair under way) is skipped:
// that operation starts it. An intent that cannot be read is said, and the
// role is started (not checked is not skipped, as relaunch did).
func openTeam(ctx context.Context, h teamHost, m *rolecfg.Manifest, project string, tabs []upTab,
	resumes map[string]bus.ResumeRecord, intent func(role string) (bus.SwapIntent, bool, error),
	notice func(string), dry bool, out io.Writer) (map[string]string, error) {
	terms, err := h.List(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("the holder: %w", err)
	}
	ids := map[string]string{}
	live := map[string]termhost.TermID{}
	for _, t := range terms {
		if t.Alive {
			live[t.Name] = t.ID
		}
	}
	var failed error
	for _, tab := range tabs {
		_, role, tabProfile := relaunch.TabLaunch(tab.Command)
		if role == "" {
			if relaunch.IsBusmonTab(tab, project) {
				fmt.Fprintf(out, "busmon: run it in your own terminal: busmon --project %s\n", project)
			}
			continue
		}
		if !slices.Contains(m.RoleNames(), role) { // as agent-launch refused it in its pane
			fmt.Fprintf(out, "%s: not in roles.toml, skipped\n", role)
			continue
		}
		if id, ok := live[role]; ok {
			fmt.Fprintf(out, "%s: running (%s)\n", role, id)
			ids[role] = string(id)
			continue
		}
		if intent != nil {
			in, held, err := intent(role)
			switch {
			case err != nil:
				fmt.Fprintf(out, "%s: intent unreadable (%v); starting it\n", role, err)
			case held && in.Kind == bus.SwapKindSelfRepair:
				fmt.Fprintf(out, "%s: skipped (self-repair in progress)\n", role)
				continue
			case held:
				fmt.Fprintf(out, "%s: skipped (swap in progress)\n", role)
				continue
			}
		}
		var rec *bus.ResumeRecord
		if r, ok := resumes[role]; ok {
			rec = &r
		}
		profile, session, invalid, note := relaunch.ResumeChoice(m, role, rec)
		if note != "" {
			notice(role + ": " + note)
		}
		if invalid != "" {
			notice(fmt.Sprintf("%s: its resume record is not used (%s); it starts fresh", role, invalid))
			profile = ""
		}
		if session == "" {
			profile = tabProfile
		}
		if dry {
			if session != "" {
				fmt.Fprintf(out, "dry run: %s: would start, resuming session %s\n", role, session)
			} else {
				fmt.Fprintf(out, "dry run: %s: would start fresh\n", role)
			}
			continue
		}
		t, err := h.Start(ctx, termhost.Spec{Project: project, Name: role, Profile: profile, ResumeSession: session})
		if err != nil {
			fmt.Fprintf(out, "%s: not started: %v\n", role, err)
			failed = errors.Join(failed, fmt.Errorf("%s: %w", role, err))
			continue
		}
		ids[role] = string(t.ID)
		if session != "" {
			fmt.Fprintf(out, "%s: started, resuming session %s (%s)\n", role, session, t.ID)
		} else {
			fmt.Fprintf(out, "%s: started (%s)\n", role, t.ID)
		}
	}
	return ids, failed
}
