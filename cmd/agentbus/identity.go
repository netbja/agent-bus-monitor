package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// personName is the person's recorded name from the files only (config.toml,
// then the legacy busmon.toml; the environment is busmon's own business), with
// the file it came from; "" when unset or invalid.
func personName(env func(string) string) (name, from string) {
	r := config.Resolver{Files: []*config.File{
		config.Load(config.Path(env), "config.toml"),
		config.LoadLegacy(config.LegacyPath(env), "busmon.toml"),
	}}
	got := r.Get("identity.as")
	if got.Source == "default" {
		return "", ""
	}
	return got.Value.(string), got.Source
}

// settingsIdentity is the recorded name, the default --as of busmon and up.
func settingsIdentity(env func(string) string) string {
	name, _ := personName(env)
	return name
}

// runIdentity prints the person's recorded name (its file on stderr), or
// records it once: `identity set <name>` appends an [identity] section to
// config.toml (created 0600) and leaves every other line as it is. A name
// already in config.toml is never replaced here: edit the file to change it.
// A name in busmon.toml does not refuse it: config.toml's wins, and set says so.
func runIdentity(args []string, env func(string) string, stdout, stderr io.Writer) int {
	path := config.Path(env)
	if len(args) == 0 {
		if as, from := personName(env); as != "" {
			fmt.Fprintln(stdout, as)
			fmt.Fprintf(stderr, "(from %s)\n", from)
			return 0
		}
		return 1
	}
	if args[0] != "set" || len(args) != 2 {
		fmt.Fprintln(stderr, "usage: agentbus identity [set <name>]")
		return 2
	}
	name := args[1]
	if !bus.ValidName(name) {
		fmt.Fprintf(stderr, "agentbus identity: %q is not a valid name (lowercase letters, digits, - or _, starting with a letter)\n", name)
		return 1
	}
	var s map[string]any
	md, err := toml.DecodeFile(path, &s)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		var pe toml.ParseError
		if errors.As(err, &pe) { // toml's own message can quote the file: the line only
			fmt.Fprintf(stderr, "agentbus identity: %s:%d does not parse; not touched\n", path, pe.Position.Line)
		} else {
			fmt.Fprintf(stderr, "agentbus identity: %s cannot be read; not touched\n", path)
		}
		return 1
	case md.IsDefined("identity", "as"):
		fmt.Fprintf(stderr, "agentbus identity: %s already names you; edit it to change\n", path)
		return 1
	case md.IsDefined("identity"):
		fmt.Fprintf(stderr, "agentbus identity: %s has an [identity] section; add as = %q there\n", path, name)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "agentbus identity: %v\n", err)
		return 1
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "agentbus identity: %v\n", err)
		return 1
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[identity]\nas = %q\n", name); err != nil {
		fmt.Fprintf(stderr, "agentbus identity: %v\n", err)
		return 1
	}
	if old := config.LoadLegacy(config.LegacyPath(env), "busmon.toml"); old.Has("identity.as") {
		if was := (config.Resolver{Files: []*config.File{old}}).String("identity.as"); was != "" && was != name {
			fmt.Fprintf(stdout, "recorded %s in config.toml; busmon.toml's name %s is now shadowed (%s)\n", name, was, path)
			return 0
		}
	}
	fmt.Fprintf(stdout, "recorded: you are %s on the bus (%s)\n", name, path)
	return 0
}
