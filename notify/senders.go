package notify

import (
	"context"
	"encoding/json"
	"net/http"
)

// ntfy posts to <server>/<topic>: the lines as the body, the title in a
// header (spec "The sender interface").
type ntfy struct {
	name, url, token string
	c                *client
}

func (s *ntfy) Name() string { return s.name }
func (s *ntfy) Kind() string { return "ntfy" }

func (s *ntfy) Send(ctx context.Context, m Message) error {
	h := http.Header{}
	h.Set("Title", m.Title)
	h.Set("Priority", map[Priority]string{Low: "low", Default: "default", High: "high"}[m.Priority])
	h.Set("Tags", "agentbus")
	h.Set("Markdown", "no")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	if s.token != "" {
		h.Set("Authorization", "Bearer "+s.token)
	}
	return s.c.post(ctx, s.name, s.url, h, []byte(capBytes(m.body(), MaxBytes)))
}

// discord posts JSON to a webhook: the title bold on the first line, and no
// mention parsed whatever the text holds (no @everyone, no role ping).
type discord struct {
	name, url string
	c         *client
}

func (s *discord) Name() string { return s.name }
func (s *discord) Kind() string { return "discord" }

type discordPayload struct {
	Username        string `json:"username"`
	Content         string `json:"content"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

func (s *discord) Send(ctx context.Context, m Message) error {
	content := "**" + m.Title + "**"
	if b := m.body(); b != "" {
		content += "\n" + b
	}
	p := discordPayload{Username: "agentbus", Content: capBytes(content, MaxBytes)}
	p.AllowedMentions.Parse = []string{}
	data, err := json.Marshal(p)
	if err != nil {
		return &SendError{Target: s.name, Reason: "cannot encode the message"}
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return s.c.post(ctx, s.name, s.url, h, data)
}

// DesktopRun shows one desktop notification (notify-send, bounded by the
// caller); urgency is notify-send's: low, normal or critical.
type DesktopRun func(ctx context.Context, title, body, urgency string) error

// desktop is notify-send on this host, one more target (Bernard Q6).
type desktop struct {
	name string
	run  DesktopRun
}

// NewDesktop is the desktop target called name; run nil = notify-send is not
// on PATH, and every send fails saying so.
func NewDesktop(name string, run DesktopRun) Sender { return &desktop{name: name, run: run} }

func (s *desktop) Name() string { return s.name }
func (s *desktop) Kind() string { return "desktop" }

func (s *desktop) Send(ctx context.Context, m Message) error {
	if s.run == nil {
		return &SendError{Target: s.name, Reason: "no notify-send on PATH"}
	}
	urgency := map[Priority]string{Low: "low", Default: "normal", High: "critical"}[m.Priority]
	if err := s.run(ctx, m.Title, capBytes(m.body(), MaxBytes), urgency); err != nil {
		return &SendError{Target: s.name, Reason: "notify-send failed"}
	}
	return nil
}
