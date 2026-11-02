package peer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeEndpoint answers /chat/completions with handle and /models with models.
func fakeEndpoint(t *testing.T, models []string, handle func(req chatRequest) (int, string)) (*httptest.Server, *[]chatRequest) {
	t.Helper()
	var seen []chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			var data []map[string]string
			for _, m := range models {
				data = append(data, map[string]string{"id": m, "object": "model"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			var req chatRequest
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("bad request body: %v", err)
			}
			seen = append(seen, req)
			code, out := handle(req)
			w.WriteHeader(code)
			_, _ = io.WriteString(w, out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

const answerJSON = `{"choices":[{"message":{"role":"assistant","content":"board.go holds the board."},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":9}}`

const toolCallJSON = `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"bus/board.go\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":20}}`

func TestClientAnswersAndToolCalls(t *testing.T) {
	calls := 0
	srv, seen := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		calls++
		if calls == 1 {
			return 200, toolCallJSON
		}
		return 200, answerJSON
	})
	c := Client{URL: srv.URL + "/v1", Model: "m", MaxTokens: 256, HTTP: srv.Client()}
	msgs := []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "q"}}
	got, usage, err := c.Chat(context.Background(), msgs, ToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Function.Name != "read_file" || got.ToolCalls[0].Function.Arguments != `{"path":"bus/board.go"}` || usage.PromptTokens != 100 {
		t.Fatalf("tool call = %+v %+v", got, usage)
	}
	got, usage, err = c.Chat(context.Background(), msgs, nil)
	if err != nil || got.Content != "board.go holds the board." || usage.CompletionTokens != 9 {
		t.Fatalf("answer = %+v %+v %v", got, usage, err)
	}
	r := (*seen)[0]
	if r.Model != "m" || r.MaxTokens != 256 || len(r.Tools) != len(ToolSpecs()) || len(r.Messages) != 2 {
		t.Fatalf("request = %+v", r)
	}
	if len((*seen)[1].Tools) != 0 {
		t.Fatal("a call without tools sent tools")
	}
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()
	msgs := []Message{{Role: "user", Content: "q"}}

	// A server that refuses tools says so with a 4xx naming them.
	srv, _ := fakeEndpoint(t, nil, func(req chatRequest) (int, string) {
		return 400, `{"error":{"message":"registry.ollama.ai/library/m does not support tools"}}`
	})
	c := Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	if _, _, err := c.Chat(ctx, msgs, ToolSpecs()); !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("no tools = %v, want ErrToolsUnsupported", err)
	}

	// Down: nobody listens, or the server fails: an endpoint fault, retried by the peer.
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	c = Client{URL: url + "/v1", Model: "m", HTTP: http.DefaultClient}
	var ef *EndpointError
	if _, _, err := c.Chat(ctx, msgs, nil); !errors.As(err, &ef) {
		t.Fatalf("down = %v, want an EndpointError", err)
	}
	srv, _ = fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 503, "loading" })
	c = Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	if _, _, err := c.Chat(ctx, msgs, nil); !errors.As(err, &ef) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("503 = %v, want an EndpointError naming it", err)
	}

	// Bad JSON is not an endpoint fault: the turn fails, nothing is retried.
	srv, _ = fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, `{"choices":[` })
	c = Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	if _, _, err := c.Chat(ctx, msgs, nil); err == nil || errors.As(err, &ef) || !errors.Is(err, ErrBadResponse) {
		t.Fatalf("bad JSON = %v, want ErrBadResponse", err)
	}
	srv, _ = fakeEndpoint(t, nil, func(chatRequest) (int, string) { return 200, `{"choices":[]}` })
	c = Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	if _, _, err := c.Chat(ctx, msgs, nil); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("no choice = %v, want ErrBadResponse", err)
	}

	// Slow: the caller's deadline ends the call.
	srv, _ = fakeEndpoint(t, nil, func(chatRequest) (int, string) { time.Sleep(300 * time.Millisecond); return 200, answerJSON })
	c = Client{URL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, _, err := c.Chat(short, msgs, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow = %v, want the deadline", err)
	}
}

func TestClientModels(t *testing.T) {
	srv, _ := fakeEndpoint(t, []string{"qwen2.5-coder:3b", "llama3:8b"}, nil)
	c := Client{URL: srv.URL + "/v1", Model: "qwen2.5-coder:3b", HTTP: srv.Client()}
	if err := c.CheckModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Model = "phi4"
	if err := c.CheckModel(context.Background()); !errors.Is(err, ErrModelNotServed) || !strings.Contains(err.Error(), "phi4") {
		t.Fatalf("missing model = %v", err)
	}
}
