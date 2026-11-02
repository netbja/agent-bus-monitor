package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Message is one OpenAI chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is the model's request to run one tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Usage is the endpoint's own token counts for one call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatRequest struct {
	Model     string     `json:"model"`
	Messages  []Message  `json:"messages"`
	Tools     []ToolSpec `json:"tools,omitempty"`
	MaxTokens int        `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   *string    `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

var (
	// ErrToolsUnsupported: the server refused the tools (a model without
	// function calling; llama-server without --jinja). The turn is tried once
	// without them.
	ErrToolsUnsupported = errors.New("the endpoint refuses tool calls for this model")
	// ErrBadResponse: an answer that is not a chat completion. The turn fails;
	// nothing is retried.
	ErrBadResponse = errors.New("the endpoint's answer is not a chat completion")
	// ErrModelNotServed: /models does not list the model.
	ErrModelNotServed = errors.New("model not served")
	// ErrRequestRefused: the endpoint refused this request for good (a 4xx
	// that no retry heals, such as a thread longer than its context, or a
	// redirect, never followed). The turn fails; the entry is acknowledged,
	// so one entry never wedges the queue.
	ErrRequestRefused = errors.New("the endpoint refused the request")
)

// refused is ErrRequestRefused with the status and the endpoint's reason.
type refused struct {
	status int
	reason string
}

func (r *refused) Error() string {
	return fmt.Sprintf("%v: status %d: %s", ErrRequestRefused, r.status, r.reason)
}
func (r *refused) Is(target error) bool { return target == ErrRequestRefused }

// StatusOf is the HTTP status an error of the client carries (a refusal, or
// an endpoint fault the server answered), 0 when there is none: what a
// caller may show without the server's own words (health details).
func StatusOf(err error) int {
	var r *refused
	if errors.As(err, &r) {
		return r.status
	}
	var ef *EndpointError
	if errors.As(err, &ef) {
		return ef.Status
	}
	return 0
}

// statusError classes a non-200: only what can heal is an EndpointError
// (retried, the entry kept): 5xx, 408, 429, and 404 (Ollama's model not
// found, which the spec maps to blocked). Any other is refused.
func statusError(what string, status int, body []byte) error {
	reason := oneLine(body)
	if status >= 500 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status == http.StatusNotFound {
		return &EndpointError{Err: fmt.Errorf("%sstatus %d: %s", what, status, reason), Status: status}
	}
	return &refused{status: status, reason: reason}
}

// EndpointError is a fault of the endpoint itself (unreachable, or a server
// error): the peer declares blocked and retries, never acknowledging the entry.
type EndpointError struct {
	Err    error
	Status int // the HTTP status when the server answered one; 0 when it gave no answer
}

func (e *EndpointError) Error() string { return "llm endpoint: " + e.Err.Error() }
func (e *EndpointError) Unwrap() error { return e.Err }

// maxBody bounds what is read of an answer.
const maxBody = 4 << 20

// Client talks to one OpenAI-compatible endpoint (Ollama, llama-server): the
// only connection the peer opens besides the bus's Redis.
type Client struct {
	URL       string // ends in /v1
	Model     string
	MaxTokens int
	HTTP      *http.Client
}

// directTransport is the default transport without a proxy: the endpoint
// is reached directly or not at all (prompts and repository text never
// travel through HTTP(S)_PROXY).
var directTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}()

// http is the client a call uses: c.HTTP's transport (a test's) or the
// direct one, and never a redirect followed (the loopback check would be
// bypassed: a 307 replays the request to another host).
func (c Client) http() *http.Client {
	hc := http.Client{Transport: directTransport}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}

// Chat sends one chat completion; tools nil sends none.
func (c Client) Chat(ctx context.Context, msgs []Message, tools []ToolSpec) (Message, Usage, error) {
	body, err := json.Marshal(chatRequest{Model: c.Model, Messages: msgs, Tools: tools, MaxTokens: c.MaxTokens})
	if err != nil {
		return Message{}, Usage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.URL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	data, status, err := c.do(req)
	if err != nil {
		return Message{}, Usage{}, err
	}
	// A 4xx naming tools while tools were sent reads as a refusal of tools
	// (the turn is then tried once without them). It may also be a complaint
	// about a tool message mid-loop; the one retry without tools is harmless.
	if status >= 400 && status < 500 && len(tools) > 0 && strings.Contains(strings.ToLower(string(data)), "tool") {
		return Message{}, Usage{}, ErrToolsUnsupported
	}
	if status != http.StatusOK {
		return Message{}, Usage{}, statusError("", status, data)
	}
	var resp chatResponse
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Choices) == 0 {
		return Message{}, Usage{}, ErrBadResponse
	}
	m := resp.Choices[0].Message
	out := Message{Role: "assistant", ToolCalls: m.ToolCalls}
	if m.Content != nil {
		out.Content = *m.Content
	}
	if out.Content == "" && len(out.ToolCalls) == 0 {
		return Message{}, Usage{}, ErrBadResponse // neither text nor a tool call
	}
	return out, resp.Usage, nil
}

// CheckModel asks GET {url}/models whether the model is served.
func (c Client) CheckModel(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.URL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	data, status, err := c.do(req)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return statusError("models: ", status, data)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return ErrBadResponse
	}
	for _, m := range list.Data {
		if m.ID == c.Model {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrModelNotServed, c.Model)
}

// do sends req; an unreachable endpoint is an EndpointError, the caller's
// own deadline or cancellation is returned as is.
func (c Client) do(req *http.Request) ([]byte, int, error) {
	resp, err := c.http().Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err // the reason, without the URL again
		}
		return nil, 0, &EndpointError{Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		return nil, 0, &EndpointError{Err: errors.New("answer cut short")}
	}
	return data, resp.StatusCode, nil
}

// oneLine is the start of an error body, on one line.
func oneLine(b []byte) string {
	s := strings.Join(strings.Fields(strings.ToValidUTF8(string(b), "?")), " ")
	if len(s) > 200 {
		cut := 200
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}
