package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/buildinfo"
)

// upDeps is what `agentbus up` touches, injected for tests.
type upDeps struct {
	Herdr     func(args ...string) ([]byte, error) // the herdr CLI
	Bootstrap func(args []string) error           // scripts/bootstrap <args>
	Env       func(string) string
}

type upTemplate struct {
	Name       string `toml:"name"`
	WorkingDir string `toml:"working_dir"`
	Tabs       []struct {
		Name    string `toml:"name"`
		Command string `toml:"command"`
	} `toml:"tabs"`
}

// runUp provisions a project (bootstrap, idempotent) and, inside herdr, opens
// its workspace directly — busmon first and focused, then every tab of the
// template — or focuses it when it is already open. It replaces "bootstrap new,
// then Ctrl+B Up and pick the project". Outside herdr it provisions and says how
// to open it.
func runUp(args []string, d upDeps, stdout, stderr io.Writer) int {
	if len(args) == 0 || !bus.ValidName(args[0]) {
		fmt.Fprintln(stderr, "usage: agentbus up <project> [<path>] [--as <your-name>] [bootstrap flags: --no-dispatch --no-missions --session <s> --index --cron --yes]")
		return 1
	}
	project := args[0]
	if err := d.Bootstrap(args); err != nil {
		fmt.Fprintf(stderr, "agentbus up: bootstrap failed: %v\n", err)
		return 1
	}
	if d.Env("HERDR_ENV") != "1" {
		fmt.Fprintf(stdout, "provisioned %s. To open it, start herdr (herdr --session %s), then run: agentbus up %s\n", project, project, project)
		return 0
	}
	if id, err := openWorkspace(d, project); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	} else if id != "" {
		fmt.Fprintf(stdout, "%s is already open: focused workspace %s\n", project, id)
		return 0
	}
	path := templatePath(d, project)
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); err != nil {
		fmt.Fprintf(stderr, "agentbus up: reading %s: %v\n", path, err)
		return 1
	}
	if len(tpl.Tabs) == 0 {
		fmt.Fprintf(stderr, "agentbus up: %s has no tabs\n", path)
		return 1
	}
	if err := layout(d, project, tpl); err != nil {
		fmt.Fprintf(stderr, "agentbus up: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "opened %s: %d tabs, busmon first (reopen later with Ctrl+B then Up)\n", project, len(tpl.Tabs))
	return 0
}

// openWorkspace focuses an already open workspace labelled project and returns
// its id, or "" when there is none.
func openWorkspace(d upDeps, project string) (string, error) {
	out, err := d.Herdr("workspace", "list")
	if err != nil {
		return "", fmt.Errorf("herdr workspace list: %w", err)
	}
	var v struct {
		Result struct {
			Workspaces []struct {
				Label       string `json:"label"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("herdr workspace list: %w", err)
	}
	for _, w := range v.Result.Workspaces {
		if w.Label == project {
			if _, err := d.Herdr("workspace", "focus", w.WorkspaceID); err != nil {
				return "", fmt.Errorf("herdr workspace focus: %w", err)
			}
			return w.WorkspaceID, nil
		}
	}
	return "", nil
}

// layout opens the workspace the way herdr-plus does: the first tab reuses the
// focused root tab, the others are created behind it, and each tab's command
// runs once every pane exists.
func layout(d upDeps, project string, tpl upTemplate) error {
	dir := os.ExpandEnv(strings.Replace(tpl.WorkingDir, "~", d.Env("HOME"), 1))
	var created struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
			Tab struct {
				TabID string `json:"tab_id"`
			} `json:"tab"`
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	out, err := d.Herdr("workspace", "create", "--cwd", dir, "--label", project, "--focus")
	if err != nil {
		return fmt.Errorf("herdr workspace create: %w", err)
	}
	if err := json.Unmarshal(out, &created); err != nil || created.Result.Workspace.WorkspaceID == "" {
		return fmt.Errorf("herdr workspace create: unexpected reply %s", strings.TrimSpace(string(out)))
	}
	ws, panes := created.Result.Workspace.WorkspaceID, []string{created.Result.RootPane.PaneID}
	if _, err := d.Herdr("tab", "rename", created.Result.Tab.TabID, tpl.Tabs[0].Name); err != nil {
		return fmt.Errorf("herdr tab rename: %w", err)
	}
	for _, t := range tpl.Tabs[1:] {
		out, err := d.Herdr("tab", "create", "--workspace", ws, "--label", t.Name, "--no-focus")
		if err != nil {
			return fmt.Errorf("herdr tab create %s: %w", t.Name, err)
		}
		var tab struct {
			Result struct {
				RootPane struct {
					PaneID string `json:"pane_id"`
				} `json:"root_pane"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &tab); err != nil || tab.Result.RootPane.PaneID == "" {
			return fmt.Errorf("herdr tab create %s: unexpected reply %s", t.Name, strings.TrimSpace(string(out)))
		}
		panes = append(panes, tab.Result.RootPane.PaneID)
	}
	for i, t := range tpl.Tabs {
		if strings.TrimSpace(t.Command) == "" {
			continue
		}
		if _, err := d.Herdr("pane", "run", panes[i], t.Command); err != nil {
			return fmt.Errorf("herdr pane run (%s): %w", t.Name, err)
		}
	}
	return nil
}

// templatePath is where bootstrap writes the project's herdr-plus template
// (scripts/bootstrap projects_dir).
func templatePath(d upDeps, project string) string {
	dir := d.Env("HERDR_PLUS_PROJECTS_DIR")
	if dir == "" {
		if out, err := d.Herdr("plugin", "config-dir", "cloudmanic.herdr-plus"); err == nil && strings.TrimSpace(string(out)) != "" {
			dir = filepath.Join(strings.TrimSpace(string(out)), "projects")
		}
	}
	if dir == "" {
		cfg := d.Env("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(d.Env("HOME"), ".config")
		}
		dir = filepath.Join(cfg, "herdr-plus", "projects")
	}
	return filepath.Join(dir, project+".toml")
}

// repoDir is the agent-bus-monitor checkout that holds scripts/bootstrap:
// AGENTBUS_REPO, else the one setup.sh stamped in the binary, else this
// binary's directory (the repository's own build).
func repoDir(env func(string) string) (string, error) {
	candidates := []string{env("AGENTBUS_REPO"), buildinfo.Repo}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "scripts", "bootstrap")); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("cannot find the agent-bus-monitor checkout (scripts/bootstrap): set AGENTBUS_REPO or reinstall with ./setup.sh install")
}

// realUpDeps runs the real herdr and the repository's bootstrap.
func realUpDeps(stdout, stderr io.Writer) upDeps {
	return upDeps{
		Herdr: func(args ...string) ([]byte, error) {
			bin := os.Getenv("HERDR_BIN")
			if bin == "" {
				bin = "herdr"
			}
			cmd := exec.Command(bin, args...)
			cmd.Stderr = stderr
			return cmd.Output()
		},
		Bootstrap: func(args []string) error {
			repo, err := repoDir(os.Getenv)
			if err != nil {
				return err
			}
			cmd := exec.Command(filepath.Join(repo, "scripts", "bootstrap"), args...)
			cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, os.Stdin
			return cmd.Run()
		},
		Env: os.Getenv,
	}
}
