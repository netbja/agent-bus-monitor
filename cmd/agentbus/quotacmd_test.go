package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestStatuslineNeverFailsAndPassesStdinThrough(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	in := `{"model":{"display_name":"Opus"},"rate_limits":{"five_hour":{"used_percentage":12,"resets_at":1792116530}}}`
	var out bytes.Buffer
	if code := runStatusline(nil, strings.NewReader(in), &out); code != 0 || !strings.Contains(out.String(), "Opus · 5h 12%") {
		t.Fatalf("built-in: %d %q", code, out.String())
	}
	out.Reset()
	if code := runStatusline([]string{"--then", "cat"}, strings.NewReader(in), &out); code != 0 || out.String() != in {
		t.Fatalf("--then got %q", out.String())
	}
	big := strings.Repeat(" ", 70<<10) + in
	out.Reset()
	if code := runStatusline([]string{"--then", "wc -c"}, strings.NewReader(big), &out); code != 0 || strings.TrimSpace(out.String()) != strconv.Itoa(len(big)) {
		t.Fatalf("over-cap stdin not replayed: %q", out.String())
	}
	out.Reset()
	if code := runStatusline(nil, strings.NewReader("garbage"), &out); code != 0 {
		t.Fatal("bad input failed the status line")
	}
}
