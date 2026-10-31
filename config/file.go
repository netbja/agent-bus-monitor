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
// else ~/.config/agentbus/config.toml.
func Path(getenv func(string) string) string { return inConfigDir(getenv, "config.toml") }

// LegacyPath is busmon's own file, still read below config.toml.
func LegacyPath(getenv func(string) string) string { return inConfigDir(getenv, "busmon.toml") }

func inConfigDir(getenv func(string) string, name string) string {
	if d := getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "agentbus", name)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agentbus", name)
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
	Label    string
	Path     string
	Exists   bool
	Warnings []Warning
	values   map[string]fileValue
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
	f := &File{Label: label, Path: path, values: map[string]fileValue{}}
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
	for _, name := range names {
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
