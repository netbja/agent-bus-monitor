package dispatch

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The self-repair chore (self-repair spec, step 6) calls its pass only while
// this dispatcher holds the lease, with the lease token for its fenced
// ledger write, and logs a failure once per distinct error.
func TestSelfRepairChoreRunsOnlyWithTheLease(t *testing.T) {
	b, _ := dialRun(t)
	ctx := context.Background()
	var tokens []string
	var log bytes.Buffer
	l := &loop{b: b, token: "sr-holder", last: map[string]string{},
		cfg: Config{Log: &log, SelfRepair: SelfRepairConfig{Pass: func(_ context.Context, token string) error {
			tokens = append(tokens, token)
			return errors.New("herdr gone")
		}}}}
	l.selfRepairPass(ctx)
	if len(tokens) != 0 {
		t.Fatal("ran without the lease")
	}
	if ok, err := b.AcquireDispatch(ctx, "sr-holder", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	l.selfRepairPass(ctx)
	l.selfRepairPass(ctx)
	if len(tokens) != 2 || tokens[0] != "sr-holder" {
		t.Fatalf("calls %v with the lease", tokens)
	}
	if n := strings.Count(log.String(), "herdr gone"); n != 1 {
		t.Fatalf("the error logged %d times:\n%s", n, log.String())
	}
	if (SelfRepairConfig{}).on() {
		t.Fatal("a config without a pass is on")
	}
}
