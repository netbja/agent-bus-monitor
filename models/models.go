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
	"strconv"
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
	// Seen is when each id was first and last listed (model-detection §3.1).
	Seen map[string]Seen `json:"seen,omitempty"`
	// SourceAt is the source's own age, from evidence only (Unix ms; 0 =
	// unknown): Codex's fetched_at, Kimi's file mtime, the read itself for a
	// live listing (grok, agy); never Claude's (§3.2).
	SourceAt int64 `json:"source_at,omitempty"`
}

// Seen is an id's first and last listing (Unix ms).
type Seen struct {
	First int64 `json:"first"`
	Last  int64 `json:"last"`
}

// Event is one change the table saw (§3.1): added, removed, default, alias.
type Event struct {
	At       int64  `json:"at"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`             // the id added, removed or now default; for alias, the alias
	From     string `json:"from,omitempty"` // the previous default, or the id the alias resolved to
	To       string `json:"to,omitempty"`   // alias: the id it resolves to now
}

// AliasAt is the id an alias was last seen resolving to.
type AliasAt struct {
	ID string `json:"id"`
	At int64  `json:"at"`
}

// MaxEvents is how many events the table keeps, the newest.
const MaxEvents = 200

// Table is the machine's model table.
type Table struct {
	Providers map[string]*Provider `json:"providers"`
	Events    []Event              `json:"events,omitempty"`
	Aliases   map[string]AliasAt   `json:"aliases,omitempty"` // a client alias -> the id it resolves to (Claude: opus)
}

// addEvent appends e, keeping the newest MaxEvents.
func (t *Table) addEvent(e Event) {
	t.Events = append(t.Events, e)
	if n := len(t.Events); n > MaxEvents {
		t.Events = append([]Event(nil), t.Events[n-MaxEvents:]...)
	}
}

// RecordAlias records that alias (of provider) resolves to id: the first
// resolution silently, a move as an alias event (§3.1, G5). It reports
// whether that was news.
func (t *Table) RecordAlias(provider, alias, id string, now time.Time) bool {
	if t.Aliases == nil {
		t.Aliases = map[string]AliasAt{}
	}
	old, had := t.Aliases[alias]
	t.Aliases[alias] = AliasAt{ID: id, At: now.UnixMilli()}
	if !had || old.ID == id {
		return false
	}
	t.addEvent(Event{At: now.UnixMilli(), Provider: provider, Kind: "alias", ID: alias, From: old.ID, To: id})
	return true
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
	// SourceAt is the source's own age after a successful Read (Unix ms, 0 =
	// unknown); nil = unknown (§3.2).
	SourceAt func(now time.Time) int64
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
		t.remember(r.Provider, p, rows, r.Partial, now)
		p.SourceAt = 0
		if r.SourceAt != nil {
			p.SourceAt = r.SourceAt(now)
		}
		p.OK, p.Error, p.ReadAt, p.Home, p.Rows, p.Partial = true, "", now.UnixMilli(), home, rows, r.Partial
	}
}

// remember updates p.Seen with a successful read's rows and writes the
// events (§3.1): added only after a first successful read (installing a
// client is no news); removed only when the source is not partial and the
// previous read is fresh too; default when the source's default moved.
func (t *Table) remember(provider string, p *Provider, rows []Row, partial bool, now time.Time) {
	at := now.UnixMilli()
	hadRead := p.ReadAt != 0
	prevFresh := hadRead && now.Sub(time.UnixMilli(p.ReadAt)) <= MaxAge
	if p.Seen == nil {
		p.Seen = map[string]Seen{}
	}
	listed := map[string]bool{}
	newDefault := ""
	for _, r := range rows {
		listed[r.ID] = true
		if r.Default && newDefault == "" {
			newDefault = r.ID
		}
		s, seen := p.Seen[r.ID]
		if !seen {
			s.First = at
			if hadRead {
				t.addEvent(Event{At: at, Provider: provider, Kind: "added", ID: r.ID})
			}
		}
		s.Last = at
		p.Seen[r.ID] = s
	}
	oldDefault := ""
	for _, r := range p.Rows {
		if r.Default && oldDefault == "" {
			oldDefault = r.ID
		}
		if !listed[r.ID] && !partial && prevFresh {
			t.addEvent(Event{At: at, Provider: provider, Kind: "removed", ID: r.ID})
		}
	}
	if hadRead && newDefault != "" && newDefault != oldDefault {
		t.addEvent(Event{At: at, Provider: provider, Kind: "default", ID: newDefault, From: oldDefault})
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
		// Another maker's model served on this provider's account (agy's
		// Claude) is as selectable as its own: same quota, same client.
		if r.Tier != tier {
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

var datedSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// BaseID drops Claude Code's context suffix ("[1m]") and a release date
// (-YYYYMMDD or -YYYY-MM-DD).
func BaseID(id string) string {
	if i := strings.Index(id, "["); i > 0 {
		id = id[:i]
	}
	return datedSuffix.ReplaceAllString(id, "")
}

// versionSplit is §3.3's split of a base id: a prefix that ends on a
// non-digit (else a lazy prefix takes a date's first digits and its last four
// become a version), a version of 1-4 digit components, a suffix that starts
// on a non-digit: an 8-digit date can never be one.
var versionSplit = regexp.MustCompile(`^(.*?\D|)(\d{1,4}(?:[.-]\d{1,4})*)(\D.*|)$`)

// VersionNewer reports whether to is a listed-newer candidate for from
// (§3.3): the same prefix and suffix once both are BaseID'd, and a higher
// version compared as a tuple of numbers. The same base is the same model.
func VersionNewer(from, to string) bool {
	a, b := BaseID(from), BaseID(to) // the same base gives equal versions: never newer
	ma, mb := versionSplit.FindStringSubmatch(a), versionSplit.FindStringSubmatch(b)
	if ma == nil || mb == nil || ma[1] != mb[1] || ma[3] != mb[3] {
		return false
	}
	va, vb := versionParts(ma[2]), versionParts(mb[2])
	for i := 0; i < max(len(va), len(vb)); i++ {
		x, y := 0, 0
		if i < len(va) {
			x = va[i]
		}
		if i < len(vb) {
			y = vb[i]
		}
		if x != y {
			return y > x
		}
	}
	return false
}

func versionParts(v string) []int {
	var out []int
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' }) {
		n, _ := strconv.Atoi(f)
		out = append(out, n)
	}
	return out
}

// ListedNewer is the highest id p lists that is VersionNewer than id
// (a hint, "newer?", never evidence: §3.3).
func (p *Provider) ListedNewer(id string) (string, bool) {
	best := ""
	for _, r := range p.Rows {
		if VersionNewer(id, r.ID) && (best == "" || VersionNewer(best, r.ID)) {
			best = r.ID
		}
	}
	return best, best != ""
}
