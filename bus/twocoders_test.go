package bus

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// role-coder2: two implementers never hold one task. The board refuses a
// claim of a task another owner holds, and only a tracked request's target
// may accept, block or complete it; two claims at once give one owner.
func TestTwoCodersNeverTakeOneTask(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()

	if err := b.BoardClaim(ctx, "plan-board", "coder", "feat/busmon-plan-board"); err != nil {
		t.Fatal(err)
	}
	if err := b.BoardClaim(ctx, "plan-board", "coder2", ""); err == nil || !strings.Contains(err.Error(), "owned by coder") {
		t.Fatalf("coder2 took coder's task: %v", err)
	}

	if _, err := b.Request(ctx, "agy-client-impl", "master", "coder", "", "implement agy", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "agy-client-impl", "coder2"); err == nil {
		t.Fatal("coder2 accepted coder's request")
	}
	if err := b.BoardClaim(ctx, "agy-client-impl", "coder2", ""); err == nil {
		t.Fatal("coder2 claimed coder's request on the board")
	}
	if err := b.BlockRequest(ctx, "agy-client-impl", "coder2", "mine now"); err == nil {
		t.Fatal("coder2 blocked coder's request")
	}
	if err := b.AcceptRequest(ctx, "agy-client-impl", "coder"); err != nil {
		t.Fatalf("the target could not accept: %v", err)
	}
	if _, err := b.CompleteRequest(ctx, "agy-client-impl", "coder2", "done"); err == nil {
		t.Fatal("coder2 completed coder's request")
	}

	// Two claims at the same moment: exactly one owner.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, who := range []string{"coder", "coder2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = b.BoardClaim(ctx, "notify-setup", who, "")
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("two claims at once: %v, %v (want exactly one refused)", errs[0], errs[1])
	}
}
