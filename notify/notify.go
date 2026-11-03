// Package notify tells the person, away from the desk, what waits for them
// (spec docs/superpowers/specs/2026-11-03-notifications-design.md): the
// senders (ntfy, Discord, the desktop), their one HTTP client, and the
// person's notify.toml, where the targets and their secrets live. A URL, a
// topic, a token or a webhook never leaves this package in an error, a
// warning or a log line.
package notify

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Priority is how loud a message is: ntfy's Priority, the desktop's urgency.
// Discord has none.
type Priority int

const (
	Default Priority = iota
	Low
	High
)

// MaxBytes is the most a message's text may hold (Discord's limit; ntfy's
// 4096 is higher).
const MaxBytes = 2000

// Message is one notification: a title and one line per event.
type Message struct {
	Project  string
	Title    string
	Lines    []string
	Priority Priority
}

// Sender sends a message to one target of notify.toml.
type Sender interface {
	Name() string // the target's name in notify.toml
	Kind() string // "ntfy", "discord", "desktop"
	Send(ctx context.Context, m Message) error
}

// SendError is a failed send: the target and the status or a short reason,
// never the URL nor the response body.
type SendError struct {
	Target string
	Status int    // 0 = no HTTP answer
	Reason string // a short reason when there is no status
}

func (e *SendError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s: %d %s", e.Target, e.Status, strings.ToLower(httpStatusText(e.Status)))
	}
	return e.Target + ": " + e.Reason
}

// body is the lines, one per line.
func (m Message) body() string { return strings.Join(m.Lines, "\n") }

// capBytes cuts s to at most n bytes on a rune boundary, marking the cut.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const mark = "…"
	cut := n - len(mark)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + mark
}
