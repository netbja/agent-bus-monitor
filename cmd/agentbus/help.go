package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

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
		cli.Index(out, "agentbus", globalFlags, commands)
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
