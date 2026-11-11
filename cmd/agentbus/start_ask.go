package main

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/netbja/agent-bus-monitor/relaunch"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// asker is the assistant's one screen (agentbus-start spec, "The
// assistant"): plain lines on the person's terminal. Enter starts, a
// digit changes that line, q or the end of input quits having written
// nothing.
type asker struct {
	in     *bufio.Reader
	out    io.Writer
	m      *rolecfg.Manifest
	env    func(string) string
	look   func(exe string) (string, error) // the agent CLI lookup up uses
	live   map[string]relaunch.Life         // an existing project's agents; nil unknown
	busmon relaunch.Verdict                 // what up would do to its busmon tab; "" unknown
	short  bool                             // an existing project: the short screen until c or a digit

	chosenTeam bool // the person changed the team: start.team follows
}

// errQuit: the person quit; nothing was written.
var errQuit = errors.New("quit: nothing written")

func (a *asker) line(prompt string) (string, error) {
	fmt.Fprint(a.out, prompt)
	s, err := a.in.ReadString('\n')
	if err != nil && s == "" {
		return "", errQuit // the end of input, Ctrl+D: nothing written
	}
	return strings.TrimSpace(s), nil
}

// run shows the screen until the person starts or quits.
func (a *asker) run(p *startPlan) error {
	for {
		if a.short && p.unsettled() == "" {
			a.shortScreen(*p)
		} else {
			a.screen(*p)
		}
		in, err := a.line("> ")
		if err != nil {
			return err
		}
		if in != "" && in != "q" && in != "Q" {
			a.short = false // c or a change: the full screen from now on
		}
		switch in {
		case "c", "C":
			continue
		case "":
			if why := p.unsettled(); why != "" {
				fmt.Fprintf(a.out, "  %s first\n", why)
				continue
			}
			if !a.anyClient(p.Team) {
				fmt.Fprintln(a.out, "  no agent CLI of this team is installed: install one (above), then start again")
				continue
			}
			return nil
		case "q", "Q":
			return errQuit
		case "1":
			if err := a.askProject(p); err != nil {
				return err
			}
		case "2":
			fmt.Fprintln(a.out, "  the folder is where start was run: run agentbus start <path> for another")
		case "3":
			if err := a.askName(p); err != nil {
				return err
			}
		case "4":
			if err := a.askTeam(p); err != nil {
				return err
			}
		default:
			fmt.Fprintln(a.out, "  Enter, a line's number, or q")
		}
	}
}

// unsettled is what must be answered before starting, "" when nothing is.
func (p startPlan) unsettled() string {
	switch {
	case p.ProjectAsk != "":
		return "1 Project: " + p.ProjectAsk
	case p.NameAsk != "":
		return "3 You: " + p.NameAsk
	case p.TeamAsk != "":
		return "4 Team: " + p.TeamAsk
	case !hasMaster(p.Team):
		return "4 Team: master is required (the escalation target and the pilot)"
	}
	return ""
}

func (a *asker) screen(p startPlan) {
	fmt.Fprintf(a.out, "\nagentbus start — %s\n\n", p.Folder)
	val := func(v, ask string) string {
		if ask != "" {
			return "? " + ask
		}
		return v
	}
	fmt.Fprintf(a.out, "  1  Project    %-24s %s\n", val(p.Project, p.ProjectAsk), p.ProjectWhy)
	fmt.Fprintf(a.out, "  2  Folder     %s\n", p.Folder)
	fmt.Fprintf(a.out, "  3  You        %-24s %s\n", val(p.Name, p.NameAsk), p.NameWhy)
	fmt.Fprintf(a.out, "  4  Team       %s\n", strings.ReplaceAll(teamSpec(p.Team), ",", "  "))
	for _, l := range a.clientLines(p.Team) {
		fmt.Fprintf(a.out, "                %s\n", l)
	}
	if p.TeamAsk != "" {
		fmt.Fprintf(a.out, "                ? %s\n", p.TeamAsk)
	}
	if p.Existing && (a.live != nil || a.busmonDown() != "") {
		status, enter := a.openState(p)
		fmt.Fprintf(a.out, "\n  %s\n  Enter: %s   1-4: change   q: quit\n", status, enter)
		return
	}
	if !p.Existing && teamKey(p.Team) != teamKey(completeTeam(a.m)) {
		fmt.Fprintln(a.out, "\n  This team becomes the default for new projects (start.team in config.toml).")
	}
	fmt.Fprintln(a.out, "\n  Enter: start the team   1-4: change   q: quit, nothing written")
}

// shortScreen is an existing project's screen (start-busmon-pane): what
// runs and what Enter does, nothing to fill in.
func (a *asker) shortScreen(p startPlan) {
	fmt.Fprintf(a.out, "\nagentbus start — %s\n\n", p.Folder)
	status, enter := a.openState(p)
	fmt.Fprintf(a.out, "  %s\n  Enter: %s   c: change project, name or team   q: quit\n", status, enter)
}

// busmonDown says why busmon's tab would be reopened, "" when it would not
// or is unknown.
func (a *asker) busmonDown() string {
	switch a.busmon {
	case relaunch.VerdictRelaunch:
		return "busmon not running (its pane is a shell)"
	case relaunch.VerdictCreate:
		return "busmon's tab is missing"
	}
	return ""
}

// openState is an existing project's status line and what Enter does: the
// agents by their life, busmon by up's own judgement of its tab, which
// needs herdr only, so it is said even when the agents cannot be read.
func (a *asker) openState(p startPlan) (status, enter string) {
	busmon := a.busmonDown()
	if a.live == nil {
		status, enter = p.Project+": its agents' state cannot be read", "open it (agentbus up's rule)"
		if busmon != "" {
			status += "; " + busmon
			enter = "reopen busmon, and relaunch any agent that is gone (agentbus up's rule)"
		}
		return status + ".", enter
	}
	var dead []string
	for _, r := range p.Team {
		if a.live[r.Role] != relaunch.LifeAlive {
			dead = append(dead, r.Role)
		}
	}
	if len(dead) == 0 && busmon == "" {
		return fmt.Sprintf("%s is running: %d of %d agents live.", p.Project, len(p.Team), len(p.Team)), "focus its workspace"
	}
	status = fmt.Sprintf("%s is open: %d of %d agents live", p.Project, len(p.Team)-len(dead), len(p.Team))
	var does []string
	if len(dead) > 0 {
		status += " (" + strings.Join(dead, ", ") + " not)"
		does = append(does, "relaunch "+strings.Join(dead, " and ")+" in new panes")
	}
	if busmon != "" {
		status += ", " + busmon
		does = append(does, "reopen busmon")
	}
	return status + ".", strings.Join(does, ", ")
}

// clientLines says, per client of the team, whether it is installed.
func (a *asker) clientLines(team []rolecfg.TeamRole) []string {
	var lines []string
	seen := map[string]bool{}
	for _, r := range team {
		p, err := a.m.ResolveWith(r.Role, a.profileOf(r))
		if err != nil {
			continue
		}
		exe := rolecfg.ClientExecutable(p)
		if exe == "" || seen[exe] {
			continue
		}
		seen[exe] = true
		if _, err := a.look(exe); err != nil && !isUnknownCLI(err) {
			lines = append(lines, fmt.Sprintf("%s ✗ not found%s", exe, installHint(exe)))
		} else {
			lines = append(lines, exe+" ✓")
		}
	}
	return lines
}

func (a *asker) anyClient(team []rolecfg.TeamRole) bool {
	for _, l := range a.clientLines(team) {
		if strings.HasSuffix(l, "✓") {
			return true
		}
	}
	return false
}

// profileOf is the profile a team role runs on: its own when none is named.
func (a *asker) profileOf(r rolecfg.TeamRole) string {
	if r.Profile != "" {
		return r.Profile
	}
	own, _ := a.m.Resolve(r.Role)
	return own.Profile
}

func (a *asker) askProject(p *startPlan) error {
	for {
		in, err := a.line(fmt.Sprintf("  Project [%s]: ", p.Project))
		if err != nil || in == "" && p.Project != "" {
			return err
		}
		if err := p.setProject(in, "typed here", a.env, a.m); err != nil {
			fmt.Fprintln(a.out, "    a lowercase letter, then lowercase letters, digits, - or _, up to 32; not another folder's project")
			continue
		}
		return nil
	}
}

func (a *asker) askName(p *startPlan) error {
	for {
		in, err := a.line(fmt.Sprintf("  You [%s]: ", p.Name))
		if err != nil || in == "" && p.Name != "" {
			return err
		}
		if !bus.ValidName(in) {
			fmt.Fprintln(a.out, "    a lowercase letter, then lowercase letters, digits, - or _, up to 32")
			continue
		}
		p.Name, p.NameWhy, p.NameAsk = in, "typed here", ""
		return nil
	}
}

// askTeam toggles boot roles by number (master stays) and, with c and a
// number, chooses a role's client among its own profile and its
// fallback_profiles; Enter ends.
func (a *asker) askTeam(p *startPlan) error {
	roles := a.m.RolesByTier("boot")
	for {
		for i, role := range roles {
			mark, on := "-", slices.IndexFunc(p.Team, func(r rolecfg.TeamRole) bool { return r.Role == role })
			profile := ""
			if on >= 0 {
				mark, profile = "✓", a.profileOf(p.Team[on])
			} else {
				own, _ := a.m.Resolve(role)
				profile = own.Profile
			}
			fmt.Fprintf(a.out, "    %d %-10s %-20s %s\n", i+1, role, profile, mark)
		}
		in, err := a.line("  number: on/off   c then number: client   Enter when done\n  > ")
		if err != nil || in == "" {
			return err
		}
		client := strings.HasPrefix(in, "c")
		n, err := strconv.Atoi(strings.TrimPrefix(in, "c"))
		if err != nil || n < 1 || n > len(roles) {
			fmt.Fprintln(a.out, "    a role's number, or c and its number")
			continue
		}
		role := roles[n-1]
		before := slices.Clone(p.Team)
		at := slices.IndexFunc(p.Team, func(r rolecfg.TeamRole) bool { return r.Role == role })
		switch {
		case client:
			if err := a.askClient(p, role); err != nil {
				return err
			}
		case at >= 0 && role == "master":
			fmt.Fprintln(a.out, "    master cannot be removed: it is the escalation target and the pilot")
		case at >= 0:
			p.Team = slices.Delete(p.Team, at, at+1)
			if role == "foureyes" {
				fmt.Fprintln(a.out, "    no reviewer: 4-eyes reviews will wait for one")
			}
		default:
			p.Team = append(p.Team, rolecfg.TeamRole{Role: role})
		}
		team, err := a.m.ParseTeam(teamSpec(p.Team)) // back in the manifest's order
		if err != nil {                              // a role of the template the toggles cannot keep (foureyes)
			p.Team = before
			fmt.Fprintf(a.out, "    %v: the team is left as it was; give it with agentbus start --roles\n", err)
			continue
		}
		p.Team, p.TeamWhy, p.TeamAsk, a.chosenTeam = team, "chosen here", "", true
	}
}

// askClient offers the role's own profile and its fallback_profiles; one
// whose client is confirmed absent cannot be chosen.
func (a *asker) askClient(p *startPlan, role string) error {
	own, _ := a.m.Resolve(role)
	options := append([]string{own.Profile}, a.m.Fallbacks(role)...)
	if len(options) == 1 {
		fmt.Fprintf(a.out, "    no other client configured for %s (fallback_profiles in roles.toml)\n", role)
		return nil
	}
	var line []string
	for i, name := range options {
		prof, _ := a.m.ResolveWith(role, name)
		mark := "✓"
		if _, err := a.look(rolecfg.ClientExecutable(prof)); err != nil && !isUnknownCLI(err) {
			mark = "✗"
		}
		line = append(line, fmt.Sprintf("%d %s (%s) %s", i+1, name, prof.Client, mark))
	}
	in, err := a.line(fmt.Sprintf("    %s runs on: %s\n    > ", role, strings.Join(line, "   ")))
	if err != nil || in == "" {
		return err
	}
	n, err := strconv.Atoi(in)
	if err != nil || n < 1 || n > len(options) || strings.HasSuffix(line[n-1], "✗") {
		fmt.Fprintln(a.out, "    not one to choose: a number marked ✓")
		return nil
	}
	choice := options[n-1]
	if choice == own.Profile {
		choice = ""
	}
	at := slices.IndexFunc(p.Team, func(r rolecfg.TeamRole) bool { return r.Role == role })
	if at < 0 {
		p.Team = append(p.Team, rolecfg.TeamRole{Role: role, Profile: choice})
	} else {
		p.Team[at].Profile = choice
	}
	return nil
}

// installHint is a missing client's install page, as up names it.
func installHint(exe string) string {
	for _, c := range rolecfg.Clients {
		if c.Exe == exe {
			return ": " + c.Install
		}
	}
	return ""
}

// isUnknownCLI: the lookup could not tell (up counts it as found).
func isUnknownCLI(err error) bool { return errors.Is(err, errCLIUnknown) }
