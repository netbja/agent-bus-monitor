package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/quota"
)

// agentbus statusline records the session's context fill (context-alert)
// and still prints the status line.
func TestStatuslineRecordsTheContextFill(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	in := `{"session_id":"abc-1","model":{"display_name":"Opus"},"context_window":{"used_percentage":81.5}}`
	var out bytes.Buffer
	if code := runStatusline(nil, strings.NewReader(in), &out); code != 0 || !strings.Contains(out.String(), "ctx 82%") {
		t.Fatalf("exit %d, line %q", code, out.String())
	}
	if r, ok := quota.ReadContext(quota.ContextDir(), "abc-1"); !ok || r.Pct != 81.5 {
		t.Fatalf("recorded %+v %v", r, ok)
	}
}
