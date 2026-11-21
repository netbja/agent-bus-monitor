package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/cli"
)

// busmonFlags is the one table behind busmon's flags and its -h: each flag
// is registered under its long and its short name. Go's flag package also
// keeps the single-dash long forms (-project) working.
var busmonFlags = []cli.Flag{
	{Long: "project", Short: 'p', Arg: "<project>", Help: "project namespace (or AGENT_BUS_PROJECT, else the one last used on this broker)"},
	{Long: "host", Short: 'H', Arg: "<host>", Help: "Redis host (overrides REDIS_HOST; a unix socket or an ACL user: --url)"},
	{Long: "url", Short: 'u', Arg: "<url>", Help: "Redis URL, redis://[user:password@]host:port[/db] or unix:///path.sock (or REDIS_URL; a password is safer there, out of ps); never with --host; with broker.acl on, busmon's own ACL user is used"},
	{Long: "limit", Short: 'n', Arg: "<n>", Help: "ACTIVITY backfill: replay the last N lines on launch (0 = all history; or AGENT_BUS_BUSMON_LIMIT)"},
	{Long: "reset", Short: 'r', Help: "purge the project's streams before launching (asks to confirm)"},
	{Long: "yes", Short: 'y', Help: "skip the --reset or --delete question (--delete still refuses a project that is not asleep, and still backs it up first)"},
	{Long: "list", Short: 'l', Help: "list the projects on this bus and exit"},
	{Long: "delete", Short: 'd', Arg: "<project>", Help: "delete every key of a project (DEL, not --reset's XTRIM) and exit: as the Projects page, only a project asleep, after a backup, naming the hosts where its units remain"},
	{Long: "theme", Short: 't', Arg: "<theme>", Help: "colour theme: auto, dark or light (or AGENT_BUS_BUSMON_THEME; default auto)"},
	{Long: "as", Short: 'a', Arg: "<name>", Help: "your name on the bus, shown as a human (or AGENT_BUS_HUMAN, identity.as, then person.name; else \"pilot\")"},
	{Long: "version", Short: 'V', Help: "print the version and exit"},
	{Long: "new", Short: 'k', Help: "choose a project template and open it with agentbus up"},
	{Long: "from", Short: 'f', Arg: "<project>", Help: "the project a switch left: B goes back to it (set by the switch itself)"},
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
	from, url                        string
	limit                            int
	reset, yes, list, version, pick  bool
}

// parseFlags parses busmon's command line from the table. set names the
// flags given (by long name, whichever form was typed). -h and --help return
// flag.ErrHelp after printing the help to out.
func parseFlags(args []string, out io.Writer) (busmonOpts, map[string]bool, error) {
	o := busmonOpts{}
	ptr := optPtrs(&o)
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
	fs.BoolVar(&o.pick, "pick", false, "") // --new's name before busmon-cli-flags item 5: parsed, not listed
	long["pick"] = "new"
	if err := fs.Parse(args); err != nil {
		return o, nil, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[long[f.Name]] = true })
	return o, set, nil
}

// optPtrs is each flag's variable, by long name: parseFlags fills them, the
// project switch reads them back (switchArgv).
func optPtrs(o *busmonOpts) map[string]any {
	return map[string]any{
		"project": &o.project, "host": &o.host, "limit": &o.limit, "reset": &o.reset, "yes": &o.yes,
		"list": &o.list, "delete": &o.delete, "theme": &o.theme, "as": &o.as, "version": &o.version, "new": &o.pick,
		"from": &o.from, "url": &o.url,
	}
}

// switchOneShot are the flags a project switch never carries: actions that
// end busmon before its screen, and the project itself.
// --url is not carried either: applyURL set REDIS_URL, which the switched
// busmon inherits, so a password in it never reaches an argv.
var switchOneShot = map[string]bool{"from": true, "project": true, "reset": true, "delete": true, "yes": true, "list": true, "new": true, "version": true, "url": true}

// switchArgv is the command line busmon re-executes itself with to open
// target (project-switch note §2.2): rebuilt from the parsed options, never
// edited as text (Go's flag accepts too many spellings), --<long>=<value>
// for each flag given that is not a one-shot, then --project=<target>. A
// flag added later is carried by default.
func switchArgv(o busmonOpts, set map[string]bool, from, target string) []string {
	var argv []string
	ptr := optPtrs(&o)
	for _, f := range busmonFlags {
		if !set[f.Long] || switchOneShot[f.Long] {
			continue
		}
		switch p := ptr[f.Long].(type) {
		case *string:
			argv = append(argv, "--"+f.Long+"="+*p)
		case *int:
			argv = append(argv, "--"+f.Long+"="+strconv.Itoa(*p))
		case *bool:
			argv = append(argv, "--"+f.Long+"="+strconv.FormatBool(*p))
		}
	}
	argv = append(argv, "--project="+target)
	if from != "" {
		argv = append(argv, "--from="+from) // B's way back (project-switch-ux item 4)
	}
	return argv
}

// applyURL makes --url the broker every connection of this busmon reaches,
// by setting REDIS_URL (bus.Connect reads it first, and so does a busmon
// started from this one). --host beside a URL, given or in REDIS_URL, is
// refused: bus.Connect would drop it without a word (busmon-cli-flags
// item 4).
func applyURL(o busmonOpts, set map[string]bool, getenv func(string) string, setenv func(string, string) error) error {
	if set["url"] {
		if set["host"] {
			return fmt.Errorf("give --host and --url one at a time: the URL names the host")
		}
		if _, err := bus.ConnectOptions(func(k string) string {
			if k == "REDIS_URL" {
				return o.url
			}
			return getenv(k)
		}, ""); err != nil {
			return fmt.Errorf("--url %w", err)
		}
		return setenv("REDIS_URL", o.url)
	}
	if set["host"] && getenv("REDIS_URL") != "" {
		return fmt.Errorf("REDIS_URL is set and names the broker: --host would be ignored; unset REDIS_URL, or give the broker with --url")
	}
	return nil
}
