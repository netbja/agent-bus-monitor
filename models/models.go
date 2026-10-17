// Package models keeps a table of the models each provider currently offers
// (v0.15.2, spec 2026-10-17-swaps-history-and-model-drift-design.md §10, §12).
// Every client already keeps a free local catalogue; one reader per client
// reads it. No reader calls a model or opens a credential file.
package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Tiers, from the strongest.
const (
	Frontier  = "frontier"
	Workhorse = "workhorse"
	Fast      = "fast"
	Legacy    = "legacy"
)

// ValidTier reports a known tier.
func ValidTier(t string) bool {
	switch t {
	case Frontier, Workhorse, Fast, Legacy:
		return true
	}
	return false
}

// Row is one model of one provider, in its source's order.
type Row struct {
	Provider string `json:"provider"`
	Client   string `json:"client"`
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Family   string `json:"family,omitempty"` // when the provider serves another maker's model (agy)
	Tier     string `json:"tier,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Alias    bool   `json:"alias,omitempty"` // a client alias that follows the latest of a family
	// Newer names a model its source says replaces this one ("upgrade",
	// "Older…", "Previous generation…"): the only evidence for "newer".
	Newer string `json:"newer,omitempty"`
	Note  string `json:"note,omitempty"` // the source's own description
}

// Provider is one provider's part of the table: the outcome of the last
// refresh, and the rows of the last complete, successful read.
type Provider struct {
	OK     bool   `json:"ok"`
	At     int64  `json:"at"`              // Unix ms of the last attempt
	ReadAt int64  `json:"read_at"`         // Unix ms of the last successful read (its rows)
	Error  string `json:"error,omitempty"` // the last attempt's error
	Home   string `json:"home,omitempty"`  // which client home the rows came from
	Rows   []Row  `json:"rows"`
	// Partial: the source lists aliases and a few options, not every id it
	// accepts (Claude Code); an id missing from it is not "gone".
	Partial bool `json:"partial,omitempty"`
}

// Table is the machine's model table.
type Table struct {
	Providers map[string]*Provider `json:"providers"`
}

// MaxAge: a read older than this is not trusted to judge gone or newer.
const MaxAge = 7 * 24 * time.Hour

// Fresh is the provider's rows when its last successful read is recent.
func (t *Table) Fresh(provider string, now time.Time) (*Provider, bool) {
	p, ok := t.Providers[provider]
	if !ok || p.ReadAt == 0 || now.Sub(time.UnixMilli(p.ReadAt)) > MaxAge {
		return p, false
	}
	return p, true
}

// CachePath is ~/.cache/agentbus/models.json.
func CachePath() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "agentbus", "models.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "agentbus", "models.json")
}

// Load reads the table; a missing file is an empty table.
func Load(path string) (*Table, error) {
	t := &Table{Providers: map[string]*Provider{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(b, t); err != nil {
		return &Table{Providers: map[string]*Provider{}}, err
	}
	if t.Providers == nil {
		t.Providers = map[string]*Provider{}
	}
	return t, nil
}

// Save writes the table atomically (0600).
func (t *Table) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Reader reads one provider's catalogue.
type Reader struct {
	Provider string
	Partial  bool
	Read     func(ctx context.Context) (rows []Row, home string, err error)
}

// Refresh runs every reader and merges the outcome into t: a provider whose
// read fails keeps its previous rows and records the error (§12.2).
func (t *Table) Refresh(ctx context.Context, readers []Reader, tiers TierMap, now time.Time) {
	for _, r := range readers {
		p := t.Providers[r.Provider]
		if p == nil {
			p = &Provider{}
			t.Providers[r.Provider] = p
		}
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		rows, home, err := r.Read(rctx)
		cancel()
		p.At = now.UnixMilli()
		if err == nil && len(rows) == 0 {
			err = errors.New("no model listed")
		}
		if err != nil {
			p.OK, p.Error = false, err.Error()
			continue
		}
		for i := range rows {
			if rows[i].Tier == "" {
				rows[i].Tier = tiers.Tier(rows[i])
			}
		}
		p.OK, p.Error, p.ReadAt, p.Home, p.Rows, p.Partial = true, "", now.UnixMilli(), home, rows, r.Partial
	}
}

// TierMap assigns a tier to ids the source does not rank: ordered rules of
// an id pattern (a regexp) and a tier; the first match wins.
type TierMap []TierRule

// TierRule is one rule.
type TierRule struct {
	Match *regexp.Regexp
	Tier  string
}

// Tier is the row's tier from its description first, then the rules.
func (m TierMap) Tier(r Row) string {
	if t := tierFromText(r.Note + " " + r.Label); t != "" {
		return t
	}
	for _, rule := range m {
		if rule.Match.MatchString(r.ID) {
			return rule.Tier
		}
	}
	return ""
}

// tierFromText reads a tier from a description's own words.
func tierFromText(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "older") || strings.Contains(l, "previous generation") || strings.Contains(l, "legacy") || strings.Contains(l, "deprecated"):
		return Legacy
	case strings.Contains(l, "frontier") || strings.Contains(l, "most capable"):
		return Frontier
	case strings.Contains(l, "workhorse") || strings.Contains(l, "balanced"):
		return Workhorse
	case strings.Contains(l, "fast") || strings.Contains(l, "affordable") || strings.Contains(l, "lightweight"):
		return Fast
	}
	return ""
}

// DefaultTiers are the built-in rules for ids no source ranks (Claude's
// families, Grok's build-fast, Gemini's sizes). models.toml rules come first.
func DefaultTiers() TierMap {
	rule := func(re, tier string) TierRule { return TierRule{regexp.MustCompile(re), tier} }
	return TierMap{
		rule(`^(fable|claude-fable-)`, Frontier),
		rule(`^(opus|claude-opus-)`, Frontier),
		rule(`^(sonnet|claude-sonnet-)`, Workhorse),
		rule(`^(haiku|claude-haiku-)`, Fast),
		rule(`-fast$|flash-low$|-mini$`, Fast),
		rule(`flash`, Workhorse),
		rule(`^grok-\d`, Frontier),
	}
}

// Resolve is the model a tier stands for at provider: the source's default
// row when it is in that tier, else the first row of that tier in source
// order (§12.1). ok is false when the table cannot say.
func (t *Table) Resolve(provider, tier string, now time.Time) (Row, bool) {
	p, fresh := t.Fresh(provider, now)
	if !fresh {
		return Row{}, false
	}
	var first *Row
	for i := range p.Rows {
		r := p.Rows[i]
		if r.Tier != tier || r.Family != "" {
			continue
		}
		if r.Default {
			return r, true
		}
		if first == nil {
			first = &p.Rows[i]
		}
	}
	if first == nil {
		return Row{}, false
	}
	return *first, true
}

// Find returns the row of id at provider, resolving the client's own name
// rules: a Claude alias is a row itself; a dated or context-suffixed id
// matches its base id.
func (p *Provider) Find(id string) (Row, bool) {
	base := BaseID(id)
	for _, r := range p.Rows {
		if r.ID == id || BaseID(r.ID) == base {
			return r, true
		}
	}
	return Row{}, false
}

var datedSuffix = regexp.MustCompile(`-\d{8}$`)

// BaseID drops Claude Code's context suffix ("[1m]") and a release date.
func BaseID(id string) string {
	if i := strings.Index(id, "["); i > 0 {
		id = id[:i]
	}
	return datedSuffix.ReplaceAllString(id, "")
}
