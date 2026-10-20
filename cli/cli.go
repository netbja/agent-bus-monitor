// Package cli is the one table behind both binaries' help and flags: each
// command's usage, flags (long and short) and examples. The help is rendered
// from it and short flags are expanded from it, so neither can drift from the
// other. Spec: docs/superpowers/specs/2026-10-21-cli-help-design.md.
package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Flag is one option: --Long, and -Short when Short is set. Arg names the
// value; empty means a boolean.
type Flag struct {
	Long  string
	Short rune
	Arg   string
	Help  string
}

// Command is one subcommand's entry in the table.
type Command struct {
	Name      string
	Aliases   []string
	Usage     []string // synopsis lines, without the binary's name
	Summary   string
	Flags     []Flag
	Examples  []string
	NoProject bool // runs with no project and no broker
	Internal  bool // an entry point for scripts, herdr or units: long flags only
	Text      bool // its trailing words are a message: no global flag is read among them
}

// Expand returns args with each defined short flag (-x, globals and flags)
// rewritten to its long form, and --long=value split into --long value for a
// defined flag that takes a value. Anything else is left as it is: no
// bundling, no attached values, since these commands take free-text words.
func Expand(globals, flags []Flag, args []string) []string {
	out := make([]string, 0, len(args)+1)
	for _, a := range args {
		if f, ok := shortFlag(a, globals, flags); ok {
			out = append(out, "--"+f.Long)
			continue
		}
		if name, val, ok := strings.Cut(a, "="); ok && strings.HasPrefix(name, "--") {
			if f, ok := longFlag(name[2:], globals, flags); ok && f.Arg != "" {
				out = append(out, name, val)
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

func shortFlag(a string, sets ...[]Flag) (Flag, bool) {
	r := []rune(a)
	if len(r) != 2 || r[0] != '-' || r[1] == '-' {
		return Flag{}, false
	}
	for _, set := range sets {
		for _, f := range set {
			if f.Short != 0 && f.Short == r[1] {
				return f, true
			}
		}
	}
	return Flag{}, false
}

func longFlag(name string, sets ...[]Flag) (Flag, bool) {
	for _, set := range sets {
		for _, f := range set {
			if f.Long == name {
				return f, true
			}
		}
	}
	return Flag{}, false
}

// Lookup finds a command by name or alias.
func Lookup(cmds []Command, name string) (Command, bool) {
	for _, c := range cmds {
		if c.Name == name || slices.Contains(c.Aliases, name) {
			return c, true
		}
	}
	return Command{}, false
}

// Validate checks the table: unique names and aliases, a usage line each,
// and per command no duplicate long or short, no shadowed global, shorts that
// are single ASCII letters ('h' is help), and no short on an internal command.
func Validate(globals []Flag, cmds []Command) error {
	names := map[string]bool{}
	for _, c := range cmds {
		for _, n := range append([]string{c.Name}, c.Aliases...) {
			if names[n] {
				return fmt.Errorf("command %q: name %q used twice", c.Name, n)
			}
			names[n] = true
		}
		if len(c.Usage) == 0 {
			return fmt.Errorf("command %q: no usage line", c.Name)
		}
		if err := validateFlags(globals, c.Flags, c.Internal); err != nil {
			return fmt.Errorf("command %q: %w", c.Name, err)
		}
	}
	return validateFlags(nil, globals, false)
}

// ValidateFlags checks one flag set on its own (busmon's).
func ValidateFlags(flags []Flag) error { return validateFlags(nil, flags, false) }

func validateFlags(globals, flags []Flag, internal bool) error {
	longs, shorts := map[string]bool{"help": true}, map[rune]bool{'h': true}
	for _, g := range globals {
		longs[g.Long] = true
		if g.Short != 0 {
			shorts[g.Short] = true
		}
	}
	for _, f := range flags {
		if f.Long == "" || longs[f.Long] {
			return fmt.Errorf("flag --%s: empty or used twice", f.Long)
		}
		longs[f.Long] = true
		if f.Short == 0 {
			continue
		}
		if internal {
			return fmt.Errorf("flag --%s: an internal command takes long flags only", f.Long)
		}
		if !(f.Short >= 'a' && f.Short <= 'z' || f.Short >= 'A' && f.Short <= 'Z') || shorts[f.Short] {
			return fmt.Errorf("flag --%s: short -%c is not a letter or is used twice", f.Long, f.Short)
		}
		shorts[f.Short] = true
	}
	return nil
}

// Render writes one command's help: usage lines, aliases, summary, flags and
// examples.
func Render(w io.Writer, bin string, c Command) {
	for i, u := range c.Usage {
		lead := "usage: "
		if i > 0 {
			lead = "       "
		}
		fmt.Fprintf(w, "%s%s %s\n", lead, bin, u)
	}
	if len(c.Aliases) > 0 {
		fmt.Fprintf(w, "alias: %s\n", strings.Join(c.Aliases, ", "))
	}
	if c.Summary != "" {
		fmt.Fprintf(w, "\n%s\n", c.Summary)
	}
	if len(c.Flags) > 0 {
		fmt.Fprint(w, "\nflags:\n")
		writeFlags(w, c.Flags)
	}
	if len(c.Examples) > 0 {
		fmt.Fprint(w, "\nexamples:\n")
		for _, e := range c.Examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
}

// Index writes the overview: global flags, then every command with its
// summary, the internal ones apart.
func Index(w io.Writer, bin string, globals []Flag, cmds []Command) {
	fmt.Fprintf(w, "usage: %s [flags] <command> [args...]\n", bin)
	if len(globals) > 0 {
		fmt.Fprint(w, "\nflags:\n")
		writeFlags(w, globals)
	}
	width := 0
	for _, c := range cmds {
		width = max(width, len(c.Name))
	}
	section := func(title string, internal bool) {
		first := true
		for _, c := range cmds {
			if c.Internal != internal {
				continue
			}
			if first {
				fmt.Fprintf(w, "\n%s:\n", title)
				first = false
			}
			fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("  %-*s  %s", width, c.Name, c.Summary), " "))
		}
	}
	section("commands", false)
	section("internal (scripts, herdr, units)", true)
	fmt.Fprintf(w, "\n%s help <command>, or %s <command> --help, shows one command.\n", bin, bin)
}

// FlagLabel is "-s, --since <id>", or "    --loop" without a short.
func FlagLabel(f Flag) string {
	s := "    "
	if f.Short != 0 {
		s = "-" + string(f.Short) + ", "
	}
	s += "--" + f.Long
	if f.Arg != "" {
		s += " " + f.Arg
	}
	return s
}

func writeFlags(w io.Writer, flags []Flag) {
	width := 0
	for _, f := range flags {
		width = max(width, len(FlagLabel(f)))
	}
	for _, f := range flags {
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("  %-*s  %s", width, FlagLabel(f), f.Help), " "))
	}
}
