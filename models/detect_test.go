package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// model-detection step 1 (spec docs/superpowers/specs/2026-11-08-model-detection-design.md
// §3.1-§3.3): the table remembers what it saw, the source's own age, and
// the listed-newer rule.

var detNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// listing is a reader that lists ids (the first is the default), or fails.
func listing(provider string, partial bool, ids *[]string, fail *error) Reader {
	return Reader{Provider: provider, Partial: partial, Read: func(context.Context) ([]Row, string, error) {
		if *fail != nil {
			return nil, "", *fail
		}
		var rows []Row
		for i, id := range *ids {
			rows = append(rows, Row{Provider: provider, ID: id, Default: i == 0})
		}
		return rows, "h", nil
	}}
}

func kinds(evs []Event) string {
	var out []string
	for _, e := range evs {
		s := e.Kind + " " + e.Provider + " " + e.ID
		if e.From != "" {
			s += " from " + e.From
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

// §3.1: the first read fills Seen silently; a later one writes added, and
// removed only when both reads are fresh and the source is not partial; a
// failed read changes neither Seen nor Events; a new default is an event.
func TestRefreshRemembers(t *testing.T) {
	ids := []string{"grok-4.6", "grok-4.5"}
	var fail error
	tbl := &Table{Providers: map[string]*Provider{}}
	r := []Reader{listing("xai", false, &ids, &fail)}
	tbl.Refresh(context.Background(), r, nil, detNow)
	p := tbl.Providers["xai"]
	if len(tbl.Events) != 0 || p.Seen["grok-4.6"].First != detNow.UnixMilli() || p.Seen["grok-4.5"].Last != detNow.UnixMilli() {
		t.Fatalf("first read: events %q, seen %+v", kinds(tbl.Events), p.Seen)
	}
	later := detNow.Add(48 * time.Hour)
	ids = []string{"grok-4.7", "grok-4.6"}
	tbl.Refresh(context.Background(), r, nil, later)
	if got := kinds(tbl.Events); got != "added xai grok-4.7; removed xai grok-4.5; default xai grok-4.7 from grok-4.6" {
		t.Fatalf("second read: %q", got)
	}
	if s := p.Seen["grok-4.6"]; s.First != detNow.UnixMilli() || s.Last != later.UnixMilli() {
		t.Fatalf("seen %+v", s)
	}
	if s := p.Seen["grok-4.5"]; s.Last != detNow.UnixMilli() {
		t.Fatalf("a removed id's last listing moved: %+v", s)
	}
	n := len(tbl.Events)
	fail = errors.New("grok: not signed in")
	tbl.Refresh(context.Background(), r, nil, later.Add(time.Hour))
	if len(tbl.Events) != n || p.Seen["grok-4.7"].Last != later.UnixMilli() {
		t.Fatal("a failed read changed Seen or Events")
	}
	// A listing back after a stale gap: added again never, removed not judged.
	fail = nil
	ids = []string{"grok-4.7"}
	tbl.Refresh(context.Background(), r, nil, later.Add(30*24*time.Hour))
	if len(tbl.Events) != n {
		t.Fatalf("a read after a stale one judged removals: %q", kinds(tbl.Events[n:]))
	}
}

// A partial source (Claude) never writes removed.
func TestPartialNeverRemoves(t *testing.T) {
	ids := []string{"opus", "sonnet", "claude-fable-5-1[1m]"}
	var fail error
	tbl := &Table{Providers: map[string]*Provider{}}
	r := []Reader{listing("anthropic", true, &ids, &fail)}
	tbl.Refresh(context.Background(), r, nil, detNow)
	ids = []string{"opus", "sonnet"}
	tbl.Refresh(context.Background(), r, nil, detNow.Add(time.Hour))
	if strings.Contains(kinds(tbl.Events), "removed") {
		t.Fatalf("%q", kinds(tbl.Events))
	}
}

// Events keep the newest MaxEvents.
func TestEventsCapped(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{}}
	for i := 0; i < MaxEvents+30; i++ {
		tbl.addEvent(Event{At: int64(i), Provider: "p", Kind: "added", ID: fmt.Sprint(i)})
	}
	if len(tbl.Events) != MaxEvents || tbl.Events[0].ID != "30" || tbl.Events[MaxEvents-1].ID != fmt.Sprint(MaxEvents+29) {
		t.Fatalf("%d events, first %s", len(tbl.Events), tbl.Events[0].ID)
	}
}

// §3.1 alias: the first id an alias resolves to is recorded silently; a
// move writes one event with from; the same id again writes nothing.
func TestAliasMoves(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{}}
	if tbl.RecordAlias("anthropic", "opus", "claude-opus-5", detNow) {
		t.Fatal("the first resolution is news")
	}
	if !tbl.RecordAlias("anthropic", "opus", "claude-opus-5-5", detNow.Add(time.Hour)) {
		t.Fatal("a move is not news")
	}
	if tbl.RecordAlias("anthropic", "opus", "claude-opus-5-5", detNow.Add(2*time.Hour)) {
		t.Fatal("the same id twice is news")
	}
	if got := kinds(tbl.Events); got != "alias anthropic opus from claude-opus-5" || tbl.Aliases["anthropic"]["opus"].ID != "claude-opus-5-5" {
		t.Fatalf("%q %+v", got, tbl.Aliases)
	}
	if e := tbl.Events[0]; e.To != "claude-opus-5-5" {
		t.Fatalf("event %+v", e)
	}
}

// An older table without the new fields loads and saves.
func TestOldTableLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	old := `{"providers":{"xai":{"ok":true,"at":1,"read_at":1,"rows":[{"provider":"xai","client":"grok","id":"grok-4.6"}]}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	tbl, err := Load(path)
	if err != nil || tbl.Providers["xai"].Rows[0].ID != "grok-4.6" || tbl.Events != nil {
		t.Fatalf("%v %+v", err, tbl)
	}
	if err := tbl.Save(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var back map[string]any
	if json.Unmarshal(b, &back) != nil {
		t.Fatal("not JSON")
	}
}

// §3.2 the source's own age: Codex's fetched_at, Kimi's file mtime through
// Env.Stat, grok and agy the read itself (they ask their server live),
// Claude unknown.
func TestSourceAt(t *testing.T) {
	fetched := "2026-11-07T22:26:01.5Z"
	e := fakeEnv(map[string]string{
		"/h/.codex/models_cache.json": `{"fetched_at":"` + fetched + `","models":[{"slug":"gpt-6-astra","display_name":"x","visibility":"list"}]}`,
		"/h/.kimi-code/config.toml":   "default_model = \"k3\"\n[models.k3]\nmodel = \"k3\"\n",
		"/h/.claude.json":             `{}`,
	}, map[string]string{"grok": grokSample, "agy": agySample})
	kimiAt := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	e.Stat = func(path string) (time.Time, error) {
		if path == "/h/.kimi-code/config.toml" {
			return kimiAt, nil
		}
		return time.Time{}, os.ErrNotExist
	}
	tbl := &Table{Providers: map[string]*Provider{}}
	tbl.Refresh(context.Background(), Readers(e), DefaultTiers(), detNow)
	want := map[string]int64{"openai": time.Date(2026, 11, 7, 22, 26, 1, 500000000, time.UTC).UnixMilli(),
		"moonshot": kimiAt.UnixMilli(), "xai": detNow.UnixMilli(), "google": detNow.UnixMilli(), "anthropic": 0}
	for prov, at := range want {
		p := tbl.Providers[prov]
		if p == nil || !p.OK {
			t.Errorf("%s not read: %+v", prov, p)
			continue
		}
		if p.SourceAt != at {
			t.Errorf("%s source_at %d, want %d", prov, p.SourceAt, at)
		}
	}
	// No Stat (an older caller): Kimi's age is unknown, never the read time.
	e.Stat = nil
	tbl = &Table{Providers: map[string]*Provider{}}
	tbl.Refresh(context.Background(), Readers(e), DefaultTiers(), detNow)
	if at := tbl.Providers["moonshot"].SourceAt; at != 0 {
		t.Errorf("kimi without Stat: %d", at)
	}
}

// §3.3 listed-newer, every example of the spec, both ways where it says so.
func TestListedNewer(t *testing.T) {
	for _, c := range []struct {
		from, to string
		newer    bool
	}{
		{"grok-4.6", "grok-4.7", true},
		{"gpt-6-sol", "gpt-6.1-sol", true},
		{"claude-opus-5", "claude-opus-5-5", true},
		{"model-4-20251001", "model-5-20251001", true},
		{"gemini-3.8-flash-high", "gemini-4-flash-high", true},
		{"grok-4.6", "grok-4.7-build-fast", false},
		{"kimi-code/k3", "kimi-code/k3.1-pro", false},
		{"claude-haiku-4-5", "claude-haiku-4-5-20251001", false},
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5", false},
		{"claude-haiku-4-5-20250101", "claude-haiku-4-5-20251001", false},
		{"m-2025-01-01", "m-2025-06-01", false},
		{"claude-fable-5-1[1m]", "claude-fable-5-1", false},
		{"gpt-4o", "gpt-4.1", false},
		{"gpt-4.1", "gpt-4o", false},
		{"gemini-3.8-flash-low", "gemini-3.8-flash-high", false},
		{"grok-4.6", "gemini-4.7", false},               // another stem
		{"m-20250101-pro", "m-20250601-pro", false},     // a date inside an id is never a version
		{"claude-haiku-4-5", "claude-haiku-4-5", false}, // the same id
	} {
		if got := VersionNewer(c.from, c.to); got != c.newer {
			t.Errorf("VersionNewer(%q, %q) = %v, want %v", c.from, c.to, got, c.newer)
		}
		if c.newer && VersionNewer(c.to, c.from) {
			t.Errorf("VersionNewer(%q, %q): older counted newer", c.to, c.from)
		}
	}
	if BaseID("m-2025-06-01") != "m" || BaseID("claude-haiku-4-5-20251001") != "claude-haiku-4-5" {
		t.Errorf("BaseID: %q %q", BaseID("m-2025-06-01"), BaseID("claude-haiku-4-5-20251001"))
	}
	p := &Provider{Rows: []Row{{ID: "grok-4.6"}, {ID: "grok-4.7"}, {ID: "grok-4.8-mini"}, {ID: "grok-4.6.1"}}}
	if got, ok := p.ListedNewer("grok-4.6"); !ok || got != "grok-4.7" {
		t.Errorf("ListedNewer = %q %v, want the highest listed: grok-4.7", got, ok)
	}
	p2 := &Provider{Rows: []Row{{ID: "grok-4.6.1"}, {ID: "grok-4.7"}}} // low then high: the highest wins, not the first
	if got, _ := p2.ListedNewer("grok-4.6"); got != "grok-4.7" {
		t.Errorf("ListedNewer low-then-high = %q", got)
	}
	if got, ok := p.ListedNewer("grok-4.7"); ok {
		t.Errorf("ListedNewer(grok-4.7) = %q", got)
	}
}

// A reader that states no age after one that did: unknown, never the old one.
func TestSourceAtUnknownReplacesAnOldAge(t *testing.T) {
	ids := []string{"a-1"}
	var fail error
	r := listing("p", false, &ids, &fail)
	r.SourceAt = func(now time.Time) int64 { return now.UnixMilli() }
	tbl := &Table{Providers: map[string]*Provider{}}
	tbl.Refresh(context.Background(), []Reader{r}, nil, detNow)
	r.SourceAt = nil
	tbl.Refresh(context.Background(), []Reader{r}, nil, detNow.Add(time.Hour))
	if at := tbl.Providers["p"].SourceAt; at != 0 {
		t.Fatalf("source_at %d", at)
	}
}

// reviewer-codex: an alias is its provider's own: the same name at another
// provider is another alias, never a move of the first.
func TestAliasPerProvider(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{}}
	tbl.RecordAlias("anthropic", "opus", "claude-opus-5", detNow)
	if tbl.RecordAlias("moonshot", "opus", "kimi-opus", detNow.Add(time.Hour)) {
		t.Fatal("another provider's alias was a move")
	}
	if tbl.RecordAlias("anthropic", "opus", "claude-opus-5", detNow.Add(2*time.Hour)) || len(tbl.Events) != 0 {
		t.Fatalf("anthropic's opus compared with moonshot's: %q", kinds(tbl.Events))
	}
	if tbl.Aliases["anthropic"]["opus"].ID != "claude-opus-5" || tbl.Aliases["moonshot"]["opus"].ID != "kimi-opus" {
		t.Fatalf("%+v", tbl.Aliases)
	}
}

// foureyes: a table from before Seen existed (reads, no Seen) fills Seen
// silently on its first refresh: no added, no removed for what it lists.
func TestUpgradedTableNoFlood(t *testing.T) {
	tbl := &Table{Providers: map[string]*Provider{"xai": {OK: true, ReadAt: detNow.Add(-time.Hour).UnixMilli(),
		Rows: []Row{{Provider: "xai", ID: "a", Default: true}, {Provider: "xai", ID: "b"}, {Provider: "xai", ID: "c"}}}}}
	ids := []string{"a", "b", "c"}
	var fail error
	tbl.Refresh(context.Background(), []Reader{listing("xai", false, &ids, &fail)}, nil, detNow)
	if len(tbl.Events) != 0 || len(tbl.Providers["xai"].Seen) != 3 {
		t.Fatalf("upgrade: events %q, seen %d", kinds(tbl.Events), len(tbl.Providers["xai"].Seen))
	}
	ids = []string{"a", "b", "c", "d"}
	tbl.Refresh(context.Background(), []Reader{listing("xai", false, &ids, &fail)}, nil, detNow.Add(time.Hour))
	if got := kinds(tbl.Events); got != "added xai d" {
		t.Fatalf("after the upgrade: %q", got)
	}
}
