package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
)

func noLook(string) (string, error) { return "", os.ErrNotExist }

// withConfig is a getenv whose config home holds config.toml = body, plus env.
func withConfig(t *testing.T, body string, env map[string]string) func(string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agentbus"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agentbus", "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{"XDG_CONFIG_HOME": dir}
	for k, v := range env {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

func TestServiceReadersReadConfigTOML(t *testing.T) {
	file := "[dispatch]\nquota_warn = 70\nquota_hold = 90\nquota_max_age = \"10m\"\nquota_long_hold = \"0\"\ntokens = false\nauto_clear = true\nprompt_grace = 45\ncontext_alert = 60\n"
	getenv := withConfig(t, file, nil)
	q := serviceQuota(getenv, io.Discard)
	if q.WarnPct != 70 || q.HoldPct != 90 || q.MaxAge != 10*time.Minute || q.LongHold != -1 {
		t.Errorf("quota from config.toml = %+v", q)
	}
	if tk := serviceTokens(getenv, func() (string, error) { return t.TempDir(), nil }); tk.Root != "" {
		t.Errorf("tokens = false did not turn the pass off: %+v", tk)
	}
	if !serviceClear(getenv, noLook, nil).On {
		t.Error("auto_clear = true did not turn it on")
	}
	if g := dispatch.PromptGrace(getenv); g != 45*time.Second {
		t.Errorf("prompt grace = %v", g)
	}
	if a := bus.ContextAlert(getenv); a != 60 {
		t.Errorf("context alert = %v", a)
	}
	// The env still wins, by its own rule.
	getenv = withConfig(t, file, map[string]string{"AGENT_BUS_QUOTA_WARN": "75", "AGENT_BUS_TOKENS": "1", "AGENT_BUS_AUTO_CLEAR": "yes", "AGENT_BUS_PROMPT_GRACE": "0"})
	if q := serviceQuota(getenv, io.Discard); q.WarnPct != 75 || q.HoldPct != 90 {
		t.Errorf("env over file: %+v", q)
	}
	if tk := serviceTokens(getenv, func() (string, error) { return t.TempDir(), nil }); tk.Root == "" {
		t.Error("AGENT_BUS_TOKENS=1 did not win over tokens = false")
	}
	if serviceClear(getenv, noLook, nil).On {
		t.Error("AGENT_BUS_AUTO_CLEAR=yes is off by today's rule, whatever the file says")
	}
	if g := dispatch.PromptGrace(getenv); g != 0 {
		t.Errorf("AGENT_BUS_PROMPT_GRACE=0 = %v, want off", g)
	}
	// quota off in the file.
	if q := serviceQuota(withConfig(t, "[dispatch]\nquota = false\n", nil), io.Discard); q.Read != nil {
		t.Error("quota = false did not turn the policy off")
	}
}

// No file: today's results, unchanged.
func TestServiceReadersWithoutAFile(t *testing.T) {
	getenv := func(string) string { return "" }
	q := serviceQuota(getenv, io.Discard)
	if q.Read == nil || q.WarnPct != 85 || q.HoldPct != 95 || q.MaxAge != 30*time.Minute || q.LongHold != 2*time.Hour {
		t.Errorf("default quota = %+v", q)
	}
	if serviceClear(getenv, noLook, nil).On || dispatch.PromptGrace(getenv) != 30*time.Second || bus.ContextAlert(getenv) != 80 {
		t.Error("a default changed")
	}
}

func TestRunawayThresholdsReadConfigTOML(t *testing.T) {
	th, ok := runawayThresholdsFromEnv(withConfig(t, "[health.runaway]\nsilent_after = \"45m\"\nscreen_repeats = 6\n", nil))
	if !ok || th.SilentAfter != 45*time.Minute || th.RepeatedScreens != 6 || th.FastGrowth != health.RunawayFastGrowth {
		t.Errorf("thresholds = %+v ok=%v", th, ok)
	}
	if _, ok := runawayThresholdsFromEnv(withConfig(t, "[health.runaway]\nsilent_after = \"45m\"\n", map[string]string{"AGENTBUS_HEALTH_RUNAWAY_SILENT_AFTER": "soon"})); ok {
		t.Error("an invalid env value must make the check unknown, whatever the file says")
	}
}

// The registry's defaults are the code's own.
func TestRegistryDefaultsMatchTheReaders(t *testing.T) {
	d := health.DefaultRunawayThresholds()
	r := config.Resolver{}
	if r.Duration("health.runaway.silent_after") != d.SilentAfter || r.Int("health.runaway.context_growth") != d.FastGrowth ||
		r.Duration("health.runaway.growth_span") != d.GrowthSpan || r.Int("health.runaway.screen_repeats") != d.RepeatedScreens {
		t.Errorf("runaway defaults differ: registry vs %+v", d)
	}
	if r.Float("dispatch.context_alert") != bus.ContextAlertDefault || r.Duration("dispatch.prompt_grace") != dispatch.PromptGraceDefault {
		t.Error("context alert or prompt grace default differs")
	}
	if lim := bus.Limits(); r.Int("bus.report_max") != lim.PreviewRunes || r.Int("bus.report_full_max") != lim.FullRunes || r.Int("bus.verdict_max") != lim.VerdictEntries {
		t.Errorf("bus caps differ: %+v", lim)
	}
}
