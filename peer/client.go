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
)

// EndpointError is a fault of the endpoint itself (unreachable, or a server
// error): the peer declares blocked and retries, never acknowledging the entry.
type EndpointError struct{ Err error }

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

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
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
	if status >= 400 && status < 500 && len(tools) > 0 && strings.Contains(strings.ToLower(string(data)), "tool") {
		return Message{}, Usage{}, ErrToolsUnsupported
	}
	if status != http.StatusOK {
		return Message{}, Usage{}, &EndpointError{Err: fmt.Errorf("status %d: %s", status, oneLine(data))}
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
		return &EndpointError{Err: fmt.Errorf("models: status %d", status)}
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
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
