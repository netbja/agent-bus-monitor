package peer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Turn answers one bus entry: a fresh conversation (no memory across
// messages), bounded in tool calls and in time.
type Turn struct {
	Client   Client
	Tools    Tools
	Prompt   string        // the role prompt, sent as the system message
	MaxCalls int           // tool calls per turn (8)
	Timeout  time.Duration // the turn's wall clock
	Log      func(string)  // the pane's transcript; nil logs nothing
}

// Result is a turn's outcome: the reply's text, always set unless Run
// returns an error.
type Result struct {
	Text     string
	Calls    int
	Usage    Usage
	NoTools  bool // the endpoint refused tools; answered without
	TimedOut bool
}

// Run runs the turn. Its only error is an EndpointError (or the caller's own
// cancellation): the runner then keeps the entry and retries. Anything else
// (a refusal, a bad answer, the wall clock) is a Result whose text says so.
func (t Turn) Run(ctx context.Context, e bus.Event, thread string) (Result, error) {
	tctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	msgs := []Message{{Role: "system", Content: t.Prompt}, {Role: "user", Content: entryText(e, thread)}}
	var res Result
	tools := ToolSpecs()
	for {
		if res.Calls >= t.MaxCalls {
			tools = nil // the bound is reached: one last call, for the answer
		}
		m, u, err := t.Client.Chat(tctx, msgs, tools)
		res.Usage.PromptTokens += u.PromptTokens
		res.Usage.CompletionTokens += u.CompletionTokens
		switch {
		case errors.Is(err, ErrToolsUnsupported) && !res.NoTools:
			res.NoTools, tools = true, nil
			t.log("the endpoint refuses tool calls: answering without them")
			continue
		case err != nil && ctx.Err() != nil:
			return res, ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			res.TimedOut = true
			res.Text = fmt.Sprintf("I timed out after %s with %d tool call(s) made, before an answer.", t.Timeout, res.Calls)
			return res, nil
		case err != nil:
			var ef *EndpointError
			if errors.As(err, &ef) {
				return res, err
			}
			res.Text = "I could not answer: " + err.Error()
			return res, nil
		}
		if len(m.ToolCalls) == 0 || tools == nil {
			res.Text = strings.TrimSpace(m.Content)
			if res.Text == "" {
				res.Text = "I have no answer."
			}
			if res.NoTools {
				res.Text += "\n\n(Answered without tools: the endpoint refuses tool calls for this model.)"
			}
			res.Text = capAnswer(res.Text)
			return res, nil
		}
		msgs = append(msgs, Message{Role: "assistant", Content: m.Content, ToolCalls: m.ToolCalls})
		for _, call := range m.ToolCalls {
			res.Calls++
			out := "error: the bound of tool calls for this turn is reached; answer now"
			if res.Calls <= t.MaxCalls {
				out = t.Tools.Run(tctx, call.Function.Name, call.Function.Arguments)
			}
			t.log(fmt.Sprintf("tool %s %s -> %d bytes", call.Function.Name, call.Function.Arguments, len(out)))
			msgs = append(msgs, Message{Role: "tool", ToolCallID: call.ID, Content: out})
		}
	}
}

// capAnswer keeps an answer within what one bus entry holds (CmdMaxRunes):
// max_tokens is only a request to the server, so a longer answer is cut, on a
// rune, with a note saying where (SEC-LLM-1). Its format runes are dropped
// first (stripFormat).
func capAnswer(s string) string {
	s = stripFormat(strings.ToValidUTF8(s, "\uFFFD"))
	if utf8.RuneCountInString(s) <= bus.CmdMaxRunes {
		return s
	}
	keep := bus.CmdMaxRunes - 64 // room for the note
	cut, n := 0, 0
	for i := range s {
		if n == keep {
			cut = i
			break
		}
		n++
	}
	return s[:cut] + fmt.Sprintf("\n\n[answer cut at %d runes]", keep)
}

func (t Turn) log(s string) {
	if t.Log != nil {
		t.Log(s)
	}
}

// entryText is the user message: who sent what, and the thread it is on.
func entryText(e bus.Event, thread string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Message %s from %s [%s]:\n%s\n", e.ID, e.From, e.Type, e.Message)
	if thread != "" {
		b.WriteString("\nIts thread, oldest first:\n")
		b.WriteString(thread)
	}
	return b.String()
}
