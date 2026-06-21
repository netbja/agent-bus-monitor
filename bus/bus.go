// Package bus holds the shared conventions of the Agent Bus: connection,
// message sanitisation, and transport-neutral constants. Both the busmon TUI
// and the agentbus CLI import it so the conventions live in exactly one place.
// The Streams transport is in stream.go; this file holds only the primitives
// that survive the pub/sub cutover.
package bus

import (
	"context"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// Report kinds carried in the report stream "kind" field.
const (
	ReportNote = "note" // intentional, agent-authored report → relayed verbatim
	ReportAuto = "auto" // Stop-hook safety-net summary → LLM-gated (phase 2)
)

const defaultReportMax = 500

// reportMaxLen resolves the report rune cap: AGENT_BUS_REPORT_MAX if it parses
// to a positive int, else defaultReportMax (500). Read per call so it stays
// settable from tests and per-process env.
func reportMaxLen() int {
	if v := os.Getenv("AGENT_BUS_REPORT_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultReportMax
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

// SanitizeReportMessage strips control characters — the line-based `agentbus
// listen` consumer breaks on embedded newlines — collapses runs of whitespace,
// and truncates to the resolved cap so a report stays one bounded line.
func SanitizeReportMessage(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	out := strings.Join(strings.Fields(mapped), " ")
	if max := reportMaxLen(); len([]rune(out)) > max {
		out = strings.TrimSpace(string([]rune(out)[:max])) + "…"
	}
	return out
}
