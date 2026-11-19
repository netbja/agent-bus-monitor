package modelcheck

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
)

// v0.15.2: the table turns a matching launch into gone or newer, from a
// fresh complete read only, and resolves a tier.
func TestDoctorCatalogueVerdicts(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"openai": {OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{
			{ID: "gpt-6.1-sol", Tier: models.Workhorse},
			{ID: "gpt-6-sol", Tier: models.Legacy, Newer: "gpt-6.1-sol"},
			{ID: "gpt-6-astra", Tier: models.Frontier},
		}},
		"stale": {OK: false, ReadAt: now.Add(-10 * 24 * time.Hour).UnixMilli()},
	}}
	d := DoctorDeps{Table: tbl, Now: func() time.Time { return now }}
	for _, c := range []struct{ provider, launched, want string }{
		{"openai", "gpt-6.1-sol", ""},
		{"openai", "gpt-6-sol", "newer"},
		{"openai", "gpt-5-old", "gone"},
		{"stale", "anything", ""}, // an old catalogue judges nothing
	} {
		if v, _ := catalogueVerdict(d, c.provider, c.launched); v != c.want {
			t.Errorf("%s/%s: %q, want %q", c.provider, c.launched, v, c.want)
		}
	}
	cfg := models.Config{Families: map[string][]string{"grok": {"grok-4.7", "grok-4.6"}}}
	tbl.Providers["xai"] = &models.Provider{OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{{ID: "grok-4.7"}, {ID: "grok-4.6"}}}
	d.Config = cfg
	if v, why := catalogueVerdict(d, "xai", "grok-4.6"); v != "newer" || !strings.Contains(why, "grok-4.7") {
		t.Fatalf("family order: %q %q", v, why)
	}
}

func TestDoctorPartialCatalogueNeverGone(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"anthropic": {OK: true, ReadAt: now.UnixMilli(), Partial: true, Rows: []models.Row{{ID: "opus", Alias: true}}},
	}}
	d := DoctorDeps{Table: tbl, Now: func() time.Time { return now }}
	if v, _ := catalogueVerdict(d, "anthropic", "claude-opus-5-5"); v != "" {
		t.Fatalf("an id a partial catalogue does not list was judged %q", v)
	}
}

// Codex re-check: "newer" only names a model the fresh catalogue lists.
func TestDoctorNewerMustExist(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"openai": {OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{
			{ID: "gpt-6-sol", Tier: models.Legacy, Newer: "gpt-7-missing"},
		}},
	}}
	d := DoctorDeps{Table: tbl, Now: func() time.Time { return now }}
	if v, _ := catalogueVerdict(d, "openai", "gpt-6-sol"); v != "" {
		t.Fatalf("newer pointed at a missing model: %q", v)
	}
}

// model-detection §3.3: a listed higher version is "newer?", with its words,
// and never fails the doctor (it is a hint, not evidence); a family order
// in models.toml is the evidence and wins over it.
func TestDoctorListedNewerIsAHint(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"xai": {OK: true, ReadAt: now.UnixMilli(), Rows: []models.Row{{ID: "grok-4.7", Default: true}, {ID: "grok-4.6"}, {ID: "grok-4.7-build-fast"}}},
	}}
	d := DoctorDeps{Table: tbl, Now: func() time.Time { return now }}
	v, why := catalogueVerdict(d, "xai", "grok-4.6")
	if v != "newer?" || !strings.Contains(why, "listed, not stated as a successor") || !strings.Contains(why, "grok-4.7") {
		t.Fatalf("hint: %q %q", v, why)
	}
	if v, _ := catalogueVerdict(d, "xai", "grok-4.7"); v != "" {
		t.Fatalf("the newest listed: %q", v)
	}
	d.Config = models.Config{Families: map[string][]string{"grok": {"grok-4.6", "grok-4.7"}}} // the person says 4.6 is newest
	if v, _ := catalogueVerdict(d, "xai", "grok-4.6"); v != "" {
		t.Fatalf("a family order lost to the hint: %q", v)
	}
	tbl.Providers["anthropic"] = &models.Provider{OK: true, ReadAt: now.UnixMilli(), Partial: true, Rows: []models.Row{
		{ID: "opus", Alias: true, Newer: "claude-opus-5-5"}, {ID: "claude-opus-5-5"}}}
	if v, _ := catalogueVerdict(d, "anthropic", "opus"); v != "" {
		t.Fatalf("an alias follows its family by itself, never newer: %q", v)
	}
	if DoctorFails("newer?") || !DoctorFails("newer") || !DoctorFails("gone") {
		t.Fatal("newer? must never fail the doctor")
	}
}

// model-detection §3.2: a source older than models.source_max_age withholds
// every verdict (unknown in models check, nothing in roles doctor); an
// unknown source age (0) withholds nothing.
func TestJudgeModelOldSource(t *testing.T) {
	now := time.Now()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"moonshot":  {OK: true, ReadAt: now.UnixMilli(), SourceAt: now.Add(-12 * 24 * time.Hour).UnixMilli(), Rows: []models.Row{{ID: "kimi-code/k3"}}},
		"anthropic": {OK: true, ReadAt: now.UnixMilli(), Partial: true, Rows: []models.Row{{ID: "opus", Alias: true}}},
	}}
	cfg := models.Config{SourceMaxAge: 7 * 24 * time.Hour}
	j := judgeModel(tbl, cfg, now, "moonshot", "kimi-code/gone")
	if j.Verdict != "unknown" || !strings.Contains(j.Why, "12 days old") || !strings.Contains(j.Why, "models.source_max_age") {
		t.Fatalf("old source: %+v", j)
	}
	if v, _ := catalogueVerdict(DoctorDeps{Table: tbl, Config: cfg, Now: func() time.Time { return now }}, "moonshot", "kimi-code/gone"); v != "" {
		t.Fatalf("doctor judged an old source: %q", v)
	}
	cfg.SourceMaxAge = 14 * 24 * time.Hour
	if j := judgeModel(tbl, cfg, now, "moonshot", "kimi-code/gone"); j.Verdict != "gone" {
		t.Fatalf("a source within its max age: %+v", j)
	}
	if j := judgeModel(tbl, models.Config{SourceMaxAge: time.Hour}, now, "anthropic", "opus"); j.Verdict != "ok" {
		t.Fatalf("an unknown source age withheld: %+v", j)
	}
}
