package main

import (
	"testing"
)

func TestHealthCheckParsingHelpers(t *testing.T) {
	for _, tc := range []struct {
		version, minimum string
		want             bool
	}{
		{"v1.2.3", "1.2.2", true}, {"1.2.3", "1.2.3", true}, {"1.2.2", "1.2.3", false}, {"development", "1.0.0", false},
	} {
		if got := versionAtLeast(tc.version, tc.minimum); got != tc.want {
			t.Errorf("versionAtLeast(%q,%q)=%v", tc.version, tc.minimum, got)
		}
	}
	for _, tc := range []struct {
		value string
		want  int
	}{{"12k ctx", 12000}, {"1.5m", 1500000}, {"unknown", 0}, {"250", 250}} {
		if got := parseContextTokens(tc.value); got != tc.want {
			t.Errorf("parseContextTokens(%q)=%d want %d", tc.value, got, tc.want)
		}
	}
}

func TestPluginStateFromJSON(t *testing.T) {
	list := func(id, version string) []byte {
		return []byte(`{"id":"cli:plugin","result":{"plugins":[{"actions":[],"enabled":true,"plugin_id":"` + id +
			`","plugin_root":"/x","source":{"kind":"local"},"version":"` + version + `"}]}}`)
	}
	for _, tc := range []struct {
		name            string
		json            []byte
		binary          string
		problem, readOK bool
	}{
		{"equal", list(pluginID, "0.20.0"), "v0.20.0", false, true},
		{"older", list(pluginID, "0.19.0"), "v0.20.0", true, true},
		{"absent", list("cloudmanic.herdr-plus", "0.1.16"), "v0.20.0", true, true},
		{"garbage", []byte("not json"), "v0.20.0", false, false},
		{"dev", list(pluginID, "0.19.0"), "dev", false, true},
	} {
		problem, ok := pluginProblem(tc.json, tc.binary)
		if problem != tc.problem || ok != tc.readOK {
			t.Errorf("%s: pluginProblem=(%v,%v) want (%v,%v)", tc.name, problem, ok, tc.problem, tc.readOK)
		}
	}
	if reg, v, ok := pluginState(list(pluginID, "0.20.0"), pluginID); !reg || v != "0.20.0" || !ok {
		t.Errorf("pluginState=(%v,%q,%v)", reg, v, ok)
	}
}
