package models

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Env is the readers' outside world; tests replace it.
type Env struct {
	Getenv func(string) string
	Home   string
	Read   func(path string) ([]byte, error)
	// Run runs a client's own listing command (fixed argv) and returns stdout.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// RealEnv is the machine's.
func RealEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Getenv: os.Getenv, Home: home, Read: os.ReadFile,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if _, err := exec.LookPath(name); err != nil {
				return nil, err
			}
			c := exec.CommandContext(ctx, name, args...)
			c.Stdin = nil
			return c.Output()
		}}
}

// Readers are the five built-in readers (§10.1). None opens a credential
// file: they read only the catalogue files named here, or the client's own
// listing command.
func Readers(e Env) []Reader {
	return []Reader{
		{Provider: "openai", Read: func(ctx context.Context) ([]Row, string, error) { return readCodex(e) }},
		{Provider: "anthropic", Partial: true, Read: func(ctx context.Context) ([]Row, string, error) { return readClaude(e) }},
		{Provider: "xai", Read: func(ctx context.Context) ([]Row, string, error) { return readGrok(ctx, e) }},
		{Provider: "moonshot", Read: func(ctx context.Context) ([]Row, string, error) { return readKimi(e) }},
		{Provider: "google", Read: func(ctx context.Context) ([]Row, string, error) { return readAgy(ctx, e) }},
	}
}

// --- Codex: $CODEX_HOME/models_cache.json, refreshed by Codex itself.

type codexCache struct {
	Models []struct {
		Slug        string          `json:"slug"`
		DisplayName string          `json:"display_name"`
		Description string          `json:"description"`
		Visibility  string          `json:"visibility"`
		Priority    int             `json:"priority"`
		Upgrade     json.RawMessage `json:"upgrade"`
	} `json:"models"`
}

func readCodex(e Env) ([]Row, string, error) {
	home := e.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(e.Home, ".codex")
	}
	b, err := e.Read(filepath.Join(home, "models_cache.json"))
	if err != nil {
		return nil, home, err
	}
	var c codexCache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, home, err
	}
	sort.SliceStable(c.Models, func(i, j int) bool { return c.Models[i].Priority < c.Models[j].Priority })
	var rows []Row
	for _, m := range c.Models {
		if m.Slug == "" || m.Visibility == "hide" {
			continue
		}
		r := Row{Provider: "openai", Client: "codex", ID: m.Slug, Label: m.DisplayName, Note: m.Description,
			Newer: upgradeTarget(m.Upgrade)}
		rows = append(rows, r)
	}
	return rows, home, nil
}

// upgradeTarget reads Codex's "upgrade": a model id, or an object naming one.
func upgradeTarget(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o map[string]any
	if json.Unmarshal(raw, &o) == nil {
		for _, k := range []string{"model", "slug", "id", "to", "value"} {
			if v, ok := o[k].(string); ok {
				return v
			}
		}
	}
	return ""
}

// --- Claude Code: its aliases, plus the model options it caches in
// .claude.json (only those keys: the file also holds account fields, never
// read here).

var claudeAliases = []Row{
	{ID: "fable", Label: "Fable (latest)", Alias: true},
	{ID: "opus", Label: "Opus (latest)", Alias: true, Default: true},
	{ID: "sonnet", Label: "Sonnet (latest)", Alias: true},
	{ID: "haiku", Label: "Haiku (latest)", Alias: true},
}

func readClaude(e Env) ([]Row, string, error) {
	dir := e.Getenv("CLAUDE_CONFIG_DIR")
	path := filepath.Join(e.Home, ".claude.json")
	if dir != "" {
		path = filepath.Join(dir, ".claude.json")
	}
	var rows []Row
	for _, a := range claudeAliases {
		a.Provider, a.Client = "anthropic", "claude-code"
		rows = append(rows, a)
	}
	b, err := e.Read(path)
	if err != nil {
		return rows, path, nil // the aliases alone are a complete answer
	}
	var cfg struct {
		Options []struct {
			Value       string `json:"value"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"additionalModelOptionsCache"`
		OrgDefault json.RawMessage `json:"orgModelDefaultCache"`
	}
	if json.Unmarshal(b, &cfg) == nil {
		for _, o := range cfg.Options {
			if o.Value != "" {
				rows = append(rows, Row{Provider: "anthropic", Client: "claude-code", ID: o.Value, Label: o.Label, Note: o.Description})
			}
		}
		// The organisation's default model, when set: a string, or an object
		// naming it. It becomes the default row.
		if id := upgradeTarget(cfg.OrgDefault); id != "" {
			for i := range rows {
				rows[i].Default = false
			}
			found := false
			for i := range rows {
				if rows[i].ID == id {
					rows[i].Default, found = true, true
				}
			}
			if !found {
				rows = append(rows, Row{Provider: "anthropic", Client: "claude-code", ID: id, Label: "organisation default", Default: true})
			}
		}
	}
	return rows, path, nil
}

// --- Grok: `grok models`.

func readGrok(ctx context.Context, e Env) ([]Row, string, error) {
	out, err := e.Run(ctx, "grok", "models")
	if err != nil {
		return nil, "grok models", err
	}
	var rows []Row
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "* ") && !strings.HasPrefix(line, "- ") {
			continue
		}
		f := strings.Fields(line[2:])
		if len(f) == 0 {
			continue
		}
		rows = append(rows, Row{Provider: "xai", Client: "grok", ID: f[0], Default: strings.Contains(line, "(default)")})
	}
	return rows, "grok models", nil
}

// --- Kimi: ~/.kimi-code/config.toml (models and default_model; the oauth
// tables are never read).

func readKimi(e Env) ([]Row, string, error) {
	path := filepath.Join(e.Home, ".kimi-code", "config.toml")
	if d := e.Getenv("KIMI_CODE_HOME"); d != "" {
		path = filepath.Join(d, "config.toml")
	}
	b, err := e.Read(path)
	if err != nil {
		return nil, path, err
	}
	var cfg struct {
		Default string `toml:"default_model"`
		Models  map[string]struct {
			Model       string `toml:"model"`
			DisplayName string `toml:"display_name"`
		} `toml:"models"`
	}
	meta, err := toml.Decode(string(b), &cfg)
	if err != nil {
		return nil, path, err
	}
	// The config's own declaration order (§12.1), not alphabetical.
	var ids []string
	seen := map[string]bool{}
	for _, k := range meta.Keys() {
		if len(k) >= 2 && k[0] == "models" && !seen[k[1]] {
			if _, ok := cfg.Models[k[1]]; ok {
				seen[k[1]] = true
				ids = append(ids, k[1])
			}
		}
	}
	var rows []Row
	for _, id := range ids {
		rows = append(rows, Row{Provider: "moonshot", Client: "kimi-cli", ID: id, Label: cfg.Models[id].DisplayName,
			Default: id == cfg.Default, Alias: true})
	}
	return rows, path, nil
}

// --- agy (Antigravity, Google account): `agy models`, "id<TAB>label" lines.
// It also serves other makers' models on the Google account: such rows keep
// provider google and name their family.

func readAgy(ctx context.Context, e Env) ([]Row, string, error) {
	out, err := e.Run(ctx, "agy", "models")
	if err != nil {
		return nil, "agy models", err
	}
	var rows []Row
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		id, label, ok := strings.Cut(sc.Text(), "\t")
		id = strings.TrimSpace(id)
		if !ok || id == "" {
			continue
		}
		r := Row{Provider: "google", Client: "agy", ID: id, Label: strings.TrimSpace(label)}
		switch {
		case strings.HasPrefix(id, "claude-"):
			r.Family = "anthropic"
		case strings.HasPrefix(id, "gpt-"):
			r.Family = "openai"
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, "agy models", errors.New("agy listed no model")
	}
	return rows, "agy models", nil
}
