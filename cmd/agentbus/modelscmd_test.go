package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
	"github.com/netbja/agent-bus-monitor/modelcheck"
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

const checkManifest = cmdtest.CheckManifest

// checkFixture is cmdtest.ModelsCheckFixture in a home the CLI's models
// settings read.
func checkFixture(t *testing.T, now time.Time) (cache string, manifest func() (*rolecfg.Manifest, error)) {
	t.Helper()
	dir := t.TempDir()
	isolateModelSettings(t, dir, "")
	return cmdtest.ModelsCheckFixture(t, dir, now)
}

// isolateModelSettings: the CLI's models settings come from an empty home,
// with AGENT_BUS_MODELS_SOURCE_MAX_AGE set to maxAge when not "".
func isolateModelSettings(t *testing.T, dir, maxAge string) {
	t.Helper()
	old := modelsGetenv
	t.Cleanup(func() { modelsGetenv = old })
	modelsGetenv = cmdtest.ModelsEnv(dir, maxAge)
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

var noEnv = cmdtest.NoEnv

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
	m := "schema_version = 2\n[defaults]\nprofile = \"gf\"\n[profiles.gf]\nclient = \"grok\"\nprovider = \"xai\"\ntier = \"fast\"\n[roles.coder]\ntier = \"boot\"\n"
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

// The fixture's adapter is one more reader for the CLI: the dispatcher's
// TestDispatcherReadsNoAdapter reads the same roles.toml and must not see it.
func TestModelsReadsTheFixturesAdapter(t *testing.T) {
	path := cmdtest.AdapterRoles(t)
	manifest := func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }
	if got := modelReaders(models.Env{}, manifest, io.Discard); len(got) != len(models.Readers(models.Env{}))+1 {
		t.Fatalf("the fixture's adapter is not seen by modelReaders: %d readers", len(got))
	}
}

var age = cmdtest.Age
var countingEnv = cmdtest.CountingEnv

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

// §3.9: an adapter with a models argv in roles.toml is read with the
// built-in clients: its rows appear under its profiles' provider. One that
// claims a built-in provider is refused, by name.
func TestModelsAdapterReader(t *testing.T) {
	now := time.Now()
	cache, _ := checkFixture(t, now)
	dir := t.TempDir()
	path := filepath.Join(dir, "roles.toml")
	m := checkManifest + `[adapters.deep]
executable = "./deep"
models = ["./deep-models", "--list"]
[adapters.fake]
executable = "./fake"
models = ["./fake-models"]
[profiles.ds]
client = "deep"
provider = "deepseek"
model = "deepseek-v4"
[profiles.fk]
client = "fake"
provider = "openai"
model = "gpt-6-sol"
`
	if err := os.WriteFile(path, []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := func() (*rolecfg.Manifest, error) { return rolecfg.Load(path) }
	env := noEnv()
	env.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == filepath.Join(dir, "deep-models") && len(args) == 1 && args[0] == "--list" {
			return []byte("deepseek-v4\tDeepSeek V4\n"), nil
		}
		return nil, errors.New("not installed")
	}
	var out, errw bytes.Buffer
	if code := runModels(context.Background(), []string{"--refresh", "--provider", "deepseek"}, env, cache, "", manifest, now, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "deepseek-v4") {
		t.Fatalf("the adapter's rows are missing:\n%s\n%s", out.String(), errw.String())
	}
	if !strings.Contains(errw.String(), "adapter fake: provider openai has a built-in reader") {
		t.Fatalf("a built-in provider was not refused:\n%s", errw.String())
	}
	out.Reset()
	runModels(context.Background(), []string{"check"}, env, cache, "", manifest, now, &out, &errw)
	if !strings.Contains(out.String(), "ds         deepseek") || strings.Contains(out.String(), "deepseek-v4                        unknown") {
		t.Fatalf("the adapter's profile is not judged against its rows:\n%s", out.String())
	}
}

// reviewer-codex: a tier profile on a source older than models.source_max_age
// is unknown, never ok, and that provider's untiered rows are not counted.
func TestModelsCheckTierOldSource(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cache, manifest := checkFixture(t, now)
	tbl, _ := models.Load(cache)
	tbl.Providers["openai"].SourceAt = now.Add(-12 * 24 * time.Hour).UnixMilli()
	tbl.Providers["xai"].SourceAt = now.Add(-12 * 24 * time.Hour).UnixMilli() // xai holds the fixture's untiered row
	if err := tbl.Save(cache); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	runModels(context.Background(), []string{"check", "--json"}, noEnv(), cache, "", manifest, now, &out, &errw)
	var rep modelcheck.CheckReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, r := range rep.Profiles {
		if r.Profile == "spare" && (r.Verdict != "unknown" || !strings.Contains(r.Why, "openai's catalogue is 12 days old")) {
			t.Fatalf("tier profile on an old source: %+v", r)
		}
	}
	if len(rep.Untiered) != 0 {
		t.Fatalf("an old source's untiered rows counted: %+v", rep.Untiered)
	}
}
