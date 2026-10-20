package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

const pluginID = "netbja.agentbus"

// pluginDeps are the seams of `agentbus plugin`: herdr's CLI, the environment,
// the process replacement of busmon-pane, and the pane's stdin.
type pluginDeps struct {
	Herdr func(args ...string) ([]byte, error)
	Env   func(string) string
	Exec  func(argv0 string, argv []string) error
	Stdin io.Reader
}

func realPluginDeps() pluginDeps {
	return pluginDeps{
		Herdr: func(args ...string) ([]byte, error) {
			return exec.Command(herdrBin(os.Getenv), args...).CombinedOutput()
		},
		Env:   os.Getenv,
		Exec:  func(argv0 string, argv []string) error { return syscall.Exec(argv0, argv, os.Environ()) },
		Stdin: os.Stdin,
	}
}

// pluginContext is the part of herdr's HERDR_PLUGIN_CONTEXT_JSON we use.
type pluginContext struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceLabel string `json:"workspace_label"`
}

// readPluginContext tolerates a missing, malformed or extended value.
func readPluginContext(env func(string) string) pluginContext {
	var c pluginContext
	if raw := env("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return pluginContext{}
		}
	}
	return c
}

func runPlugin(args []string, d pluginDeps, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: agentbus plugin projects|busmon|busmon-pane")
		return 2
	}
	switch args[0] {
	case "projects":
		return pluginOpen(d, stderr, "picker", readPluginContext(d.Env), nil)
	case "busmon":
		return pluginBusmon(d, stderr)
	case "busmon-pane":
		return pluginBusmonPane(d, stdout, stderr)
	}
	fmt.Fprintf(stderr, "agentbus plugin: unknown entry %q\n", args[0])
	return 2
}

// pluginOpen opens one of the plugin's panes in the captured workspace.
func pluginOpen(d pluginDeps, stderr io.Writer, entry string, c pluginContext, envs []string) int {
	a := []string{"plugin", "pane", "open", "--plugin", pluginID, "--entrypoint", entry}
	if c.WorkspaceID != "" {
		a = append(a, "--workspace", c.WorkspaceID)
	}
	for _, e := range envs {
		a = append(a, "--env", e)
	}
	if out, err := d.Herdr(a...); err != nil {
		fmt.Fprintf(stderr, "agentbus plugin: herdr plugin pane open failed: %v %s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	return 0
}

func pluginBusmon(d pluginDeps, stderr io.Writer) int {
	c := readPluginContext(d.Env)
	fallback := func(why string) int {
		return pluginOpen(d, stderr, "picker", c, []string{"AGENTBUS_PLUGIN_REASON=" + why})
	}
	label := c.WorkspaceLabel
	if !bus.ValidName(label) {
		return fallback(fmt.Sprintf("workspace %q is not an Agent Bus project name; pick one", label))
	}
	rows, _ := projlist.Templates(rolecfg.ProjectsDir(d.Env))
	var path string
	for _, r := range rows {
		if r.Name == label {
			path = r.Path
		}
	}
	if path == "" {
		return fallback(fmt.Sprintf("workspace %q has no project template; pick one", label))
	}
	envs := []string{"AGENTBUS_PLUGIN_PROJECT=" + label}
	if as := pluginIdentity(d.Env, path); as != "" {
		envs = append(envs, "AGENTBUS_PLUGIN_AS="+as)
	}
	return pluginOpen(d, stderr, "busmon", c, envs)
}

// pluginIdentity is AGENT_BUS_HUMAN, else the --as of the template's busmon
// tab. The command is only parsed, never run; an unparsable one yields "".
func pluginIdentity(env func(string) string, templatePath string) string {
	if h := env("AGENT_BUS_HUMAN"); h != "" {
		return h
	}
	var t struct {
		Tabs []struct {
			Name    string `toml:"name"`
			Command string `toml:"command"`
		} `toml:"tabs"`
	}
	if _, err := toml.DecodeFile(templatePath, &t); err != nil {
		return ""
	}
	for _, tab := range t.Tabs {
		if tab.Name != "busmon" {
			continue
		}
		words, err := rolecfg.ShellSplit(tab.Command)
		if err != nil {
			return ""
		}
		for i := 0; i+1 < len(words); i++ {
			if words[i] == "--as" {
				return words[i+1]
			}
		}
	}
	return ""
}

func pluginBusmonPane(d pluginDeps, stdout, stderr io.Writer) int {
	project := d.Env("AGENTBUS_PLUGIN_PROJECT")
	if !bus.ValidName(project) {
		return pluginPaneFail(d, stdout, fmt.Sprintf("busmon: no valid project was passed to this pane (AGENTBUS_PLUGIN_PROJECT=%q).", project))
	}
	exe, err := os.Executable()
	if err == nil {
		if r, e := filepath.EvalSymlinks(exe); e == nil {
			exe = r
		}
	}
	if err != nil {
		return pluginPaneFail(d, stdout, fmt.Sprintf("agentbus plugin: cannot locate busmon: %v", err))
	}
	busmon := filepath.Join(filepath.Dir(exe), "busmon")
	argv := []string{busmon, "--project", project}
	if as := d.Env("AGENTBUS_PLUGIN_AS"); as != "" {
		argv = append(argv, "--as", as)
	}
	err = d.Exec(busmon, argv)
	return pluginPaneFail(d, stdout, fmt.Sprintf("agentbus plugin: exec %s: %v", busmon, err))
}

// pluginPaneFail shows why the pane cannot start and keeps it open until a
// line arrives on stdin, so the reason can be read.
func pluginPaneFail(d pluginDeps, stdout io.Writer, reason string) int {
	fmt.Fprintf(stdout, "%s\nPress Enter to close.\n", reason)
	if d.Stdin != nil {
		_, _ = bufio.NewReader(d.Stdin).ReadString('\n')
	}
	return 1
}
