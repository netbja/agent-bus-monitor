package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/cmdtest"
)

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
