package config

import "time"

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
				return Resolved{Key: k, Value: v, Source: "env " + k.Env}
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
			return Resolved{Key: k, Value: v.value, Source: f.Label, Line: v.line}
		}
	}
	return Resolved{Key: k, Value: k.Default, Source: "default"}
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
