package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCache(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadCCStatusline(t *testing.T) {
	p := writeCache(t, `{"sessionUsage":25,"sessionResetAt":"2026-07-23T11:39:59.962655+00:00",
	  "weeklyUsage":44,"weeklyResetAt":"2026-07-28T13:00:00.962677+00:00",
	  "weeklySonnetUsage":0,"weeklyOpusUsage":12,"extraUsageEnabled":false,"tokenHash":"abc"}`)

	got, err := ReadCCStatusline(p)
	if err != nil {
		t.Fatalf("ReadCCStatusline: %v", err)
	}
	if got.Provider != AnthropicProvider {
		t.Errorf("Provider = %q, want %q", got.Provider, AnthropicProvider)
	}
	if got.SessionPct != 25 || got.WeeklyPct != 44 {
		t.Errorf("pcts = %v/%v, want 25/44", got.SessionPct, got.WeeklyPct)
	}
	if got.WeeklyReset != "2026-07-28T13:00:00.962677+00:00" {
		t.Errorf("WeeklyReset = %q", got.WeeklyReset)
	}
	if got.Source != "ccstatusline" {
		t.Errorf("Source = %q", got.Source)
	}
	// Zero-valued gauges are dropped so the table doesn't show noise; non-zero
	// ones ride in Extra rather than forcing a schema field per provider.
	if _, ok := got.Extra["weekly_sonnet_pct"]; ok {
		t.Error("zero weekly_sonnet_pct should be omitted")
	}
	if got.Extra["weekly_opus_pct"] != 12 {
		t.Errorf("weekly_opus_pct = %v, want 12", got.Extra["weekly_opus_pct"])
	}
	// TS comes from the file mtime — when the figures were fetched, not now.
	if got.TS == 0 {
		t.Error("TS should be set from the cache mtime")
	}
}

func TestReadCCStatuslineUnknownKeysAreIgnored(t *testing.T) {
	// A ccstatusline upgrade that adds fields must not break the read.
	p := writeCache(t, `{"sessionUsage":5,"brandNewField":{"nested":true},"weeklyUsage":9}`)
	got, err := ReadCCStatusline(p)
	if err != nil {
		t.Fatalf("ReadCCStatusline: %v", err)
	}
	if got.SessionPct != 5 || got.WeeklyPct != 9 {
		t.Errorf("got %v/%v, want 5/9", got.SessionPct, got.WeeklyPct)
	}
}

func TestReadCCStatuslineErrors(t *testing.T) {
	if _, err := ReadCCStatusline(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("missing cache should error (the caller turns it into a note)")
	}
	if _, err := ReadCCStatusline(writeCache(t, "not json")); err == nil {
		t.Error("malformed cache should error")
	}
}

func TestCCStatuslinePathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/xdg")
	if got, want := CCStatuslinePath(), "/xdg/ccstatusline/usage.json"; got != want {
		t.Errorf("CCStatuslinePath() = %q, want %q", got, want)
	}
}
