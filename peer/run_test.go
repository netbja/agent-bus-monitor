package peer

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func runnerBus(t *testing.T) *bus.Bus {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "peer"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	return b
}

// syncBuf is the pane's transcript, written by the runner's goroutine.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func startRunner(t *testing.T, b *bus.Bus, tr Turn) (*syncBuf, func()) {
	t.Helper()
	out := &syncBuf{}
	r := &Runner{Bus: b, Agent: "local", Consumer: "test", Turn: tr, RetryEvery: 20 * time.Millisecond, LeaseWait: time.Second, Out: out}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	return out, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the runner did not stop")
		}
	}
}

// waitThread waits until thread id has n entries.
func waitThread(t *testing.T, b *bus.Bus, id string, n int) []bus.Event {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		evs, err := b.Thread(context.Background(), id)
		if err == nil && len(evs) >= n {
			return evs
		}
	}
	t.Fatalf("thread %s never reached %d entries", id, n)
	return nil
}

func states(t *testing.T, b *bus.Bus) []string {
	t.Helper()
	evs, _, err := b.Recent(context.Background(), []string{"status"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Agent == "local" {
			out = append(out, e.State)
		}
	}
	return out
}

// An entry sent while the peer was down is drained; one sent later is
// received; each gets a reply on its thread, a report, working then idle.
func TestRunnerAnswersOnTheThread(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	first, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "What does main.go do?")
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, answerJSON })
	out, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	evs := waitThread(t, b, first, 2)
	if evs[1].Type != bus.CmdReply || evs[1].From != "local" || evs[1].Target != "master" || evs[1].Message != "board.go holds the board." {
		t.Fatalf("reply = %+v", evs[1])
	}
	second, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "And sub/b.txt?")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, second, 2)
	// A reply on an existing thread is answered on that same thread.
	if _, err := b.Cmd(ctx, "master", "local", bus.CmdReply, first, "Thanks, and the board?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, first, 4)
	stop()
	if got := strings.Join(states(t, b), " "); !strings.Contains(got, "working idle working idle working idle") {
		t.Fatalf("states = %s", got)
	}
	reports, _, err := b.Recent(ctx, []string{"report"}, 10)
	if err != nil || len(reports) != 3 || !strings.Contains(reports[0].Message, "master") {
		t.Fatalf("reports = %+v %v", reports, err)
	}
	if !strings.Contains(out.String(), "What does main.go do?") || !strings.Contains(out.String(), "board.go holds the board.") {
		t.Fatalf("transcript:\n%s", out)
	}
	if armed, _ := b.Armed(ctx, "local"); armed {
		t.Fatal("the lease outlived the runner")
	}
}

// An endpoint fault keeps the entry: blocked with the reason, the entry
// still pending, retried until the endpoint answers; then idle.
func TestRunnerBlocksWhileTheEndpointIsDown(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) {
		if calls.Add(1) <= 3 {
			return 503, "loading model"
		}
		return 200, answerJSON
	})
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	id, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "q")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, id, 2)
	stop()
	got := states(t, b)
	if !strings.Contains(strings.Join(got, " "), "working blocked") || got[len(got)-1] != "idle" {
		t.Fatalf("states = %v, want blocked during the fault, idle after", got)
	}
	evs, _ := b.Thread(ctx, id)
	if len(evs) != 2 {
		t.Fatalf("thread = %d entries, want one reply", len(evs))
	}
}

// A second peer under one name cannot take the lease, says so, and exits.
func TestRunnerRefusesASecondPeer(t *testing.T) {
	b := runnerBus(t)
	held, err := b.TakeReceiver(context.Background(), "local", "first", bus.DefaultHorizon, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	r := &Runner{Bus: b, Agent: "local", Consumer: "second", LeaseWait: 100 * time.Millisecond, Out: &syncBuf{}}
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "another") {
		t.Fatalf("Run = %v, want refused: another peer holds the lease", err)
	}
}

// A tracked request (a directive with a task) is accepted, answered and
// completed: the board entry is done and the response is on its thread
// (reviewer-codex).
func TestRunnerCompletesATrackedRequest(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, answerJSON })
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	id, err := b.Request(ctx, "explain-board", "master", "local", "", "Explain bus/board.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	evs := waitThread(t, b, id, 2)
	stop()
	if evs[1].Type != bus.CmdReply || evs[1].Message != "board.go holds the board." {
		t.Fatalf("response = %+v", evs[1])
	}
	board, err := b.Board(ctx)
	if err != nil || board["explain-board"].State != "done" {
		t.Fatalf("board = %+v %v, want the request done", board["explain-board"], err)
	}
	if len(evs) != 2 {
		t.Fatalf("thread = %d entries, want the request and one response", len(evs))
	}
}

// A wake=no entry is a notice (a gate closed without a verdict, a person's
// close): acknowledged, no turn, no reply (foureyes). A reply from another
// local-llm peer gets no turn either, so two peers never answer each other
// forever.
func TestRunnerTurnsOnlyOnWhatAsksForOne(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	if _, err := b.Status(ctx, "other", "idle", "", bus.AgentIdent{Client: "local-llm"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { calls.Add(1); return 200, answerJSON })
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	notice, err := b.CmdNoWake(ctx, "master", "local", bus.CmdReply, "9-0", "gate dmk closed without a verdict")
	if err != nil {
		t.Fatal(err)
	}
	fromPeer, err := b.Cmd(ctx, "other", "local", bus.CmdReply, "8-0", "what do you think?")
	if err != nil {
		t.Fatal(err)
	}
	last, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "q")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, last, 2) // entries are handled in order: the two before are done
	stop()
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d turns, want only the directive's", n)
	}
	for _, id := range []string{"9-0", "8-0"} {
		if evs, _ := b.Thread(ctx, id); len(evs) != 1 {
			t.Errorf("thread %s = %d entries, want no reply (notice %s, peer %s)", id, len(evs), notice, fromPeer)
		}
	}
}

// Once the reply is sent, a failed report is logged, never a reason to
// answer again (foureyes).
func TestRunnerAnswersOnceWhenTheReportFails(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, answerJSON })
	var fails atomic.Int32
	out := &syncBuf{}
	r := &Runner{Bus: b, Agent: "local", Consumer: "test", Turn: newTurn(t, srv, time.Minute), RetryEvery: 20 * time.Millisecond, LeaseWait: time.Second, Out: out,
		report: func(ctx context.Context, agent, kind, msg string) (string, error) {
			if fails.Add(1) == 1 {
				return "", context.DeadlineExceeded
			}
			return b.Report(ctx, agent, kind, msg)
		}}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.Run(rctx) }()
	id, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "q")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, id, 2)
	time.Sleep(300 * time.Millisecond) // a second answer would come now
	cancel()
	<-done
	if evs, _ := b.Thread(ctx, id); len(evs) != 2 {
		t.Fatalf("thread = %d entries, want one reply", len(evs))
	}
	if !strings.Contains(out.String(), "report") {
		t.Fatalf("the failed report is not said:\n%s", out)
	}
}

// A server error on accept (LOADING while Redis reloads) is no refusal: the
// request stays pending and is answered once the broker serves again
// (reviewer-codex).
func TestRunnerKeepsARequestOnAServerError(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, answerJSON })
	var tries atomic.Int32
	r := &Runner{Bus: b, Agent: "local", Consumer: "test", Turn: newTurn(t, srv, time.Minute), RetryEvery: 20 * time.Millisecond, LeaseWait: time.Second, Out: &syncBuf{},
		accept: func(ctx context.Context, task, agent string) error {
			if tries.Add(1) == 1 {
				return errors.New("LOADING Redis is loading the dataset in memory")
			}
			return b.AcceptRequest(ctx, task, agent)
		}}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.Run(rctx) }()
	id, err := b.Request(ctx, "t-loading", "master", "local", "", "Explain bus/board.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, id, 2)
	cancel()
	<-done
	board, _ := b.Board(ctx)
	if board["t-loading"].State != "done" || tries.Load() < 2 {
		t.Fatalf("board = %+v after %d accepts, want done after a retry", board["t-loading"], tries.Load())
	}
}

// An error that does not heal (here an accept that always fails with a
// server error) leaves the entry pending and backs the peer off, never a hot
// loop of re-drains (foureyes).
func TestRunnerBacksOffAfterAnError(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, answerJSON })
	var tries atomic.Int32
	r := &Runner{Bus: b, Agent: "local", Consumer: "test", Turn: newTurn(t, srv, time.Minute), RetryEvery: 20 * time.Millisecond, LeaseWait: time.Second, Out: &syncBuf{},
		accept: func(context.Context, string, string) error {
			tries.Add(1)
			return errors.New("ERR something persistent")
		}}
	if _, err := b.Request(ctx, "t-stuck", "master", "local", "", "q", 0); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(rctx) }()
	// The lease stays held through every retry delay (reviewer-codex): the
	// dispatcher never sees the peer unarmed while Redis is fine.
	for tries.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	unarmed := 0
	for rctx.Err() == nil {
		if armed, err := b.Armed(ctx, "local"); err == nil && !armed && rctx.Err() == nil {
			unarmed++
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if unarmed != 0 {
		t.Fatalf("the peer was unarmed in %d samples during its backoff", unarmed)
	}
	if n := tries.Load(); n < 2 || n > 3 {
		t.Fatalf("%d tries in 2.5 s, want a retry under the lease after a backoff (1 s, then 2 s)", n)
	}
	if board, _ := b.Board(ctx); board["t-stuck"].State != "requested" {
		t.Fatalf("board = %+v, want the request still pending", board["t-stuck"])
	}
}

// A request already completed and delivered again is acknowledged without a
// turn and without a second reply (foureyes).
func TestRunnerAcknowledgesADoneRequest(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	id, err := b.Request(ctx, "t-done", "master", "local", "", "q", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "t-done", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "t-done", "local", "answered before"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { calls.Add(1); return 200, answerJSON })
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	last, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "next")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, last, 2) // in order: the done request was handled before
	stop()
	if evs, _ := b.Thread(ctx, id); len(evs) != 2 || calls.Load() != 1 {
		t.Fatalf("thread = %d entries, %d turns: want the first response only and one turn (the next directive)", len(evs), calls.Load())
	}
}

// An endpoint answering more than a bus entry holds gets one reply (cut),
// and the entry is acknowledged: the next one is answered, and no turn is
// run twice (SEC-LLM-1: the refusal used to leave it pending, retried forever).
func TestRunnerAnswersAnOversizedAnswerOnce(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { calls.Add(1); return 200, longAnswer(70000) })
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	first, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "write me a novel")
	if err != nil {
		t.Fatal(err)
	}
	evs := waitThread(t, b, first, 2)
	if !strings.Contains(evs[1].Message, "[answer cut at ") {
		t.Fatalf("the reply is not the cut answer: ...%s", evs[1].Message[max(0, len(evs[1].Message)-60):])
	}
	second, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "next")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, second, 2)
	time.Sleep(200 * time.Millisecond) // a retried first entry would turn again now
	stop()
	if evs, _ := b.Thread(ctx, first); len(evs) != 2 || calls.Load() != 2 {
		t.Fatalf("thread = %d entries, %d turns: want one reply and one turn per entry", len(evs), calls.Load())
	}
}

// An entry whose reply the bus refuses for good (here a sender that is no
// agent name, which only raw Redis can write) is said, reported and
// acknowledged: one turn, never retried, the next entry answered.
func TestRunnerAcknowledgesAReplyTheBusRefuses(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	raw, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if err := raw.XAdd(ctx, &redis.XAddArgs{Stream: b.Project() + ":cmd", Values: map[string]any{
		"from": "Not A Name", "target": "local", "type": bus.CmdDirective, "ref": "", "command": "q", "text_complete": "yes"}}).Err(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { calls.Add(1); return 200, answerJSON })
	out, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	next, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "next")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, next, 2)
	time.Sleep(200 * time.Millisecond)
	stop()
	if calls.Load() != 2 {
		t.Fatalf("%d turns, want 2: the refused reply was retried", calls.Load())
	}
	if !strings.Contains(out.String(), "cannot be sent") {
		t.Fatalf("the refusal is not said:\n%s", out)
	}
	reports, _, err := b.Recent(ctx, []string{"report"}, 10)
	found := false
	for _, r := range reports {
		found = found || strings.Contains(r.Message, "could not send")
	}
	if err != nil || !found {
		t.Fatalf("no report of the unsent answer: %+v %v", reports, err)
	}
}

// hostile carries what a model, an endpoint or a bus sender could aim at the
// person's terminal: OSC 52 (clipboard), a CSI clear, a bell, DEL, a C1 CSI.
const hostile = "a\x1b]52;c;ZXZpbA==\x07b\x1b[2Jc\x7fd\u009b31me\r\nf\tg"

// paneSafe fails on any byte the terminal would act on: below 0x20 other
// than \n and \t, DEL, or a C1 control (SEC-LLM-2).
func paneSafe(t *testing.T, what, s string) {
	t.Helper()
	for i, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s holds control %U at byte %d: %q", what, r, i, s)
		}
	}
}

// Every pane line is stripped of terminal controls, whoever wrote its text;
// newlines and tabs stay, so an answer keeps its layout.
func TestRunnerPaneCarriesNoTerminalControl(t *testing.T) {
	out := &syncBuf{}
	(&Runner{Out: out}).say("from master: " + hostile)
	paneSafe(t, "the pane", out.String())
	if !strings.Contains(out.String(), "f\tg") || strings.Count(out.String(), "\n") != 2 {
		t.Fatalf("the layout is not kept: %q", out)
	}
}

// An endpoint's error body never reaches the bus: it can echo a configured
// URL path or a request detail. The blocked status and the refusal reply
// carry fixed words and the status only (R3).
func TestRunnerPublishesNoEndpointBody(t *testing.T) {
	b := runnerBus(t)
	ctx := context.Background()
	const leak = "LEAK /v1/tenant-7f3a/secret-path"
	var calls atomic.Int32
	srv, _ := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		switch n := calls.Add(1); {
		case n <= 2:
			return 500, leak // blocked, then healed
		case strings.Contains(req.Messages[1].Content, "refuse me"):
			return 400, leak
		}
		return 200, answerJSON
	})
	_, stop := startRunner(t, b, newTurn(t, srv, time.Minute))
	first, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "q")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, b, first, 2)
	second, err := b.Cmd(ctx, "master", "local", bus.CmdDirective, "", "refuse me")
	if err != nil {
		t.Fatal(err)
	}
	evs := waitThread(t, b, second, 2)
	stop()
	if !strings.Contains(evs[1].Message, "400") {
		t.Fatalf("the refusal does not give its status: %q", evs[1].Message)
	}
	all, _, err := b.Recent(ctx, []string{"status", "cmd", "report"}, 200)
	if err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, e := range all {
		blocked = blocked || e.State == "blocked"
		if strings.Contains(e.Message, "LEAK") || strings.Contains(e.Message, "secret-path") {
			t.Errorf("entry %s carries the body: %q", e.ID, e.Message)
		}
	}
	if !blocked {
		t.Fatal("the 500 never declared blocked")
	}
}

// Residual a of the PR 259 security re-check: Unicode format runes (bidi
// overrides and isolates, zero-width characters, every other Cf) reach
// neither the pane nor the answer put on the bus: display spoofing only, but
// no reader should see a line reversed by one.
func TestPaneAndAnswerCarryNoFormatRune(t *testing.T) {
	const spoof = "ok\u202egnp.exe\u202c \u2066x\u2069 zero\u200bwidth soft\u00adhyphen\ufeffend\n\tkept"
	out := &syncBuf{}
	(&Runner{Out: out}).say(spoof)
	for _, s := range []string{out.String(), capAnswer(spoof)} {
		for _, r := range s {
			if hidesText(r) {
				t.Fatalf("a format rune %U reached %q", r, s)
			}
		}
		if !strings.Contains(s, "\n\tkept") || !strings.Contains(s, "gnp.exe") {
			t.Fatalf("the text or its layout was lost: %q", s)
		}
	}
}

// foureyes and master on db88a3f: only the runes that reorder or hide text
// go; the zero-width joiner and non-joiner (an emoji family is one picture, a
// Persian word keeps its spelling), the tag characters of a subdivision flag
// and the Arabic number sign stay.
func TestFormatStripKeepsJoiners(t *testing.T) {
	const family = "\U0001F468\u200d\U0001F469\u200d\U0001F467"
	const persian = "\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645"
	const england = "\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F" // tag characters (Cf)
	const arabicNumber = "\u0600\u0661\u0662"                                                // the Arabic number sign (Cf) before its digits
	for _, s := range []string{family, persian, england, arabicNumber} {
		if got := stripFormat(s); got != s {
			t.Errorf("stripFormat(%q) = %q: a joiner was dropped", s, got)
		}
	}
	if got := stripFormat("a\u200bb\u2060c\u202ed\u061ce\u00adf\ufeffg\u2066h\u200fi\u180ej"); got != "abcdefghij" {
		t.Errorf("the other format runes stay: %q", got)
	}
	// foureyes on ba39026: the deprecated format controls, the interlinear
	// annotation marks, the shorthand and the musical format controls render
	// invisibly too: each range's first and last go.
	if got := stripFormat("a\u206ab\u206fc\ufff9d\ufffbe\U0001bca0f\U0001bca3g\U0001d173h\U0001d17ai"); got != "abcdefghi" {
		t.Errorf("the other format runes stay: %q", got)
	}
}
