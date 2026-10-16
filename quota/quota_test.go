package quota

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 15, 22, 0, 0, 0, time.UTC)

func fixed() time.Time { return t0 }

func slInput(fiveH, sevenD float64, reset5, reset7 int64) []byte {
	b, _ := json.Marshal(map[string]any{"model": map[string]string{"display_name": "Opus"},
		"rate_limits": map[string]any{
			"five_hour": map[string]any{"used_percentage": fiveH, "resets_at": reset5},
			"seven_day": map[string]any{"used_percentage": sevenD, "resets_at": reset7}}})
	return b
}

func TestParseStatuslineKeepsOnlyKnownWindows(t *testing.T) {
	in := []byte(`{"session_id":"s","rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1792110570},
		"seven_day_opus":{"used_percentage":4,"resets_at":1792697370},"mystery":{"used_percentage":9,"resets_at":1},
		"seven_day":{"resets_at":1792697370},"spend_limit":{"used_percentage":50,"resets_at":1792697370,"period":"monthly"}}}`)
	ws, err := ParseStatusline(in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, w := range ws {
		got[w.Name] = w.UsedPct
	}
	if len(got) != 3 || got["5h"] != 23.5 || got["7d-opus"] != 4 || got["spend-monthly"] != 50 {
		t.Fatalf("windows %v", got) // seven_day lacks a percent, mystery is unknown
	}
	if _, err := ParseStatusline(make([]byte, StatuslineMax+1)); err == nil {
		t.Fatal("over the cap was parsed")
	}
}

func TestMergeRule(t *testing.T) {
	r := int64(1792110570)
	st, _ := MergeWindows(nil, []Window{{Name: "5h", UsedPct: 40, ResetsAt: r}, {Name: "7d", UsedPct: 10, ResetsAt: r + 100}}, t0)
	later := t0.Add(time.Minute)
	if out, ch := MergeWindows(st, []Window{{Name: "5h", UsedPct: 40, ResetsAt: r}}, later); ch || out["5h"].ObservedAt != t0.UnixMilli() {
		t.Fatal("same value: must keep the observation time")
	}
	if out, ch := MergeWindows(st, []Window{{Name: "5h", UsedPct: 90, ResetsAt: r - 1}}, later); ch || out["5h"].UsedPct != 40 {
		t.Fatal("an older reset replaced")
	}
	out, ch := MergeWindows(st, []Window{{Name: "5h", UsedPct: 12, ResetsAt: r}}, later)
	if !ch || out["5h"].UsedPct != 12 || out["5h"].ObservedAt != later.UnixMilli() {
		t.Fatal("same reset, lower percent must replace (a correction)")
	}
	if out["7d"].UsedPct != 10 {
		t.Fatal("a window absent from the reading was dropped")
	}
	if out, _ := MergeWindows(st, []Window{{Name: "5h", UsedPct: 3, ResetsAt: r + 18000}}, later); out["5h"].UsedPct != 3 {
		t.Fatal("a later reset must replace")
	}
}

func TestStatuslineFileAndClaudeReader(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "quota-claude.json")
	reset5, reset7 := t0.Add(2*time.Hour).Unix(), t0.Add(72*time.Hour).Unix()
	if err := RecordStatusline(cache, slInput(40, 54, reset5, reset7), t0); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(cache); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(cache)
	if strings.Contains(string(raw), "Opus") {
		t.Fatal("non-quota stdin leaked into the cache")
	}
	r, _ := ClaudeReader{Cache: cache, Transcripts: filepath.Join(dir, "none"), Now: fixed}.Read(context.Background())
	w, ok := Tightest(r.Usable(t0, 30*time.Minute))
	if r.Unknown || !ok || w.Name == "" || len(r.Windows) != 2 || w.UsedPct != 54 {
		t.Fatalf("reading %+v", r)
	}
}

func TestClaudeBackstop(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "p")
	os.MkdirAll(proj, 0o700)
	live := t0.Add(90 * time.Minute).Unix()
	past := t0.Add(-time.Hour).Unix()
	lines := []string{
		`{"timestamp":"2026-10-15T21:50:00Z","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":` + itoa(past) + `},"message":{"content":"secret text"}}`,
		`{"timestamp":"2026-10-15T21:55:00Z","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":` + itoa(live) + `},"message":{"content":"secret text"}}`,
	}
	f := filepath.Join(proj, "s.jsonl")
	os.WriteFile(f, []byte("\n"+strings.Join(lines, "\n")+"\n"), 0o600)
	os.Chtimes(f, t0, t0)
	r, _ := ClaudeReader{Cache: filepath.Join(dir, "none.json"), Transcripts: filepath.Join(dir, "projects"), Now: fixed}.Read(context.Background())
	if len(r.Windows) != 1 || r.Windows[0].UsedPct != 100 || r.Windows[0].ResetsAt != live || r.Source != "statusline+transcript" {
		t.Fatalf("backstop reading %+v", r)
	}
	// A fresher statusline window outranks it.
	cache := filepath.Join(dir, "c.json")
	RecordStatusline(cache, slInput(30, 10, live+300, live+9000), t0)
	r, _ = ClaudeReader{Cache: cache, Transcripts: filepath.Join(dir, "projects"), Now: fixed}.Read(context.Background())
	for _, w := range r.Windows {
		if w.Name == "5h" && w.UsedPct != 30 {
			t.Fatalf("backstop outranked a fresher reading: %+v", w)
		}
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestCodexJSONLNewestAcrossDays(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "2026", "10", "13")
	os.MkdirAll(old, 0o700)
	rec := func(ts string, pct float64) string {
		return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":` +
			itoa(int64(pct)) + `,"window_minutes":300,"resets_at":1792116530},"secondary":{"used_percent":6.0,"window_minutes":10080,"resets_at":1792703330}}}}`
	}
	f := filepath.Join(old, "rollout.jsonl")
	os.WriteFile(f, []byte(rec("2026-10-15T21:00:00Z", 30)+"\n"+rec("2026-10-15T21:10:00Z", 38)+"\n{\"truncated"), 0o600)
	os.Chtimes(f, t0, t0)
	r, ok := codexJSONL(dir, t0)
	if !ok || len(r.Windows) != 2 || r.Windows[0].Name != "5h" || r.Windows[0].UsedPct != 38 || r.Windows[1].Name != "7d" {
		t.Fatalf("reading %+v ok=%v", r, ok)
	}
}

func TestCodexRPCAnswerShapes(t *testing.T) {
	by := `{"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":40,"windowDurationMins":300,"resetsAt":1792116530},"secondary":{"usedPercent":6,"windowDurationMins":10080,"resetsAt":1792703330}}}}`
	ws, err := parseCodexRateLimits(json.RawMessage(by))
	if err != nil || len(ws) != 2 || ws[0].Name != "5h" || ws[0].UsedPct != 40 {
		t.Fatalf("by limit id: %+v %v", ws, err)
	}
	single := `{"rateLimits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1792116530},"secondary":null}}`
	if ws, err := parseCodexRateLimits(json.RawMessage(single)); err != nil || len(ws) != 1 || ws[0].UsedPct != 12 {
		t.Fatalf("single: %+v %v", ws, err)
	}
	if _, err := parseCodexRateLimits(json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty answer accepted")
	}
}

func grokReader(path string) FileReader {
	p := Builtins(func(string) (string, error) { return "", nil })["xai"]
	return FileReader{Name: "xai", Kind: p.Kind, Path: path, Match: p.Match, ObservedAt: p.ObservedAt, Windows: p.Windows, Now: fixed}
}

func TestGrokBillingLine(t *testing.T) {
	dir := t.TempDir()
	paid := `{"ts":"2026-10-15T21:30:00.000Z","msg":"billing: fetched credits config","ctx":{"config":{"creditUsagePercent":59.0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-10-20T18:15:42.139+00:00"}}}}`
	free := `{"ts":"2026-10-15T21:40:00.000Z","msg":"billing: fetched credits config","ctx":{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2026-10-20T18:15:42.139+00:00"}}}}`
	other := `{"ts":"2026-10-15T21:50:00.000Z","msg":"something else"}`
	f := filepath.Join(dir, "unified.jsonl")
	os.WriteFile(f, []byte(paid+"\n"+other+"\n"), 0o600)
	r, _ := grokReader(f).Read(context.Background())
	if r.Unknown || len(r.Windows) != 1 || r.Windows[0].Name != "weekly" || r.Windows[0].UsedPct != 59 ||
		r.ObservedAt != time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("paid: %+v", r)
	}
	os.WriteFile(f, []byte(paid+"\n"+free+"\n"), 0o600)
	if r, _ := grokReader(f).Read(context.Background()); !r.Unknown {
		t.Fatalf("free-tier shape (no percent) must be unknown: %+v", r)
	}
	quarterly := strings.Replace(paid, "USAGE_PERIOD_TYPE_WEEKLY", "USAGE_PERIOD_TYPE_QUARTERLY", 1)
	os.WriteFile(f, []byte(quarterly+"\n"), 0o600)
	if r, _ := grokReader(f).Read(context.Background()); !r.Unknown {
		t.Fatal("an unmapped period type must be unknown")
	}
	os.WriteFile(f, []byte(other+"\n"), 0o600)
	if r, _ := grokReader(f).Read(context.Background()); !r.Unknown {
		t.Fatal("no billing line must be unknown")
	}
}

func TestCommandReader(t *testing.T) {
	dir := t.TempDir()
	script := func(body string) string {
		p := filepath.Join(dir, "q"+itoa(int64(len(body))))
		os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700)
		return p
	}
	good := script(`echo '{"windows":[{"name":"5h","used_pct":0.5,"resets_at":1792116530}]}'`)
	r, _ := CommandReader{Name: "moonshot", Argv: []string{good}, Now: fixed}.Read(context.Background())
	if r.Unknown || r.Provider != "moonshot" || r.Windows[0].UsedPct != 0.5 || r.ObservedAt != t0.UnixMilli() {
		t.Fatalf("good: %+v", r)
	}
	for _, body := range []string{"echo nonsense", "exit 3", `echo '{"windows":[{"name":"5h","used_pct":40}]}'`} {
		if r, _ := (CommandReader{Name: "x", Argv: []string{script(body)}, Now: fixed}).Read(context.Background()); !r.Unknown {
			t.Fatalf("%q: want unknown, got %+v", body, r)
		}
	}
}

func TestUsableAndTightest(t *testing.T) {
	r := Reading{Provider: "p", ObservedAt: t0.Add(-10 * time.Minute).UnixMilli(), Windows: []Window{
		{Name: "5h", UsedPct: 70, ResetsAt: t0.Add(time.Hour).Unix()},
		{Name: "7d", UsedPct: 96, ResetsAt: t0.Add(48 * time.Hour).Unix()},
		{Name: "old", UsedPct: 99, ResetsAt: t0.Add(-time.Minute).Unix()},
	}}
	w, _ := Tightest(r.Usable(t0, 30*time.Minute))
	if w.Name != "7d" {
		t.Fatalf("tightest %+v (a past reset must not count)", w)
	}
	if len(r.Usable(t0, 5*time.Minute)) != 0 {
		t.Fatal("an aged reading must be unusable for policy")
	}
	if len((Reading{Unknown: true}).Usable(t0, time.Hour)) != 0 {
		t.Fatal("unknown usable")
	}
}

func TestConfigOverridesAndOff(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "quota.toml")
	os.WriteFile(cfgPath, []byte(`
[providers.xai]
kind = "off"
[providers.local]
kind = "unlimited"
[providers.moonshot]
kind = "command"
argv = ["/bin/true"]
every = "5m"
`), 0o600)
	cfg, err := LoadConfig(cfgPath, func(string) (string, error) { return "/bin/x", nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["xai"]; ok {
		t.Fatal("off did not drop the provider")
	}
	if cfg["local"].Kind != "unlimited" || cfg["anthropic"].Kind != "claude" || cfg["moonshot"].Every != "5m" {
		t.Fatalf("cfg %+v", cfg)
	}
}

// F1: a window observed long ago is not usable just because another window
// of the same reading is fresh.
func TestUsableAgesEachWindow(t *testing.T) {
	r := Reading{Provider: "anthropic", ObservedAt: t0.UnixMilli(), Windows: []Window{
		{Name: "5h", UsedPct: 10, ResetsAt: t0.Add(time.Hour).Unix(), ObservedAt: t0.UnixMilli()},
		{Name: "7d", UsedPct: 97, ResetsAt: t0.Add(72 * time.Hour).Unix(), ObservedAt: t0.Add(-5 * time.Hour).UnixMilli()},
	}}
	u := r.Usable(t0, 30*time.Minute)
	if len(u) != 1 || u[0].Name != "5h" {
		t.Fatalf("usable %+v: the aged 7d window must be dropped", u)
	}
}

// F4: a non-numeric or out-of-range window never reaches the policy.
func TestCleanDropsInvalidWindows(t *testing.T) {
	r := Reading{Provider: "openai", ObservedAt: 1, Windows: []Window{{Name: "5h", UsedPct: math.NaN(), ResetsAt: 2}, {Name: "7d", UsedPct: -3, ResetsAt: 2}}}
	if c := r.Clean(); !c.Unknown {
		t.Fatalf("all invalid: %+v", c)
	}
	r.Windows = append(r.Windows, Window{Name: "ok", UsedPct: 40, ResetsAt: 2})
	if c := r.Clean(); c.Unknown || len(c.Windows) != 1 || c.Windows[0].Name != "ok" {
		t.Fatalf("one valid: %+v", c)
	}
}

// A limit hit is void once the API has answered since, in any transcript of
// the account: a quota reset granted before the announced time.
func TestClaudeBackstopVoidedByALaterAnswer(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "projects")
	weekly := t0.Add(96 * time.Hour).Unix()
	write := func(name string, lines ...string) {
		p := filepath.Join(root, "p", name)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte("\n"+strings.Join(lines, "\n")+"\n"), 0o600)
		os.Chtimes(p, t0, t0)
	}
	hit := `{"type":"assistant","timestamp":"2026-10-15T21:40:00Z","isApiErrorMessage":true,"error":"rate_limit","message":{"model":"<synthetic>"},"quotaLimits":{"status":"rejected","rateLimitType":"seven_day","resetsAt":` + itoa(weekly) + `}}`
	read := func() Reading {
		r, _ := ClaudeReader{Cache: filepath.Join(dir, "none.json"), Transcripts: root, Now: fixed}.Read(context.Background())
		return r
	}
	// The hit alone holds the weekly window.
	write("a.jsonl", hit, `{"type":"assistant","timestamp":"2026-10-15T21:30:00Z","message":{"model":"claude-opus-5-5"}}`)
	if r := read(); len(r.Windows) != 1 || r.Windows[0].UsedPct != 100 {
		t.Fatalf("hit not read: %+v", r)
	}
	// A synthetic error after it proves nothing.
	write("b.jsonl", `{"type":"assistant","timestamp":"2026-10-15T21:45:00Z","isApiErrorMessage":true,"message":{"model":"<synthetic>"}}`)
	if r := read(); len(r.Windows) != 1 {
		t.Fatalf("a synthetic error voided the hit: %+v", r)
	}
	// A real answer in another session after it voids it.
	write("c.jsonl", `{"type":"assistant","timestamp":"2026-10-15T21:50:00Z","message":{"model":"claude-opus-5-5","content":"secret"}}`)
	r := read()
	if len(r.Windows) != 0 || !r.Unknown || r.AvailableAt != time.Date(2026, 10, 15, 21, 50, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("a later answer did not void the hit: %+v", r)
	}
	if !r.ServedSince(t0, 30*time.Minute) {
		t.Fatalf("ServedSince false at %v for %v", t0, time.UnixMilli(r.AvailableAt))
	}
	// A new hit after that answer holds again.
	write("d.jsonl", strings.Replace(hit, "21:40:00Z", "21:55:00Z", 1))
	if r := read(); len(r.Windows) != 1 {
		t.Fatalf("a newer hit was voided by an older answer: %+v", r)
	}
}
