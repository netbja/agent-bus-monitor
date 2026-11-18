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
	if err != nil || len(thread) != 4 || thread[1].Type != CmdBlocked || thread[2].Type != CmdBlocked || thread[3].Ref != id || thread[3].Message != "finished\npreuves ✓" || thread[3].Target != "sender" {
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

// blockedEntries returns the "blocked" cmd entries on the project's stream.
func blockedEntries(t *testing.T, b *Bus) []map[string]interface{} {
	t.Helper()
	msgs, err := b.r.XRange(context.Background(), StreamKey(b.project, "cmd"), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]interface{}
	for _, m := range msgs {
		if m.Values["type"] == "blocked" {
			out = append(out, m.Values)
		}
	}
	return out
}

func TestBlockNotifiesRequesterOnce(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	id, err := b.Request(ctx, "task", "boss", "dev", "", "do it", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := b.BlockRequest(ctx, "task", "dev", "need spec"); err != nil {
			t.Fatal(err)
		}
	}
	got := blockedEntries(t, b)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 blocked entry, got %d", len(got))
	}
	e := got[0]
	if e["from"] != "dev" || e["target"] != "boss" || e["ref"] != id || e["command"] != "need spec" || e["task"] != "task" || e["text_complete"] != "yes" {
		t.Fatalf("bad blocked entry %v", e)
	}
}

func TestBlockNewReasonNotifiesAgain(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "task", "boss", "dev", "", "do it", time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"one", "one", "two"} {
		if err := b.BlockRequest(ctx, "task", "dev", reason); err != nil {
			t.Fatal(err)
		}
	}
	if got := blockedEntries(t, b); len(got) != 2 || got[1]["command"] != "two" {
		t.Fatalf("want 2 entries ending in reason two, got %v", got)
	}
}

func TestBlockAfterAcceptNotifies(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "task", "boss", "dev", "", "do it", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "task", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "task", "dev", "stuck"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "task", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "task", "dev", "stuck"); err != nil {
		t.Fatal(err)
	}
	if got := blockedEntries(t, b); len(got) != 2 {
		t.Fatalf("block after accept must notify each time, got %d", len(got))
	}
}

func TestBlockedEntryWakesRequester(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if err := b.EnsureGroup(ctx, "boss", DefaultHorizon); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "task", "boss", "dev", "", "do it", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := b.BlockRequest(ctx, "task", "dev", "need spec"); err != nil {
		t.Fatal(err)
	}
	o, err := b.Obligation(ctx, "boss", DefaultHorizon)
	if err != nil {
		t.Fatal(err)
	}
	if o.Unread != 1 {
		t.Fatalf("requester Unread=%d, want 1", o.Unread)
	}
}

func TestCompleteRequestRecordsCommit(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "chg-t1-1", "conductor", "kimi", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "chg-t1-1", "kimi"); err != nil {
		t.Fatal(err)
	}
	id, err := b.CompleteRequestAt(ctx, "chg-t1-1", "kimi", "done", "abc1234", "mission/x")
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.CompleteRequestAt(ctx, "chg-t1-1", "kimi", "done", "fff9999", "other")
	if err != nil || again != id {
		t.Fatalf("repeat done: %q %v, want %q", again, err, id)
	}
	board, _ := b.Board(ctx)
	r := board["chg-t1-1"].Request
	if r.Commit != "abc1234" || r.Branch != "mission/x" {
		t.Fatalf("commit/branch = %q/%q", r.Commit, r.Branch)
	}
}

// request-lost-body (master, concierge-memory-opinion 98 h old): a request
// whose body retention removed can never be accepted, and done needed an
// accept, so nobody could close it. Its target or its sender may now close
// it, unaccepted, with a reason; the entry says why. Anyone else is still
// refused, and a request whose body is there still needs an accept.
func TestRequestWithALostBodyCanBeClosed(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	for _, task := range []string{"lost-a", "lost-b", "kept"} {
		if _, err := b.Request(ctx, task, "master", "dev", "", "body", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.r.XTrimMaxLen(ctx, StreamKey(b.project, "cmd"), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "kept2", "master", "dev", "", "body", 0); err != nil { // its body is there
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "lost-a", "coder", "not mine"); err == nil {
		t.Fatal("a third agent closed a request")
	}
	if _, err := b.CompleteRequest(ctx, "lost-a", "dev", "body lost to retention; closed"); err != nil {
		t.Fatalf("the target could not close it: %v", err)
	}
	if _, err := b.CompleteRequest(ctx, "lost-b", "master", "body lost to retention; dropped"); err != nil {
		t.Fatalf("the sender could not close it: %v", err)
	}
	if _, err := b.CompleteRequest(ctx, "kept2", "dev", "done"); err == nil {
		t.Fatal("an intact request was completed without an accept")
	}
	if _, err := b.CompleteRequest(ctx, "kept2", "master", "done"); err == nil {
		t.Fatal("the sender completed an intact request")
	}
	// The sender never completes a request its target accepted.
	if err := b.AcceptRequest(ctx, "kept2", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "kept2", "master", "done"); err == nil {
		t.Fatal("the sender completed an accepted request")
	}
	if err := b.AcceptRequest(ctx, "kept", "dev"); err == nil || !strings.Contains(err.Error(), "agentbus request done") {
		t.Fatalf("the refusal does not say how to close it: %v", err)
	}
	board, err := b.Board(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"lost-a", "lost-b"} {
		e := board[task]
		if e.State != "done" || e.Request.ClosedUnaccepted != "body missing" || e.Request.ResponseID == "" {
			t.Fatalf("%s: %+v %+v", task, e, e.Request)
		}
	}
	// The closing reply goes to the other side: the sender's close tells the
	// target, the target's tells the sender (foureyes).
	for task, want := range map[string]string{"lost-a": "master", "lost-b": "dev"} {
		msgs, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), board[task].Request.ResponseID, board[task].Request.ResponseID).Result()
		if err != nil || len(msgs) != 1 || msgs[0].Values["target"] != want {
			t.Fatalf("%s's closing reply: %v %v, want target %s", task, msgs, err, want)
		}
	}
}

// An expired request, never accepted, is closable the same way.
func TestExpiredRequestCanBeClosed(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Request(ctx, "late", "master", "dev", "", "body", 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := b.AcceptRequest(ctx, "late", "dev"); err == nil {
		t.Fatal("an expired request was accepted")
	}
	if _, err := b.CompleteRequest(ctx, "late", "dev", "expired before I saw it"); err != nil {
		t.Fatalf("the target could not close it: %v", err)
	}
	board, _ := b.Board(ctx)
	if e := board["late"]; e.State != "done" || e.Request.ClosedUnaccepted != "expired" {
		t.Fatalf("%+v %+v", e, e.Request)
	}
}
