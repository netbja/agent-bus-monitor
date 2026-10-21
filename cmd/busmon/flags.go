package main

import (
	"flag"
	"io"

	"github.com/netbja/agent-bus-monitor/cli"
)

// busmonFlags is the one table behind busmon's flags and its -h: each flag
// is registered under its long and its short name. Go's flag package also
// keeps the single-dash long forms (-project) working.
var busmonFlags = []cli.Flag{
	{Long: "project", Short: 'p', Arg: "<project>", Help: "project namespace (or AGENT_BUS_PROJECT)"},
	{Long: "host", Short: 'H', Arg: "<host>", Help: "Redis host (overrides REDIS_HOST)"},
	{Long: "limit", Short: 'n', Arg: "<n>", Help: "ACTIVITY backfill: replay the last N lines on launch (0 = all history; or AGENT_BUS_BUSMON_LIMIT)"},
	{Long: "reset", Short: 'r', Help: "purge the project's streams before launching (asks to confirm)"},
	{Long: "yes", Short: 'y', Help: "skip the --reset or --delete confirmation"},
	{Long: "list", Short: 'l', Help: "list the projects on this bus and exit"},
	{Long: "delete", Short: 'd', Arg: "<project>", Help: "delete every key of a project (DEL, not --reset's XTRIM) and exit"},
	{Long: "theme", Short: 't', Arg: "<theme>", Help: "colour theme: auto, dark or light (or AGENT_BUS_BUSMON_THEME; default auto)"},
	{Long: "as", Short: 'a', Arg: "<name>", Help: "your name on the bus, shown as a human (or AGENT_BUS_HUMAN; default \"pilot\")"},
	{Long: "version", Short: 'V', Help: "print the version and exit"},
	{Long: "pick", Short: 'k', Help: "choose a project template and open it with agentbus up (the herdr plugin's Projects picker)"},
}

var busmonHelp = cli.Command{
	Name:    "busmon",
	Usage:   []string{"[flags]"},
	Summary: "live dashboard of one project's agents, activity, board and requests",
	Flags:   busmonFlags,
	Examples: []string{
		"busmon -p demo",
		"busmon -p demo -n 0 -t light",
		"busmon -l",
		"busmon -d old-project",
	},
}

// busmonOpts holds the parsed flags.
type busmonOpts struct {
	host, project, delete, theme, as string
	limit                            int
	reset, yes, list, version, pick  bool
}

// parseFlags parses busmon's command line from the table. set names the
// flags given (by long name, whichever form was typed). -h and --help return
// flag.ErrHelp after printing the help to out.
func parseFlags(args []string, out io.Writer) (busmonOpts, map[string]bool, error) {
	o := busmonOpts{}
	ptr := map[string]any{
		"project": &o.project, "host": &o.host, "limit": &o.limit, "reset": &o.reset, "yes": &o.yes,
		"list": &o.list, "delete": &o.delete, "theme": &o.theme, "as": &o.as, "version": &o.version, "pick": &o.pick,
	}
	fs := flag.NewFlagSet("busmon", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { cli.Render(out, "busmon", busmonHelp) }
	long := map[string]string{}
	for _, f := range busmonFlags {
		names := []string{f.Long, string(f.Short)}
		for _, n := range names {
			long[n] = f.Long
			switch p := ptr[f.Long].(type) {
			case *string:
				fs.StringVar(p, n, "", f.Help)
			case *int:
				fs.IntVar(p, n, 25, f.Help)
			case *bool:
				fs.BoolVar(p, n, false, f.Help)
			default:
				panic("busmon flag --" + f.Long + " has no variable")
			}
		}
	}
	if err := fs.Parse(args); err != nil {
		return o, nil, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[long[f.Name]] = true })
	return o, set, nil
}
