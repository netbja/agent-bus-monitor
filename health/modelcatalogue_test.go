package health

import (
	"strings"
	"testing"
	"time"
)

// model-detection §3.5: each warn cause, the info line, no warn for ok
// alone, unknown when the table or the manifest cannot be read.
func TestEvaluateModelCatalogue(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	base := ModelCatalogueInput{Known: true, Now: now}
	if out := EvaluateModelCatalogue(base); out.Status != OK || out.Detail != "every configured model is listed; no news in 24h" {
		t.Fatalf("quiet: %+v", out)
	}
	for name, c := range map[string]struct {
		in   ModelCatalogueInput
		want string
	}{
		"gone":    {ModelCatalogueInput{Findings: []CatalogueFinding{{Profile: "old", Field: "model", Model: "gpt-5-old", Verdict: "gone"}}}, "profile old: gpt-5-old gone"},
		"newer":   {ModelCatalogueInput{Findings: []CatalogueFinding{{Profile: "sol", Field: "model", Model: "gpt-6-sol", Verdict: "newer", Candidate: "gpt-6.1-sol"}}}, "profile sol: gpt-6-sol newer gpt-6.1-sol"},
		"newer?":  {ModelCatalogueInput{Findings: []CatalogueFinding{{Profile: "grok", Field: "model", Model: "grok-4.6", Verdict: "newer?", Candidate: "grok-4.7"}}}, "profile grok: grok-4.6 newer? grok-4.7"},
		"old":     {ModelCatalogueInput{OldSources: []OldSource{{Provider: "moonshot", Age: 12 * 24 * time.Hour, Hint: "open kimi once"}}}, "moonshot's catalogue is 12 days old: open kimi once"},
		"failing": {ModelCatalogueInput{FailedReads: []FailedRead{{Provider: "google", For: 3 * 24 * time.Hour, Error: "timeout"}}}, "google's read has failed for 3 days (timeout)"},
	} {
		in := c.in
		in.Known, in.Now = true, now
		out := EvaluateModelCatalogue(in)
		if out.Status != Warn || !strings.Contains(out.Detail, c.want) {
			t.Errorf("%s: %+v, want warn with %q", name, out, c.want)
		}
	}
	// A read failing for 2 days or less is no finding yet.
	if out := EvaluateModelCatalogue(ModelCatalogueInput{Known: true, Now: now, FailedReads: []FailedRead{{Provider: "google", For: 47 * time.Hour}}}); out.Status != OK {
		t.Fatalf("a young failure: %+v", out)
	}
	news := []CatalogueEvent{
		{At: now.Add(-2 * time.Hour), Provider: "xai", Kind: "added", ID: "grok-4.7"},
		{At: now.Add(-3 * time.Hour), Provider: "anthropic", Kind: "alias", ID: "opus", To: "claude-opus-5-5"},
		{At: now.Add(-4 * time.Hour), Provider: "xai", Kind: "default", ID: "grok-4.7"},
		{At: now.Add(-5 * time.Hour), Provider: "openai", Kind: "removed", ID: "gpt-5-old"}, // not news: a gone model is a finding
		{At: now.Add(-25 * time.Hour), Provider: "xai", Kind: "added", ID: "grok-4.5"},      // older than 24h
	}
	out := EvaluateModelCatalogue(ModelCatalogueInput{Known: true, Now: now, Events: news})
	if out.Status != OK || out.Detail != "new: xai grok-4.7 (2h) · anthropic opus now claude-opus-5-5 · xai default grok-4.7" {
		t.Fatalf("news: %+v", out)
	}
	if out := EvaluateModelCatalogue(ModelCatalogueInput{Now: now, ReadErr: "role manifest unavailable"}); out.Status != Unknown || out.Detail != "role manifest unavailable" {
		t.Fatalf("unknown: %+v", out)
	}
	if d, ok := ByID("model-catalogue"); !ok || d.Handler != "sentinel" || d.Interval != time.Hour {
		t.Fatalf("definition %+v", d)
	}
}

// The finding's key, sentinel's de-duplication (§3.6): (profile, field,
// model, verdict, candidate).
func TestCatalogueFindingKey(t *testing.T) {
	f := CatalogueFinding{Profile: "grok", Field: "model", Model: "grok-4.6", Verdict: "newer?", Candidate: "grok-4.7"}
	if f.Key() != "grok|model|grok-4.6|newer?|grok-4.7" {
		t.Fatal(f.Key())
	}
}
