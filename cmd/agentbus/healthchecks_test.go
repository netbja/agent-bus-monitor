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

func TestHerdrPlusVersionRequiresParseableMinimum(t *testing.T) {
	for _, tc := range []struct {
		output          string
		present, parsed bool
		want            string
	}{
		{"", false, true, ""},
		{"cloudmanic.herdr-plus v0.1.16", true, true, "v0.1.16"},
		{"cloudmanic.herdr-plus installed", true, false, ""},
	} {
		present, version, parsed := herdrPlusVersion([]byte(tc.output))
		if present != tc.present || parsed != tc.parsed || version != tc.want {
			t.Errorf("herdrPlusVersion(%q)=(%v,%q,%v)", tc.output, present, version, parsed)
		}
	}
}
