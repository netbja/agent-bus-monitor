package bus

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestLifecycle(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	id, err := b.Request(ctx, "task", "sender", "dev", "", "é漢🙂\n  multiline\ttext", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := board["task"]
	r := e.Request
	if e.State != "requested" || e.Owner != "dev" || r.ID != id || r.Thread != id || r.CreatedAt == 0 || r.ExpiresAt <= r.CreatedAt || r.AcceptedAt != 0 || r.Availability != "retained" {
		t.Fatalf("bad request %+v %+v", e, r)
	}
	if _, err := b.Request(ctx, "task", "sender", "dev", "", "retry", 0); err == nil || !strings.Contains(err.Error(), id) {
		t.Fatalf("duplicate lost identity %v", err)
	}
	if err := b.AcceptRequest(ctx, "task", "peer"); err == nil {
		t.Fatal("wrong target accepted")
	}
	if _, err := b.CompleteRequest(ctx, "task", "dev", "done"); err == nil {
		t.Fatal("done without acceptance")
	}
	if err := b.BoardClaim(ctx, "task", "dev", ""); err == nil {
		t.Fatal("claim bypassed explicit acceptance")
	}
	if err := b.BoardState(ctx, "task", "done"); err == nil {
		t.Fatal("state bypassed explicit completion")
	}
	if err := b.BlockRequest(ctx, "task", "dev", "need specification"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "task", "dev"); err != nil {
		t.Fatal(err)
	}
	board, _ = b.Board(ctx)
	accepted := board["task"].Request.AcceptedAt
	if accepted == 0 || board["task"].Request.BlockedReason != "" {
		t.Fatal(board)
	}
	if err := b.BlockRequest(ctx, "task", "dev", "dependency"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "task", "dev", "done"); err == nil {
		t.Fatal("completed blocked request")
	}
	if err := b.AcceptRequest(ctx, "task", "dev"); err != nil {
		t.Fatal(err)
	}
	response, err := b.CompleteRequest(ctx, "task", "dev", "finished\npreuves ✓")
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.CompleteRequest(ctx, "task", "dev", "finished\npreuves ✓")
	if err != nil || again != response {
		t.Fatalf("duplicate completion: %q %v", again, err)
	}
	board, _ = b.Board(ctx)
	e = board["task"]
	r = e.Request
	if e.State != "done" || r.AcceptedAt != accepted || r.ResponseID != response || r.RespondedAt == 0 || r.Delivery != "queued" {
		t.Fatalf("lifecycle %+v %+v", e, r)
	}
	thread, err := b.Thread(ctx, id)
	if err != nil || len(thread) != 2 || thread[1].Ref != id || thread[1].Message != "finished\npreuves ✓" || thread[1].Target != "sender" {
		t.Fatalf("thread %+v %v", thread, err)
	}
}

func TestConcurrentBoardClaimAndRequest(t *testing.T) {
	for _, requests := range []bool{false, true} {
		t.Run(map[bool]string{false: "claim", true: "request"}[requests], func(t *testing.T) {
			b := dialTest(t)
			ctx := context.Background()
			var wins atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for _, owner := range []string{"alice", "bob"} {
				wg.Add(1)
				go func(owner string) {
					defer wg.Done()
					<-start
					var err error
					if requests {
						_, err = b.Request(ctx, "task", owner, "dev", "", "body", 0)
					} else {
						err = b.BoardClaim(ctx, "task", owner, "")
					}
					if err == nil {
						wins.Add(1)
					}
				}(owner)
			}
			close(start)
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("concurrent wins %d", wins.Load())
			}
			if requests {
				n, err := b.r.XLen(ctx, StreamKey(b.project, "cmd")).Result()
				if err != nil || n != 1 {
					t.Fatalf("duplicate publications %d %v", n, err)
				}
			}
		})
	}
}

func TestRequestTrimmedBeforeRead(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "task", "sender", "dev", "custom-thread", "body", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.r.XTrimMaxLen(ctx, StreamKey(b.project, "cmd"), 0).Err(); err != nil {
		t.Fatal(err)
	}
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := board["task"].Request
	if r.Availability != "missing" || r.Delivery != "queued" || r.Thread != "custom-thread" || board["task"].State != "requested" {
		t.Fatal(r)
	}
	if err := b.AcceptRequest(ctx, "task", "dev"); err == nil {
		t.Fatal("accepted trimmed request")
	}
}

func TestTextFidelityAndLimits(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	body := strings.Repeat("é漢🙂\n  proof\t", 500)
	id, err := b.Report(ctx, "dev", ReportNote, body)
	if err != nil {
		t.Fatal(err)
	}
	report, err := b.ReportByID(ctx, id)
	if err != nil || report.Full != body || report.TextComplete != "yes" {
		t.Fatalf("report fidelity: %v", err)
	}
	if _, err := b.AppendVerdict(ctx, Verdict{Author: "dev", Reviewer: "peer", Subject: "test", Decision: "approve", Message: body}); err != nil {
		t.Fatal(err)
	}
	vs, err := b.Verdicts(ctx, "test")
	if err != nil || len(vs) != 1 || vs[0].Full != body || vs[0].TextComplete != "yes" {
		t.Fatalf("verdict fidelity: %v", err)
	}
	cid, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", body)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := b.Thread(ctx, cid)
	if err != nil || len(evs) != 1 || evs[0].Message != body || evs[0].TextComplete != "yes" {
		t.Fatalf("cmd fidelity: %v", err)
	}
	for _, bad := range []string{strings.Repeat("🙂", Limits().FullRunes+1), string([]byte{0xff})} {
		if _, err := b.Report(ctx, "dev", ReportNote, bad); err == nil {
			t.Fatal("invalid report accepted")
		}
		if _, err := b.AppendVerdict(ctx, Verdict{Author: "dev", Reviewer: "peer", Subject: "test", Decision: "approve", Message: bad}); err == nil {
			t.Fatal("invalid verdict accepted")
		}
	}
	if _, err := b.Cmd(ctx, "sender", "dev", CmdDirective, "", strings.Repeat("é", CmdMaxRunes+1)); err == nil {
		t.Fatal("oversize cmd accepted")
	}
	for _, kind := range []string{"report", "cmd"} {
		n, err := b.r.XLen(ctx, StreamKey(b.project, kind)).Result()
		if err != nil || n != 1 {
			t.Fatalf("rejected message stored: %s %d %v", kind, n, err)
		}
	}
	n, err := b.r.XLen(ctx, VerdictsKey(b.project)).Result()
	if err != nil || n != 1 {
		t.Fatalf("rejected verdict stored %d %v", n, err)
	}
	legacy := ParseEntry(StreamKey(b.project, "report"), "1-0", map[string]string{"message": "legacy…"})
	if legacy.TextComplete != "" {
		t.Fatal("invented completeness")
	}
}
