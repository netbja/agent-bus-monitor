package quota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// usageNormal is a get_usage answer as Claude Code 2.1.325 gives it on a Pro
// subscription (values made up), with keys this reader does not know.
const usageNormal = `{"session":{"total_cost_usd":0},"subscription_type":"pro","rate_limits_available":true,
"rate_limits":{
 "five_hour":{"utilization":26,"resets_at":"2026-11-14T22:50:00.139078+00:00","limit_dollars":null},
 "seven_day":{"utilization":55,"resets_at":"2026-11-17T13:00:00.139099+00:00"},
 "seven_day_opus":null,"tangelo":null,"pace":{"x":1},
 "limits":[
  {"kind":"session","group":"session","percent":26,"resets_at":"2026-11-14T22:50:00.139078+00:00","scope":null},
  {"kind":"weekly_all","group":"weekly","percent":55,"resets_at":"2026-11-17T13:00:00.139099+00:00","scope":null},
  {"kind":"weekly_scoped","group":"weekly","percent":12,"resets_at":"2026-11-17T13:00:00+00:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null}},
  {"kind":"some_future_kind","percent":99,"resets_at":"2026-11-17T13:00:00+00:00"}
 ],
 "spend":{"percent":0}},
"behaviors":null}`

func unix(t *testing.T, s string) int64 {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at.Unix()
}

func byName(ws []Window) map[string]Window {
	out := map[string]Window{}
	for _, w := range ws {
		out[w.Name] = w
	}
	return out
}

// The parser maps five_hour/seven_day to 5h/7d and a weekly_scoped limit to
// 7d-<model>; unknown keys and kinds are ignored, null windows skipped.
func TestParseClaudeUsage(t *testing.T) {
	ws, note := parseClaudeUsage([]byte(usageNormal))
	got := byName(ws)
	if len(ws) != 3 || note != "" {
		t.Fatalf("windows %+v note %q", ws, note)
	}
	want := map[string]Window{
		"5h":       {Name: "5h", UsedPct: 26, ResetsAt: unix(t, "2026-11-14T22:50:00.139078+00:00"), Minutes: 300},
		"7d":       {Name: "7d", UsedPct: 55, ResetsAt: unix(t, "2026-11-17T13:00:00.139099+00:00"), Minutes: 10080},
		"7d-fable": {Name: "7d-fable", UsedPct: 12, ResetsAt: unix(t, "2026-11-17T13:00:00+00:00"), Minutes: 10080},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %+v, want %+v", name, got[name], w)
		}
	}
}

// Without five_hour/seven_day the session and weekly_all limits stand in.
func TestParseClaudeUsageLimitsOnly(t *testing.T) {
	ws, _ := parseClaudeUsage([]byte(`{"rate_limits_available":true,"rate_limits":{"limits":[
	 {"kind":"session","percent":40,"resets_at":"2026-11-14T22:50:00Z"},
	 {"kind":"weekly_all","percent":60,"resets_at":"2026-11-17T13:00:00Z"}]}}`))
	got := byName(ws)
	if got["5h"].UsedPct != 40 || got["7d"].UsedPct != 60 || len(ws) != 2 {
		t.Fatalf("%+v", ws)
	}
}

// rate_limits_available false (an API-key account), an unknown format, an
// error answer: no window, a note saying why.
func TestParseClaudeUsageNothing(t *testing.T) {
	for name, body := range map[string]string{
		"not available":                 `{"subscription_type":null,"rate_limits_available":false,"rate_limits":null}`,
		"not available, windows anyway": `{"rate_limits_available":false,"rate_limits":{"five_hour":{"utilization":3,"resets_at":"2026-11-14T22:50:00Z"}}}`,
		"unknown":                       `{"something_else":{"a":1}}`,
		"not json":                      `get_usage is not supported in this context`,
	} {
		ws, note := parseClaudeUsage([]byte(body))
		if len(ws) != 0 || note == "" {
			t.Errorf("%s: windows %+v note %q", name, ws, note)
		}
		if strings.HasPrefix(name, "not available") && !strings.Contains(note, "no rate limits") {
			t.Errorf("%s: note %q does not say the account has no rate limits", name, note)
		}
	}
}

// fakeClaude writes a claude stand-in: it records its argv, directory and
// CLAUDE_CONFIG_DIR, answers initialize, then answers get_usage with
// $FAKE_USAGE (a file) — or sleeps, or answers an error.
func fakeClaude(t *testing.T, mode string) (bin, seen string) {
	t.Helper()
	dir := t.TempDir()
	seen = filepath.Join(dir, "seen")
	usage := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(usage, []byte(strings.ReplaceAll(usageNormal, "\n", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	var answer string
	switch mode {
	case "normal":
		answer = `printf '{"type":"control_response","response":{"subtype":"success","request_id":"r2","response":%s}}\n' "$(cat ` + usage + `)"`
	case "error":
		answer = `echo '{"type":"control_response","response":{"subtype":"error","request_id":"r2","error":"get_usage is not supported in this context"}}'`
	case "hang":
		answer = `sleep 30`
	}
	script := "#!/bin/sh\n" +
		"{ echo \"argv $*\"; echo \"dir $(pwd)\"; echo \"config ${CLAUDE_CONFIG_DIR-unset}\"; } > " + seen + "\n" +
		"read l1\n" +
		"echo '{\"type\":\"system\",\"subtype\":\"init\"}'\n" +
		"echo '{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"r1\",\"response\":{}}}'\n" +
		"read l2\n" +
		"case \"$l2\" in *'\"get_usage\"'*'\"skip_behaviors\":true'*) ;; *) echo bad request >&2; exit 3;; esac\n" +
		answer + "\n" +
		"sleep 30\n" // stays up: the probe kills it
	bin = filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, seen
}

func probeAt(t *testing.T, bin string, now time.Time) *ClaudeProbe {
	t.Helper()
	return &ClaudeProbe{Bin: bin, Cache: filepath.Join(t.TempDir(), "probe.json"), Every: 5 * time.Minute,
		Timeout: 3 * time.Second, Now: func() time.Time { return now }}
}

// The probe runs claude -p in stream-json from an empty directory, with the
// account's config dir, no settings but local, no MCP, no hooks, no session
// kept; it reads get_usage's answer and kills the CLI.
func TestClaudeProbeRunsTheCLI(t *testing.T) {
	bin, seen := fakeClaude(t, "normal")
	now := time.Unix(1794690000, 0)
	p := probeAt(t, bin, now)
	p.ConfigDir = "/accounts/two"
	start := time.Now()
	ws, at, note := p.Windows(context.Background())
	if len(ws) != 3 || note != "" || at != now.UnixMilli() {
		t.Fatalf("windows %+v at %d note %q", ws, at, note)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the probe waited for the CLI to exit (%v): it must kill it", time.Since(start))
	}
	b, _ := os.ReadFile(seen)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	wantArgv := "argv -p --input-format stream-json --output-format stream-json --verbose --setting-sources local --strict-mcp-config --settings {\"disableAllHooks\":true} --no-session-persistence"
	if len(lines) != 3 || lines[0] != wantArgv || lines[2] != "config /accounts/two" {
		t.Fatalf("the CLI saw %q", lines)
	}
	dir := strings.TrimPrefix(lines[1], "dir ")
	if ents, err := os.ReadDir(dir); err == nil || len(ents) != 0 {
		t.Fatalf("the probe's directory %s is left behind (%v)", dir, ents)
	}
}

// Without a config dir of its own, the probe leaves CLAUDE_CONFIG_DIR as it
// is: unset stays unset (Claude Code's default home).
func TestClaudeProbeKeepsTheDefaultHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	bin, seen := fakeClaude(t, "normal")
	p := probeAt(t, bin, time.Unix(1794690000, 0))
	if ws, _, note := p.Windows(context.Background()); len(ws) == 0 {
		t.Fatal(note)
	}
	if b, _ := os.ReadFile(seen); !strings.Contains(string(b), "config unset") {
		t.Fatalf("%s", b)
	}
}

// An error answer and a CLI that never answers (the timeout) give no window
// and a note; the timeout kills the CLI on time.
func TestClaudeProbeFailures(t *testing.T) {
	for _, mode := range []string{"error", "hang"} {
		bin, _ := fakeClaude(t, mode)
		p := probeAt(t, bin, time.Unix(1794690000, 0))
		p.Timeout = 500 * time.Millisecond
		start := time.Now()
		ws, _, note := p.Windows(context.Background())
		if len(ws) != 0 || note == "" {
			t.Errorf("%s: windows %+v note %q", mode, ws, note)
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf("%s: took %v", mode, time.Since(start))
		}
	}
	p := probeAt(t, filepath.Join(t.TempDir(), "no-claude"), time.Unix(1794690000, 0))
	if ws, _, note := p.Windows(context.Background()); len(ws) != 0 || note == "" {
		t.Errorf("a missing CLI: %+v %q", ws, note)
	}
}

// The probe runs at most once per Every, a failure included: the next
// reads in that span come from its cache.
func TestClaudeProbeCadence(t *testing.T) {
	bin, seen := fakeClaude(t, "normal")
	now := time.Unix(1794690000, 0)
	p := probeAt(t, bin, now)
	if ws, _, _ := p.Windows(context.Background()); len(ws) != 3 {
		t.Fatal(ws)
	}
	os.Remove(seen)
	now2 := now.Add(4 * time.Minute)
	p.Now = func() time.Time { return now2 }
	ws, at, _ := p.Windows(context.Background())
	if len(ws) != 3 || at != now.UnixMilli() {
		t.Fatalf("cached: %+v at %d", ws, at)
	}
	if _, err := os.Stat(seen); err == nil {
		t.Fatal("the CLI ran again within Every")
	}
	now3 := now.Add(6 * time.Minute)
	p.Now = func() time.Time { return now3 }
	if _, at, _ := p.Windows(context.Background()); at != now3.UnixMilli() {
		t.Fatalf("not read again after Every: at %d", at)
	}
	// A failure is cached too: a missing CLI is not retried at every read.
	q := probeAt(t, filepath.Join(t.TempDir(), "no-claude"), now)
	q.Cache = p.Cache + ".fail"
	q.Windows(context.Background())
	q.Bin = bin
	os.Remove(seen)
	if ws, _, _ := q.Windows(context.Background()); len(ws) != 0 {
		t.Fatal("a cached failure was retried within Every")
	}
}

// ClaudeReader takes the probe first (Source cli-usage); a statusline
// window observed after the probe still wins; a window only the statusline
// knows is kept.
func TestClaudeReaderProbeFirst(t *testing.T) {
	bin, _ := fakeClaude(t, "normal")
	now := time.Unix(1794690000, 0)
	cache := filepath.Join(t.TempDir(), "sl.json")
	later := now.Add(time.Minute).UnixMilli()
	earlier := now.Add(-time.Minute).UnixMilli()
	writeStore(t, cache, map[string]storedWindow{
		"5h":         {Window: Window{Name: "5h", UsedPct: 30, ResetsAt: 1794696600, Minutes: 300}, ObservedAt: later},
		"7d":         {Window: Window{Name: "7d", UsedPct: 50, ResetsAt: 1794920400, Minutes: 10080}, ObservedAt: earlier},
		"7d-overage": {Window: Window{Name: "7d-overage", UsedPct: 5, ResetsAt: 1794920400, Minutes: 10080}, ObservedAt: earlier},
	})
	r, err := ClaudeReader{Cache: cache, Transcripts: t.TempDir(), Now: func() time.Time { return now }, Probe: probeAt(t, bin, now)}.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byName(r.Windows)
	if r.Source != "cli-usage" || got["5h"].UsedPct != 30 || got["7d"].UsedPct != 55 || got["7d-fable"].UsedPct != 12 || got["7d-overage"].UsedPct != 5 {
		t.Fatalf("source %q windows %+v", r.Source, r.Windows)
	}
	if got["7d"].ObservedAt != now.UnixMilli() || r.ObservedAt != later {
		t.Fatalf("observed: 7d %d, reading %d", got["7d"].ObservedAt, r.ObservedAt)
	}
}

// A probe that fails leaves the reader exactly as before: the statusline,
// and its "no reading yet" with the probe's reason added.
func TestClaudeReaderFallsBackToTheStatusline(t *testing.T) {
	bin, _ := fakeClaude(t, "error")
	now := time.Unix(1794690000, 0)
	cache := filepath.Join(t.TempDir(), "sl.json")
	writeStore(t, cache, map[string]storedWindow{
		"5h": {Window: Window{Name: "5h", UsedPct: 30, ResetsAt: 1794696600, Minutes: 300}, ObservedAt: now.UnixMilli()},
	})
	c := ClaudeReader{Cache: cache, Transcripts: t.TempDir(), Now: func() time.Time { return now }}
	without, _ := c.Read(context.Background())
	c.Probe = probeAt(t, bin, now)
	with, _ := c.Read(context.Background())
	if with.Source != "statusline" || len(with.Windows) != 1 || with.Windows[0] != without.Windows[0] {
		t.Fatalf("with a failed probe %+v, without %+v", with, without)
	}
	empty := ClaudeReader{Cache: filepath.Join(t.TempDir(), "none.json"), Transcripts: t.TempDir(), Now: func() time.Time { return now }, Probe: probeAt(t, bin, now)}
	u, _ := empty.Read(context.Background())
	if !u.Unknown || !strings.Contains(u.Note, "no statusline reading yet") || !strings.Contains(u.Note, "get_usage") {
		t.Fatalf("%+v", u)
	}
}

// writeStore writes a statusline file holding ws.
func writeStore(t *testing.T, path string, ws map[string]storedWindow) {
	t.Helper()
	b, err := json.Marshal(claudeStore{Windows: ws})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The person's condition (claude-usage-probe): the probe changes the claude
// reader only. Every other provider's reader — agy, codex, grok (a jsonl-last
// reader), kimi, a generic json or command reader, unlimited — is built the
// same with quota.claude.probe on and off; the claude reader gets the probe
// only when it is on.
func TestClaudeProbeLeavesTheOtherReadersAlone(t *testing.T) {
	bin, _ := fakeClaude(t, "normal")
	t.Setenv("PATH", filepath.Dir(bin)+":/usr/bin:/bin")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config.toml
	cfg := map[string]ProviderConfig{
		"anthropic": {Kind: "claude"},
		"openai":    {Kind: "codex", Every: "5m"},
		"moonshot":  {Kind: "kimi"},
		"google":    {Kind: "agy"},
		"xai":       {Kind: "jsonl-last", Path: "~/.grok/logs/unified.jsonl", Match: map[string]string{"msg": "billing"}},
		"generic":   {Kind: "json", Path: "/tmp/q.json"},
		"script":    {Kind: "command", Argv: []string{"/bin/true"}},
		"local":     {Kind: "unlimited"},
	}
	build := func(on string) map[string]Reader {
		t.Setenv(config.EnvName("quota.claude.probe"), on)
		out := map[string]Reader{}
		for _, r := range Readers(cfg, nil) {
			out[r.Provider()] = r
		}
		return out
	}
	off, on := build("0"), build("1")
	if len(off) != len(cfg) || len(on) != len(cfg) {
		t.Fatalf("readers off %d, on %d, want %d", len(off), len(on), len(cfg))
	}
	for name := range cfg {
		if name == "anthropic" {
			continue
		}
		if !reflect.DeepEqual(off[name], on[name]) {
			t.Errorf("%s: the probe setting changed its reader:\noff %#v\non  %#v", name, off[name], on[name])
		}
	}
	claude := func(r Reader) ClaudeReader { return r.(named).Reader.(ClaudeReader) }
	if claude(off["anthropic"]).Probe != nil {
		t.Error("quota.claude.probe off: the claude reader still probes")
	}
	if p := claude(on["anthropic"]).Probe; p == nil || p.Bin != bin {
		t.Errorf("quota.claude.probe on: probe %+v, want the claude on PATH", p)
	}
}

// A statusline file holding no windows ("{}") reads as an empty store, not a
// nil map the probe's windows would panic on (foureyes).
func TestClaudeReaderProbeOverAnEmptyStatuslineFile(t *testing.T) {
	bin, _ := fakeClaude(t, "normal")
	now := time.Unix(1794690000, 0)
	cache := filepath.Join(t.TempDir(), "sl.json")
	if err := os.WriteFile(cache, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := ClaudeReader{Cache: cache, Transcripts: t.TempDir(), Now: func() time.Time { return now }, Probe: probeAt(t, bin, now)}.Read(context.Background())
	if err != nil || r.Source != "cli-usage" || len(r.Windows) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
}
