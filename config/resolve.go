package config

import (
	"fmt"
	"strings"
	"time"
)

// Resolver resolves a key from the environment, then each file in order,
// then the registry's default. Flags are the caller's, above it.
type Resolver struct {
	Getenv func(string) string
	Files  []*File // config.toml first, then busmon.toml
}

// Resolved is a key's effective value and where it came from.
type Resolved struct {
	Key     *Key
	Value   any    // of Key.Kind; the default when Invalid
	Source  string // "env <NAME>", a file's label, or "default"
	Line    int    // the file's line, when Source is a file
	Invalid bool   // the env value is refused by its rule: the caller stops or reads unknown
	// Why is a refusal's reason when it is not the env rule's: a DevOnly
	// value outside development mode, the key named.
	Why string
}

// Get resolves the key named; an unknown name panics (a programming error).
func (r Resolver) Get(name string) Resolved {
	k, ok := Lookup(name)
	if !ok {
		panic("config: no key " + name)
	}
	if k.Env != "" && r.Getenv != nil {
		if raw := r.Getenv(k.Env); raw != "" {
			switch v, res := k.env(raw); res {
			case envUse:
				return r.devChecked(Resolved{Key: k, Value: v, Source: "env " + k.Env})
			case envInvalid:
				return Resolved{Key: k, Value: k.Default, Source: "env " + k.Env, Invalid: true}
			}
		}
	}
	for _, f := range r.Files {
		if f == nil {
			continue
		}
		if v, ok := f.values[name]; ok {
			return r.devChecked(Resolved{Key: k, Value: v.value, Source: f.Label, Line: v.line})
		}
	}
	return Resolved{Key: k, Value: k.Default, Source: "default"}
}

// devChecked refuses a DevOnly value outside development mode: Invalid, the
// default as its value, Why naming the key.
func (r Resolver) devChecked(g Resolved) Resolved {
	for _, v := range g.Key.DevOnly {
		if g.Value == v && !r.Bool("dev") {
			g.Value, g.Invalid = g.Key.Default, true
			g.Why = fmt.Sprintf("%s: %s only in development mode (AGENT_BUS_DEV=1 or dev = true)", g.Key.Name, v)
		}
	}
	return g
}

// SelfRepairMode is dispatch.self_repair as the watcher reads it: on, off
// or shadow, and off when refused (a refused switch never acts).
func SelfRepairMode(r Resolver) string {
	if g := r.Get("dispatch.self_repair"); !g.Invalid {
		return g.Value.(string)
	}
	return "off"
}

// Machine is the resolver of a process whose environment is getenv: the env,
// then that environment's config.toml, then busmon.toml (its keys only).
func Machine(getenv func(string) string) Resolver {
	return Resolver{Getenv: getenv, Files: []*File{
		Load(Path(getenv), "config.toml"),
		LoadLegacy(LegacyPath(getenv), "busmon.toml"),
	}}
}

// Warnings are the problems of every file the resolver reads, in order.
func (r Resolver) Warnings() []Warning {
	var out []Warning
	for _, f := range r.Files {
		if f != nil {
			out = append(out, f.Warnings...)
		}
	}
	// A file's DevOnly value outside development mode: a warning of that
	// file, at its line (the env's refusal is said by its reader).
	for _, k := range Keys {
		if len(k.DevOnly) == 0 {
			continue
		}
		if g := r.Get(k.Name); g.Why != "" && g.Line > 0 {
			out = append(out, Warning{File: g.Source, Line: g.Line, Key: k.Name, Msg: strings.TrimPrefix(g.Why, k.Name+": ") + "; refused"})
		}
	}
	return out
}

// EnvName is a key's environment variable, for a reader that keeps its own
// raw rule; an unknown name panics (a programming error).
func EnvName(name string) string {
	k, ok := Lookup(name)
	if !ok {
		panic("config: no key " + name)
	}
	return k.Env
}

// Bool, Int, Float, Duration and String are Get's value for a key of that kind.
func (r Resolver) Bool(name string) bool              { return r.Get(name).Value.(bool) }
func (r Resolver) Int(name string) int                { return r.Get(name).Value.(int) }
func (r Resolver) Float(name string) float64          { return r.Get(name).Value.(float64) }
func (r Resolver) Duration(name string) time.Duration { return r.Get(name).Value.(time.Duration) }
func (r Resolver) String(name string) string          { return r.Get(name).Value.(string) }
