package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata goldens")

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run go test -run %s -update)", name, err, t.Name())
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// checkManifest: every model-detection §3.4 case, used and unused.
const checkManifest = `schema_version = 2
[defaults]
profile = "main"
[profiles.main]
client = "claude-code"
provider = "anthropic"
model = "opus"
options = { permission = "manual" }
[profiles.pinned]
client = "claude-code"
provider = "anthropic"
model = "claude-opus-4-8"
options = { permission = "manual" }
[profiles.spare]
client = "codex"
provider = "openai"
tier = "workhorse"
[profiles.sol]
client = "codex"
provider = "openai"
model = "gpt-6-sol"
[profiles.old]
client = "codex"
provider = "openai"
model = "gpt-5-old"
[profiles.grok]
client = "grok"
provider = "xai"
model = "grok-4.6"
[profiles.kimi]
client = "kimi-cli"
provider = "moonshot"
tier = "frontier"
[profiles.gem]
client = "agy"
provider = "google"
model = "gemini-3.8-flash-high"
[roles.coder]
tier = "boot"
fallback_profiles = ["spare", "grok"]
[roles.master]
tier = "boot"
[roles.ui]
tier = "pop"
profile = "sol"
`

// checkFixture writes a fresh table (and its file young) and the manifest.
func checkFixture(t *testing.T, now time.Time) (cache string, manifest func() (*rolecfg.Manifest, error)) {
	t.Helper()
	dir := t.TempDir()
	isolateModelSettings(t, dir, "")
	cache = filepath.Join(dir, "models.json")
	at := now.UnixMilli()
	tbl := &models.Table{Providers: map[string]*models.Provider{
		"anthropic": {OK: true, At: at, ReadAt: at, Partial: true, Rows: []models.Row{{ID: "opus", Alias: true, Default: true, Tier: models.Frontier}}},
		"openai": {OK: true, At: at, ReadAt: at, Rows: []models.Row{
			{ID: "gpt-6.1-sol", Tier: models.Workhorse, Default: true},
			{ID: "gpt-6-sol", Tier: models.Legacy, Newer: "gpt-6.1-sol"},
		}},
		"xai":      {OK: true, At: at, ReadAt: at, Rows: []models.Row{{ID: "grok-4.7", Default: true, Tier: models.Frontier}, {ID: "grok-4.6"}}},
		"moonshot": {OK: true, At: at, ReadAt: at, Rows: []models.Row{{ID: "kimi-code/k3", Default: true}, {ID: "kimi-code/kimi-for-coding"}}},
		"google":   {OK: false, At: at, Error: "timeout", ReadAt: now.Add(-9 * 24 * time.Hour).UnixMilli(), Rows: []models.Row{{ID: "gemini-3.8-flash-high"}}},
	}}
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(path, []byte(checkManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return cache, func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }
}

// isolateModelSettings: the settings come from an empty home, with
// AGENT_BUS_MODELS_SOURCE_MAX_AGE set to maxAge when not "".
func isolateModelSettings(t *testing.T, dir, maxAge string) {
	t.Helper()
	old := modelsGetenv
	t.Cleanup(func() { modelsGetenv = old })
	modelsGetenv = func(k string) string {
		switch k {
		case "HOME", "XDG_CONFIG_HOME":
			return dir
		case "AGENT_BUS_MODELS_SOURCE_MAX_AGE":
			return maxAge
		}
		return ""
	}
}

// §3.2: the listing prints the source's own age; models check says unknown
// for a source older than models.source_max_age.
func TestModelsSourceAge(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cache, manifest := checkFixture(t, now)
	tbl, _ := models.Load(cache)
	tbl.Providers["xai"].SourceAt = now.Add(-12 * 24 * time.Hour).UnixMilli()
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runModels(context.Background(), []string{"--provider", "xai"}, noEnv(), cache, "", manifest, now, &out, &errw); code != 0 || !strings.Contains(out.String(), "source 12 days old") {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	out.Reset()
	runModels(context.Background(), []string{"check"}, noEnv(), cache, "", manifest, now, &out, &errw)
	if !strings.Contains(out.String(), "xai's catalogue is 12 days old (models.source_max_age 7 days)") {
		t.Fatalf("default 7d not applied:\n%s", out.String())
	}
	isolateModelSettings(t, t.TempDir(), "30d")
	out.Reset()
	runModels(context.Background(), []string{"check"}, noEnv(), cache, "", manifest, now, &out, &errw)
	if strings.Contains(out.String(), "catalogue is 12 days old") || !strings.Contains(out.String(), "newer?") {
		t.Fatalf("models.source_max_age 30d not read:\n%s", out.String())
	}
}

// noEnv: a client is never called (a test reads no real client).
func noEnv() models.Env {
	return models.Env{Getenv: func(string) string { return "" }, Home: "/nonexistent",
		Read: func(string) ([]byte, error) { return nil, errors.New("no file") },
		Run:  func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not installed") }}
}

// model-detection §3.4, §3.7: every profile and every fallback, judged.
func TestModelsCheckGolden(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cache, manifest := checkFixture(t, now)
	var out, errw bytes.Buffer
	code := runModels(context.Background(), []string{"check"}, noEnv(), cache, filepath.Join(t.TempDir(), "none.toml"), manifest, now, &out, &errw)
	if code != 3 {
		t.Fatalf("exit %d, want 3 (gone, newer, untiered): %s", code, errw.String())
	}
	checkGolden(t, "models-check.golden", out.String())
	out.Reset()
	if code := runModels(context.Background(), []string{"check", "--json"}, noEnv(), cache, filepath.Join(t.TempDir(), "none.toml"), manifest, now, &out, &errw); code != 3 {
		t.Fatalf("json exit %d", code)
	}
	for _, want := range []string{`"verdict": "newer?"`, `"candidate": "grok-4.7"`, `"verdict": "untiered"`, `"used_by"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("json lacks %s:\n%s", want, out.String())
		}
	}
}

// newer? alone (a hint) never fails models check.
func TestModelsCheckHintAloneExitsZero(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cache, _ := checkFixture(t, now)
	path := filepath.Join(t.TempDir(), "roles.toml")
	m := "schema_version = 2\n[defaults]\nprofile = \"grok\"\n[profiles.grok]\nclient = \"grok\"\nprovider = \"xai\"\nmodel = \"grok-4.6\"\n[roles.coder]\ntier = \"boot\"\n"
	if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	code := runModels(context.Background(), []string{"check"}, noEnv(), cache, filepath.Join(t.TempDir(), "none.toml"), func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }, now, &out, &errw)
	if code != 0 || !strings.Contains(out.String(), "newer?") {
		t.Fatalf("exit %d:\n%s%s", code, out.String(), errw.String())
	}
}

// A tier profile that cannot resolve fails the check by itself: its launch
// would fail (§3.7).
func TestModelsCheckUntieredAloneFails(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cache, _ := checkFixture(t, now)
	path := filepath.Join(t.TempDir(), "roles.toml")
	m := "schema_version = 2\n[defaults]\nprofile = \"kimi\"\n[profiles.kimi]\nclient = \"kimi-cli\"\nprovider = \"moonshot\"\ntier = \"frontier\"\n[roles.coder]\ntier = \"boot\"\n"
	if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	code := runModels(context.Background(), []string{"check"}, noEnv(), cache, filepath.Join(t.TempDir(), "none.toml"), func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }, now, &out, &errw)
	if code != 3 || !strings.Contains(out.String(), "untiered") {
		t.Fatalf("exit %d:\n%s%s", code, out.String(), errw.String())
	}
}

// model-detection §3.1: what moved, oldest first, filtered by --since.
func TestModelsChangesGolden(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cache, manifest := checkFixture(t, now)
	tbl, _ := models.Load(cache)
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	tbl.Events = []models.Event{
		{At: ms(10 * 24 * time.Hour), Provider: "openai", Kind: "removed", ID: "gpt-5-old"},
		{At: ms(3 * 24 * time.Hour), Provider: "xai", Kind: "added", ID: "grok-4.7"},
		{At: ms(3 * 24 * time.Hour), Provider: "xai", Kind: "default", ID: "grok-4.7", From: "grok-4.6"},
		{At: ms(2 * time.Hour), Provider: "anthropic", Kind: "alias", ID: "opus", From: "claude-opus-5", To: "claude-opus-5-5"},
	}
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runModels(context.Background(), []string{"--changes"}, noEnv(), cache, "", manifest, now, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	checkGolden(t, "models-changes.golden", out.String())
	out.Reset()
	if code := runModels(context.Background(), []string{"--changes", "--since", "7d"}, noEnv(), cache, "", manifest, now, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if strings.Contains(out.String(), "gpt-5-old") || !strings.Contains(out.String(), "grok-4.7") {
		t.Fatalf("--since 7d:\n%s", out.String())
	}
	out.Reset()
	if code := runModels(context.Background(), []string{"--changes", "--provider", "xai"}, noEnv(), cache, "", manifest, now, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if strings.Contains(out.String(), "anthropic") || strings.Contains(out.String(), "openai") || !strings.Contains(out.String(), "xai") {
		t.Fatalf("--provider xai:\n%s", out.String())
	}
	out.Reset()
	if code := runModels(context.Background(), []string{"--changes", "--since", "soon"}, noEnv(), cache, "", manifest, now, &out, &errw); code != 1 {
		t.Fatalf("a bad --since: exit %d", code)
	}
}

// age makes the table's last refresh d old (every provider's last attempt).
func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	tbl, err := models.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tbl.Providers {
		p.At = time.Now().Add(-d).UnixMilli()
	}
	if err := tbl.Save(path); err != nil {
		t.Fatal(err)
	}
}

// countingEnv lists grok-4.8 from the grok client and counts the calls.
func countingEnv(calls *int, block <-chan struct{}) models.Env {
	e := noEnv()
	e.Run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name != "grok" {
			return nil, errors.New("not installed")
		}
		*calls++
		if block != nil {
			<-block // ignores its deadline: the refresh must not wait for it
		}
		return []byte("Default model: grok-4.8\n\nAvailable models:\n  * grok-4.8 (default)\n"), nil
	}
	return e
}

// §3.5, G7: any models form refreshes a table over 24 h old, and only then.
func TestModelsAutoRefresh(t *testing.T) {
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	calls := 0
	var out, errw bytes.Buffer
	if code := runModels(context.Background(), []string{"--provider", "xai"}, countingEnv(&calls, nil), cache, "", manifest, now, &out, &errw); code != 0 || calls != 0 {
		t.Fatalf("a young table was refreshed: exit %d, calls %d", code, calls)
	}
	age(t, cache, 25*time.Hour)
	out.Reset()
	if code := runModels(context.Background(), []string{"--provider", "xai"}, countingEnv(&calls, nil), cache, "", manifest, now, &out, &errw); code != 0 || calls != 1 {
		t.Fatalf("an old table was not refreshed: exit %d, calls %d", code, calls)
	}
	if !strings.Contains(out.String(), "grok-4.8") {
		t.Fatalf("the answer is not the refreshed table:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "refresh incomplete: openai") {
		t.Fatalf("a failed reader is not named:\n%s", errw.String())
	}
}

// §3.5: a reader that hangs past the deadline gives the old rows, their age
// and "refresh incomplete", never a wait past the deadline.
func TestModelsAutoRefreshBounded(t *testing.T) {
	if autoRefreshBudget != 10*time.Second {
		t.Fatalf("the automatic refresh budget is %v, the spec says 10 s", autoRefreshBudget)
	}
	defer func(b time.Duration) { autoRefreshBudget = b }(autoRefreshBudget)
	autoRefreshBudget = 200 * time.Millisecond
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	age(t, cache, 25*time.Hour)
	block := make(chan struct{})
	defer close(block)
	calls := 0
	var out, errw bytes.Buffer
	start := time.Now()
	code := runModels(context.Background(), []string{"--provider", "xai"}, countingEnv(&calls, block), cache, "", manifest, now, &out, &errw)
	if d := time.Since(start); d > autoRefreshBudget+time.Second {
		t.Fatalf("waited %v past a %v budget", d, autoRefreshBudget)
	}
	if code != 0 || !strings.Contains(out.String(), "grok-4.6") || !strings.Contains(out.String(), "ago") {
		t.Fatalf("not the old rows with their age: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(errw.String(), "refresh incomplete: xai") {
		t.Fatalf("no refresh incomplete line:\n%s", errw.String())
	}
}

// §3.5: another process holding the lock skips the refresh; the answer comes
// from the table as it is.
func TestModelsAutoRefreshSkipsAHeldLock(t *testing.T) {
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	age(t, cache, 25*time.Hour)
	lock, err := os.OpenFile(cache+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	calls := 0
	var out, errw bytes.Buffer
	if code := runModels(context.Background(), []string{"--provider", "xai"}, countingEnv(&calls, nil), cache, "", manifest, now, &out, &errw); code != 0 || calls != 0 {
		t.Fatalf("refreshed under a held lock: exit %d, calls %d", code, calls)
	}
	if !strings.Contains(out.String(), "grok-4.6") || !strings.Contains(errw.String(), "another refresh") {
		t.Fatalf("out:\n%s\nerr:\n%s", out.String(), errw.String())
	}
}

// foureyes (step 2, P2): a budget overrun still saves what was read: the
// providers that answered keep their new rows, and the attempt time moves,
// so the next `agentbus models` does not pay the budget again.
func TestRefreshTableSavesWhatWasReadOnOverrun(t *testing.T) {
	now := time.Now()
	cache, _ := checkFixture(t, now)
	age(t, cache, 25*time.Hour)
	block := make(chan struct{})
	defer close(block)
	readers := []models.Reader{
		{Provider: "xai", Read: func(context.Context) ([]models.Row, string, error) { <-block; return nil, "", nil }},
		{Provider: "openai", Read: func(context.Context) ([]models.Row, string, error) {
			return []models.Row{{Provider: "openai", ID: "gpt-7-sol"}}, "codex", nil
		}},
	}
	failed, held, err := refreshTable(context.Background(), cache, nil, readers, now, 100*time.Millisecond)
	if err != nil || held || failed["xai"] == "" {
		t.Fatalf("failed %v held %v err %v", failed, held, err)
	}
	tbl, err := models.Load(cache)
	if err != nil {
		t.Fatal(err)
	}
	if p := tbl.Providers["openai"]; p.Rows[0].ID != "gpt-7-sol" || p.ReadAt != now.UnixMilli() {
		t.Fatalf("the answered provider was not saved: %+v", p)
	}
	if tbl.RefreshedAt() != now.UnixMilli() {
		t.Fatalf("attempt time %d, want %d", tbl.RefreshedAt(), now.UnixMilli())
	}
}

// foureyes (step 2, P3): a table dated in the future (a clock moved back) is
// refreshed, never trusted as young.
func TestModelsAutoRefreshFutureTable(t *testing.T) {
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	age(t, cache, -2*time.Hour)
	calls := 0
	var out, errw bytes.Buffer
	if code := runModels(context.Background(), []string{"--provider", "xai"}, countingEnv(&calls, nil), cache, "", manifest, now, &out, &errw); code != 0 || calls != 1 {
		t.Fatalf("a future-dated table was not refreshed: exit %d, calls %d", code, calls)
	}
}

// foureyes (step 2, P3): the usage errors.
func TestModelsUsageRefusals(t *testing.T) {
	now := time.Now()
	cache, manifest := checkFixture(t, now)
	for _, args := range [][]string{
		{"check", "--refresh"}, {"check", "--changes"}, {"check", "--provider", "xai"}, {"check", "--since", "7d"},
		{"--since", "7d"}, {"stray"},
	} {
		var out, errw bytes.Buffer
		if code := runModels(context.Background(), args, noEnv(), cache, "", manifest, now, &out, &errw); code != 2 || !strings.Contains(errw.String(), "usage:") {
			t.Errorf("%v: exit %d %q", args, code, errw.String())
		}
	}
	for _, since := range []string{"0d", "-1d", "0s", "xd"} {
		var out, errw bytes.Buffer
		if code := runModels(context.Background(), []string{"--changes", "--since", since}, noEnv(), cache, "", manifest, now, &out, &errw); code != 1 {
			t.Errorf("--since %s: exit %d", since, code)
		}
	}
}
