package personmem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

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

// An id is a stream's: when two of the project's streams hold the same id,
// the source is ambiguous and BusReader says so rather than pick one
// (reviewer-codex on fae1939c).
func TestBusReaderRefusesAnAmbiguousID(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "pa" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { c.Del(ctx, bus.StreamKey(project, "cmd"), bus.StreamKey(project, "notify")) })
	id := "1795205976983-7"
	c.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(project, "cmd"), ID: id, Values: []any{"from", "charlie", "target", "master", "type", "directive", "command", "yes", "human", "yes", "typed", "yes"}})
	c.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(project, "notify"), ID: id, Values: []any{"from", "charlie", "message", "no", "human", "yes", "typed", "yes"}})
	rd := BusReader{Client: c}
	if _, _, err := rd.Entry(ctx, Source{project, id}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous id: %v", err)
	}
}

// Live in one stream, archived from another: still ambiguous; the same
// stream live and archived is one entry (reviewer-codex on 731810b4).
func TestBusReaderAmbiguousAcrossStreamAndArchive(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "pb" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { c.Del(ctx, bus.StreamKey(project, "cmd")) })
	id := "1795205976983-9"
	fields := map[string]string{"from": "charlie", "target": "master", "type": "directive", "command": "yes", "human": "yes", "typed": "yes"}
	c.XAdd(ctx, &redis.XAddArgs{Stream: bus.StreamKey(project, "cmd"), ID: id, Values: fields})
	dir := t.TempDir()
	archive := func(entries ...history.Entry) {
		t.Helper()
		var text []byte
		for _, e := range entries {
			line, _ := json.Marshal(e)
			text = append(append(text, line...), '\n')
		}
		day := time.UnixMilli(1795205976983).UTC().Format("2006-01-02")
		if err := os.MkdirAll(filepath.Join(dir, project), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, project, day+".jsonl"), text, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rd := BusReader{Client: c, HistoryDir: func(p string) string { return filepath.Join(dir, p) }}
	archive(history.Entry{Stream: "cmd", ID: id, F: fields}) // the same entry, archived too
	if e, ok, err := rd.Entry(ctx, Source{project, id}); err != nil || !ok || e.Body != "yes" {
		t.Fatalf("live and archived cmd: %+v %v %v", e, ok, err)
	}
	archive(history.Entry{Stream: "notify", ID: id, F: map[string]string{"from": "charlie", "message": "no", "human": "yes", "typed": "yes"}})
	if _, _, err := rd.Entry(ctx, Source{project, id}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("live cmd, archived notify: %v", err)
	}
}
