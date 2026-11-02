package bus

import (
	"context"
	"errors"
	"regexp"
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

// Every error_reply of the request script is a refusal: a new one added to
// the script cannot drift past requestRefusals (foureyes).
func TestEveryScriptRefusalIsListed(t *testing.T) {
	re := regexp.MustCompile(`error_reply\('([^']*)'\)`)
	found := re.FindAllStringSubmatch(requestTransitionSrc, -1)
	if len(found) < 8 {
		t.Fatalf("only %d error replies found in the script", len(found))
	}
	for _, m := range found {
		if err := requestError(errors.New(m[1])); !errors.Is(err, ErrRequestRefused) || err.Error() != m[1] {
			t.Errorf("the script's %q is not a refusal (%v)", m[1], err)
		}
	}
}
