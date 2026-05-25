// Package bus holds the shared conventions of the Agent Bus: connection,
// message sanitisation, and transport-neutral constants. Both the busmon TUI
// and the agentbus CLI import it so the conventions live in exactly one place.
// The Streams transport is in stream.go; this file holds only the primitives
// that survive the pub/sub cutover.
package bus

import (
	"context"
	"os"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// Report kinds carried in the report stream "kind" field.
const (
	ReportNote = "note" // intentional, agent-authored report → relayed verbatim
	ReportAuto = "auto" // Stop-hook safety-net summary → LLM-gated (phase 2)
)

const maxReportLen = 120

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
