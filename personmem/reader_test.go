package personmem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// BusReader finds an entry in its project's stream, then in the history
// archive once the stream has trimmed it, with its typed and said fields.
func TestBusReader(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "pm" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	t.Cleanup(func() { c.Del(ctx, bus.StreamKey(project, "cmd")) })
	id, err := b.TypedCmd(ctx, "charlie", "master", "yes, dev first")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rd := BusReader{Client: c, HistoryDir: func(p string) string { return filepath.Join(dir, p) }}
	e, ok, err := rd.Entry(ctx, Source{project, id})
	if err != nil || !ok || !e.Typed || e.Body != "yes, dev first" || e.From != "charlie" || e.Kind != "cmd" {
		t.Fatalf("stream: %+v %v %v", e, ok, err)
	}
	// Trimmed from the stream, read from the archive's day file.
	old := "1795205976983-0"
	day := time.UnixMilli(1795205976983).UTC().Format("2006-01-02")
	line, _ := json.Marshal(history.Entry{Stream: "cmd", ID: old, F: map[string]string{"from": "charlie", "target": "master", "type": "reply",
		"command": `Answer to "Language": Any code`, "human": "yes", "typed": "yes", "said": "Any code"}})
	if err := os.MkdirAll(filepath.Join(dir, project), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, project, day+".jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	e, ok, err = rd.Entry(ctx, Source{project, old})
	if err != nil || !ok || !e.Typed || e.Said != "Any code" {
		t.Fatalf("archive: %+v %v %v", e, ok, err)
	}
	if _, ok, err := rd.Entry(ctx, Source{project, "1-1"}); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
}

func TestParseSource(t *testing.T) {
	if s, err := ParseSource("demo:1795205976983-0"); err != nil || s.Project != "demo" || s.ID != "1795205976983-0" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"demo", "Demo:1-0", "demo:abc", "demo:1-", ":1-0"} {
		if _, err := ParseSource(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}
