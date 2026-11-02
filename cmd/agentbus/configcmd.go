package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/service"
)

// agentbus config (spec docs/superpowers/specs/2026-10-29-config-file-design.md):
// each setting's effective value and its source. Read-only, no project and no
// broker; --project only reads that project's env file, locally. It never
// prints a password, a URL nor any line of the env file but registry keys and
// the project's two facts.

type configRow struct {
	Key      string     `json:"key"`
	Value    string     `json:"value"`
	Source   string     `json:"source"`
	Line     int        `json:"line,omitempty"`
	ReadBy   string     `json:"read_by"`
	Flag     string     `json:"flag,omitempty"`
	Invalid  bool       `json:"invalid,omitempty"`
	Services *configSvc `json:"services,omitempty"`
}

type configSvc struct {
	Value   string `json:"value"`
	Source  string `json:"source"`
	Differs bool   `json:"differs,omitempty"`
}

type configFile struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type configProject struct {
	Name             string `json:"name"`
	EscalateTo       string `json:"escalate_to"`
	EscalateToSource string `json:"escalate_to_source"`
	Reload           string `json:"reload"`
	ReloadSource     string `json:"reload_source"`
}

// configLLM is one of config.toml's [llm.<name>] endpoints: host and port
// only, never the URL (the rule above).
type configLLM struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	Host   string `json:"host"`
	Remote bool   `json:"remote,omitempty"`
	Line   int    `json:"line"`
}

type configDoc struct {
	Rows       []configRow    `json:"rows"`
	Files      []configFile   `json:"files"`
	LLM        []configLLM    `json:"llm"`
	Connection []string       `json:"connection"`
	Warnings   []string       `json:"warnings"`
	Project    *configProject `json:"project,omitempty"`
}

func runConfig(args []string, project string, getenv func(string) string, out, errw io.Writer) int {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			fmt.Fprintln(errw, "usage: agentbus config [--project <p>] [--json]")
			return 2
		}
		asJSON = true
	}
	if project != "" && !bus.ValidName(project) {
		fmt.Fprintf(errw, "agentbus config: %q is not a valid project name\n", project)
		return 2
	}
	doc, err := buildConfigDoc(project, getenv)
	if err != nil {
		fmt.Fprintln(errw, "agentbus config:", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return 1
		}
		return 0
	}
	printConfigDoc(out, doc)
	return 0
}

func buildConfigDoc(project string, getenv func(string) string) (configDoc, error) {
	shell := config.Machine(getenv)
	cfg, legacy := shell.Files[0], shell.Files[1]

	var services *config.Resolver
	var svcEnv func(string) string
	var proj *configProject
	if project != "" {
		envFile, facts, err := projectEnv(project, getenv)
		if err != nil {
			return configDoc{}, err
		}
		proj = facts
		// The services see the env file up wrote (AGENT_BUS_* only: up never copies
		// the runaway keys), then their own config.toml, then the default. They
		// find config.toml through the env file's XDG_CONFIG_HOME.
		svcEnv = func(k string) string {
			if strings.HasPrefix(k, "AGENT_BUS_") {
				return envFile[k]
			}
			return ""
		}
		svcPath := func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return envFile[k]
			}
			return getenv(k)
		}
		services = &config.Resolver{Getenv: svcEnv, Files: []*config.File{config.Load(config.Path(svcPath), "config.toml")}}
	}

	doc := configDoc{Project: proj}
	legacyUsed := 0
	for _, k := range config.Keys {
		r := shell.Get(k.Name)
		row := configRow{Key: k.Name, Value: config.Format(r.Value), Source: r.Source, Line: r.Line, ReadBy: k.ReadBy, Flag: k.Flag, Invalid: r.Invalid}
		if r.Invalid {
			row.Value = "invalid"
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("%s: invalid; %s", r.Source, k.Invalid))
		}
		if k.Env != "" && !r.Invalid && getenv(k.Env) != "" && r.Source != "env "+k.Env {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("env %s: set but not a value its rule takes; %s applies", k.Env, r.Source))
		}
		if r.Source == legacy.Label {
			legacyUsed++
			row.Source = legacy.Label + " (legacy)"
		}
		if services != nil && servedByServices(k) {
			s := services.Get(k.Name)
			svc := &configSvc{Value: config.Format(s.Value), Source: s.Source}
			if strings.HasPrefix(s.Source, "env ") {
				svc.Source = "env file (frozen at up)"
			}
			if k.Env != "" && !s.Invalid && svcEnv(k.Env) != "" && !strings.HasPrefix(s.Source, "env ") {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("project %s env file %s: set but not a value its rule takes; %s applies", project, k.Env, s.Source))
			}
			if s.Invalid {
				svc.Value = "invalid"
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("project %s env file %s: invalid; %s", project, k.Env, k.Invalid))
			}
			svc.Differs = svc.Value != row.Value
			row.Services = svc
		}
		doc.Rows = append(doc.Rows, row)
	}

	doc.Files = []configFile{
		{Name: "config.toml", Path: cfg.Path, State: fileState(cfg)},
		{Name: "busmon.toml", Path: legacy.Path, State: legacyState(legacy, legacyUsed)},
		plainFile("quota.toml", cfg.Path),
		plainFile("models.toml", cfg.Path),
	}
	var fileWarnings []string
	for _, f := range []*config.File{cfg, legacy} {
		for _, w := range f.Warnings {
			fileWarnings = append(fileWarnings, w.String())
		}
	}
	// The services' config.toml, when it is another file than this shell's.
	if services != nil && services.Files[0].Path != cfg.Path {
		for _, w := range services.Files[0].Warnings {
			fileWarnings = append(fileWarnings, fmt.Sprintf("project %s services' %s (%s)", project, w.String(), services.Files[0].Path))
		}
	}
	doc.Warnings = append(fileWarnings, doc.Warnings...)
	doc.LLM = []configLLM{}
	for _, e := range cfg.Endpoints() {
		host := "?"
		if u, err := url.Parse(e.URL); err == nil {
			host = u.Host
		}
		doc.LLM = append(doc.LLM, configLLM{Name: e.Name, Model: e.Model, Host: host, Remote: e.Remote, Line: e.Line})
	}
	doc.Connection = connectionFacts(getenv)
	return doc, nil
}

// servedByServices: a key the project's services read (busmon's are not).
func servedByServices(k *config.Key) bool {
	return strings.Contains(k.ReadBy, "dispatch") || strings.Contains(k.ReadBy, "writer")
}

func fileState(f *config.File) string {
	switch {
	case !f.Exists:
		return "absent"
	case len(f.Warnings) == 1:
		return "1 warning"
	case len(f.Warnings) > 1:
		return fmt.Sprintf("%d warnings", len(f.Warnings))
	}
	return "ok"
}

func legacyState(f *config.File, used int) string {
	if !f.Exists {
		return "absent"
	}
	what := "keys"
	if used == 1 {
		what = "key"
	}
	return fmt.Sprintf("present (legacy: %d %s still read from it)", used, what)
}

// plainFile is a file beside config.toml that agentbus config only names.
func plainFile(name, beside string) configFile {
	if beside == "" { // no config home: neither XDG_CONFIG_HOME nor HOME is set
		return configFile{Name: name, State: "unknown (no config home)"}
	}
	path := filepath.Join(filepath.Dir(beside), name)
	state := "absent"
	if _, err := os.Stat(path); err == nil {
		state = "ok"
	}
	return configFile{Name: name, Path: path, State: state}
}

// projectEnv reads the project's env file: only the registry's env names and
// the two per-project facts are kept, never another line of it.
func projectEnv(project string, getenv func(string) string) (map[string]string, *configProject, error) {
	paths, err := service.PathsFrom(getenv)
	if err != nil {
		return nil, nil, err
	}
	path := paths.EnvFile(project)
	all, err := service.LoadEnvFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("no env file for project %s at %s: agentbus up has not set up its services on this machine", project, path)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	keep := map[string]string{}
	for _, k := range config.Keys {
		if v, ok := all[k.Env]; ok && k.Env != "" {
			keep[k.Env] = v
		}
	}
	if v, ok := all["XDG_CONFIG_HOME"]; ok {
		keep["XDG_CONFIG_HOME"] = v
	}
	facts := &configProject{Name: project, EscalateTo: "master", EscalateToSource: "default", Reload: "on", ReloadSource: "default"}
	if v := all[service.EscalateEnv]; v != "" {
		facts.EscalateTo, facts.EscalateToSource = v, "env file"
	}
	if v := all[service.ReloadEnv]; v != "" {
		facts.Reload, facts.ReloadSource = v, "env file"
	}
	return keep, facts, nil
}

// connectionFacts names which variables are set, the host and the port: never
// a password nor a URL.
func connectionFacts(getenv func(string) string) []string {
	var out []string
	if getenv("REDIS_URL") != "" {
		out = append(out, "REDIS_URL set (env)")
	} else {
		for _, x := range []struct{ name, def string }{{"REDIS_HOST", "localhost"}, {"REDIS_PORT", "6380"}} {
			if v := getenv(x.name); v != "" {
				out = append(out, x.name+" "+v+" (env)")
			} else {
				out = append(out, x.name+" "+x.def+" (default)")
			}
		}
	}
	switch {
	case getenv("REDIS_PASSWORD") != "":
		out = append(out, "REDIS_PASSWORD set (env)")
	case getenv("REDIS_URL") != "":
	default:
		if p := bus.PasswordFile(getenv); p != "" {
			if _, err := os.Stat(p); err == nil {
				out = append(out, "password: file "+p)
				break
			}
		}
		out = append(out, "password: the built-in default")
	}
	return out
}

func printConfigDoc(out io.Writer, doc configDoc) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if doc.Project != nil {
		fmt.Fprintln(tw, "key\tvalue\tsource\tservices see\t\tread by")
	} else {
		fmt.Fprintln(tw, "key\tvalue\tsource\tread by")
	}
	for _, r := range doc.Rows {
		if r.Services != nil {
			mark := ""
			if r.Services.Differs {
				mark = " *"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s%s\t%s\n", r.Key, r.Value, r.Source, r.Services.Value, r.Services.Source, mark, r.ReadBy)
			continue
		}
		if doc.Project != nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t-\t\t%s\n", r.Key, r.Value, r.Source, r.ReadBy)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Key, r.Value, r.Source, r.ReadBy)
	}
	tw.Flush()
	fmt.Fprintln(out)
	var files []string
	for _, f := range doc.Files {
		files = append(files, f.Name+" "+f.State)
	}
	fmt.Fprintln(out, "files: "+strings.Join(files, " · "))
	fmt.Fprintln(out, "connection: "+strings.Join(doc.Connection, ", "))
	llm := "none"
	if len(doc.LLM) > 0 {
		var eps []string
		for _, e := range doc.LLM {
			where := "loopback"
			if e.Remote {
				where = "remote"
			}
			eps = append(eps, fmt.Sprintf("%s %s %s (%s)", e.Name, e.Model, e.Host, where))
		}
		llm = strings.Join(eps, " · ")
	}
	fmt.Fprintln(out, "llm endpoints: "+llm)
	var flags []string
	for _, r := range doc.Rows {
		if r.Flag != "" {
			flags = append(flags, r.Key+": "+r.Flag)
		}
	}
	fmt.Fprintln(out, "flags over the env: "+strings.Join(flags, " · "))
	if p := doc.Project; p != nil {
		fmt.Fprintf(out, "project %s: escalate_to %s (%s), reload %s (%s); * = the services see another value than this shell\n",
			p.Name, p.EscalateTo, p.EscalateToSource, p.Reload, p.ReloadSource)
	}
	if len(doc.Warnings) == 0 {
		fmt.Fprintln(out, "warnings: none")
		return
	}
	fmt.Fprintln(out, "warnings:")
	for _, w := range doc.Warnings {
		fmt.Fprintln(out, "  "+w)
	}
}
