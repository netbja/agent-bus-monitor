package dispatch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// services-restart-buttons §4, the person's Q3: every line the dispatcher
// logs carries "ts", RFC 3339 with the zone, from the same clock as the
// service's text lines (the local clock).
func TestLogLinesCarryTheirTime(t *testing.T) {
	var buf bytes.Buffer
	l := &loop{cfg: Config{Log: &buf}}
	before := time.Now().Truncate(time.Second)
	l.log(map[string]any{"agent": "coder", "action": "none", "rule": 1, "reason": "no obligation"})
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("%q: %v", buf.String(), err)
	}
	ts, _ := got["ts"].(string)
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil || at.Before(before) || at.After(time.Now()) {
		t.Fatalf("ts %q: %v", ts, err)
	}
	if !strings.HasSuffix(ts, "Z") && !strings.ContainsAny(ts[len(ts)-6:], "+-") {
		t.Fatalf("ts %q has no zone", ts)
	}
	if _, z := at.Zone(); z != func() int { _, o := time.Now().Zone(); return o }() {
		t.Fatalf("ts %q is not the local clock's zone", ts)
	}
	if got["action"] != "none" || got["agent"] != "coder" {
		t.Fatalf("fields lost: %v", got)
	}
}
