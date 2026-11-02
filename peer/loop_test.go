package peer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func toolCall(id, name, args string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, id, name, args)
}

func newTurn(t *testing.T, srv *httptest.Server, timeout time.Duration) Turn {
	return Turn{Client: Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}, Tools: toolsFixture(t),
		Prompt: "You are local, read-only.", MaxCalls: 8, Timeout: timeout}
}

var entry = bus.Event{ID: "1-0", From: "master", Target: "local", Type: "directive", Message: "What does main.go do?"}

// A turn: the prompt, the entry and its thread go in; tool calls run against
// the root; the final text comes back with the endpoint's usage summed.
func TestTurnRunsToolsThenAnswers(t *testing.T) {
	srv, seen := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		if len(req.Messages) == 2 {
			return 200, toolCall("c1", "read_file", `{"path":"main.go"}`)
		}
		return 200, answerJSON
	})
	res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "1-0 master -> local [directive]: What does main.go do?\n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "board.go holds the board." || res.Calls != 1 || res.Usage.PromptTokens != 130 || res.NoTools || res.TimedOut {
		t.Fatalf("result = %+v", res)
	}
	first := (*seen)[0]
	if first.Messages[0].Role != "system" || !strings.Contains(first.Messages[0].Content, "read-only") ||
		!strings.Contains(first.Messages[1].Content, "What does main.go do?") || !strings.Contains(first.Messages[1].Content, "master") ||
		!strings.Contains(first.Messages[1].Content, "[directive]") {
		t.Fatalf("first request = %+v", first.Messages)
	}
	second := (*seen)[1].Messages
	last := second[len(second)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" || !strings.Contains(last.Content, "func main()") {
		t.Fatalf("tool result = %+v", last)
	}
	if second[len(second)-2].Role != "assistant" || len(second[len(second)-2].ToolCalls) != 1 {
		t.Fatalf("the assistant's tool call is not echoed: %+v", second)
	}
}

// At most MaxCalls tool calls; then one last call without tools for the answer.
// A malformed call is an error result for the model and counts.
func TestTurnBoundsToolCalls(t *testing.T) {
	srv, seen := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		if len(req.Tools) == 0 {
			return 200, answerJSON
		}
		return 200, toolCall("c", "read_file", `{"path":`)
	})
	res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Calls != 8 || res.Text != "board.go holds the board." || len(*seen) != 9 {
		t.Fatalf("result = %+v after %d requests", res, len(*seen))
	}
	for _, m := range (*seen)[1].Messages {
		if m.Role == "tool" && !strings.HasPrefix(m.Content, "error:") {
			t.Fatalf("a malformed call's result = %q", m.Content)
		}
	}
}

// A server that refuses tools: the turn is answered once without them and
// the reply says so.
func TestTurnWithoutTools(t *testing.T) {
	srv, seen := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		if len(req.Tools) > 0 {
			return 400, `{"error":"model does not support tools"}`
		}
		return 200, answerJSON
	})
	res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "")
	if err != nil || !res.NoTools || !strings.Contains(res.Text, "board.go holds the board.") || !strings.Contains(res.Text, "without tools") || len(*seen) != 2 {
		t.Fatalf("result = %+v %v after %d requests", res, err, len(*seen))
	}
}

// A slow endpoint past the wall clock: the turn stops and says so.
func TestTurnTimesOut(t *testing.T) {
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { time.Sleep(300 * time.Millisecond); return 200, answerJSON })
	res, err := newTurn(t, srv, 50*time.Millisecond).Run(context.Background(), entry, "")
	if err != nil || !res.TimedOut || !strings.Contains(res.Text, "timed out") {
		t.Fatalf("result = %+v %v", res, err)
	}
}

// An endpoint fault is the one error a turn returns (the runner retries with
// the entry kept); a refusal or a bad answer is a reply saying why.
func TestTurnFailures(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	tr := Turn{Client: Client{URL: url + "/v1", Model: "m"}, Tools: toolsFixture(t), MaxCalls: 8, Timeout: time.Minute}
	var ef *EndpointError
	if _, err := tr.Run(context.Background(), entry, ""); !errors.As(err, &ef) {
		t.Fatalf("down = %v, want an EndpointError", err)
	}
	for body, want := range map[string]string{
		`{"choices":[`: "not a chat completion",
	} {
		srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, body })
		res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "")
		if err != nil || !strings.Contains(res.Text, want) {
			t.Errorf("%s = %+v %v", body, res, err)
		}
	}
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) {
		return 400, `{"error":"the request exceeds the available context size"}`
	})
	res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "")
	if err != nil || !strings.Contains(res.Text, "exceeds the available context size") {
		t.Fatalf("refused = %+v %v", res, err)
	}
}

func toolCalls(calls ...[3]string) string {
	var parts []string
	for _, c := range calls {
		parts = append(parts, fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}`, c[0], c[1], c[2]))
	}
	return `{"choices":[{"message":{"role":"assistant","content":"looking","tool_calls":[` + strings.Join(parts, ",") + `]}}]}`
}

// One answer carrying more calls than remain: calls past the bound get the
// bound's error and are never run; then the last call, without tools, ends
// the turn with its text even if the model still emits a tool call.
func TestTurnBoundWithinOneAnswerAndTheLastCall(t *testing.T) {
	n := 0
	srv, seen := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		n++
		switch {
		case len(req.Tools) == 0: // the last call: text plus a stray tool call
			return 200, `{"choices":[{"message":{"role":"assistant","content":"final words","tool_calls":[{"id":"z","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}]}}]}`
		case n <= 7:
			return 200, toolCall(fmt.Sprintf("c%d", n), "read_file", `{"path":"main.go"}`)
		default: // 7 made: three more in one answer
			return 200, toolCalls([3]string{"c8", "read_file", `{"path":"main.go"}`}, [3]string{"c9", "read_file", `{"path":"main.go"}`}, [3]string{"c10", "read_file", `{"path":"main.go"}`})
		}
	})
	res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "final words" || len(*seen) != 9 {
		t.Fatalf("result = %+v after %d requests, want the last call's text after 9", res, len(*seen))
	}
	results := map[string]string{}
	for _, m := range (*seen)[8].Messages {
		if m.Role == "tool" {
			results[m.ToolCallID] = m.Content
		}
	}
	if !strings.Contains(results["c8"], "func main()") {
		t.Errorf("the 8th call was not run: %q", results["c8"])
	}
	for _, id := range []string{"c9", "c10"} {
		if !strings.Contains(results[id], "bound of tool calls") || strings.Contains(results[id], "func main()") {
			t.Errorf("%s past the bound = %q, want the bound's error, never run", id, results[id])
		}
	}
}

// The wall clock holds through a tool: a tool that waits until the turn's
// deadline ends in a timed-out reply, and a named pipe read never holds the
// turn (reviewer-codex).
func TestTurnClockHoldsThroughTools(t *testing.T) {
	srv, _ := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		if len(req.Messages) == 2 {
			return 200, toolCall("c1", "bus_thread", `{"id":"1-0"}`)
		}
		return 200, answerJSON
	})
	tr := newTurn(t, srv, 100*time.Millisecond)
	tr.Tools.Thread = func(ctx context.Context, _ string) ([]bus.Event, error) { <-ctx.Done(); return nil, ctx.Err() }
	start := time.Now()
	res, err := tr.Run(context.Background(), entry, "")
	if err != nil || !res.TimedOut || time.Since(start) > 2*time.Second {
		t.Fatalf("slow tool = %+v %v after %s", res, err, time.Since(start))
	}

	srv, _ = fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		if len(req.Messages) == 2 {
			return 200, toolCall("c1", "read_file", `{"path":"pipe"}`)
		}
		return 200, answerJSON
	})
	tr = newTurn(t, srv, time.Minute)
	if err := syscall.Mkfifo(filepath.Join(tr.Tools.Root, "pipe"), 0o644); err != nil {
		t.Skip("no mkfifo:", err)
	}
	done := make(chan Result, 1)
	go func() { r, _ := tr.Run(context.Background(), entry, ""); done <- r }()
	select {
	case r := <-done:
		if r.Text != "board.go holds the board." {
			t.Fatalf("pipe turn = %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a named pipe held the turn")
	}
}

// longAnswer is a chat completion whose text is n runes of 'é' (two bytes
// each): what an endpoint ignoring max_tokens sends (SEC-LLM-1).
func longAnswer(n int) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, strings.Repeat("é", n))
}

// An answer longer than a bus entry holds is cut, on a rune, under
// CmdMaxRunes, with a note saying where: it can always be published.
func TestTurnCutsAnAnswerTheBusCannotHold(t *testing.T) {
	srv, _ := fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, longAnswer(70000) })
	res, err := newTurn(t, srv, time.Minute).Run(context.Background(), entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.ValidateText(res.Text, bus.CmdMaxRunes); err != nil {
		t.Fatalf("the answer cannot be published: %v", err)
	}
	if !strings.HasSuffix(res.Text, "runes]") || !strings.Contains(res.Text, "[answer cut at ") {
		t.Fatalf("no cut note at the end: ...%s", res.Text[len(res.Text)-60:])
	}
	if !strings.HasPrefix(res.Text, strings.Repeat("é", 60000)) {
		t.Fatal("the answer's start is not kept")
	}
	// An answer that fits is left whole.
	srv, _ = fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, longAnswer(1000) })
	if res, _ := newTurn(t, srv, time.Minute).Run(context.Background(), entry, ""); res.Text != strings.Repeat("é", 1000) {
		t.Fatalf("a short answer was changed: %d runes", len([]rune(res.Text)))
	}
}
