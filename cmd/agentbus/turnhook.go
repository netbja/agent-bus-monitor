package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The turn hooks (auto-state spec, docs/superpowers/specs/2026-11-01-auto-state-design.md):
// `agentbus roles turn start` on the client's turn start (UserPromptSubmit),
// `roles turn end` on its turn end (Stop) and on a new session (SessionStart).
// The state follows the client's turns: Bus.TurnState, the snapshot only.

// turnHookBudget bounds the whole call, the dial included: it sits on every
// prompt, so a down broker costs at most this.
const turnHookBudget = 2 * time.Second

// turnHook never prints on stdout (UserPromptSubmit's stdout becomes the
// model's context) and always exits 0 (a Stop hook exiting 2 blocks the
// stop). A prompt beginning with "/" starts no model turn and flips nothing:
// the typed /clear must neither void a hand-off nor read as a turn, and a
// local command gets no Stop. A subagent's events are not the agent's.
func turnHook(args, env []string, stdin io.Reader, stdout io.Writer,
	flip func(ctx context.Context, project, agent, state string) error) int {
	if len(args) != 1 || (args[0] != "start" && args[0] != "end") {
		return 0
	}
	get := func(k string) string { v, _ := lookupEnv(env, k); return v }
	agent, project := get("AGENT_BUS_AGENT"), get("AGENT_BUS_PROJECT")
	if !bus.ValidName(agent) || !bus.ValidName(project) {
		return 0
	}
	var in struct {
		Prompt  string `json:"prompt"`
		AgentID string `json:"agent_id"`
	}
	raw, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if json.Unmarshal(raw, &in) != nil || in.AgentID != "" {
		return 0
	}
	state := "idle"
	if args[0] == "start" {
		if strings.HasPrefix(strings.TrimSpace(in.Prompt), "/") {
			return 0
		}
		state = "working"
	}
	ctx, cancel := context.WithTimeout(context.Background(), turnHookBudget)
	defer cancel()
	_ = flip(ctx, project, agent, state) // nothing is said: no broker, no proof, or a declared state means no flip
	return 0
}

// realTurnFlip dials the broker within ctx, proves the caller as status does,
// and flips.
func realTurnFlip(ctx context.Context, project, agent, state string) error {
	client, err := bus.ConnectContext(ctx, "")
	if err != nil {
		return err
	}
	defer client.Close()
	b, err := bus.Open(client, project)
	if err != nil {
		return err
	}
	return b.TurnState(ctx, agent, state, statusIdent(ctx))
}
