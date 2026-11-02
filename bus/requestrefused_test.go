package bus

import (
	"context"
	"errors"
	"testing"
)

// The request script's own refusals are ErrRequestRefused, with their
// message unchanged; any other Redis error (LOADING, an unknown command) is
// not, so a caller keeps the entry and retries (reviewer-codex).
func TestRequestRefusalsAreTyped(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	err := b.AcceptRequest(ctx, "no-such-task", "peer")
	if !errors.Is(err, ErrRequestRefused) || err.Error() != "unknown request" {
		t.Fatalf("unknown task = %v, want ErrRequestRefused saying unknown request", err)
	}
	if _, err := b.Request(ctx, "t1", "master", "peer", "", "body", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "t1", "someone-else"); !errors.Is(err, ErrRequestRefused) {
		t.Fatalf("not the target = %v", err)
	}
	if _, err := b.CompleteRequest(ctx, "t1", "peer", "x"); !errors.Is(err, ErrRequestRefused) {
		t.Fatalf("complete before accept = %v", err)
	}
	other := b.r.Do(ctx, "NO-SUCH-COMMAND").Err() // a server error that is no refusal
	if other == nil || errors.Is(requestError(other), ErrRequestRefused) {
		t.Fatalf("a server error %v reads as a refusal", other)
	}
	loading := errors.New("LOADING Redis is loading the dataset in memory")
	if errors.Is(requestError(loading), ErrRequestRefused) {
		t.Fatal("LOADING reads as a refusal")
	}
}
