package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The archive chore writes only within the lease: a dispatcher that lost it
// archives nothing.
func TestHistoryPassWritesOnlyWithTheLease(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	if _, err := b.Notify(ctx, "dispatch", "hello"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "history")
	log := &syncBuf{}
	l := &loop{b: b, token: "mine", cfg: Config{Log: log, History: HistoryConfig{Dir: dir, KeepDays: 90}}}
	l.historyPass(ctx)
	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Fatalf("archived without the lease: %v", files)
	}
	if ok, err := b.AcquireDispatch(ctx, "mine", 15*time.Second); !ok || err != nil {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	l.historyPass(ctx)
	files, _ := filepath.Glob(filepath.Join(dir, "????-??-??.jsonl")) // the day file; gaps.jsonl holds the first start's gap
	if len(files) != 1 {
		t.Fatalf("one day file: %v (log %s)", files, log)
	}
	data, _ := os.ReadFile(files[0])
	if !strings.Contains(string(data), `"stream":"notify"`) || !strings.Contains(string(data), "hello") {
		t.Fatalf("the notice archived: %s", data)
	}
}
