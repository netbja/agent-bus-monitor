package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
)

// busmonSettingsPath is busmon's settings file (v0.18), which also holds the
// person's name: $XDG_CONFIG_HOME/agentbus/busmon.toml, else ~/.config/….
func busmonSettingsPath(env func(string) string) string {
	if x := env("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "agentbus", "busmon.toml")
	}
	return filepath.Join(env("HOME"), ".config", "agentbus", "busmon.toml")
}

// settingsIdentity is [identity] as from busmon.toml, "" when unset or invalid.
func settingsIdentity(env func(string) string) string {
	var s struct {
		Identity struct {
			As string `toml:"as"`
		} `toml:"identity"`
	}
	if _, err := toml.DecodeFile(busmonSettingsPath(env), &s); err != nil || !bus.ValidName(s.Identity.As) {
		return ""
	}
	return s.Identity.As
}

// runIdentity prints the person's recorded name, or records it once:
// `identity set <name>` appends an [identity] section to busmon.toml (created
// 0600) and leaves every other line as it is. A name already recorded is
// never replaced here: edit the file to change it.
func runIdentity(args []string, env func(string) string, stdout, stderr io.Writer) int {
	path := busmonSettingsPath(env)
	if len(args) == 0 {
		if as := settingsIdentity(env); as != "" {
			fmt.Fprintln(stdout, as)
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
		fmt.Fprintf(stderr, "agentbus identity: %s does not parse (%v); not touched\n", path, err)
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
	fmt.Fprintf(stdout, "recorded: you are %s on the bus (%s)\n", name, path)
	return 0
}
