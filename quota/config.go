package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is ~/.config/agentbus/quota.toml (spec §5): built-in readers are on
// when their CLI is found; the file adds providers or overrides them. The
// log-backed ones (kimi: moonshot, Kimi Code's session logs; agy: google,
// Antigravity's CLI logs) only see a limit hit, never a percentage, and take
// no option; `kind = "off"` turns one off.
//
//	[providers.xai]
//	kind = "jsonl-last"            # json | jsonl-last | command | unlimited | claude | codex | kimi | agy | off
//	path = "~/.grok/logs/unified.jsonl"
//	match = { msg = "billing: fetched credits config" }
//	observed_at = "ts"
//	every = "1m"                   # minimum interval between reads (cached machine-wide)
//	[[providers.xai.windows]]
//	name_from = "ctx.config.currentPeriod.type"
//	names = { USAGE_PERIOD_TYPE_WEEKLY = "weekly", USAGE_PERIOD_TYPE_MONTHLY = "monthly" }
//	used_pct = "ctx.config.creditUsagePercent"
//	resets_at = "ctx.config.currentPeriod.end"
type Config struct {
	Providers map[string]ProviderConfig `toml:"providers"`
}

// ProviderConfig is one provider's reader.
type ProviderConfig struct {
	Kind       string            `toml:"kind"`
	Path       string            `toml:"path"`
	Match      map[string]string `toml:"match"`
	ObservedAt string            `toml:"observed_at"`
	Windows    []FieldWindow     `toml:"windows"`
	Argv       []string          `toml:"argv"`
	Every      string            `toml:"every"`
}

// ConfigPath is $XDG_CONFIG_HOME/agentbus/quota.toml or ~/.config/agentbus/quota.toml.
func ConfigPath() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "agentbus", "quota.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agentbus", "quota.toml")
}

// Builtins is the default configuration: Claude Code through the statusline
// file, Codex, Kimi Code's session logs, agy's CLI logs, and Grok's billing line — each when its CLI is on PATH, or (for
// the file readers) when its local source exists.
func Builtins(lookPath func(string) (string, error)) map[string]ProviderConfig {
	out := map[string]ProviderConfig{}
	if _, err := lookPath("claude"); err == nil || exists(ClaudeCachePath()) {
		out["anthropic"] = ProviderConfig{Kind: "claude"}
	}
	if _, err := lookPath("codex"); err == nil {
		out["openai"] = ProviderConfig{Kind: "codex", Every: "5m"}
	}
	home, _ := os.UserHomeDir()
	if _, err := lookPath("kimi"); err == nil || exists(filepath.Join(home, ".kimi-code", "sessions")) {
		out["moonshot"] = ProviderConfig{Kind: "kimi"}
	}
	if _, err := lookPath("agy"); err == nil || exists(filepath.Join(home, ".gemini", "antigravity-cli", "log")) {
		out["google"] = ProviderConfig{Kind: "agy"}
	}
	if _, err := lookPath("grok"); err == nil || exists(filepath.Join(home, ".grok", "logs", "unified.jsonl")) {
		out["xai"] = ProviderConfig{Kind: "jsonl-last", Path: "~/.grok/logs/unified.jsonl",
			Match: map[string]string{"msg": "billing: fetched credits config"}, ObservedAt: "ts",
			Windows: []FieldWindow{{NameFrom: "ctx.config.currentPeriod.type",
				Names:   map[string]string{"USAGE_PERIOD_TYPE_WEEKLY": "weekly", "USAGE_PERIOD_TYPE_MONTHLY": "monthly", "USAGE_PERIOD_TYPE_DAILY": "daily"},
				UsedPct: "ctx.config.creditUsagePercent", ResetsAt: "ctx.config.currentPeriod.end"}}}
	}
	return out
}

// LoadConfig merges the file over the built-ins; a provider with kind "off"
// is dropped. A missing file is not an error.
func LoadConfig(path string, lookPath func(string) (string, error)) (map[string]ProviderConfig, error) {
	cfg := Builtins(lookPath)
	var file Config
	if _, err := toml.DecodeFile(path, &file); err != nil && !os.IsNotExist(err) {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	for name, p := range file.Providers {
		if p.Kind == "off" {
			delete(cfg, name)
			continue
		}
		cfg[name] = p
	}
	return cfg, nil
}

// Readers builds the readers of a configuration.
func Readers(cfg map[string]ProviderConfig, now func() time.Time) []Reader {
	home, _ := os.UserHomeDir()
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return filepath.Join(home, p[2:])
		}
		return p
	}
	names := make([]string, 0, len(cfg))
	for n := range cfg {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Reader
	for _, name := range names {
		p := cfg[name]
		var r Reader
		switch p.Kind {
		case "claude":
			// One account per Claude config dir: the transcripts the backstop
			// reads, and the answers that void a limit hit, are that dir's only.
			claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
			if claudeHome == "" {
				claudeHome = filepath.Join(home, ".claude")
			}
			r = ClaudeReader{Cache: ClaudeCachePath(), Transcripts: filepath.Join(claudeHome, "projects"), Now: now}
		case "codex":
			codexHome := os.Getenv("CODEX_HOME")
			if codexHome == "" {
				codexHome = filepath.Join(home, ".codex")
			}
			bin, _ := exec.LookPath("codex")
			r = CodexReader{Bin: bin, Home: codexHome, Now: now}
		case "kimi":
			r = KimiReader{Root: filepath.Join(home, ".kimi-code", "sessions"), Now: now}
		case "agy":
			r = AgyReader{Dir: filepath.Join(home, ".gemini", "antigravity-cli", "log"), Now: now}
		case "json", "jsonl-last":
			r = FileReader{Name: name, Kind: p.Kind, Path: expand(p.Path), Match: p.Match, ObservedAt: p.ObservedAt, Windows: p.Windows, Now: now}
		case "command":
			r = CommandReader{Name: name, Argv: p.Argv, Now: now}
		case "unlimited":
			r = UnlimitedReader{Name: name}
		default:
			continue
		}
		if every, err := time.ParseDuration(p.Every); err == nil && every > 0 {
			r = Cached{Reader: r, Every: every, Now: now}
		}
		out = append(out, named{Reader: r, name: name})
	}
	return out
}

// named makes a reader report under its configured provider name.
type named struct {
	Reader
	name string
}

func (n named) Provider() string { return n.name }

func (n named) Read(ctx context.Context) (Reading, error) {
	r, err := n.Reader.Read(ctx)
	r.Provider = n.name
	return r, err
}

// Cached reads at most once per Every, machine-wide: the reading is kept in
// ~/.cache/agentbus/quota-<provider>.json under a lock, so several projects'
// dispatchers share one call.
type Cached struct {
	Reader
	Every time.Duration
	Now   func() time.Time
}

func (c Cached) Read(ctx context.Context) (Reading, error) {
	path := filepath.Join(CacheDir(), "quota-"+c.Reader.Provider()+".json")
	var r Reading
	err := withLock(path, func() error {
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &r) == nil &&
			c.Now().Sub(time.UnixMilli(r.CollectedAt)) < c.Every {
			return nil
		}
		var err error
		if r, err = c.Reader.Read(ctx); err != nil {
			return err
		}
		b, _ := json.Marshal(r)
		return writeAtomic(path, b)
	})
	return r, err
}

// ReadAll reads every reader; a reader error becomes an unknown reading.
func ReadAll(ctx context.Context, rs []Reader) []Reading {
	out := make([]Reading, 0, len(rs))
	for _, r := range rs {
		got, err := r.Read(ctx)
		if err != nil {
			got = Unknown(r.Provider(), "", "read failed")
		}
		if got.Provider == "" {
			got.Provider = r.Provider()
		}
		out = append(out, got.Clean())
	}
	return out
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
