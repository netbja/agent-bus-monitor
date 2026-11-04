package quota

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// isolate points every home and cache lookup at a temporary directory, so no
// test reads or writes the real home.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	return dir
}

func TestProviderNames(t *testing.T) {
	for _, tc := range []struct {
		r    Reader
		want string
	}{
		{ClaudeReader{}, "anthropic"},
		{CodexReader{}, "openai"},
		{KimiReader{}, "moonshot"},
		{AgyReader{}, "google"},
		{FileReader{Name: "xai"}, "xai"},
		{CommandReader{Name: "cmd"}, "cmd"},
		{UnlimitedReader{Name: "local"}, "local"},
		{named{Reader: UnlimitedReader{Name: "inner"}, name: "outer"}, "outer"},
	} {
		if got := tc.r.Provider(); got != tc.want {
			t.Errorf("%T: got %q, want %q", tc.r, got, tc.want)
		}
	}
}

func TestConfigPathAndContextDir(t *testing.T) {
	dir := isolate(t)
	if got, want := ConfigPath(), filepath.Join(dir, "config", "agentbus", "quota.toml"); got != want {
		t.Errorf("ConfigPath with XDG_CONFIG_HOME: got %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := ConfigPath(), filepath.Join(dir, ".config", "agentbus", "quota.toml"); got != want {
		t.Errorf("ConfigPath from HOME: got %q, want %q", got, want)
	}
	if got, want := ContextDir(), filepath.Join(dir, "cache", "agentbus", "context"); got != want {
		t.Errorf("ContextDir: got %q, want %q", got, want)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	if got, want := CacheDir(), filepath.Join(dir, ".cache", "agentbus"); got != want {
		t.Errorf("CacheDir from HOME: got %q, want %q", got, want)
	}
}

func TestFileProviders(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "quota.toml")
	os.WriteFile(good, []byte("[providers.xai]\nkind = \"off\"\n[providers.local]\nkind = \"unlimited\"\n"), 0o600)
	bad := filepath.Join(dir, "bad.toml")
	os.WriteFile(bad, []byte("[providers\n"), 0o600)
	for _, tc := range []struct {
		name string
		path string
		want map[string]bool
	}{
		{"names every provider, off included", good, map[string]bool{"xai": true, "local": true}},
		{"missing file", filepath.Join(dir, "none.toml"), nil},
		{"unparsable file", bad, nil},
	} {
		if got := FileProviders(tc.path); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, err := LoadConfig(bad, func(string) (string, error) { return "", errors.New("none") }); err == nil {
		t.Error("LoadConfig of an unparsable file must fail")
	}
}

func TestReadersBuildsEachKind(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir()) // no codex binary: its RPC stays off
	cfg := map[string]ProviderConfig{
		"a-claude":  {Kind: "claude"},
		"b-codex":   {Kind: "codex"},
		"c-kimi":    {Kind: "kimi"},
		"d-agy":     {Kind: "agy"},
		"e-json":    {Kind: "json", Path: "~/q.json"},
		"f-jsonl":   {Kind: "jsonl-last", Path: "/abs/q.jsonl"},
		"g-command": {Kind: "command", Argv: []string{"/bin/true"}},
		"h-local":   {Kind: "unlimited", Every: "1m"},
		"i-bogus":   {Kind: "bogus"},
		"j-badevry": {Kind: "unlimited", Every: "soon"},
	}
	rs := Readers(cfg, fixed)
	var names []string
	for _, r := range rs {
		names = append(names, r.Provider())
	}
	want := []string{"a-claude", "b-codex", "c-kimi", "d-agy", "e-json", "f-jsonl", "g-command", "h-local", "j-badevry"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("readers %v, want %v (an unknown kind is skipped)", names, want)
	}
	types := map[string]string{}
	for _, r := range rs {
		types[r.Provider()] = reflect.TypeOf(r.(named).Reader).Name()
	}
	for name, typ := range map[string]string{"a-claude": "ClaudeReader", "b-codex": "CodexReader", "c-kimi": "KimiReader",
		"d-agy": "AgyReader", "e-json": "FileReader", "g-command": "CommandReader", "h-local": "Cached", "j-badevry": "UnlimitedReader"} {
		if types[name] != typ {
			t.Errorf("%s: reader %s, want %s", name, types[name], typ)
		}
	}
	home := os.Getenv("HOME")
	for _, r := range rs {
		if fr, ok := r.(named).Reader.(FileReader); ok {
			switch r.Provider() {
			case "e-json":
				if fr.Path != filepath.Join(home, "q.json") {
					t.Errorf("~/ not expanded: %q", fr.Path)
				}
			case "f-jsonl":
				if fr.Path != "/abs/q.jsonl" {
					t.Errorf("absolute path changed: %q", fr.Path)
				}
			}
		}
	}
}

func TestReadersHonourClientHomes(t *testing.T) {
	dir := isolate(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude-alt"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex-alt"))
	rs := Readers(map[string]ProviderConfig{"anthropic": {Kind: "claude"}, "openai": {Kind: "codex"}}, fixed)
	for _, r := range rs {
		switch in := r.(named).Reader.(type) {
		case ClaudeReader:
			if in.Transcripts != filepath.Join(dir, "claude-alt", "projects") {
				t.Errorf("claude transcripts %q", in.Transcripts)
			}
		case CodexReader:
			if in.Home != filepath.Join(dir, "codex-alt") || in.Bin != "" {
				t.Errorf("codex %+v", in)
			}
		default:
			t.Errorf("unexpected reader %T", in)
		}
	}
}

type stubReader struct {
	name  string
	r     Reading
	err   error
	calls *int
}

func (s stubReader) Provider() string { return s.name }
func (s stubReader) Read(context.Context) (Reading, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.r, s.err
}

func TestReadAll(t *testing.T) {
	good := Reading{Provider: "p", Windows: []Window{{Name: "5h", UsedPct: 10, ResetsAt: 2}}}
	got := ReadAll(context.Background(), []Reader{
		stubReader{name: "p", r: good},
		stubReader{name: "q", err: errors.New("boom")},
		stubReader{name: "r", r: Reading{Unlimited: true}},
		stubReader{name: "s", r: Reading{Provider: "s", Windows: []Window{{Name: "5h", UsedPct: math.NaN(), ResetsAt: 2}}}},
	})
	if len(got) != 4 {
		t.Fatalf("got %d readings", len(got))
	}
	if got[0].Unknown || got[0].Windows[0].UsedPct != 10 {
		t.Errorf("good: %+v", got[0])
	}
	if !got[1].Unknown || got[1].Provider != "q" || got[1].Note != "read failed" {
		t.Errorf("failed reader: %+v", got[1])
	}
	if got[2].Provider != "r" || !got[2].Unlimited {
		t.Errorf("empty provider must take the reader's: %+v", got[2])
	}
	if !got[3].Unknown {
		t.Errorf("an invalid window must be cleaned to unknown: %+v", got[3])
	}
}

func TestNamedReportsUnderItsName(t *testing.T) {
	r, err := named{Reader: UnlimitedReader{Name: "inner"}, name: "outer"}.Read(context.Background())
	if err != nil || r.Provider != "outer" {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestCachedReadsOncePerInterval(t *testing.T) {
	isolate(t)
	now := t0
	clock := func() time.Time { return now }
	calls := 0
	inner := stubReader{name: "cachedp", calls: &calls,
		r: Reading{Provider: "cachedp", CollectedAt: t0.UnixMilli(), Windows: []Window{{Name: "5h", UsedPct: 5, ResetsAt: 9}}}}
	c := Cached{Reader: inner, Every: time.Minute, Now: clock}
	for i, step := range []struct {
		advance time.Duration
		calls   int
	}{
		{0, 1},                // nothing cached: reads
		{30 * time.Second, 1}, // within Every: the file answers
		{31 * time.Second, 2}, // past Every: reads again
	} {
		now = now.Add(step.advance)
		r, err := c.Read(context.Background())
		if err != nil || r.Windows[0].UsedPct != 5 {
			t.Fatalf("step %d: %+v, %v", i, r, err)
		}
		if calls != step.calls {
			t.Fatalf("step %d: %d calls, want %d", i, calls, step.calls)
		}
	}
	st, err := os.Stat(filepath.Join(CacheDir(), "quota-cachedp.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v, %v", st, err)
	}
	failing := Cached{Reader: stubReader{name: "failp", err: errors.New("boom")}, Every: time.Minute, Now: clock}
	if _, err := failing.Read(context.Background()); err == nil {
		t.Fatal("a failing reader with no cache must return its error")
	}
}

func TestWriteAtomicAndWithLockFailures(t *testing.T) {
	dir := t.TempDir()
	if err := writeAtomic(filepath.Join(dir, "missing", "f.json"), []byte("{}")); err == nil {
		t.Error("writeAtomic into a missing directory must fail")
	}
	p := filepath.Join(dir, "f.json")
	if err := writeAtomic(p, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "{}" {
		t.Errorf("content %q", b)
	}
	// A file where the directory should be: MkdirAll fails.
	block := filepath.Join(dir, "block")
	os.WriteFile(block, nil, 0o600)
	if err := withLock(filepath.Join(block, "sub", "x.json"), func() error { return nil }); err == nil {
		t.Error("withLock under a file must fail")
	}
	want := errors.New("inner")
	if err := withLock(filepath.Join(dir, "y.json"), func() error { return want }); err != want {
		t.Errorf("withLock must return f's error, got %v", err)
	}
}

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log.jsonl")
	os.WriteFile(p, []byte("first line\nsecond\nthird\n"), 0o600)
	for _, tc := range []struct {
		name string
		path string
		max  int64
		want []string
	}{
		{"whole file", p, 1 << 20, []string{"first line", "second", "third"}},
		{"tail drops the partial first line", p, 10, []string{"third"}},
		{"missing file", filepath.Join(dir, "none"), 10, nil},
	} {
		var got []string
		for _, l := range tailLines(tc.path, tc.max) {
			got = append(got, string(l))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Window{Name: "5h", UsedPct: 10, ResetsAt: 1}
	for _, tc := range []struct {
		name string
		r    Reading
		err  string
	}{
		{"no provider", Reading{}, "no provider"},
		{"unknown", Reading{Provider: "p", Unknown: true}, ""},
		{"unlimited", Reading{Provider: "p", Unlimited: true}, ""},
		{"no window", Reading{Provider: "p"}, "no window"},
		{"unnamed window", Reading{Provider: "p", Windows: []Window{{UsedPct: 1, ResetsAt: 1}}}, "no name"},
		{"NaN", Reading{Provider: "p", Windows: []Window{{Name: "w", UsedPct: math.NaN(), ResetsAt: 1}}}, "out of range"},
		{"negative", Reading{Provider: "p", Windows: []Window{{Name: "w", UsedPct: -1, ResetsAt: 1}}}, "out of range"},
		{"too high", Reading{Provider: "p", Windows: []Window{{Name: "w", UsedPct: 1001, ResetsAt: 1}}}, "out of range"},
		{"no reset", Reading{Provider: "p", Windows: []Window{{Name: "w", UsedPct: 1}}}, "no reset"},
		{"valid", Reading{Provider: "p", Windows: []Window{ok}}, ""},
	} {
		err := tc.r.Validate()
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: unexpected %v", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.err)
		}
	}
}

func TestWindowName(t *testing.T) {
	for _, tc := range []struct {
		min  int
		want string
	}{
		{300, "5h"}, {1440, "daily"}, {10080, "7d"}, {60, "60m"}, {0, "window"}, {-5, "window"},
	} {
		if got := WindowName(tc.min); got != tc.want {
			t.Errorf("WindowName(%d) = %q, want %q", tc.min, got, tc.want)
		}
	}
}

func TestNumberAndTimeFields(t *testing.T) {
	rec := map[string]any{
		"f": 12.5, "s": "40", "bad": "x", "b": true,
		"rfc": "2026-10-15T21:30:00Z", "secs": "1792116530", "ms": float64(1792116530123),
		"fsecs": float64(1792116530), "nested": map[string]any{"v": 3.0}, "null": nil,
	}
	for _, tc := range []struct {
		path string
		want float64
		ok   bool
	}{
		{"f", 12.5, true}, {"s", 40, true}, {"bad", 0, false}, {"b", 0, false},
		{"nested.v", 3, true}, {"missing", 0, false}, {"f.deeper", 0, false}, {"null", 0, false},
	} {
		got, ok := number(rec, tc.path)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("number(%q) = %v, %v; want %v, %v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
	for _, tc := range []struct {
		path string
		want time.Time
		ok   bool
	}{
		{"rfc", time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC), true},
		{"secs", time.Unix(1792116530, 0), true},
		{"ms", time.UnixMilli(1792116530123), true},
		{"fsecs", time.Unix(1792116530, 0), true},
		{"bad", time.Time{}, false},
		{"b", time.Time{}, false},
		{"missing", time.Time{}, false},
		{"", time.Time{}, false},
	} {
		got, ok := timeField(rec, tc.path)
		if ok != tc.ok || (ok && !got.Equal(tc.want)) {
			t.Errorf("timeField(%q) = %v, %v; want %v, %v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFileReaderJSONAndUnknowns(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "q.json")
	windows := []FieldWindow{{Name: "monthly", UsedPct: "used", Ratio: true, ResetsAt: "reset"}}
	os.WriteFile(p, []byte(`{"used":0.25,"reset":"2026-11-01T00:00:00Z"}`), 0o600)
	mtime := t0.Add(-time.Hour)
	os.Chtimes(p, mtime, mtime)
	r, _ := FileReader{Name: "x", Kind: "json", Path: p, Windows: windows, Now: fixed}.Read(context.Background())
	if r.Unknown || r.Windows[0].UsedPct != 25 || r.ObservedAt != mtime.UnixMilli() {
		t.Fatalf("json with ratio and the file's time: %+v", r)
	}
	for _, tc := range []struct {
		name string
		f    FileReader
		note string
	}{
		{"missing json", FileReader{Name: "x", Kind: "json", Path: filepath.Join(dir, "none"), Now: fixed}, "file not readable"},
		{"unknown kind", FileReader{Name: "x", Kind: "yaml", Path: p, Now: fixed}, "unknown kind yaml"},
		{"window field missing", FileReader{Name: "x", Kind: "json", Path: p, Now: fixed,
			Windows: []FieldWindow{{Name: "m", UsedPct: "nope", ResetsAt: "reset"}}}, "lacks a window field"},
		{"no window at all", FileReader{Name: "x", Kind: "json", Path: p, Now: fixed}, "no window"},
		{"no matching record", FileReader{Name: "x", Kind: "json", Path: p, Now: fixed,
			Match: map[string]string{"msg": "x"}}, "no complete matching record"},
	} {
		r, err := tc.f.Read(context.Background())
		if err != nil || !r.Unknown || !strings.Contains(r.Note, tc.note) {
			t.Errorf("%s: got %+v, %v; want unknown %q", tc.name, r, err, tc.note)
		}
	}
}

func TestFieldWindowExtract(t *testing.T) {
	rec := map[string]any{"kind": "W", "pct": 5.0, "reset": "1792116530"}
	for _, tc := range []struct {
		name string
		fw   FieldWindow
		ok   bool
	}{
		{"name from a mapped value", FieldWindow{NameFrom: "kind", Names: map[string]string{"W": "weekly"}, UsedPct: "pct", ResetsAt: "reset"}, true},
		{"name field missing", FieldWindow{NameFrom: "nokind", Names: map[string]string{"W": "weekly"}, UsedPct: "pct", ResetsAt: "reset"}, false},
		{"unmapped value", FieldWindow{NameFrom: "kind", Names: map[string]string{}, UsedPct: "pct", ResetsAt: "reset"}, false},
		{"no reset", FieldWindow{Name: "w", UsedPct: "pct", ResetsAt: "none"}, false},
		{"no name at all", FieldWindow{UsedPct: "pct", ResetsAt: "reset"}, false},
		{"fixed name", FieldWindow{Name: "w", UsedPct: "pct", ResetsAt: "reset"}, true},
	} {
		if _, ok := tc.fw.extract(rec); ok != tc.ok {
			t.Errorf("%s: ok %v, want %v", tc.name, ok, tc.ok)
		}
	}
}

func TestLimitWriter(t *testing.T) {
	var sb strings.Builder
	w := &limitWriter{w: &sb, n: 4}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first write: %d, %v", n, err)
	}
	if _, err := w.Write([]byte("de")); err == nil {
		t.Fatal("a write past the limit must fail")
	}
	if sb.String() != "abc" {
		t.Fatalf("got %q", sb.String())
	}
}

func TestCommandReaderWithoutArgvOrBinary(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		argv []string
		note string
	}{
		{"no argv", nil, "no argv"},
		{"missing binary", []string{filepath.Join(dir, "absent")}, "cannot start"},
	} {
		r, _ := CommandReader{Name: "c", Argv: tc.argv, Now: fixed}.Read(context.Background())
		if !r.Unknown || !strings.Contains(r.Note, tc.note) {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
}

// fakeCodex writes a stand-in for `codex app-server` in a temporary
// directory: it answers initialize, then account/rateLimits/read.
func fakeCodex(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCodexReaderRPC(t *testing.T) {
	limits := `{"rateLimits":{"primary":{"usedPercent":33,"windowDurationMins":300,"resetsAt":1792116530}}}`
	answer := `read l; echo '{"method":"note"}'; echo '{"id":0,"result":{}}'; read l; read l; echo '{"id":1,"result":` + limits + `}'`
	home := t.TempDir()
	r, err := CodexReader{Bin: fakeCodex(t, answer), Home: home, Now: fixed}.Read(context.Background())
	if err != nil || r.Unknown || r.Source != "codex app-server" || len(r.Windows) != 1 || r.Windows[0].UsedPct != 33 {
		t.Fatalf("rpc: %+v, %v", r, err)
	}
	for _, tc := range []struct {
		name string
		body string
	}{
		{"initialize refused", `read l; echo '{"id":0,"error":{"code":1}}'`},
		{"read refused", `read l; echo '{"id":0,"result":{}}'; read l; read l; echo '{"id":1,"error":{"code":1}}'`},
		{"closed without an answer", `read l; exit 0`},
	} {
		if _, err := codexRPC(context.Background(), fakeCodex(t, tc.body)); err == nil {
			t.Errorf("%s: want an error", tc.name)
		}
		// The reader falls back to the session logs, here empty: unknown.
		r, err := CodexReader{Bin: fakeCodex(t, tc.body), Home: home, Now: fixed}.Read(context.Background())
		if err != nil || !r.Unknown {
			t.Errorf("%s: reader %+v, %v", tc.name, r, err)
		}
	}
	if _, err := codexRPC(context.Background(), filepath.Join(home, "absent")); err == nil {
		t.Error("a missing binary must fail")
	}
}

func TestCodexReaderWithoutBinaryReadsTheLogs(t *testing.T) {
	r, err := CodexReader{Home: t.TempDir(), Now: fixed}.Read(context.Background())
	if err != nil || !r.Unknown || r.Provider != "openai" {
		t.Fatalf("no binary, no logs: %+v, %v", r, err)
	}
}
