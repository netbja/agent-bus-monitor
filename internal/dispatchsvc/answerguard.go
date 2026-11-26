package dispatchsvc

import (
	"context"
	"errors"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
)

// The answer guard (board-drift §8, check person-unanswered): the retained
// cmd history, the agents and the person's recorded name (identity.as)
// judged by health.EvaluatePersonUnanswered; its wakes and the person's
// notices are written once each by stallWake.

// personUnanswered reads the answer guard's input and judges it.
func (s *stallReader) personUnanswered(ctx context.Context) (health.Outcome, error) {
	set := config.Machine(s.getenv)
	in := health.PersonUnansweredInput{Now: s.now(), After: set.Duration("dispatch.answer_after")}
	if g := set.Get("identity.as"); g.Source != "default" {
		in.Person, _ = g.Value.(string)
	}
	defer s.keep(func() { s.lastAnswer = &in })
	if in.After <= 0 || in.Person == "" {
		return health.EvaluatePersonUnanswered(in), nil
	}
	snaps, err := s.b.Agents(ctx)
	if err != nil {
		in.ReadErr = "agents unavailable"
		return health.EvaluatePersonUnanswered(in), nil
	}
	cmds, _, err := s.b.Recent(ctx, []string{"cmd"}, stallCmdScan)
	if err != nil {
		in.ReadErr = "cmd history unavailable"
		return health.EvaluatePersonUnanswered(in), nil
	}
	for _, e := range cmds {
		in.Cmds = append(in.Cmds, health.AnswerCmd{ID: e.ID, From: e.From, Target: e.Target, Type: e.Type, Text: e.Message,
			Human: e.Human, Typed: e.Typed, NoWake: e.NoWake})
	}
	var live map[string]bool
	if s.liveTerms != nil {
		live = s.liveTerms(ctx)
	}
	for name, snap := range snaps {
		if name == in.Person {
			continue // the person is never an agent (person-never-agent)
		}
		in.Agents = append(in.Agents, health.AnswerAgent{Name: name, State: snap.State,
			NoTerminal: live != nil && bus.ShownState(snap.State, snap.Pane, true, live[name]) == bus.StateNoTerminal})
	}
	return health.EvaluatePersonUnanswered(in), nil
}

// answerWake writes the answer guard's wakes (to the agent) and the
// person's notices, each once per directive under its mark.
func (s *stallReader) answerWake(ctx context.Context, token string) error {
	s.mu.Lock()
	var wakes, notices []health.StallWake
	if s.lastAnswer != nil {
		wakes, notices = health.PersonUnansweredWakes(*s.lastAnswer), health.PersonUnansweredNotices(*s.lastAnswer)
	}
	s.mu.Unlock()
	for _, w := range wakes {
		if _, err := s.b.FencedCmdOnce(ctx, token, w.Mark, w.Episode, "dispatch", w.Target, bus.CmdDirective, w.Text, false); err != nil && !errors.Is(err, bus.ErrSwapInProgress) {
			return err
		}
	}
	for _, n := range notices {
		if _, err := s.b.FencedNotifyOnceForPerson(ctx, token, n.Mark, n.Episode, "dispatch", n.Text); err != nil {
			return err
		}
	}
	return nil
}

// holderLiveTerms reads the project's holder for its agents with a live
// terminal; nil when no holder of the project answers (nothing proven).
func holderLiveTerms(project string, getenv func(string) string) func(context.Context) map[string]bool {
	return func(ctx context.Context) map[string]bool {
		h, err := holder.NewHost(project, holder.BuildID(), getenv)
		if err != nil {
			return nil
		}
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if info, err := h.Describe(c); err != nil || len(info.Builds) == 0 {
			return nil
		}
		list, err := h.List(c, project)
		if err != nil {
			return nil
		}
		out := map[string]bool{}
		for _, t := range list {
			if t.Project == project && t.Alive {
				out[t.Name] = true
			}
		}
		return out
	}
}
