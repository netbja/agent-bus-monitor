package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// request accept --branch and request branch record the work branch
// (code-intel §7); a bare word after accept, or a missing branch, is a
// usage error.
func TestRequestWorkBranchCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.BoardKey(b.Project()))
	for _, task := range []string{"t1", "t2"} {
		if err := runRequest(ctx, b, "master", []string{"send", task, "dev", "body"}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	wb := func(task string) string {
		board, err := b.Board(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return board[task].Request.WorkBranch
	}
	if err := runRequest(ctx, b, "dev", []string{"accept", "t1", "--branch", "dev/one"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := wb("t1"); got != "dev/one" {
		t.Fatalf("accept --branch: %q", got)
	}
	for _, args := range [][]string{{"accept", "t2", "extra"}, {"branch", "t1"}, {"branch", "t1", "a", "b"}, {"branch"}} {
		if err := runRequest(ctx, b, "dev", args, io.Discard); err == nil || !strings.HasPrefix(err.Error(), "usage: request ") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if err := runRequest(ctx, b, "dev", []string{"branch", "t1", "dev/two"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := wb("t1"); got != "dev/two" {
		t.Fatalf("request branch: %q", got)
	}
	if err := runRequest(ctx, b, "dev", []string{"accept", "t2"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := wb("t2"); got != "" {
		t.Fatalf("a plain accept: %q", got)
	}
}
