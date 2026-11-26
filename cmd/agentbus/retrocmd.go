package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
)

// retroBus is what agentbus retro reads and writes (self-improving loop,
// step 1).
type retroBus interface {
	Board(ctx context.Context) (map[string]bus.BoardEntry, error)
	Thread(ctx context.Context, threadID string) ([]bus.Event, error)
	TaskFriction(ctx context.Context, task string) ([]bus.FrictionSignal, error)
	SignalText(ctx context.Context, s bus.FrictionSignal) (string, error)
	RetroOwner(ctx context.Context, task string) (string, error)
	RetroRequest(ctx context.Context, task, to string) (bus.Retro, error)
	FindRetro(ctx context.Context, task string) (name, owner string, err error)
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	AcceptRequest(ctx context.Context, task, agent string) error
	CompleteRequest(ctx context.Context, task, agent, body string) (string, error)
}

type retroDeps struct {
	Bus      retroBus
	Self     string
	Getenv   func(string) string
	Ancestor func(context.Context) (string, error)
	Master   string
	Plus     retroPlusDeps // step 2's verbs (retropropose.go)
}

const retroUsage = "usage: retro request <task> | retro evidence <task> | retro none <task> <root cause...> | retro propose <task> --file <retro.md> | " +
	"retro show <task> | retro rejected [--file <f>] | retro route <task> | retro withdraw <task> | retro close <task> --pr <n>"

func runRetro(ctx context.Context, d retroDeps, args []string, stdout io.Writer) error {
	if len(args) >= 1 {
		if ok, err := runRetroPlus(ctx, d, d.Plus, args[0], args[1:], stdout); ok {
			return err
		}
	}
	if len(args) < 2 {
		return errors.New(retroUsage)
	}
	verb, task := args[0], args[1]
	if !bus.ValidName(task) {
		return fmt.Errorf("retro: invalid task %q", task)
	}
	switch verb {
	case "request":
		if len(args) != 2 {
			return errors.New(retroUsage)
		}
		if why := agentcaller.Refuse(ctx, d.Getenv, d.Ancestor, "a retro request", "agentbus retro request"); why != "" {
			return errors.New(why)
		}
		owner, err := d.Bus.RetroOwner(ctx, task)
		if err != nil {
			return err
		}
		to := owner
		agents, err := d.Bus.Agents(ctx)
		if err != nil {
			return err
		}
		if _, alive := agents[owner]; !alive { // a pop that has ended: master pops it again or retires the retro
			to = d.Master
		}
		r, err := d.Bus.RetroRequest(ctx, task, to)
		if err != nil {
			return err
		}
		state := "sent to"
		if !r.Sent {
			state = "already open for"
		}
		_, err = fmt.Fprintf(stdout, "retro %s %s %s (%s)\n", r.Task, state, r.To, bus.FrictionSummary(r.Signals))
		return err
	case "evidence":
		if len(args) != 2 {
			return errors.New(retroUsage)
		}
		return retroEvidence(ctx, d.Bus, task, stdout)
	case "none":
		if len(args) < 3 {
			return errors.New(retroUsage)
		}
		if d.Self == "" {
			return errors.New("retro none: run it as the retro's owner (AGENT_BUS_AGENT)")
		}
		name, _, err := d.Bus.FindRetro(ctx, task)
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("retro none: %s has no retro", task)
		}
		if err := d.Bus.AcceptRequest(ctx, name, d.Self); err != nil {
			return err
		}
		id, err := d.Bus.CompleteRequest(ctx, name, d.Self, "no lasting lesson: "+strings.Join(args[2:], " "))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, id)
		return err
	}
	return fmt.Errorf("retro: no verb %q (%s)", verb, retroUsage)
}

// retroEvidence prints the task's bus history: its thread, then each
// friction signal with its own text. Only bus ids are cited; no session is
// ever copied out.
func retroEvidence(ctx context.Context, b retroBus, task string, out io.Writer) error {
	board, err := b.Board(ctx)
	if err != nil {
		return err
	}
	e, ok := board[task]
	if !ok || e.Request == nil {
		return fmt.Errorf("retro evidence: %s is not a tracked request", task)
	}
	r := e.Request
	fmt.Fprintf(out, "task %s, owner %s, state %s\n\nthread %s\n", task, r.Target, e.State, r.Thread)
	thread, err := b.Thread(ctx, r.Thread)
	if err != nil {
		return err
	}
	if len(thread) == 0 {
		fmt.Fprintln(out, "  (no entries: removed by retention)")
	}
	for _, ev := range thread {
		fmt.Fprintf(out, "  %s %s→%s %s: %s\n", ev.ID, ev.From, ev.Target, ev.Type, ev.Message)
	}
	sigs, err := b.TaskFriction(ctx, task)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nsignals (%d)\n", len(sigs))
	for _, s := range sigs {
		text, err := b.SignalText(ctx, s)
		if err != nil {
			text = "(" + err.Error() + ")"
		}
		fmt.Fprintf(out, "  %s: %s\n", s, text)
	}
	return nil
}
