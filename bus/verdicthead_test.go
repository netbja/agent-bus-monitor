package bus

import (
	"context"
	"strings"
	"testing"
)

// forge-delivery: a verdict may name the head it read (full SHA-1 or SHA-256,
// lower hex), additive; a short or malformed one is refused; none = no field.
func TestVerdictHead(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	sha1 := strings.Repeat("a1", 20)
	sha256 := strings.Repeat("b2", 32)
	for _, h := range []string{sha1, sha256, ""} {
		if _, err := b.AppendVerdict(ctx, Verdict{Subject: "s-head", Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: h}); err != nil {
			t.Fatalf("head %q: %v", h, err)
		}
	}
	for _, bad := range []string{"a1b2c3d", strings.ToUpper(sha1), sha1 + "0", strings.Repeat("g", 40)} {
		if _, err := b.AppendVerdict(ctx, Verdict{Subject: "s-head", Author: "coder", Reviewer: "foureyes", Decision: "approve", Head: bad}); err == nil {
			t.Errorf("head %q accepted", bad)
		}
	}
	vs, err := b.Verdicts(ctx, "s-head")
	if err != nil || len(vs) != 3 || vs[0].Head != sha1 || vs[1].Head != sha256 || vs[2].Head != "" {
		t.Fatalf("Verdicts = %+v, %v", vs, err)
	}
	after, err := b.VerdictsAfter(ctx, "", 10)
	if err != nil || len(after) < 3 || after[0].Head != sha1 {
		t.Fatalf("VerdictsAfter = %+v, %v", after, err)
	}
	if !ValidHead(sha1) || ValidHead("a1b2c3d") {
		t.Fatal("ValidHead")
	}
}
