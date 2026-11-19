package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
)

func noLook(string) (string, error) { return "", os.ErrNotExist }

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
	getenv = withConfig(t, file, map[string]string{"AGENT_BUS_QUOTA_WARN": "75", "AGENT_BUS_TOKENS": "1", "AGENT_BUS_AUTO_CLEAR": "0", "AGENT_BUS_PROMPT_GRACE": "0"})
	if q := serviceQuota(getenv, io.Discard); q.WarnPct != 75 || q.HoldPct != 90 {
		t.Errorf("env over file: %+v", q)
	}
	if tk := serviceTokens(getenv, func() (string, error) { return t.TempDir(), nil }); tk.Root == "" {
		t.Error("AGENT_BUS_TOKENS=1 did not win over tokens = false")
	}
	if serviceClear(getenv, noLook, nil).On {
		t.Error("AGENT_BUS_AUTO_CLEAR=0 did not win over auto_clear = true")
	}
	if g := dispatch.PromptGrace(getenv); g != 0 {
		t.Errorf("AGENT_BUS_PROMPT_GRACE=0 = %v, want off", g)
	}
	// auto-clear: on with no file at all, off by the file's false, and the
	// env's 0 wins over a file that turns it on.
	if !serviceClear(withConfig(t, "", nil), noLook, nil).On {
		t.Error("auto-clear is off with no setting; the default is on")
	}
	if serviceClear(withConfig(t, "[dispatch]\nauto_clear = false\n", nil), noLook, nil).On {
		t.Error("auto_clear = false did not turn it off")
	}
	if serviceClear(withConfig(t, "[dispatch]\nauto_clear = true\n", map[string]string{"AGENT_BUS_AUTO_CLEAR": "0"}), noLook, nil).On {
		t.Error("AGENT_BUS_AUTO_CLEAR=0 did not turn it off over the file")
	}
	// A value the rule does not take is skipped: the file's false still applies.
	if serviceClear(withConfig(t, "[dispatch]\nauto_clear = false\n", map[string]string{"AGENT_BUS_AUTO_CLEAR": "true"}), noLook, nil).On {
		t.Error("AGENT_BUS_AUTO_CLEAR=true overrode auto_clear = false; it must be skipped")
	}
	// quota off in the file.
	if q := serviceQuota(withConfig(t, "[dispatch]\nquota = false\n", nil), io.Discard); q.Read != nil {
		t.Error("quota = false did not turn the policy off")
	}
}

// No file: the registry's defaults (auto-clear on since autoclear-default).
func TestServiceReadersWithoutAFile(t *testing.T) {
	getenv := func(string) string { return "" }
	q := serviceQuota(getenv, io.Discard)
	if q.Read == nil || q.WarnPct != 85 || q.HoldPct != 95 || q.MaxAge != 30*time.Minute || q.LongHold != 2*time.Hour {
		t.Errorf("default quota = %+v", q)
	}
	if !serviceClear(getenv, noLook, nil).On || dispatch.PromptGrace(getenv) != 30*time.Second || bus.ContextAlert(getenv) != 80 {
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

// A bad value in config.toml falls back and is said once, never its value.
func TestSettingsWarningsAreSaid(t *testing.T) {
	getenv := withConfig(t, "[dispatch]\nquota_warn = 0\n[x]\ntoken = \"SECRET-v\"\n", nil)
	var buf bytes.Buffer
	printSettingsWarnings(getenv, &buf)
	out := buf.String()
	if !strings.Contains(out, "agentbus: settings: config.toml:2: dispatch.quota_warn:") || !strings.Contains(out, "x.token: unknown key") || strings.Contains(out, "SECRET") {
		t.Errorf("warnings:\n%s", out)
	}
	if strings.Count(out, "quota_warn") != 1 {
		t.Errorf("not once:\n%s", out)
	}
	printSettingsWarnings(func(string) string { return "" }, &buf)
}

// TestSettingsWarningsAreSaid's bad value falls back in the dispatcher's
// quota settings.
func TestSettingsBadValueFallsBack(t *testing.T) {
	getenv := withConfig(t, "[dispatch]\nquota_warn = 0\n[x]\ntoken = \"SECRET-v\"\n", nil)
	if q := serviceQuota(getenv, io.Discard); q.WarnPct != 85 {
		t.Errorf("the bad value did not fall back: %v", q.WarnPct)
	}
}

// service run logs the settings warnings once at start.
func TestServiceRunLogsSettingsWarnings(t *testing.T) {
	b, d := runFixture(t)
	mark(t, b)
	d.Settings = func() []string {
		return []string{"config.toml:2: dispatch.quota_warn: want a number above 0, up to 100; ignored"}
	}
	d.RunDispatch = func(ctx context.Context, b *bus.Bus, escalateTo string, onLease func(bool), log io.Writer) int {
		return 0
	}
	var log bytes.Buffer
	serviceRun(context.Background(), b, "dispatch", nil, d, &log)
	if strings.Count(log.String(), "service: settings: config.toml:2: dispatch.quota_warn") != 1 {
		t.Errorf("log:\n%s", log.String())
	}
}

// The pop's end (pop-auto-retire §8): on with no setting, after 15 minutes;
// off by the file's false, the env's 0, or a delay of 0.
func TestServicePopRetire(t *testing.T) {
	missions := func(context.Context, string) ([]string, error) { return nil, nil }
	if c := servicePopRetire(withConfig(t, "", nil), missions); !c.On || c.After != 15*time.Minute || c.Missions == nil || c.ProcStart == nil {
		t.Errorf("default = %+v", c)
	}
	if c := servicePopRetire(withConfig(t, "[dispatch]\npop_retire = false\n", nil), missions); c.On {
		t.Error("pop_retire = false did not turn it off")
	}
	if c := servicePopRetire(withConfig(t, "", map[string]string{"AGENT_BUS_POP_RETIRE": "0"}), missions); c.On {
		t.Error("AGENT_BUS_POP_RETIRE=0 did not turn it off")
	}
	if c := servicePopRetire(withConfig(t, "[dispatch]\npop_retire_after = \"0\"\n", nil), missions); c.On {
		t.Error("pop_retire_after = 0 did not turn it off")
	}
	if c := servicePopRetire(withConfig(t, "[dispatch]\npop_retire_after = \"30m\"\n", nil), missions); !c.On || c.After != 30*time.Minute {
		t.Errorf("30m = %+v", c)
	}
}

// TestWithPopEnd's two literals are exactly what servicePopRetire gives for
// no setting and for pop_retire = false: the link is checked, not assumed.
func TestServicePopRetireGivesWithPopEndsLiterals(t *testing.T) {
	missions := func(context.Context, string) ([]string, error) { return nil, nil }
	fn := func(f any) uintptr { return reflect.ValueOf(f).Pointer() }
	for _, tc := range []struct {
		body string
		want dispatch.PopRetireConfig
	}{
		{"", dispatch.PopRetireConfig{On: true, After: 15 * time.Minute, Missions: missions, ProcStart: dispatch.ProcStart}},
		{"[dispatch]\npop_retire = false\n", dispatch.PopRetireConfig{On: false, After: 15 * time.Minute, Missions: missions, ProcStart: dispatch.ProcStart}},
	} {
		c := servicePopRetire(withConfig(t, tc.body, nil), missions)
		if c.On != tc.want.On || c.After != tc.want.After || fn(c.Missions) != fn(tc.want.Missions) || fn(c.ProcStart) != fn(tc.want.ProcStart) || c.End != nil {
			t.Errorf("%q: %+v, want %+v", tc.body, c, tc.want)
		}
	}
}

// pop-retire-auto: the dispatcher ends a done pop itself when it can reach
// the project's herdr session (the services' env names it); without one,
// End stays nil and master is told as before. Off, nothing is set.
func TestWithPopEnd(t *testing.T) {
	missions := func(context.Context, string) ([]string, error) { return nil, nil }
	c := dispatch.PopRetireConfig{On: true, After: 15 * time.Minute, Missions: missions, ProcStart: dispatch.ProcStart} // servicePopRetire with no setting (TestServicePopRetireGivesWithPopEndsLiterals)
	env := func(string) string { return "" }
	if withPopEnd(c, nil, nil, env).End != nil {
		t.Error("an End with no project's bus")
	}
	b, err := bus.Open(nil, "agent-bus-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if withPopEnd(c, b, nil, env).End == nil { // the pop's terminal closes on the project's holder
		t.Error("no End for a project")
	}
	off := dispatch.PopRetireConfig{On: false, After: 15 * time.Minute, Missions: missions, ProcStart: dispatch.ProcStart} // servicePopRetire with pop_retire = false
	if withPopEnd(off, b, nil, env).End != nil {
		t.Error("an End with pop_retire off")
	}
}

var withConfig = cmdtest.WithConfig
