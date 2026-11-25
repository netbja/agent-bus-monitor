package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/cli"
)

// expandArgs rewrites the short flags of the command line to their long
// forms from the table: the global flags before the command, then the
// command's own flags after it. Global shorts are never expanded after the
// command: there -p is a message word (mkdir -p), and --project is read
// anywhere, so expanding it would write to another project. An internal
// entry point, the scripts' roles verbs and an unknown command keep their
// arguments as they are.
func expandArgs(args []string) []string {
	i := 0
	for i < len(args) {
		a := args[i]
		f, ok := globalFlag(a)
		if !ok {
			break
		}
		i++
		if f.Arg != "" && !strings.Contains(a, "=") {
			i++
		}
	}
	i = min(i, len(args))
	out := cli.Expand(globalFlags, nil, args[:i])
	if i == len(args) {
		return out
	}
	name, rest := args[i], args[i+1:]
	c, ok := cli.Lookup(commands, name)
	switch {
	case !ok || c.Internal,
		name == "roles" && (len(rest) == 0 || rest[0] != "doctor"),
		name == "service" && len(rest) > 0 && rest[0] == "run":
		return append(append(out, name), rest...)
	}
	return append(append(out, name), cli.Expand(nil, c.Flags, rest)...)
}

// globalArgs takes --host and --project out of args: before the command,
// and after it too unless the command's trailing words are a message (Text),
// where "--project x" is text and must not switch the project.
func globalArgs(args []string) (rest []string, host, project string) {
	i := 0
	for i < len(args) {
		f, ok := globalFlag(args[i])
		if !ok {
			break
		}
		i++
		if f.Arg != "" && !strings.Contains(args[i-1], "=") {
			i++
		}
	}
	i = min(i, len(args))
	lead, tail := args[:i], slices.Clone(args[i:])
	lead, host = extractFlag(lead, "--host")
	lead, project = extractFlag(lead, "--project")
	if c, ok := cli.Lookup(commands, first(tail)); !ok || !c.Text {
		if host == "" {
			tail, host = extractFlag(tail, "--host")
		}
		if project == "" {
			tail, project = extractFlag(tail, "--project")
		}
	}
	return append(slices.Clone(lead), tail...), host, project
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// globalFlag reports whether a is a global flag: -p, --project or
// --project=value.
func globalFlag(a string) (cli.Flag, bool) {
	name, _, _ := strings.Cut(a, "=")
	for _, f := range globalFlags {
		if a == "-"+string(f.Short) || name == "--"+f.Long {
			return f, true
		}
	}
	return cli.Flag{}, false
}

// versionAlias reads --version and -v, as the first word, as the command
// version; other command lines are returned unchanged.
func versionAlias(args []string) []string {
	if len(args) >= 1 && (args[0] == "--version" || args[0] == "-v") {
		return append([]string{"version"}, args[1:]...)
	}
	return args
}

// printVersion is agentbus version's output: the protocol, then the build.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "agentbus protocol v%d\nagentbus version %s\n", bus.ProtocolVersion, buildinfo.Version)
}

// helpRequest reports whether args (the global flags removed) ask for help,
// and for which command ("" for the index): help [cmd], --help, -h, or
// <cmd> --help|-h right after the command. Anywhere else --help is a word of
// a message.
func helpRequest(args []string) (string, bool) {
	isHelp := func(s string) bool { return s == "help" || s == "--help" || s == "-h" }
	switch {
	case len(args) == 0:
		return "", false
	case isHelp(args[0]):
		if len(args) > 1 {
			return args[1], true
		}
		return "", true
	case len(args) > 1 && args[1] != "help" && isHelp(args[1]):
		return args[0], true
	}
	return "", false
}

// runHelp prints the index, or one command's help: exit 0, or 2 for an
// unknown command.
func runHelp(name string, out, errw io.Writer) int {
	if name == "" {
		cli.Index(out, "agentbus", globalFlags, commands, helpPage)
		return 0
	}
	c, ok := cli.Lookup(commands, name)
	if !ok {
		fmt.Fprintf(errw, "error: unknown command %q; agentbus help lists them\n", name)
		return 2
	}
	cli.Render(out, "agentbus", c)
	return 0
}

// helpSections groups every command for the top-level help, in the order
// shown (TestEveryCommandInOneSection: each registered command exactly once).
var helpSections = []cli.Section{
	{Title: "AGENT COMMANDS", Names: []string{"status", "report", "reports", "background", "handoff", "checkback"}},
	{Title: "MESSAGE COMMANDS", Names: []string{"cmd", "reply", "notify", "thread", "ask", "asks", "answer", "listen", "subscribe", "history"}},
	{Title: "REVIEW COMMANDS", Names: []string{"challenge", "verdict", "verdicts", "gate"}},
	{Title: "WORK COMMANDS", Names: []string{"board", "request", "retro", "plan", "idea", "merge-queue", "deliver", "ci", "run"}},
	{Title: "TERMINAL COMMANDS", Names: []string{"agents", "pop", "retire", "reload", "swap", "swaps", "pane", "bind", "screen", "attach", "self-repair", "pilot"}},
	{Title: "PROJECT COMMANDS", Names: []string{"up", "start", "down", "pause", "resume", "suspend", "shutdown", "projects", "service", "holder", "dispatch", "missions", "mission"}},
	{Title: "USAGE COMMANDS", Names: []string{"usage", "budget", "tokens", "quota", "models", "refresh", "limits"}},
	{Title: "ADMIN COMMANDS", Names: []string{"config", "identity", "notifications", "roles", "health", "backup", "restore", "update", "mem", "check", "term", "version", "help"}},
	{Title: "INTERNAL COMMANDS", Names: []string{"setup", "peer", "fake-client", "statusline"}},
}

// helpPage is the top-level help: the sections, then the examples and where
// to read more.
var helpPage = cli.Page{
	Sections:  helpSections,
	Examples:  []string{"agentbus up", "agentbus status", "agentbus board", "agentbus update"},
	LearnMore: []string{"Use `agentbus help <command>` for more about a command."},
}
