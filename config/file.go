package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Path is the machine's config file: $XDG_CONFIG_HOME/agentbus/config.toml,
// else $HOME/.config/agentbus/config.toml, both read through getenv only (an
// injected environment never reaches the process's real home); "" with
// neither, which loads as an absent file.
func Path(getenv func(string) string) string { return inConfigDir(getenv, "config.toml") }

// LegacyPath is busmon's own file, still read below config.toml.
func LegacyPath(getenv func(string) string) string { return inConfigDir(getenv, "busmon.toml") }

func inConfigDir(getenv func(string) string, name string) string {
	if d := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "agentbus", name)
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".config", "agentbus", name)
	}
	return ""
}

// Warning is one problem with a file, never fatal: the key it names falls
// back to the next source down. It never carries a value from the file.
type Warning struct {
	File string // the file's label
	Line int    // 0 = unknown
	Key  string // "" = the whole file
	Msg  string
}

func (w Warning) String() string {
	at := w.File
	if w.Line > 0 {
		at += fmt.Sprintf(":%d", w.Line)
	}
	if w.Key != "" {
		at += ": " + w.Key
	}
	return at + ": " + w.Msg
}

// File is a loaded settings file: the registry values it sets, valid ones
// only, and its warnings.
type File struct {
	Label     string
	Path      string
	Exists    bool
	Warnings  []Warning
	values    map[string]fileValue
	refused   map[string]fileValue // a FailClosed key's line its rule refuses (value nil)
	// projects are the [project.<name>] tables: name -> key -> value, only
	// for the Project keys (quota settings §2.2).
	projects map[string]map[string]fileValue
	endpoints []Endpoint           // config.toml's [llm.<name>] tables (llm.go)
}

type fileValue struct {
	value any
	line  int
}

// Has reports whether the file sets key with a valid value.
func (f *File) Has(key string) bool {
	if f == nil {
		return false
	}
	_, ok := f.values[key]
	return ok
}

// Load reads config.toml. A missing file is an empty one; anything else wrong
// is a warning.
func Load(path, label string) *File { return load(path, label, false) }

// LoadLegacy reads busmon.toml: only the keys busmon.toml has always held.
func LoadLegacy(path, label string) *File { return load(path, label, true) }

// secretNames are the whole last names an unknown key is called out for.
var secretNames = map[string]bool{"password": true, "passwd": true, "secret": true, "token": true,
	"api_key": true, "apikey": true, "credential": true, "credentials": true, "url": true}

func load(path, label string, legacy bool) *File {
	f := &File{Label: label, Path: path, values: map[string]fileValue{}, refused: map[string]fileValue{}}
	if path == "" {
		return f
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f
	}
	f.Exists = true
	if err != nil {
		f.Warnings = append(f.Warnings, Warning{File: label, Msg: "cannot be read; ignored"})
		return f
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		w := Warning{File: label, Msg: "does not parse; ignored"}
		var pe toml.ParseError
		if errors.As(err, &pe) {
			w.Line = pe.Position.Line // toml's message can quote the file: never shown
		}
		f.Warnings = append(f.Warnings, w)
		return f
	}
	lines := keyLines(string(data))
	leaves := map[string]any{}
	flatten("", raw, leaves)
	names := make([]string, 0, len(leaves))
	for name := range leaves {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if lines[names[i]] != lines[names[j]] {
			return lines[names[i]] < lines[names[j]]
		}
		return names[i] < names[j]
	})
	if !legacy {
		f.loadEndpoints(leaves, lines, tableLines(string(data)))
	}
	for _, name := range names {
		if !legacy && strings.HasPrefix(name, "llm.") {
			continue // an endpoint, read above
		}
		if rest, ok := strings.CutPrefix(name, "project."); ok && !legacy {
			f.loadProject(rest, leaves[name], lines[name])
			continue
		}
		k, known := Lookup(name)
		if known && legacy && !k.Legacy {
			known = false
		}
		if !known {
			msg := "unknown key"
			last := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
			if secretNames[last] {
				msg += "; a secret does not belong in this file"
			}
			f.Warnings = append(f.Warnings, Warning{File: label, Line: lines[name], Key: name, Msg: msg})
			continue
		}
		v, why := k.file(leaves[name])
		if why != "" && k.FailClosed {
			f.Warnings = append(f.Warnings, Warning{File: label, Line: lines[name], Key: name, Msg: why + "; " + k.Invalid})
			f.refused[name] = fileValue{line: lines[name]}
			continue
		}
		if why != "" {
			f.Warnings = append(f.Warnings, Warning{File: label, Line: lines[name], Key: name, Msg: why + "; ignored"})
			continue
		}
		f.values[name] = fileValue{value: v, line: lines[name]}
	}
	return f
}

// flatten turns nested tables into dotted leaf keys; an array or an inline
// value is a leaf.
func flatten(prefix string, m map[string]any, out map[string]any) {
	for k, v := range m {
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			flatten(name, sub, out)
			continue
		}
		out[name] = v
	}
}

// keyLines finds the line of each `key = …` under its [table] header; good
// enough for a warning (quoted or dotted keys are read as written).
func keyLines(text string) map[string]int {
	out := map[string]int{}
	table := ""
	for i, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		switch {
		case s == "" || strings.HasPrefix(s, "#"):
		case strings.HasPrefix(s, "[["):
			table = strings.TrimSpace(strings.Trim(s, "[]"))
		case strings.HasPrefix(s, "["):
			if end := strings.Index(s, "]"); end > 0 {
				table = strings.TrimSpace(s[1:end])
			}
		default:
			eq := strings.Index(s, "=")
			if eq <= 0 {
				continue
			}
			key := strings.Trim(strings.TrimSpace(s[:eq]), `"'`)
			if table != "" {
				key = table + "." + key
			}
			out[key] = i + 1 // TOML refuses a key set twice
		}
	}
	return out
}

// loadProject reads one leaf of a [project.<name>] table, rest being
// "<name>.<key>": only a valid project name and a Project key; anything
// else is a warning, as an unknown key is.
func (f *File) loadProject(rest string, v any, line int) {
	project, key, ok := strings.Cut(rest, ".")
	k, known := Lookup(key)
	switch {
	case !ok || !validName(project):
		f.Warnings = append(f.Warnings, Warning{File: f.Label, Line: line, Key: "project." + rest, Msg: "not a project's table ([project.<name>], a lowercase name)"})
		return
	case !known || !k.Project:
		f.Warnings = append(f.Warnings, Warning{File: f.Label, Line: line, Key: "project." + rest, Msg: "has no per-project value (only the quota thresholds do)"})
		return
	}
	val, why := k.file(v)
	if why != "" {
		f.Warnings = append(f.Warnings, Warning{File: f.Label, Line: line, Key: "project." + rest, Msg: why + "; ignored"})
		return
	}
	if f.projects == nil {
		f.projects = map[string]map[string]fileValue{}
	}
	if f.projects[project] == nil {
		f.projects[project] = map[string]fileValue{}
	}
	f.projects[project][key] = fileValue{value: val, line: line}
}

// ProjectValue resolves a Project key for project (quota settings §2.2, the
// person's Q4b): config.toml's [project.<project>] table, then the project's
// service env file (envFile, as service.LoadEnvFile reads it: the shell's
// variables at up), then config.toml's machine key, then the default. The
// process environment is never read: a service's is a start-time copy, so
// a value changed or removed on disk would never apply. A value a key's rule
// refuses is skipped, never used.
func ProjectValue(f *File, envFile map[string]string, project, name string) Resolved {
	k, ok := Lookup(name)
	if !ok || !k.Project {
		panic("config: " + name + " has no per-project value")
	}
	if f != nil {
		if v, ok := f.projects[project][name]; ok {
			return Resolved{Key: k, Value: v.value, Source: f.Label + " [project." + project + "]", Line: v.line}
		}
	}
	if raw := envFile[k.Env]; raw != "" && k.Env != "" {
		if v, res := k.env(raw); res == envUse {
			return Resolved{Key: k, Value: v, Source: "env file"}
		}
	}
	if f != nil {
		if v, ok := f.values[name]; ok {
			return Resolved{Key: k, Value: v.value, Source: f.Label, Line: v.line}
		}
	}
	return Resolved{Key: k, Value: k.Default, Source: "default"}
}
