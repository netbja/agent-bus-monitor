package dispatch

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The backup chore (backup spec, "Schedule") calls its pass only while this
// dispatcher holds the lease, and logs a failure once per distinct error.
func TestBackupChoreRunsOnlyWithTheLease(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	calls := 0
	var log bytes.Buffer
	l := &loop{b: b, token: "bk-holder", last: map[string]string{},
		cfg: Config{Log: &log, Backup: BackupConfig{Pass: func(context.Context) error { calls++; return errors.New("disk full") }}}}
	l.backupPass(ctx)
	if calls != 0 {
		t.Fatal("ran without the lease")
	}
	if ok, err := b.AcquireDispatch(ctx, "bk-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	l.backupPass(ctx)
	l.backupPass(ctx)
	if calls != 2 {
		t.Fatalf("%d calls with the lease", calls)
	}
	if n := strings.Count(log.String(), "disk full"); n != 1 {
		t.Fatalf("the error logged %d times:\n%s", n, log.String())
	}
	if (BackupConfig{}).on() {
		t.Fatal("a config without a pass is on")
	}
}
