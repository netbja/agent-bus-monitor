package main

import (
	"context"
	"testing"
)

// --live derives in the caller's checkout and writes nothing.
func TestMergeQueueLiveWritesNothing(t *testing.T) {
	c, b := mqChore(t, "git@github.com:o/r.git")
	rec := c.derive(context.Background())
	if len(rec.Items) != 1 || rec.Reason != "" {
		t.Fatalf("%+v", rec)
	}
	if got, _ := b.MergeQueue(context.Background()); got.AttemptedAt != 0 {
		t.Fatalf("live wrote the record: %+v", got)
	}
}
