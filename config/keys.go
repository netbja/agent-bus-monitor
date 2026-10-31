// Package config is the person's settings (spec
// docs/superpowers/specs/2026-10-29-config-file-design.md): one registry of
// every tunable, the machine's config.toml, the legacy busmon.toml, and the
// resolution flag > environment > config.toml > busmon.toml > default, per key.
// Flags are the caller's: Resolver starts at the environment.
//
// keys.go is the only place a tunable's env name, file key, default and env
// rule are written. Each env rule is today's, byte for byte; only the file
// value is typed.
package config

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is a setting's Go type in the file and in Resolved.Value.
type Kind int

const (
	Bool     Kind = iota // bool
	Int                  // int
	Float                // float64
	Duration             // time.Duration
	String               // string
)

func (k Kind) String() string {
	return [...]string{"bool", "int", "float", "duration", "string"}[k]
}

func (k Kind) holds(v any) bool {
	switch v.(type) {
	case bool:
		return k == Bool
	case int:
		return k == Int
	case float64:
		return k == Float
	case time.Duration:
		return k == Duration
	case string:
		return k == String
	}
	return false
}

// envResult is what an env rule makes of a set (non-empty) value.
type envResult int

const (
	envUse     envResult = iota // the value
	envSkip                     // ignored: the next source down applies
	envInvalid                  // refused: resolution stops (busmon exits, the runaway check reads unknown)
)

// Key is one row of the registry.
type Key struct {
	Name    string // file key, "<table>.<key>"
	Env     string // its environment variable; "" = file only
	Kind    Kind
	Default any // of Kind
	ReadBy  string
	Legacy  bool // also read from busmon.toml

	env  func(raw string) (any, envResult) // today's env rule, for a non-empty value
	file func(v any) (any, string)         // a decoded file value: the setting, or why not
}

var Keys = []*Key{
	{Name: "waiting.popup_after", Kind: Duration, Default: 5 * time.Minute, ReadBy: "busmon", Legacy: true, file: fileDuration(true)},
	{Name: "waiting.snooze", Kind: Duration, Default: 15 * time.Minute, ReadBy: "busmon", Legacy: true, file: fileDuration(false)},
	{Name: "waiting.bell", Kind: Bool, Default: true, ReadBy: "busmon", Legacy: true, file: fileBool},
	{Name: "waiting.fade", Kind: Bool, Default: true, ReadBy: "busmon", Legacy: true, file: fileBool},
	{Name: "recap.auto_after", Kind: Duration, Default: 8 * time.Hour, ReadBy: "busmon", Legacy: true, file: fileDuration(true)},
	{Name: "display.theme", Env: "AGENT_BUS_BUSMON_THEME", Kind: String, Default: "auto", ReadBy: "busmon", Legacy: true,
		env: func(raw string) (any, envResult) {
			if isTheme(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && isTheme(s) {
				return s, ""
			}
			return nil, "want auto, dark or light"
		}},
	{Name: "display.limit", Env: "AGENT_BUS_BUSMON_LIMIT", Kind: Int, Default: 25, ReadBy: "busmon", Legacy: true,
		env: func(raw string) (any, envResult) {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
				return n, envUse
			}
			return nil, envSkip
		},
		file: fileInt(func(int) bool { return true }, "")},
	{Name: "identity.as", Env: "AGENT_BUS_HUMAN", Kind: String, Default: "", ReadBy: "busmon, agentbus identity, ask, plugin", Legacy: true,
		env: func(raw string) (any, envResult) {
			if validName(raw) {
				return raw, envUse
			}
			return nil, envInvalid
		},
		file: func(v any) (any, string) {
			if s, ok := v.(string); ok && (s == "" || validName(s)) {
				return s, ""
			}
			return nil, "want a name: a lower-case letter, then letters, digits, - or _"
		}},
	{Name: "updates.check", Env: "AGENT_BUS_NO_UPDATE_CHECK", Kind: Bool, Default: true, ReadBy: "busmon",
		env:  func(raw string) (any, envResult) { return raw != "1", envUse }, // 1 turns it off
		file: fileBool},
	{Name: "dispatch.context_alert", Env: "AGENT_BUS_CONTEXT_ALERT", Kind: Float, Default: 80.0, ReadBy: "dispatch, busmon, health",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.ParseFloat(raw, 64); err == nil && n >= 0 && n <= 100 {
				return n, envUse
			}
			return nil, envSkip
		},
		file: filePercent},
	{Name: "dispatch.prompt_grace", Env: "AGENT_BUS_PROMPT_GRACE", Kind: Duration, Default: 30 * time.Second, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 3600 {
				return time.Duration(n) * time.Second, envUse
			}
			return nil, envSkip
		},
		file: func(v any) (any, string) {
			if n, ok := v.(int64); ok && n >= 0 && n <= 3600 {
				return time.Duration(n) * time.Second, ""
			}
			return nil, "want whole seconds from 0 to 3600"
		}},
	{Name: "dispatch.quota", Env: "AGENT_BUS_QUOTA", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "off", envUse }, // off turns it off
		file: fileBool},
	{Name: "dispatch.quota_warn", Env: "AGENT_BUS_QUOTA_WARN", Kind: Float, Default: 85.0, ReadBy: "dispatch", env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_hold", Env: "AGENT_BUS_QUOTA_HOLD", Kind: Float, Default: 95.0, ReadBy: "dispatch", env: envPositiveFloat, file: filePositivePercent},
	{Name: "dispatch.quota_max_age", Env: "AGENT_BUS_QUOTA_MAX_AGE", Kind: Duration, Default: 30 * time.Minute, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 { // <= 0 was always the default
				return d, envUse
			}
			return nil, envSkip
		},
		file: filePositiveDuration},
	{Name: "dispatch.quota_long_hold", Env: "AGENT_BUS_QUOTA_LONG_HOLD", Kind: Duration, Default: 2 * time.Hour, ReadBy: "dispatch",
		env: func(raw string) (any, envResult) { // 0 = off
			if raw == "0" {
				return time.Duration(0), envUse
			}
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				return d, envUse
			}
			return nil, envSkip
		},
		file: fileDuration(true)},
	{Name: "dispatch.tokens", Env: "AGENT_BUS_TOKENS", Kind: Bool, Default: true, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw != "0", envUse }, // 0 turns it off
		file: fileBool},
	{Name: "dispatch.auto_clear", Env: "AGENT_BUS_AUTO_CLEAR", Kind: Bool, Default: false, ReadBy: "dispatch",
		env:  func(raw string) (any, envResult) { return raw == "1", envUse }, // only 1 turns it on
		file: fileBool},
	{Name: "bus.report_max", Env: "AGENT_BUS_REPORT_MAX", Kind: Int, Default: 500, ReadBy: "each writer (per writer)", env: envPositiveInt, file: filePositiveInt},
	{Name: "bus.report_full_max", Env: "AGENT_BUS_REPORT_FULL_MAX", Kind: Int, Default: 8000, ReadBy: "each writer (per writer)", env: envPositiveInt, file: filePositiveInt},
	{Name: "bus.verdict_max", Env: "AGENT_BUS_VERDICT_MAX", Kind: Int, Default: 10000, ReadBy: "each writer (per writer)", env: envPositiveInt, file: filePositiveInt},
	{Name: "health.runaway.silent_after", Env: "AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER", Kind: Duration, Default: 30 * time.Minute, ReadBy: "dispatch (health)", env: envRunawayDuration, file: filePositiveDuration},
	{Name: "health.runaway.context_growth", Env: "AGENTBUS_HEALTH_RUNAWAY_CONTEXT_GROWTH", Kind: Int, Default: 100000, ReadBy: "dispatch (health)", env: envRunawayInt, file: filePositiveInt},
	{Name: "health.runaway.growth_span", Env: "AGENTBUS_HEALTH_RUNAWAY_GROWTH_SPAN", Kind: Duration, Default: 10 * time.Minute, ReadBy: "dispatch (health)", env: envRunawayDuration, file: filePositiveDuration},
	{Name: "health.runaway.screen_repeats", Env: "AGENTBUS_HEALTH_RUNAWAY_SCREEN_REPEATS", Kind: Int, Default: 4, ReadBy: "dispatch (health)", env: envRunawayInt, file: filePositiveInt},
}

// Lookup is the row of a file key.
func Lookup(name string) (*Key, bool) {
	for _, k := range Keys {
		if k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// validName is bus.ValidName's rule, repeated because bus reads its caps from
// this package; TestValidNameIsTheBusRule keeps the two equal.
var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString

func isTheme(s string) bool { return s == "auto" || s == "dark" || s == "light" }

// envPositiveFloat: today a value <= 0 (or none that parses) is the
// dispatcher's default, never a setting.
func envPositiveFloat(raw string) (any, envResult) {
	if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 {
		return f, envUse
	}
	return nil, envSkip
}

func envPositiveInt(raw string) (any, envResult) {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n, envUse
	}
	return nil, envSkip
}

// The runaway check reads unknown on an invalid value: it never falls through.
func envRunawayDuration(raw string) (any, envResult) {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d, envUse
	}
	return nil, envInvalid
}

func envRunawayInt(raw string) (any, envResult) {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n, envUse
	}
	return nil, envInvalid
}

func fileBool(v any) (any, string) {
	if b, ok := v.(bool); ok {
		return b, ""
	}
	return nil, "want true or false"
}

func filePercent(v any) (any, string) {
	var f float64
	switch n := v.(type) {
	case int64:
		f = float64(n)
	case float64:
		f = n
	default:
		return nil, "want a number from 0 to 100"
	}
	if f < 0 || f > 100 {
		return nil, "want a number from 0 to 100"
	}
	return f, ""
}

func filePositivePercent(v any) (any, string) {
	if f, why := filePercent(v); why == "" && f.(float64) > 0 {
		return f, ""
	}
	return nil, "want a number above 0, up to 100"
}

func fileInt(ok func(int) bool, want string) func(any) (any, string) {
	return func(v any) (any, string) {
		if n, isInt := v.(int64); isInt && n == int64(int(n)) && ok(int(n)) {
			return int(n), ""
		}
		if want == "" {
			return nil, "want a whole number"
		}
		return nil, want
	}
}

var filePositiveInt = fileInt(func(n int) bool { return n > 0 }, "want a whole number above 0")

// fileDuration is a Go duration string ("90s", "5m"); zeroOK lets "0" mean off
// (or immediately), as busmon.toml always has.
func fileDuration(zeroOK bool) func(any) (any, string) {
	return func(v any) (any, string) {
		want := "want a duration above 0, such as \"30m\""
		if zeroOK {
			want = "want a duration, such as \"30m\", or \"0\""
		}
		s, ok := v.(string)
		if !ok {
			return nil, want
		}
		if s == "0" {
			if zeroOK {
				return time.Duration(0), ""
			}
			return nil, want
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 || (d == 0 && !zeroOK) {
			return nil, want
		}
		return d, ""
	}
}

var filePositiveDuration = fileDuration(false)
