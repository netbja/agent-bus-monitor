// Package bus holds the shared conventions of the Agent Bus: connection,
// channel naming, message parsing, and publish helpers. Both the busmon TUI
// and the agentbus CLI import it so the conventions live in exactly one place.
package bus

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)

const NotifyChannel = "hermes:notify"

// Report kinds carried in the hermes:report:{agent} payload (kind|message).
const (
	ReportNote = "note" // intentional, agent-authored report → relayed verbatim
	ReportAuto = "auto" // Stop-hook safety-net summary → LLM-gated (phase 2)
)

const maxReportLen = 120

var ValidAgents = map[string]bool{
	"claude1": true, "claude2": true, "hermes_laptop": true, "hermes_vdr": true,
}

var ValidStates = map[string]bool{
	"working": true, "idle": true, "blocked": true, "done": true,
}

type silentLogger struct{}

func (silentLogger) Printf(context.Context, string, ...interface{}) {}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Connect resolves a Redis client the way agent_bus.py does: REDIS_URL takes
// precedence, otherwise REDIS_HOST/PORT/PASSWORD (defaults localhost:6380 /
// AgentBus2025!). A non-empty host overrides REDIS_HOST. It pings before
// returning so callers fail fast instead of inside the first command.
func Connect(host string) (*redis.Client, error) {
	redis.SetLogger(silentLogger{})
	var client *redis.Client
	if url := os.Getenv("REDIS_URL"); url != "" {
		opt, err := redis.ParseURL(url)
		if err != nil {
			return nil, err
		}
		client = redis.NewClient(opt)
	} else {
		if host == "" {
			host = envOr("REDIS_HOST", "localhost")
		}
		client = redis.NewClient(&redis.Options{
			Addr:     host + ":" + envOr("REDIS_PORT", "6380"),
			Password: envOr("REDIS_PASSWORD", "AgentBus2025!"),
		})
	}
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}
	return client, nil
}

func StatusChannel(agent string) string { return "status:" + agent }
func CmdChannel(agent string) string    { return "hermes:cmd:" + agent }
func ReportChannel(agent string) string { return "hermes:report:" + agent }

// Parse turns a (channel, payload) pair into its logical fields. kind is one of
// "status", "notify", "cmd", "report", or "?" for anything outside the convention.
func Parse(channel, data string) (agent, kind, state, message string) {
	switch {
	case strings.HasPrefix(channel, "status:"):
		parts := strings.SplitN(data, "|", 2)
		state = parts[0]
		if len(parts) > 1 {
			message = parts[1]
		}
		return strings.TrimPrefix(channel, "status:"), "status", state, message
	case channel == NotifyChannel:
		return "hermes", "notify", "", data
	case strings.HasPrefix(channel, "hermes:cmd:"):
		return strings.TrimPrefix(channel, "hermes:cmd:"), "cmd", "", data
	case strings.HasPrefix(channel, "hermes:report:"):
		parts := strings.SplitN(data, "|", 2)
		state = parts[0]
		if len(parts) > 1 {
			message = parts[1]
		}
		return strings.TrimPrefix(channel, "hermes:report:"), "report", state, message
	}
	return "?", "?", "", data
}

func Status(ctx context.Context, r *redis.Client, agent, state, message string) error {
	payload := state
	if message != "" {
		payload = state + "|" + message
	}
	return r.Publish(ctx, StatusChannel(agent), payload).Err()
}

// SanitizeReportMessage strips control characters — the line-based `agentbus
// listen` consumer breaks on embedded newlines — collapses runs of whitespace,
// and truncates to maxReportLen runes so a report stays one bounded line.
func SanitizeReportMessage(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	out := strings.Join(strings.Fields(mapped), " ")
	if r := []rune(out); len(r) > maxReportLen {
		out = strings.TrimSpace(string(r[:maxReportLen])) + "…"
	}
	return out
}

func reportPayload(kind, message string) string {
	return kind + "|" + SanitizeReportMessage(message)
}

// Report publishes an agent's report on hermes:report:{agent}. kind is
// ReportNote (intentional) or ReportAuto (Stop-hook safety net).
func Report(ctx context.Context, r *redis.Client, agent, kind, message string) error {
	return r.Publish(ctx, ReportChannel(agent), reportPayload(kind, message)).Err()
}

func Cmd(ctx context.Context, r *redis.Client, from, target, command string) error {
	if err := r.Publish(ctx, CmdChannel(target), command).Err(); err != nil {
		return err
	}
	return r.Publish(ctx, NotifyChannel, fmt.Sprintf("cmd:%s->%s %s", from, target, command)).Err()
}

func Notify(ctx context.Context, r *redis.Client, message string) error {
	return r.Publish(ctx, NotifyChannel, message).Err()
}

// Listen blocks, invoking fn for every message matching pattern until the
// context is cancelled or the connection drops.
func Listen(ctx context.Context, r *redis.Client, pattern string, fn func(channel, data string)) error {
	ps := r.PSubscribe(ctx, pattern)
	defer ps.Close()
	for msg := range ps.Channel() {
		fn(msg.Channel, msg.Payload)
	}
	return nil
}
