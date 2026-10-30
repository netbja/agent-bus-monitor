package setup

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Clients is setup.sh's step 5: which AI clients are on PATH (informational).
// The clients are the ones the launcher can run (rolecfg.Clients); none is a
// warning, never a failure (installer-no-client): the bus and busmon work
// without one, and nothing is installed for the person.
func (in *Installer) Clients() {
	in.O.Step("5/5 Scanning installed AI coding agents")
	found := false
	for _, c := range rolecfg.Clients {
		if p, err := in.R.LookPath(c.Exe); err == nil {
			in.O.OK(c.Exe + ": available (" + p + ")")
			found = true
		} else {
			in.O.Info(c.Exe + ": not installed (optional)")
		}
	}
	if !found {
		in.O.Warn("No agent CLI on PATH: agent-bus starts its agents in one of these, so install at least one:")
		for _, l := range rolecfg.ClientGuidance() {
			fmt.Fprintln(in.O.out, "      "+l)
		}
		in.O.Info("Meanwhile the bus and busmon work. Once one is installed: agentbus up <project> <path>")
	}
}

// installedAgentbus is the bin dir's agentbus when it is executable.
func (in *Installer) installedAgentbus() (string, bool) {
	p := filepath.Join(in.Opt.BinDir, "agentbus")
	st, err := os.Stat(p)
	return p, err == nil && st.Mode()&0o111 != 0
}

// Identity is setup.sh's name on the bus: asked once, only on a terminal and
// when none is set; --yes, a dry run, no terminal or
// AGENTBUS_SETUP_NO_IDENTITY=1 skip it. An empty answer skips it too.
func (in *Installer) Identity(ctx context.Context, p *Prompter) {
	ab, ok := in.installedAgentbus()
	if in.Opt.DryRun || in.Opt.Yes || in.Getenv("AGENTBUS_SETUP_NO_IDENTITY") == "1" || !ok || p.TTY == nil {
		return
	}
	if _, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"identity"}}); err == nil {
		return // already known
	}
	fmt.Fprint(p.TTY, "    ? Your name on the bus (lowercase, e.g. charlie; Enter to skip): ")
	if p.rd == nil {
		p.rd = bufio.NewReader(p.TTY)
	}
	line, _ := p.rd.ReadString('\n')
	who := strings.TrimSpace(line)
	if who == "" {
		return
	}
	if _, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"identity", "set", who}}); err == nil {
		in.O.OK("busmon and agentbus up will know you as " + who)
	} else {
		in.O.Warn("name not recorded; set it later with: agentbus identity set <name>")
	}
}

// Models is setup.sh's model-table refresh, through the installed agentbus
// (each client's own free catalogue, never a model call);
// AGENTBUS_SETUP_NO_MODELS=1 skips it.
func (in *Installer) Models(ctx context.Context) {
	if in.Opt.DryRun {
		in.O.Info("[dry-run] would refresh the model table (agentbus models --refresh)")
		return
	}
	ab, ok := in.installedAgentbus()
	if in.Getenv("AGENTBUS_SETUP_NO_MODELS") == "1" || !ok {
		return
	}
	out, err := in.R.Run(ctx, Cmd{Name: ab, Args: []string{"models", "--refresh"}})
	if err != nil {
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		in.O.Warn("model table not refreshed: " + lines[len(lines)-1])
		return
	}
	in.O.OK("model table refreshed (agentbus models)")
}

// Summary is setup.sh's closing box and next steps; it returns the exit code
// (1 when tools were missing).
func (o *Output) Summary(opt Options, missing int, rev string) int {
	color, title := o.c("32"), "Installation complete ("+rev+")"
	switch {
	case opt.DryRun:
		if missing > 0 {
			color = o.c("31")
		}
		title = fmt.Sprintf("Dry run complete — nothing was changed (%d missing, %d warning(s))", missing, o.Warnings)
	case o.Warnings > 0:
		color, title = o.c("33"), fmt.Sprintf("Installation complete with %d warning(s) — see above", o.Warnings)
	}
	bar := "================================================================="
	fmt.Fprintf(o.out, "\n%s%s%s\n", color, bar, o.c("0"))
	fmt.Fprintf(o.out, "%s%s %s %s\n", o.c("1"), color, title, o.c("0"))
	fmt.Fprintf(o.out, "%s%s%s\n\n", color, bar, o.c("0"))
	if missing > 0 {
		return 1
	}
	cy, b, r := o.c("36"), o.c("1"), o.c("0")
	fmt.Fprintln(o.out, "Next steps:")
	fmt.Fprintln(o.out, "  1. Start herdr (a named session per project is handy):")
	fmt.Fprintln(o.out, "     "+cy+"herdr --session myproject"+r)
	fmt.Fprintln(o.out, "")
	fmt.Fprintln(o.out, "  2. Inside it, provision and open the Agent Bus team in one command:")
	fmt.Fprintln(o.out, "     "+cy+"agentbus up myproject /path/to/your/codebase --as <your-name>"+r)
	fmt.Fprintln(o.out, "     (busmon opens first; later, reopen any project with "+b+"Ctrl+B"+r+" then "+b+"Up"+r+")")
	fmt.Fprintln(o.out, "")
	fmt.Fprintln(o.out, "  Keep it current: "+cy+"./setup.sh update"+r+" · check it: "+cy+"./setup.sh status"+r+" · remove it: "+cy+"./setup.sh uninstall"+r)
	fmt.Fprintln(o.out, "")
	return 0
}
