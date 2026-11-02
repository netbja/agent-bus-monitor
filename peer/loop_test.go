package peer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
